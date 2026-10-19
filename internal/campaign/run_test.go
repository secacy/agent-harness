package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/task"
)

type testModel struct {
	profile  budget.Profile
	complete func(context.Context) (agent.Reply, error)
}

func (m testModel) BudgetProfile() (budget.Profile, error) { return m.profile, nil }
func (m testModel) Complete(ctx context.Context, _ []agent.Message, _ []agent.ToolSpec) (agent.Reply, error) {
	return m.complete(ctx)
}

func testConfig(t *testing.T, count int) Config {
	t.Helper()
	base := t.TempDir()
	profile := budget.Profile{Provider: "controlled", Endpoint: "in-process", Model: "fixed", ContextTokens: 90, MaxOutputTokens: 10, SourceSHA256: strings.Repeat("a", 64), VerifiedAt: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)}
	config := Config{Directory: filepath.Join(base, "campaign"), Plan: Plan{ID: "test", Concurrency: 2, ExecutionConcurrency: 2, Budget: budget.Limits{MaxCalls: 100, MaxTokens: 10000}, Profile: profile}, Backend: execution.Local{}}
	for i := range count {
		input := task.Target{ID: fmt.Sprintf("target-%d", i), ProfileID: "controlled-v1", BuildConfig: "default", Repository: task.Snapshot{Directory: filepath.Join(base, "inputs", fmt.Sprint(i)), SHA256: strings.Repeat("b", 64)}, Specification: task.Specification{Path: filepath.Join(base, "spec.md"), SHA256: strings.Repeat("c", 64)}}
		config.Plan.Targets = append(config.Plan.Targets, Target{Input: input, Budget: budget.Limits{MaxCalls: 5, MaxTokens: 500}, Limits: agent.Limits{MaxModelCalls: 5, MaxToolCalls: 10}, Timeout: 5 * time.Second})
	}
	config.NewModel = func(task.Target) (budget.ProfiledModel, error) {
		return testModel{profile, func(context.Context) (agent.Reply, error) {
			return agent.Reply{Finish: agent.FinishStop, Usage: &agent.Usage{InputTokens: 15, OutputTokens: 5, TotalTokens: 20}}, nil
		}}, nil
	}
	config.Execute = func(ctx context.Context, target Target, s Services) (Attempt, error) {
		model, err := budget.NewModel(s.Budget.Ledger, target.Input.ID, "run-1", s.Model)
		if err == nil {
			_, err = model.Complete(ctx, nil, nil)
		}
		return publish(t, target, s, "ReadyForReview", err)
	}
	return config
}

// 只构造调度契约材料，不代表业务门禁真的做出了这些质量判定。
func publish(t *testing.T, target Target, s Services, verdict string, runErr error) (Attempt, error) {
	t.Helper()
	directory := filepath.Join(s.RunsDir, "run-1")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Error(err)
		return Attempt{Status: Failed}, err
	}
	status := Completed
	if runErr != nil {
		status = Failed
		verdict = ""
		if errors.Is(runErr, context.Canceled) {
			status = Cancelled
		}
	}
	attempt := Attempt{RunID: "run-1", Directory: directory, Status: status, Verdict: verdict}
	disk := map[string]any{"id": attempt.RunID, "target_id": target.Input.ID, "directory": directory, "status": status, "verdict": verdict}
	if _, err := writeJSON(filepath.Join(directory, "run.json"), disk); err != nil {
		t.Error(err)
	}
	path := filepath.Join(directory, "report.json")
	ref, err := writeJSON(path, map[string]any{"run_id": attempt.RunID, "target_id": target.Input.ID, "status": status, "verdict": verdict})
	if err != nil {
		t.Error(err)
	}
	ref.Path = path
	attempt.Report = ref
	return attempt, runErr
}
func read[T any](t *testing.T, path string) T {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value T
	if err = json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestCampaignBoundedAndOrdered(t *testing.T) {
	for _, limit := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			config := testConfig(t, 7)
			config.Plan.Concurrency = limit
			original := config.Execute
			entered, release := make(chan struct{}, limit), make(chan struct{})
			var active, peak atomic.Int32
			config.Execute = func(ctx context.Context, target Target, s Services) (Attempt, error) {
				n := active.Add(1)
				defer active.Add(-1)
				for old := peak.Load(); old < n && !peak.CompareAndSwap(old, n); old = peak.Load() {
				}
				select {
				case entered <- struct{}{}:
				default:
				}
				select {
				case <-release:
				case <-ctx.Done():
					return Attempt{Status: Cancelled}, ctx.Err()
				}
				return original(ctx, target, s)
			}
			done := make(chan struct{})
			var record Record
			var summary Summary
			var err error
			go func() { record, summary, err = Run(context.Background(), config); close(done) }()
			for range limit {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("targets did not start")
				}
			}
			close(release)
			<-done
			if err != nil || record.Status != Completed || summary.Counts.Completed != 7 || peak.Load() != int32(limit) || summary.Budget.Totals.Calls != 7 || summary.Budget.Totals.KnownTokens != 140 {
				t.Fatalf("record=%+v counts=%+v peak=%d budget=%+v err=%v", record, summary.Counts, peak.Load(), summary.Budget.Totals, err)
			}
			if checksum(mustRead(t, filepath.Join(record.Directory, record.Summary.Path))) != record.Summary.SHA256 {
				t.Fatal("summary digest mismatch")
			}
			for i, target := range summary.Targets {
				if target.ID != config.Plan.Targets[i].Input.ID || target.State != Finished || target.Attempt.Report.Path == "" {
					t.Fatal(target)
				}
				saved := read[TargetRecord](t, filepath.Join(record.Directory, "targets", target.ID, "target.json"))
				if saved.Attempt.RunID != "run-1" {
					t.Fatal(saved)
				}
			}
		})
	}
}
func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCampaignLocalFailureAndMixedVerdicts(t *testing.T) {
	config := testConfig(t, 4)
	config.Execute = func(ctx context.Context, target Target, s Services) (Attempt, error) {
		if target.Input.ID == "target-1" {
			return publish(t, target, s, "", errors.New("injected baseline failure"))
		}
		model, err := budget.NewModel(s.Budget.Ledger, target.Input.ID, "run-1", s.Model)
		if err == nil {
			_, err = model.Complete(ctx, nil, nil)
		}
		verdict := map[string]string{"target-0": "ReadyForReview", "target-2": "Rejected", "target-3": "NeedsAttention"}[target.Input.ID]
		return publish(t, target, s, verdict, err)
	}
	record, summary, err := Run(context.Background(), config)
	if err != nil || record.Status != Completed || summary.Counts.Failed != 1 || summary.Counts.Completed != 3 || summary.Budget.Totals.Calls != 3 || len(summary.Counts.Verdicts) != 3 {
		t.Fatalf("%+v %+v %v", record, summary, err)
	}
	if _, _, err = Run(context.Background(), config); !errors.Is(err, ErrConfig) {
		t.Fatal("existing campaign accepted", err)
	}
}

