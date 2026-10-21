//go:build darwin || linux

package budget

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
)

const (
	reservedBeforeModel      = "reserved_before_model"
	responseBeforeSettlement = "response_before_settlement"
)

type budgetCrashBarrier struct {
	Point         string       `json:"point"`
	PID           int          `json:"pid"`
	ModelCalls    int          `json:"model_calls"`
	ReturnedUsage *agent.Usage `json:"returned_usage,omitempty"`
}

// 子进程替身只包裹真实快照写入，窗口不能由生产配置开启。
func TestBudgetCrashHelper(t *testing.T) {
	root := os.Getenv("HARNESS_BUDGET_CRASH_ROOT")
	if root == "" {
		t.Skip("subprocess helper")
	}
	point := os.Getenv("HARNESS_BUDGET_CRASH_POINT")
	if point != reservedBeforeModel && point != responseBeforeSettlement {
		t.Fatal("unknown budget checkpoint")
	}
	tokens := int64(220)
	if os.Getenv("HARNESS_BUDGET_CRASH_EXHAUSTED") == "1" {
		tokens = 100
	}
	limits := Limits{MaxCalls: 2, MaxTokens: tokens}
	ledger, err := Create(Config{Directory: filepath.Join(root, "original"), CampaignID: "budget-crash", Profile: testProfile(), Limits: limits, Targets: map[string]Limits{"a": limits}})
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	model, err := NewModel(ledger, "a", "old-run", controlledModel{profile: testProfile(), complete: func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
		called++
		return agent.Reply{Usage: usage(15, 5)}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	persist := ledger.persist
	ledger.persist = func(next Snapshot) error {
		if point == responseBeforeSettlement && len(next.Calls) == 1 && next.Calls[0].State == StateSettled {
			pauseBudgetProcess(t, budgetCrashBarrier{point, os.Getpid(), called, next.Calls[0].Usage})
		}
		if err := persist(next); err != nil {
			return err
		}
		if point == reservedBeforeModel && len(next.Calls) == 1 && next.Calls[0].State == StateReserved {
			pauseBudgetProcess(t, budgetCrashBarrier{point, os.Getpid(), called, nil})
		}
		return nil
	}
	_, err = model.Complete(context.Background(), nil, nil)
	t.Fatal("model returned without the requested crash barrier", err)
}

func pauseBudgetProcess(t *testing.T, event budgetCrashBarrier) {
	t.Helper()
	pipe := os.NewFile(3, "budget-barrier")
	if pipe == nil {
		t.Fatal("budget checkpoint pipe is unavailable")
	}
	if err := json.NewEncoder(pipe).Encode(event); err != nil {
		t.Fatal(err)
	}
	if err := pipe.Close(); err != nil {
		t.Fatal(err)
	}
	var one [1]byte
	_, err := os.Stdin.Read(one[:])
	t.Fatal("budget checkpoint must exit only through SIGKILL", err)
}

func startBudgetCrashChild(t *testing.T, root, point string, exhausted bool) (*exec.Cmd, <-chan budgetCrashBarrier, <-chan error) {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(root, "child.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestBudgetCrashHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "HARNESS_BUDGET_CRASH_ROOT="+root, "HARNESS_BUDGET_CRASH_POINT="+point, "HARNESS_BUDGET_CRASH_EXHAUSTED=0")
	if exhausted {
		cmd.Env[len(cmd.Env)-1] = "HARNESS_BUDGET_CRASH_EXHAUSTED=1"
	}
	cmd.ExtraFiles = []*os.File{write}
	cmd.Stdout, cmd.Stderr = log, log
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	write.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); close(done) }()
	ready := make(chan budgetCrashBarrier, 1)
	go func() {
		var event budgetCrashBarrier
		if json.NewDecoder(read).Decode(&event) == nil {
			ready <- event
		}
		close(ready)
	}()
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = stdin.Close(); _ = read.Close(); _ = log.Close() })
	return cmd, ready, done
}

