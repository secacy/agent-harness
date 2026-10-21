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

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/recovery"
)

func TestRecoveryRPCXInterruptedCheckpoints(t *testing.T) {
	if os.Getenv("HARNESS_RECOVERY_ACCEPTANCE") != "1" {
		t.Skip("set HARNESS_RECOVERY_ACCEPTANCE=1 for actual rpcx interruption and checkpoint acceptance")
	}
	for _, point := range []string{"model_pending", "report_published", "run_terminal"} {
		t.Run(point, func(t *testing.T) {
			parent := t.TempDir()
			if output := os.Getenv("HARNESS_RECOVERY_ARTIFACTS"); output != "" {
				if err := os.MkdirAll(output, 0700); err != nil {
					t.Fatal(err)
				}
				var err error
				parent, err = os.MkdirTemp(output, "rpcx-"+point+"-")
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
			select {
			case event, ok := <-ready:
				if !ok || event.Point != point || event.PID != cmd.Process.Pid || event.TargetID != "interrupted" {
					t.Fatal("owned child did not reach the requested barrier", event)
				}
			case err := <-done:
				t.Fatal("child exited before the barrier", err)
			case <-time.After(4 * time.Minute):
				t.Fatal("checkpoint timed out")
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
					t.Fatal("child was not killed by SIGKILL", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("child was not reaped")
			}
			before := crashSnapshot(t, root)
			record := crashRead[campaign.Record](t, filepath.Join(root, "campaign.json"))
			ledger := crashRead[budget.Snapshot](t, filepath.Join(root, "budget/budget.json"))
			request := recovery.PrepareRequest{ID: "recovery-one", Request: recovery.Request{
				CampaignDir: root, OutputDir: filepath.Join(parent, "prepare-1"), ExpectedPlanSHA256: record.PlanSHA256, InspectRun: InspectRun,
				// 这三个屏障的同步命令均已返回；等待所属宿主死亡后无本测试执行资源。
				VerifyResourcesStopped: func(ctx context.Context, _ string) error { return ctx.Err() },
			}}
			prepared, err := recovery.Prepare(context.Background(), request)
			if err != nil || prepared.CommitState != "Prepared" || prepared.Audit.Consistency != "pass" || prepared.Audit.ExecutionState != "incomplete" {
				t.Fatal(prepared, err)
			}
			after := prepared.State.Budget.Totals
			if after.Calls != ledger.Totals.Calls || after.KnownTokens != ledger.Totals.KnownTokens || after.ReservedTokens != 0 || after.UnknownTokens != ledger.Totals.UnknownTokens+ledger.Totals.ReservedTokens {
				t.Fatal("recovery changed cumulative cost", ledger.Totals, after)
			}
			if len(prepared.State.Targets) != 2 || prepared.State.Targets[0].Resolution != "Recorded" || prepared.State.Targets[0].Current.Status != campaign.Completed {
				t.Fatal("completed peer was not retained", prepared.State.Targets)
			}
			interrupted := prepared.State.Targets[1]
			if point == "run_terminal" {
				if interrupted.Resolution != "Adopted" || interrupted.Current == nil || interrupted.Current.Status != campaign.Completed {
					t.Fatal("fully published run was not adopted", interrupted)
				}
			} else if interrupted.Resolution != "Interrupted" || interrupted.Current != nil || interrupted.Attempts[0].ObservedStatus != "Running" {
				t.Fatal("incomplete run was promoted", interrupted)
			}
			if point == "model_pending" && (after.UnknownTokens != 100 || after.KnownTokens != 20 || ledger.Totals.ReservedTokens != 100) {
				t.Fatal(ledger.Totals, after)
			}
			full := crashSnapshot(t, root)
			original := map[string]string{}
			for p, hash := range full {
				if p != "recoveries" && !strings.HasPrefix(p, "recoveries/") {
					original[p] = hash
				}
			}
			if !reflect.DeepEqual(before, original) {
				t.Fatal("original source bytes or members changed")
			}
			request.OutputDir = filepath.Join(parent, "prepare-2")
			second, err := recovery.Prepare(context.Background(), request)
			if err != nil || prepared.Checkpoint != second.Checkpoint || !reflect.DeepEqual(full, crashSnapshot(t, root)) {
				t.Fatal("second prepare changed committed history", err)
			}
			auditPut(t, parent, "evidence.json", map[string]any{"point": point, "signal": "SIGKILL", "source_unchanged": true, "second_prepare_unchanged": true, "before_totals": ledger.Totals, "after_totals": after, "targets": prepared.State.Targets, "checkpoint": prepared.Checkpoint, "scope": "checkpoint preparation only; no retry or provider request"})
			t.Logf("rpcx %s checkpoint prepared: %s", point, parent)
		})
	}
}
