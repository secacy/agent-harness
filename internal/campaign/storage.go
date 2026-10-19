package campaign

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/secacy/agent-harness/internal/quality"
)

func checksum(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// 控制记录使用同目录替换和同步；写入结果不确定也必须停止接纳。
func writeFile(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".publish-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	syncErr := file.Sync()
	if err = errors.Join(syncErr, file.Close()); err != nil {
		return err
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}
func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
func writeJSON(path string, value any) (Artifact, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return Artifact{}, err
	}
	data = append(data, '\n')
	return Artifact{SHA256: checksum(data)}, writeFile(path, data)
}
func mkdir(path string) error {
	if err := os.Mkdir(path, 0700); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

// 未存在的输入也可规范化，内容读取错误留给所属 Target 记录。
func canonical(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err = os.Lstat(absolute); errors.Is(err, os.ErrNotExist) {
		parent, err := canonical(filepath.Dir(absolute))
		return filepath.Join(parent, filepath.Base(absolute)), err
	}
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}
func within(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// 完成判断只认磁盘终态。失败的部分材料可以保留，但不能携带质量结论。
func checkAttempt(root, runsDir, targetID string, attempt Attempt, runErr error) (Attempt, error) {
	invalid := func(reason string) (Attempt, error) {
		return Attempt{Status: Failed, FailureCode: "invalid_result"}, fmt.Errorf("%w: %s", ErrResult, reason)
	}
	if attempt.RunID == "" {
		if runErr == nil || attempt.Directory != "" || attempt.Report.Path != "" || attempt.Report.SHA256 != "" || attempt.Verdict != "" || (attempt.Status != Failed && attempt.Status != Cancelled) {
			return invalid("missing run identity")
		}
		return attempt, nil
	}
	if !identifier.MatchString(attempt.RunID) || !filepath.IsAbs(attempt.Directory) {
		return invalid("invalid run identity")
	}
	directory, err := canonical(attempt.Directory)
	if err != nil || directory != filepath.Join(runsDir, attempt.RunID) {
		return invalid("run belongs to another target")
	}
	data, err := os.ReadFile(filepath.Join(directory, "run.json"))
	if err != nil {
		return invalid("run record is unavailable")
	}
	var disk struct {
		ID        string `json:"id"`
		TargetID  string `json:"target_id"`
		Directory string `json:"directory"`
		Status    Status `json:"status"`
		Verdict   string `json:"verdict"`
	}
	if json.Unmarshal(data, &disk) != nil || disk.ID != attempt.RunID || disk.TargetID != targetID || disk.Directory != directory || disk.Status != attempt.Status || disk.Verdict != attempt.Verdict {
		return invalid("run record does not match the returned result")
	}
	switch attempt.Status {
	case Completed:
		if runErr != nil || (attempt.Verdict != quality.ReadyForReview && attempt.Verdict != quality.Rejected && attempt.Verdict != quality.NeedsAttention) || attempt.Report.Path == "" {
			return invalid("incomplete delivery")
		}
	case Failed, Cancelled:
		if runErr == nil || attempt.Verdict != "" {
			return invalid("failure carries a quality verdict or lacks an error")
		}
	default:
		return invalid("run is not terminal")
	}
	if attempt.Report.Path != "" {
		reportPath, err := canonical(attempt.Report.Path)
		if err != nil || !within(directory, reportPath) || reportPath == directory {
			return invalid("report escapes its run")
		}
		info, err := os.Stat(reportPath)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
			return invalid("report is missing or too large")
		}
		data, err := os.ReadFile(reportPath)
		if err != nil || checksum(data) != attempt.Report.SHA256 {
			return invalid("report digest mismatch")
		}
		relative, _ := filepath.Rel(root, reportPath)
		attempt.Report.Path = filepath.ToSlash(relative)
	} else if attempt.Report.SHA256 != "" {
		return invalid("report digest lacks a path")
	}
	relative, _ := filepath.Rel(root, directory)
	attempt.Directory = filepath.ToSlash(relative)
	return attempt, nil
}
