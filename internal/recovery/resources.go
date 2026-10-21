package recovery

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/secacy/agent-harness/internal/audit"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/writer"
)

type CleanupRequest struct {
	Request
	Docker *execution.Docker
}
type CleanupResult struct {
	Before    audit.Report
	Resources execution.ResourceReport
	After     audit.Report
}

func resourceRegistry(root, pin string) (execution.ResourceRegistry, error) {
	var record campaign.Record
	if err := readJSON(root, "campaign.json", &record, 2); err != nil {
		return execution.ResourceRegistry{}, err
	}
	data, err := readFile(root, "plan.json")
	if err != nil {
		return execution.ResourceRegistry{}, err
	}
	var plan campaign.Plan
	if err = audit.DecodeJSON(data, &plan, 0); err != nil {
		return execution.ResourceRegistry{}, err
	}
	if record.WriterProtocol != writer.Protocol || record.Directory != root || record.PlanSHA256 != pin || checksum(data) != pin || record.ID != plan.ID || plan.Resources == nil || plan.Resources.Validate() != nil {
		return execution.ResourceRegistry{}, ErrUnsupported
	}
	return execution.ResourceRegistry{SchemaVersion: 1, CampaignID: record.ID, PlanSHA256: pin, Policy: *plan.Resources}, nil
}

// DockerResourcesStopped 是实时只读检查器，供持锁的 Check/Prepare/Execute 重新验证。
// 它不修改日志，不清理容器，也不能用锁外保存的结果授权后续执行。
func DockerResourcesStopped(d *execution.Docker) func(context.Context, string) error {
	return func(ctx context.Context, root string) error {
		if d == nil {
			return ErrResourcesUnknown
		}
		var record campaign.Record
		if err := readJSON(root, "campaign.json", &record, 2); err != nil {
			return err
		}
		registry, err := resourceRegistry(root, record.PlanSHA256)
		if err != nil {
			return err
		}
		_, err = d.ReconcileResources(ctx, root, registry, false)
		return err
	}
}

// CleanupResources 在原写锁内静态核对后清理，再实时确认静止；不修改旧 Run 或账目。
// 即使返回成功，之后恢复仍须重新取得锁并使用实时资源检查器。
func CleanupResources(ctx context.Context, request CleanupRequest) (out CleanupResult, err error) {
	if request.Docker == nil || request.InspectRun == nil || request.CampaignDir == "" || request.OutputDir == "" {
		return out, ErrConfig
	}
	root, err := filepath.EvalSymlinks(request.CampaignDir)
	if err != nil {
		return out, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return out, err
	}
	if _, err = resourceRegistry(root, request.ExpectedPlanSHA256); err != nil {
		return out, err
	}
	lease, err := writer.Open(root)
	if err != nil {
		return out, err
	}
	defer func() { err = errors.Join(err, lease.Close()) }()
	registry, err := resourceRegistry(root, request.ExpectedPlanSHA256)
	if err != nil {
		return out, err
	}
	out.Before, err = audit.Inspect(ctx, audit.Request{CampaignDir: root, OutputDir: request.OutputDir}, request.InspectRun)
	if err != nil {
		return out, err
	}
	if out.Before.Consistency != "pass" || !out.Before.ScanComplete || out.Before.InputsUnchanged == nil || !*out.Before.InputsUnchanged {
		return out, ErrBlocked
	}
	if err = lease.Verify(); err != nil {
		return out, err
	}
	out.Resources, err = request.Docker.ReconcileResources(ctx, root, registry, true)
	saveErr := publishJSON(filepath.Join(request.OutputDir, "cleanup.json"), out.Resources)
	if err = errors.Join(err, saveErr); err != nil {
		return out, err
	}
	if _, err = request.Docker.ReconcileResources(ctx, root, registry, false); err != nil {
		return out, err
	}
	out.After, err = audit.Inspect(ctx, audit.Request{CampaignDir: root, OutputDir: filepath.Join(request.OutputDir, "after")}, request.InspectRun)
	if err != nil {
		return out, err
	}
	if out.After.Consistency != "pass" || !out.After.ScanComplete || out.After.InputsUnchanged == nil || !*out.After.InputsUnchanged {
		return out, ErrBlocked
	}
	return out, lease.Verify()
}
