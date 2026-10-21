//go:build darwin || linux

package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/recovery"
	"github.com/secacy/agent-harness/internal/task"
)

func TestRecoveryRPCXControlledRerun(t *testing.T) {
	if os.Getenv("HARNESS_RECOVERY_ACCEPTANCE") != "1" {
		t.Skip("set HARNESS_RECOVERY_ACCEPTANCE=1 for actual SIGKILL and controlled rpcx rerun")
	}
	t.Setenv("HARNESS_CRASH_RECOVERY", "1")
	for _, mode := range []string{"baseline_passed", "candidate_fixed", "model_pending", "report_published", "run_terminal", "budget_exhausted"} {
		t.Run(mode, func(t *testing.T) {
			point := mode
			if mode == "budget_exhausted" {
				point = "model_pending"
				t.Setenv("HARNESS_CRASH_RECOVERY_BUDGET", "exhausted")
			}
			parent := t.TempDir()
			if output := os.Getenv("HARNESS_RECOVERY_ARTIFACTS"); output != "" {
				if err := os.MkdirAll(output, 0700); err != nil {
					t.Fatal(err)
				}
				var err error
				parent, err = os.MkdirTemp(output, "rerun-"+mode+"-")
				if err != nil {
					t.Fatal(err)
				}
			}
			parent, err := filepath.EvalSymlinks(parent)
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(parent, "campaign")
			cmd, ready, done, _ := startCrashChild(t, point, root, filepath.Join(parent, "child.log"))
			var barrier crashBarrier
			select {
			case event, ok := <-ready:
				if !ok {
					t.Fatal("child closed the barrier without reporting a checkpoint")
				}
				if event.Point != point || event.PID != cmd.Process.Pid || event.TargetID != "interrupted" {
					t.Fatal("owned child barrier mismatch", event)
				}
				barrier = event
			case err := <-done:
				t.Fatal("child exited before barrier", err)
			case <-time.After(4 * time.Minute):
				t.Fatal("child barrier timeout")
			}
			// 强杀前复读真实前缀，不能仅凭子进程发送的窗口名判断。
			if mode == "baseline_passed" || mode == "candidate_fixed" {
				assertRecoveryWindow(t, root, mode, barrier)
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
					t.Fatal("child did not terminate by SIGKILL", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("child was not reaped")
			}
			before := crashSnapshot(t, root)
			record := crashRead[campaign.Record](t, filepath.Join(root, "campaign.json"))
			plan := crashRead[campaign.Plan](t, filepath.Join(root, "plan.json"))
			request := recovery.Request{CampaignDir: root, OutputDir: filepath.Join(parent, "prepare"), ExpectedPlanSHA256: record.PlanSHA256, InspectRun: InspectRun,
				// 此屏障所有同步命令已返回，且父进程已确认所属子进程死亡；不证明任意残留资源。
				VerifyResourcesStopped: func(ctx context.Context, _ string) error { return ctx.Err() },
			}
			prepared, err := recovery.Prepare(context.Background(), recovery.PrepareRequest{Request: request, ID: "controlled-recovery"})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "baseline_passed" || mode == "candidate_fixed" {
				interrupted := prepared.State.Targets[1]
				if prepared.Audit.Consistency != "pass" || prepared.Audit.ExecutionState != "incomplete" || interrupted.Current != nil || interrupted.Resolution != "Interrupted" || len(interrupted.Attempts) != 1 || interrupted.Attempts[0].RunID != barrier.RunID {
					t.Fatal("unvalidated or unfinished run was adopted as a completed result", interrupted, prepared.Audit)
				}
			}
			selected := []string{"interrupted"}
			if mode == "run_terminal" {
				selected = []string{}
			}
			script := campaignConfig(t, []string{"correct"}, 1, execution.Local{})
			calls := 0
			runRequest := recovery.ExecuteRequest{Request: request, Targets: selected, Backend: execution.Local{}, Execute: CampaignExecutor(true), VerifyModel: func(context.Context, budget.Profile) error { return nil }, NewModel: func(task.Target) (budget.ProfiledModel, error) {
				m, err := script.NewModel(task.Target{ID: "correct"})
				if err != nil {
					return nil, err
				}
				return budgetTestModel{profile: plan.Profile, modelFunc: func(ctx context.Context, messages []agent.Message, tools []agent.ToolSpec) (agent.Reply, error) {
					calls++
					return m.Complete(ctx, messages, tools)
				}}, nil
			}}
			runRequest.OutputDir = filepath.Join(parent, "execute")
			result, err := recovery.Execute(context.Background(), runRequest)
			if mode == "budget_exhausted" {
				var denied *budget.Denied
				if !errors.As(err, &denied) || calls != 0 || result.Result != nil {
					t.Fatal("exhausted budget permitted a retry", result, calls, err)
				}
				if _, statErr := os.Stat(filepath.Join(root, campaign.RecoveryExecutionPath)); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatal("budget refusal allocated an execution")
				}
			} else {
				if err != nil || result.Result == nil || result.Audit.Consistency != "pass" || result.Audit.RecoveryExecutionState != "completed" {
					t.Fatal(result.Audit, err)
				}
				if mode == "run_terminal" {
					if calls != 0 || !reflect.DeepEqual(prepared.State.Budget, result.Result.Budget) || result.Result.Targets[1].Attempt.RunID != prepared.State.Targets[1].Current.RunID {
						t.Fatal("terminal adoption generated or changed history")
					}
				} else {
					newAttempt := result.Result.Targets[1].Attempt
					if calls != 2 || newAttempt.Status != campaign.Completed || newAttempt.Verdict != "ReadyForReview" || newAttempt.RunID == prepared.State.Targets[1].Attempts[0].RunID {
						t.Fatal("fresh baseline migration was not delivered", calls, newAttempt)
					}
					if result.Result.Budget.Totals.UnknownTokens != prepared.State.Budget.Totals.UnknownTokens || result.Result.Budget.Totals.KnownTokens != prepared.State.Budget.Totals.KnownTokens+40 || result.Result.Budget.Totals.Calls != prepared.State.Budget.Totals.Calls+2 {
						t.Fatal("retry reset the budget", prepared.State.Budget.Totals, result.Result.Budget.Totals)
					}
				}
				all := crashSnapshot(t, root)
				runRequest.OutputDir = filepath.Join(parent, "reenter")
				runRequest.NewModel = func(task.Target) (budget.ProfiledModel, error) {
					t.Fatal("completed recovery created another model")
					return nil, nil
				}
				second, err := recovery.Execute(context.Background(), runRequest)
				if err != nil || !reflect.DeepEqual(result.Result, second.Result) || !reflect.DeepEqual(all, crashSnapshot(t, root)) {
					t.Fatal("completed execution was repeated", err)
				}
			}
			original := map[string]string{}
			for p, hash := range crashSnapshot(t, root) {
				if p != "recoveries" && !strings.HasPrefix(p, "recoveries/") {
					original[p] = hash
				}
			}
			if !reflect.DeepEqual(before, original) {
				t.Fatal("old campaign, cache or Run was changed")
			}
			auditPut(t, parent, "evidence.json", map[string]any{"mode": mode, "barrier": barrier, "signal": "SIGKILL", "old_tree_unchanged": true, "controlled_model_calls": calls, "prepared_totals": prepared.State.Budget.Totals, "result": result.Result, "audit": result.Audit, "scope": "controlled local recovery; no provider request"})
			t.Log("controlled recovery evidence:", parent)
		})
	}
}