func TestBudgetSIGKILLPersistenceWindows(t *testing.T) {
	for _, point := range []string{reservedBeforeModel, responseBeforeSettlement} {
		for _, exhausted := range []bool{false, true} {
			name := point + "/available"
			if exhausted {
				name = point + "/exhausted"
			}
			t.Run(name, func(t *testing.T) {
				root := t.TempDir()
				if output := os.Getenv("HARNESS_BUDGET_CRASH_ARTIFACTS"); output != "" {
					if err := os.MkdirAll(output, 0700); err != nil {
						t.Fatal(err)
					}
					var err error
					root, err = os.MkdirTemp(output, point+"-")
					if err != nil {
						t.Fatal(err)
					}
				}
				cmd, ready, done := startBudgetCrashChild(t, root, point, exhausted)
				var barrier budgetCrashBarrier
				select {
				case event, ok := <-ready:
					if !ok {
						t.Fatal("child closed its checkpoint without an event", root)
					}
					barrier = event
				case err := <-done:
					t.Fatal("child exited before its checkpoint", err, root)
				case <-time.After(20 * time.Second):
					t.Fatal("budget checkpoint timed out", root)
				}
				if barrier.PID != cmd.Process.Pid || barrier.Point != point || point == reservedBeforeModel && (barrier.ModelCalls != 0 || barrier.ReturnedUsage != nil) || point == responseBeforeSettlement && (barrier.ModelCalls != 1 || !reflect.DeepEqual(barrier.ReturnedUsage, usage(15, 5))) {
					t.Fatal("checkpoint does not establish the requested model boundary", barrier)
				}
				path := filepath.Join(root, "original/budget.json")
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var source Snapshot
				if err = json.Unmarshal(raw, &source); err != nil || ValidateSnapshot(source) != nil || source.Totals != (Totals{Calls: 1, ReservedTokens: 100}) || len(source.Calls) != 1 || source.Calls[0].State != StateReserved || source.Calls[0].Usage != nil {
					t.Fatal("disk is not the authentic unsettled reservation", source, err)
				}
				if err := cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					var exit *exec.ExitError
					if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
						t.Fatal("child did not exit through SIGKILL", err)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("budget child was not reaped")
				}
				seed, err := RecoverSnapshot(source, "recovery", time.Now())
				if err != nil || seed.Totals != (Totals{Calls: 1, UnknownTokens: 100}) || seed.Targets["a"].Totals != seed.Totals || seed.Calls[0].Usage != nil {
					t.Fatal("recovery refunded the interrupted reservation", seed, err)
				}
				again, err := RecoverSnapshot(seed, "recovery", time.Now())
				if err != nil || !reflect.DeepEqual(seed, again) {
					t.Fatal("repeated conversion changed costs", err)
				}
				ledger, err := Activate(filepath.Join(root, "resumed"), seed, time.Now().Add(time.Minute))
				if err != nil {
					t.Fatal(err)
				}
				if err := ledger.BindRun("a", "old-run"); err == nil {
					t.Fatal("historical Run rebound")
				}
				calls := 0
				model, err := NewModel(ledger, "a", "new-run", controlledModel{profile: seed.Profile, complete: func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
					calls++
					return agent.Reply{Usage: usage(15, 5)}, nil
				}})
				if err != nil {
					t.Fatal(err)
				}
				_, err = model.Complete(context.Background(), nil, nil)
				final := disk(t, ledger)
				if exhausted {
					var denied *Denied
					if !errors.As(err, &denied) || calls != 0 || final.Totals != seed.Totals {
						t.Fatal("exhausted recovery called the model or reset costs", final, calls, err)
					}
				} else {
					if err != nil || calls != 1 || final.Totals != (Totals{Calls: 2, KnownTokens: 20, UnknownTokens: 100}) || final.Calls[1].RunID != "new-run" {
						t.Fatal("resumed model did not inherit costs", final, calls, err)
					}
					if err := ledger.settle(final.Calls[1].ID, usage(15, 5), "reply"); err != nil || !reflect.DeepEqual(final, disk(t, ledger)) {
						t.Fatal("identical settlement was not idempotent", err)
					}
				}
				if ValidateSnapshot(final) != nil || final.Calls[0].State != StateUnknown || final.Calls[0].RunID != "old-run" || final.Calls[0].RecoveryID != "recovery" {
					t.Fatal("historical call provenance changed", final)
				}
				if _, err := Activate(filepath.Join(root, "resumed"), seed, time.Now().Add(time.Minute)); !errors.Is(err, os.ErrExist) {
					t.Fatal("active ledger was reopened", err)
				}
				// 测试证人携带的完整 usage 不能事后退款；冲突结算必须停准入。
				if err := ledger.settle(source.Calls[0].ID, usage(15, 5), "reply"); !errors.Is(err, ErrIntegrity) || ledger.Err() == nil || ledger.Snapshot().Totals != final.Totals {
					t.Fatal("interrupted call was resettled", err)
				}
				if _, err := model.Complete(context.Background(), nil, nil); !errors.Is(err, ErrHalted) || calls != int(final.Totals.Calls-1) {
					t.Fatal("conflicting settlement did not stop model admission", calls, err)
				}
				unchanged, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(raw, unchanged) {
					t.Fatal("original snapshot changed", err)
				}
				sum := sha256.Sum256(raw)
				evidence := map[string]any{"barrier": barrier, "exhausted": exhausted, "signal": "SIGKILL", "original_sha256": hex.EncodeToString(sum[:]), "source": source, "recovered": seed, "final_before_conflict": final, "conflict_halted": ledger.Err() != nil, "resumed_model_calls": calls, "original_unchanged": true, "scope": "budget component; controlled model; no Campaign or provider request"}
				data, err := json.MarshalIndent(evidence, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "evidence.json"), append(data, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
				t.Log("budget persistence evidence:", root)
			})
		}
	}
}
