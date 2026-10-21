package recovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"time"

	"github.com/secacy/agent-harness/internal/audit"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/task"
)

var ErrInterrupted = errors.New("recovery execution already allocated; incomplete execution cannot be repeated")

type ExecuteRequest struct {
	Request
	// 非 nil 的空列表表示仅接纳检查点中的终态，不启动任何新尝试。
	Targets  []string
	Backend  execution.Executor
	Execute  campaign.Execute
	NewModel func(task.Target) (budget.ProfiledModel, error)
	// 可信宿主重新核实原 Profile 的能力与使用范围；不能更换原 Profile。
	VerifyModel func(context.Context, budget.Profile) error
}

type Executed struct {
	Result *campaign.RecoveryResult
	Audit  audit.Report
}

// Execute 在原 Campaign 锁内激活一次恢复执行，不自动重试已分配的尝试。
func Execute(ctx context.Context, request ExecuteRequest) (Executed, error) {
	return execute(ctx, request, nil)
}

func execute(ctx context.Context, request ExecuteRequest, observe func(string)) (out Executed, err error) {
	if request.Targets == nil {
		return out, ErrConfig
	}
	_, err = checkStopped(ctx, request.Request, true, func(root string, source Result) error {
		out.Audit = source.Audit
		var head campaign.RecoveryHead
		if err := readJSON(root, "recoveries/head.json", &head, 1); err != nil {
			return err
		}
		var checkpoint campaign.RecoveryCheckpoint
		if err := readJSON(root, head.Checkpoint.Path, &checkpoint, 1); err != nil {
			return err
		}
		var plan campaign.Plan
		if err := audit.DecodeJSON(checkpoint.Before["plan.json"], &plan, 0); err != nil {
			return err
		}
		dir := filepath.Join(root, campaign.RecoveryExecutionPath)
		if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
			var activation campaign.Activation
			if err := readJSON(root, campaign.RecoveryExecutionPath+"/activation.json", &activation, 1); err != nil {
				return errors.Join(ErrInterrupted, err)
			}
			ids := []string{}
			for _, r := range activation.Receipts {
				ids = append(ids, r.TargetID)
			}
			if !slices.Equal(ids, request.Targets) {
				return ErrConflict
			}
			var result campaign.RecoveryResult
			if err := readJSON(root, campaign.RecoveryExecutionPath+"/result.json", &result, 1); err != nil {
				return errors.Join(ErrInterrupted, err)
			}
			out.Result = &result
			return nil
		}
		now := time.Now().UTC()
		receipts, err := campaign.RecoveryReceipts(plan, checkpoint, request.Targets, now)
		if err != nil {
			return errors.Join(ErrBlocked, err)
		}
		models := map[string]budget.ProfiledModel{}
		if len(receipts) > 0 {
			if request.Backend == nil || request.Execute == nil || request.NewModel == nil || request.VerifyModel == nil {
				return ErrConfig
			}
			for _, r := range receipts {
				if err := budget.CanAdmit(checkpoint.After.Budget, r.TargetID); err != nil {
					return err
				}
			}
			if err := request.VerifyModel(ctx, plan.Profile); err != nil {
				return errors.Join(ErrBlocked, err)
			}
			for _, r := range receipts {
				i := slices.IndexFunc(plan.Targets, func(t campaign.Target) bool { return t.Input.ID == r.TargetID })
				model, err := request.NewModel(plan.Targets[i].Input)
				if err != nil {
					return err
				}
				if model == nil {
					return ErrConfig
				}
				profile, err := model.BudgetProfile()
				if err != nil || !reflect.DeepEqual(profile, plan.Profile) {
					return errors.Join(ErrBlocked, err)
				}
				models[r.TargetID] = model
			}
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if len(receipts) > 0 && !time.Now().Before(plan.Recovery.GenerationDeadline) {
			return context.DeadlineExceeded
		}
		if err = durableMkdir(dir); err != nil {
			return err
		}
		activation := campaign.Activation{SchemaVersion: 1, CampaignID: plan.ID, PlanSHA256: source.PlanSHA256, Checkpoint: head.Checkpoint, CreatedAt: now, Receipts: receipts}
		data, err := encode(activation)
		if err != nil {
			return err
		}
		if err = publishImmutable(filepath.Join(dir, "activation.json"), data); err != nil {
			return err
		}
		if observe != nil {
			observe("activation")
		}
		activationSHA := checksum(data)
		var ledger *budget.Ledger
		if len(receipts) > 0 {
			ledger, err = budget.Activate(filepath.Join(dir, "budget"), checkpoint.After.Budget, plan.Recovery.GenerationDeadline)
			if err != nil {
				return err
			}
			for _, r := range receipts {
				if err = ledger.BindRun(r.TargetID, r.RunID); err != nil {
					return err
				}
			}
		}
		if observe != nil {
			observe("ledger")
		}
		started := campaign.RecoveryStarted{SchemaVersion: 1, ActivationSHA256: activationSHA, StartedAt: time.Now().UTC()}
		if err = publishJSON(filepath.Join(dir, "started.json"), started); err != nil {
			return err
		}
		if observe != nil {
			observe("started")
		}
		result := campaign.RecoveryResult{SchemaVersion: 1, ActivationSHA256: activationSHA, StartedAt: started.StartedAt, Status: campaign.Completed, Targets: []campaign.TargetRecord{}, Budget: checkpoint.After.Budget}
		for _, t := range checkpoint.After.Targets {
			record := campaign.TargetRecord{ID: t.ID, State: campaign.Pending}
			if t.Current != nil {
				record.State, record.Attempt = campaign.Finished, t.Current
				if t.Current.Status != campaign.Completed {
					record.Error = "inherited execution failure"
				}
			}
			result.Targets = append(result.Targets, record)
		}
		if len(receipts) > 0 {
			work, cancel := context.WithDeadline(ctx, plan.Recovery.GenerationDeadline)
			defer cancel()
			commandBackend := request.Backend
			if plan.Resources != nil {
				commandBackend = execution.NewResourceGuard(commandBackend)
			}
			backend, err := execution.NewLimited(commandBackend, plan.ExecutionConcurrency)
			if err != nil {
				return err
			}
			for _, r := range receipts {
				index := slices.IndexFunc(plan.Targets, func(t campaign.Target) bool { return t.Input.ID == r.TargetID })
				target := plan.Targets[index]
				runs := filepath.Join(dir, "targets", r.TargetID, "runs")
				if err := mkdirParents(dir, r.TargetID); err != nil {
					return err
				}
				targetCtx, stop := context.WithTimeout(work, target.Timeout)
				services := campaign.Services{RunsDir: runs, RunID: r.RunID, Model: models[r.TargetID], Budget: budget.Target{Ledger: ledger, ID: r.TargetID}, Backend: backend}
				if plan.Resources != nil {
					services.ResourceOwner = &execution.ResourceOwner{Root: root, Registry: execution.ResourceRegistry{SchemaVersion: 1, CampaignID: plan.ID, PlanSHA256: source.PlanSHA256, Policy: *plan.Resources}, TargetID: r.TargetID}
				}
				attempt, runErr := request.Execute(targetCtx, target, services)
				stop()
				checked, checkErr := campaign.CheckAttempt(root, runs, r.TargetID, attempt, runErr)
				if attempt.RunID != "" && attempt.RunID != r.RunID || checked.Status == campaign.Completed && ledger.Snapshot().Targets[r.TargetID].RunID != r.RunID {
					return campaign.ErrResult
				}
				if checkErr != nil {
					return checkErr
				}
				record := campaign.TargetRecord{ID: r.TargetID, State: campaign.Finished, Attempt: &checked}
				if runErr != nil {
					record.Error = runErr.Error()
				}
				result.Targets[index] = record
				if ledger.Err() != nil {
					result.Status, result.Error = campaign.Failed, ledger.Err().Error()
					break
				}
				if work.Err() != nil {
					result.Status, result.Error = campaign.Cancelled, work.Err().Error()
					break
				}
			}
			result.Budget = ledger.Snapshot()
		}
		result.FinishedAt = time.Now().UTC()
		if err = publishJSON(filepath.Join(dir, "result.json"), result); err != nil {
			return err
		}
		out.Result = &result
		if observe != nil {
			observe("result")
		}
		out.Audit, err = audit.Inspect(context.WithoutCancel(ctx), audit.Request{CampaignDir: root, OutputDir: filepath.Join(request.OutputDir, "executed")}, request.InspectRun)
		if err == nil && (out.Audit.Consistency != "pass" || !out.Audit.ScanComplete || out.Audit.InputsUnchanged == nil || !*out.Audit.InputsUnchanged) {
			err = ErrBlocked
		}
		if result.Status == campaign.Cancelled {
			if ctx.Err() != nil {
				err = errors.Join(err, ctx.Err())
			} else {
				err = errors.Join(err, context.DeadlineExceeded)
			}
		} else if result.Status == campaign.Failed {
			err = errors.Join(err, campaign.ErrControl)
		}
		return err
	})
	return out, errors.Join(err, ctx.Err())
}

func readJSON(root, name string, value any, version int) error {
	data, err := readFile(root, name)
	if err != nil {
		return err
	}
	return audit.DecodeJSON(data, value, version)
}
func publishJSON(name string, value any) error {
	data, err := encode(value)
	if err != nil {
		return err
	}
	return publishImmutable(name, data)
}
func durableMkdir(dir string) error {
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(dir))
}
func checksum(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func mkdirParents(root, id string) error {
	for _, dir := range []string{filepath.Join(root, "targets"), filepath.Join(root, "targets", id), filepath.Join(root, "targets", id, "runs")} {
		if err := durableMkdir(dir); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	return nil
}
