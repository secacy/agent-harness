// Package recovery 检查停止后的 Campaign 是否满足恢复的前置条件。
package recovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/secacy/agent-harness/internal/audit"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/writer"
)

var (
	ErrConfig           = errors.New("invalid recovery check configuration")
	ErrUnsupported      = errors.New("campaign format does not support writer ownership")
	ErrBlocked          = errors.New("recovery prerequisites are not satisfied")
	ErrResourcesUnknown = errors.New("stopped execution resources have not been verified")
)

type Request struct {
	CampaignDir        string
	OutputDir          string // 独立的新审计目录，不在来源内部。
	ExpectedPlanSHA256 string
	InspectRun         audit.RunInspector
	// 由可信宿主只读检查精确归属与停止证据，不得清理、启动或修改任何资源。
	VerifyResourcesStopped func(context.Context, string) error
}

// Result 只是一次检查快照，不转移写锁，也不授权后续生成或重跑。
type Result struct {
	CampaignID string
	PlanSHA256 string
	Audit      audit.Report
}

// Check 临时取得旧目录的写锁并检查；始终不创建模型、重开账本或改写来源。
// 实际恢复必须在持锁期间重新检查，不能复用本次成功结果绕过准入。
func Check(ctx context.Context, request Request) (result Result, err error) {
	return checkStopped(ctx, request, false, nil)
}

// action 只在同一写锁内的完整检查之后运行，不把锁外快照当作修改许可。
func checkStopped(ctx context.Context, request Request, allowCheckpoint bool, action func(string, Result) error) (result Result, err error) {
	if err = ctx.Err(); err != nil {
		return result, err
	}
	digest, digestErr := hex.DecodeString(request.ExpectedPlanSHA256)
	if request.CampaignDir == "" || request.OutputDir == "" || request.InspectRun == nil || digestErr != nil || len(digest) != sha256.Size || strings.ToLower(request.ExpectedPlanSHA256) != request.ExpectedPlanSHA256 {
		return result, ErrConfig
	}
	root, err := filepath.EvalSymlinks(request.CampaignDir)
	if err == nil {
		root, err = filepath.Abs(root)
	}
	if err != nil {
		return result, err
	}
	before, err := readFile(root, "campaign.json")
	if err != nil {
		return result, err
	}
	var record campaign.Record
	if err = audit.DecodeJSON(before, &record, 2); err != nil {
		return result, fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	if record.WriterProtocol != writer.Protocol {
		return result, ErrUnsupported
	}
	lease, err := writer.Open(root)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, lease.Close()) }()
	// 锁前读取只用来拒绝旧协议，实际身份必须在持锁后重新核对。
	locked, err := readFile(root, "campaign.json")
	if err != nil || !bytes.Equal(before, locked) {
		return result, errors.Join(ErrBlocked, err, errors.New("campaign changed while acquiring writer ownership"))
	}
	if record.Directory != root || record.PlanSHA256 != request.ExpectedPlanSHA256 {
		return result, fmt.Errorf("%w: campaign location or plan identity differs", ErrBlocked)
	}
	if _, statErr := os.Lstat(filepath.Join(root, "recoveries")); !allowCheckpoint && !errors.Is(statErr, os.ErrNotExist) {
		return result, fmt.Errorf("%w: recovery transactions are not supported yet", ErrBlocked)
	}
	if request.VerifyResourcesStopped == nil {
		return result, ErrResourcesUnknown
	}
	if err = request.VerifyResourcesStopped(ctx, root); err != nil {
		return result, errors.Join(ErrResourcesUnknown, err)
	}
	result.CampaignID, result.PlanSHA256 = record.ID, record.PlanSHA256
	result.Audit, err = audit.Inspect(ctx, audit.Request{CampaignDir: root, OutputDir: request.OutputDir}, request.InspectRun)
	if err != nil {
		return result, err
	}
	r := result.Audit
	if r.Consistency != "pass" || !r.ScanComplete || r.InputsUnchanged == nil || !*r.InputsUnchanged {
		return result, ErrBlocked
	}
	// 审计允许保留已停止账本的 halt 事实，但恢复准入不能解除它。
	data, err := readFile(root, "budget/budget.json")
	if err != nil {
		return result, errors.Join(ErrBlocked, err)
	}
	sum := sha256.Sum256(data)
	bound := false
	for _, source := range r.Sources {
		if source.Path == "budget/budget.json" && source.SHA256 == hex.EncodeToString(sum[:]) {
			bound = true
		}
	}
	var snapshot budget.Snapshot
	if err = audit.DecodeJSON(data, &snapshot, 1); err != nil || !bound || snapshot.HaltReason != "" {
		return result, errors.Join(ErrBlocked, err, errors.New("ledger is halted or no longer matches the audit"))
	}
	current, err := readFile(root, "campaign.json")
	if err != nil || !bytes.Equal(locked, current) {
		return result, errors.Join(ErrBlocked, err, errors.New("campaign changed during recovery check"))
	}
	if err = lease.Verify(); err != nil {
		return result, errors.Join(ErrBlocked, err)
	}
	if err = ctx.Err(); err == nil && action != nil {
		err = action(root, result)
		if err == nil {
			err = lease.Verify()
		}
	}
	return result, err
}

// 这里只读固定控制文件，逐层拒绝链接并限制大小；场景材料由审计 View 读取。
func readFile(root, name string) ([]byte, error) {
	full := root
	for _, part := range strings.Split(name, "/") {
		full = filepath.Join(full, part)
		info, err := os.Lstat(full)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, audit.ErrUnsafePath
		}
	}
	info, err := os.Lstat(full)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return nil, errors.Join(ErrBlocked, err)
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, err
	}
	opened, statErr := f.Stat()
	if statErr != nil || !os.SameFile(info, opened) {
		return nil, errors.Join(ErrBlocked, statErr, f.Close())
	}
	data, readErr := io.ReadAll(io.LimitReader(f, (16<<20)+1))
	err = errors.Join(readErr, f.Close())
	if len(data) > 16<<20 {
		err = errors.Join(err, ErrBlocked)
	}
	return data, err
}