func TestCampaignBudgetShortageAndUnknownUsage(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprint(unknown), func(t *testing.T) {
			config := testConfig(t, 2)
			config.Plan.Concurrency = 1
			var calls atomic.Int32
			config.NewModel = func(task.Target) (budget.ProfiledModel, error) {
				return testModel{config.Plan.Profile, func(context.Context) (agent.Reply, error) {
					calls.Add(1)
					var usage *agent.Usage
					if !unknown {
						usage = &agent.Usage{InputTokens: 15, OutputTokens: 5, TotalTokens: 20}
					}
					return agent.Reply{Finish: agent.FinishStop, Usage: usage}, nil
				}}, nil
			}
			if unknown {
				config.Plan.Budget.MaxTokens = 100
			} else {
				config.Plan.Targets[0].Budget.MaxTokens = 50
			}
			record, summary, err := Run(context.Background(), config)
			if err != nil || record.Status != Completed || calls.Load() != 1 || summary.Counts.Failed != 1 || summary.Counts.Completed != 1 || summary.Budget.Totals.ReservedTokens != 0 || len(summary.Budget.Rejections) != 1 {
				t.Fatalf("%+v %+v %v", record, summary, err)
			}
			if unknown && summary.Budget.Totals.UnknownTokens != 100 {
				t.Fatal(summary.Budget.Totals)
			}
		})
	}
}

func TestCampaignIntegrityFaultCancelsActiveAndSkipsPending(t *testing.T) {
	config := testConfig(t, 3)
	secondStarted := make(chan struct{})
	config.NewModel = func(target task.Target) (budget.ProfiledModel, error) {
		return testModel{config.Plan.Profile, func(ctx context.Context) (agent.Reply, error) {
			if target.ID == "target-0" {
				select {
				case <-secondStarted:
				case <-ctx.Done():
					return agent.Reply{}, ctx.Err()
				}
				return agent.Reply{Usage: &agent.Usage{InputTokens: 100, OutputTokens: 1, TotalTokens: 101}}, nil
			}
			if target.ID == "target-1" {
				close(secondStarted)
				<-ctx.Done()
				return agent.Reply{}, ctx.Err()
			}
			t.Error("pending target called model")
			return agent.Reply{}, nil
		}}, nil
	}
	record, summary, err := Run(context.Background(), config)
	if !errors.Is(err, ErrControl) || !errors.Is(err, budget.ErrIntegrity) || record.Status != Failed || summary.Counts.Skipped != 1 || summary.Budget.Totals.Calls != 2 || summary.Budget.Totals.ReservedTokens != 0 || summary.Budget.Totals.UnknownTokens != 200 {
		t.Fatalf("%+v %+v %v", record, summary, err)
	}
}