func assertRecoveryWindow(t *testing.T, root, mode string, barrier crashBarrier) {
	t.Helper()
	peer := crashRead[campaign.TargetRecord](t, filepath.Join(root, "targets/completed/target.json"))
	target := crashRead[campaign.TargetRecord](t, filepath.Join(root, "targets/interrupted/target.json"))
	if peer.State != campaign.Finished || peer.Attempt == nil || peer.Attempt.Status != campaign.Completed || target.State != campaign.Active || target.Attempt != nil {
		t.Fatal("checkpoint did not preserve a completed peer and active target", peer, target)
	}
	runDir := filepath.Join(root, "targets/interrupted/runs", barrier.RunID)
	run := crashRead[Record](t, filepath.Join(runDir, "run.json"))
	baseline := crashRead[BaselineReport](t, filepath.Join(runDir, "preflight/baseline.json"))
	ledger := crashRead[budget.Snapshot](t, filepath.Join(root, "budget/budget.json"))
	if run.ID != barrier.RunID || run.Status != StatusRunning || run.Verdict != "" || baseline.Status != "pass" {
		t.Fatal("checkpoint run identity or baseline is invalid", run, baseline.Status)
	}
	want := budget.Totals{Calls: 1, KnownTokens: 20}
	if mode == "baseline_passed" {
		if run.Phase != PhaseCheckingBaseline || ledger.Targets["interrupted"].Totals.Calls != 0 {
			t.Fatal("baseline checkpoint already generated", run.Phase, ledger.Totals)
		}
	} else {
		want = budget.Totals{Calls: 2, KnownTokens: 40}
		candidate, err := readTree(context.Background(), filepath.Join(runDir, "workspace/demo-orders"))
		if err != nil || run.Phase != PhaseValidating || barrier.CandidateSHA256 == "" || candidate.hash() != barrier.CandidateSHA256 || run.CandidateSHA256 != "" {
			t.Fatal("candidate checkpoint or unpublished pin mismatch", run, barrier, err)
		}
	}
	if ledger.Totals != want {
		t.Fatal("unexpected budget before kill", ledger.Totals, want)
	}
	for _, path := range []string{"artifacts/report.json", "evidence", "cache/validation"} {
		if _, err := os.Stat(filepath.Join(runDir, path)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("independent validation or publication started before the checkpoint", path, err)
		}
	}
}
