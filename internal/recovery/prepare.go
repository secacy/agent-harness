package recovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/secacy/agent-harness/internal/audit"
	"github.com/secacy/agent-harness/internal/campaign"
)

var ErrConflict = errors.New("recovery checkpoint identity conflict")
var checkpointID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)

type CommitState string

const (
	CommitUnknown  CommitState = "Unknown"
	CommitPending  CommitState = "Pending"
	CommitPrepared CommitState = "Prepared"
)

type PrepareRequest struct {
	Request
	ID string
}

type Prepared struct {
	// Prepared 表示 head 已确认发布，Pending 表示只确认检查点，Unknown 表示写入结果不确定。
	// 任一状态都不表示 Agent 已重跑或已经授予付费权限。
	CommitState CommitState
	Checkpoint  campaign.Artifact
	State       campaign.RecoveryState
	Audit       audit.Report
}

type prepareHooks struct {
	write func(string, []byte) error
	after func(string)
}

// Prepare 封存并提交恢复控制检查点；不改旧控制/Run，不打开付费账本或激活任务。
func Prepare(ctx context.Context, request PrepareRequest) (Prepared, error) {
	return prepare(ctx, request, prepareHooks{})
}

func prepare(ctx context.Context, request PrepareRequest, hooks prepareHooks) (prepared Prepared, err error) {
	if !checkpointID.MatchString(request.ID) {
		return prepared, ErrConfig
	}
	write := hooks.write
	if write == nil {
		write = publishImmutable
	}
	observe := func(point string) {
		if hooks.after != nil {
			hooks.after(point)
		}
	}
	_, err = checkStopped(ctx, request.Request, true, func(root string, source Result) error {
		dir := filepath.Join(root, audit.RecoveryDirectory)
		entries, readErr := os.ReadDir(dir)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		name := "checkpoint-" + request.ID + ".json"
		var existing []byte
		at := time.Now().UTC()
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "checkpoint-") && entry.Name() != name {
				return ErrConflict
			}
			if entry.Name() == name {
				existing, readErr = readFile(root, audit.RecoveryDirectory+"/"+name)
				if readErr != nil {
					return readErr
				}
				var checkpoint campaign.RecoveryCheckpoint
				if readErr = audit.DecodeJSON(existing, &checkpoint, 1); readErr != nil {
					return readErr
				}
				at = checkpoint.CreatedAt
			}
		}
		files := map[string][]byte{}
		for _, pin := range source.Audit.Sources {
			if pin.Kind == "file" && audit.RecoveryControlFile(pin.Path) {
				data, err := readFile(root, pin.Path)
				if err != nil {
					return err
				}
				files[pin.Path] = data
			}
		}
		checkpoint, err := audit.BuildRecoveryCheckpoint(source.Audit.Sources, files, request.ID, at)
		if err != nil {
			return errors.Join(ErrBlocked, err)
		}
		data, err := encode(checkpoint)
		if err != nil {
			return err
		}
		if existing != nil {
			var prior campaign.RecoveryCheckpoint
			if err := audit.DecodeJSON(existing, &prior, 1); err != nil || !reflect.DeepEqual(prior, checkpoint) {
				return errors.Join(ErrConflict, err)
			}
			data = existing
		}
		if len(data) > 16<<20 {
			return fmt.Errorf("%w: checkpoint exceeds 16 MiB", ErrBlocked)
		}
		prepared.State = checkpoint.After
		sum := sha256.Sum256(data)
		prepared.Checkpoint = campaign.Artifact{Path: audit.RecoveryDirectory + "/" + name, SHA256: hex.EncodeToString(sum[:])}
		if errors.Is(readErr, os.ErrNotExist) {
			if err := os.Mkdir(dir, 0700); err != nil {
				return err
			}
			if err := syncDirectory(root); err != nil {
				return err
			}
		}
		observe("directory")
		if err := ctx.Err(); err != nil {
			return err
		}
		prepared.CommitState = CommitUnknown
		if err := write(filepath.Join(dir, name), data); err != nil {
			return err
		}
		prepared.CommitState = CommitPending
		observe("checkpoint")
		if err := ctx.Err(); err != nil {
			return err
		}
		head := campaign.RecoveryHead{SchemaVersion: 1, CampaignID: checkpoint.CampaignID, PlanSHA256: checkpoint.PlanSHA256, Checkpoint: prepared.Checkpoint}
		headData, err := encode(head)
		if err != nil {
			return err
		}
		prepared.CommitState = CommitUnknown
		if err := write(filepath.Join(dir, "head.json"), headData); err != nil {
			return err
		}
		prepared.CommitState = CommitPrepared
		observe("head")
		prepared.Audit, err = audit.Inspect(ctx, audit.Request{CampaignDir: root, OutputDir: filepath.Join(request.OutputDir, "committed")}, request.InspectRun)
		if err != nil {
			return err
		}
		if prepared.Audit.Consistency != "pass" || !prepared.Audit.ScanComplete || prepared.Audit.InputsUnchanged == nil || !*prepared.Audit.InputsUnchanged {
			return ErrBlocked
		}
		return nil
	})
	return prepared, err
}

func encode(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	return append(data, '\n'), err
}

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

// 同一写锁内发布新文件；已存在时只核对，绝不覆盖原检查点或标记。
func publishImmutable(path string, data []byte) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Size() > 16<<20 {
			return ErrConflict
		}
		prior, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(prior, data) {
			return errors.Join(ErrConflict, err)
		}
		// 上一次可能已 rename 但 sync 结果不确定，再确认文件和目录持久化。
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		if err = errors.Join(f.Sync(), f.Close()); err != nil {
			return err
		}
		return syncDirectory(filepath.Dir(path))
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".checkpoint-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		return errors.Join(err, f.Close())
	}
	if err = errors.Join(f.Sync(), f.Close()); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncDirectory(dir)
}