func TestCampaignCancellationDrainsAndDoesNotAdmitNext(t *testing.T) {
	config := testConfig(t, 3)
	config.Plan.Concurrency = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var cleanup atomic.Bool
	config.Execute = func(ctx context.Context, target Target, s Services) (Attempt, error) {
		cancel()
		<-ctx.Done()
		cleanup.Store(true)
		return publish(t, target, s, "", ctx.Err())
	}
	record, summary, err := Run(ctx, config)
	if !errors.Is(err, context.Canceled) || record.Status != Cancelled || summary.Counts.Cancelled != 1 || summary.Counts.Skipped != 2 || !cleanup.Load() || summary.Budget.Totals.Calls != 0 {
		t.Fatalf("%+v %+v %v", record, summary, err)
	}
}

func TestCampaignPublishingFailurePreservesCompletedRun(t *testing.T) {
	for _, destination := range []string{"target", "summary"} {
		t.Run(destination, func(t *testing.T) {
			config := testConfig(t, 2)
			config.Plan.Concurrency = 1
			original := config.Execute
			var originalRun []byte
			var runPath string
			config.Execute = func(ctx context.Context, target Target, s Services) (Attempt, error) {
				attempt, err := original(ctx, target, s)
				if target.Input.ID == "target-0" {
					runPath = filepath.Join(attempt.Directory, "run.json")
					originalRun = mustRead(t, runPath)
					path := filepath.Join(config.Directory, "summary.json")
					if destination == "target" {
						path = filepath.Join(config.Directory, "targets", target.Input.ID, "target.json")
						if e := os.Remove(path); e != nil {
							t.Error(e)
						}
					}
					if e := os.Mkdir(path, 0700); e != nil {
						t.Error(e)
					}
				}
				return attempt, err
			}
			record, summary, err := Run(context.Background(), config)
			if !errors.Is(err, ErrControl) || record.Status != Failed || string(mustRead(t, runPath)) != string(originalRun) {
				t.Fatalf("%+v %+v %v", record, summary, err)
			}
			if destination == "target" && summary.Counts.Skipped != 1 {
				t.Fatal(summary.Counts)
			}
			if destination == "summary" && record.Summary.Path != "" {
				t.Fatal(record.Summary)
			}
		})
	}
}

func TestCampaignRejectsUnboundOrIncompleteResults(t *testing.T) {
	for _, fault := range []string{"hash", "cross-target", "terminal", "verdict", "missing", "callback-error", "budget-binding"} {
		t.Run(fault, func(t *testing.T) {
			config := testConfig(t, 1)
			original := config.Execute
			config.Execute = func(ctx context.Context, target Target, s Services) (Attempt, error) {
				var attempt Attempt
				var err error
				if fault == "budget-binding" {
					attempt, err = publish(t, target, s, "ReadyForReview", nil)
				} else {
					attempt, err = original(ctx, target, s)
				}
				switch fault {
				case "hash":
					attempt.Report.SHA256 = strings.Repeat("0", 64)
				case "cross-target":
					attempt.Directory = filepath.Dir(s.RunsDir)
				case "terminal":
					if e := os.WriteFile(filepath.Join(attempt.Directory, "run.json"), []byte(`{"status":"Running"}`), 0600); e != nil {
						t.Error(e)
					}
				case "verdict":
					attempt.Verdict = "invented"
				case "missing":
					attempt.Report = Artifact{}
				case "callback-error":
					err = errors.New("failed after publication")
				}
				return attempt, err
			}
			record, summary, err := Run(context.Background(), config)
			if err != nil || record.Status != Completed || summary.Counts.Failed != 1 || summary.Counts.Completed != 0 || summary.Targets[0].Attempt.Verdict != "" || summary.Targets[0].Attempt.FailureCode != "invalid_result" {
				t.Fatalf("%+v %+v %v", record, summary, err)
			}
		})
	}
}

func TestCampaignRejectsInvalidPlanBeforeCreatingOutput(t *testing.T) {
	for _, fault := range []string{"duplicate", "overlap", "symlink-overlap", "profile", "limits"} {
		t.Run(fault, func(t *testing.T) {
			config := testConfig(t, 2)
			switch fault {
			case "duplicate":
				config.Plan.Targets[1].Input.ID = config.Plan.Targets[0].Input.ID
			case "overlap":
				config.Plan.Targets[0].Input.Repository.Directory = config.Directory
			case "symlink-overlap":
				link := filepath.Join(filepath.Dir(config.Directory), "alias")
				if err := os.Symlink(filepath.Dir(config.Directory), link); err != nil {
					t.Fatal(err)
				}
				config.Plan.Targets[0].Input.Repository.Directory = filepath.Join(link, "campaign")
			case "profile":
				config.Plan.Profile.Model = "other"
			case "limits":
				config.Plan.Concurrency = 0
			}
			_, _, err := Run(context.Background(), config)
			if !errors.Is(err, ErrConfig) {
				t.Fatal(err)
			}
			if _, err = os.Stat(config.Directory); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("created output on invalid plan", err)
			}
		})
	}
}
