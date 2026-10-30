package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/internal/repotools"
	"github.com/secacy/agent-harness/internal/trace"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
)

// This fixture uses the production Loop, trace writer and quality aggregation,
// but no repository command or provider. It establishes replay orchestration,
// not migration effectiveness or compiler/test behavior.
func replayFixture(t *testing.T, withTrace, withBudget bool) (string, string) {
	t.Helper()
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "campaign", "targets", "orders", "runs", "historical-run")
	for _, name := range []string{"inputs/spec", "artifacts", "evidence", "baseline/orders", "workspace/orders"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := tree{"orders/main.go": []byte("package orders\n")}
	if err := files.write(ctx, filepath.Join(root, "baseline")); err != nil {
		t.Fatal(err)
	}
	if err := files.write(ctx, filepath.Join(root, "workspace")); err != nil {
		t.Fatal(err)
	}
	spec := []byte("test-only specification\n")
	if err := os.WriteFile(filepath.Join(root, "inputs/spec/spec.md"), spec, 0600); err != nil {
		t.Fatal(err)
	}
	p := profile{ID: "fixture", RepositoryName: "orders", SpecVersion: "fixture", SpecSHA256: digest(spec), RepositorySHA256: files.module("orders").hash()}
	if err := writeJSON(filepath.Join(root, "inputs/profile.json"), p); err != nil {
		t.Fatal(err)
	}
	input := Input{RepositoryName: "orders", ProfileSHA256: p.hash(), ModelName: "recorded", MaxModelCalls: 3, MaxToolCalls: 2, SpecVersion: "fixture", SpecSHA256: digest(spec), BaselineSHA256: p.RepositorySHA256, Files: files.manifest()}
	record := Record{SchemaVersion: 2, ID: "historical-run", TargetID: "orders", Directory: "/old/machine/run", Status: StatusCompleted, Phase: PhasePublishing, StartedAt: time.Now().UTC(), AgentExitReason: agent.ExitCompleted, CandidateSHA256: p.RepositorySHA256}
	now := time.Now().UTC()
	record.FinishedAt = &now
	record.UpdatedAt = now
	var rec *trace.Recorder
	var runSpan *trace.Span
	if withTrace {
		var err error
		rec, err = trace.New(filepath.Join(root, "trace.jsonl"), record.ID)
		if err != nil {
			t.Fatal(err)
		}
		ctx = trace.Context(ctx, rec)
		ctx, runSpan, err = trace.Start(ctx, "run", input)
		if err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	model := agent.Model(modelFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
		n++
		if n == 1 {
			return agent.Reply{Finish: agent.FinishToolCalls, ToolCalls: []agent.ToolCall{{ID: "read-one", Name: "read_file", Arguments: `{"root":"workspace","path":"main.go"}`}}, Usage: &agent.Usage{InputTokens: 2, OutputTokens: 1, TotalTokens: 3}}, nil
		}
		return agent.Reply{Finish: agent.FinishStop, Content: "submitted", Usage: &agent.Usage{InputTokens: 4, OutputTokens: 1, TotalTokens: 5}}, nil
	}))
	ledgerPath := ""
	if withBudget {
		profile := budget.Profile{Provider: "controlled", Endpoint: "in-process", Model: "recorded", ContextTokens: 90, MaxOutputTokens: 10, SourceSHA256: strings.Repeat("e", 64), VerifiedAt: time.Now().UTC()}
		ledgerPath = filepath.Join(root, "..", "..", "..", "..", "..", "budget", "budget.json")
		ledger, err := budget.Create(budget.Config{Directory: filepath.Dir(ledgerPath), CampaignID: "fixture", Profile: profile, Limits: budget.Limits{MaxCalls: 3, MaxTokens: 300}, Targets: map[string]budget.Limits{"orders": {MaxCalls: 3, MaxTokens: 300}}})
		if err != nil {
			t.Fatal(err)
		}
		wrapped, err := budget.NewModel(ledger, "orders", record.ID, budgetTestModel{modelFunc: model.Complete, profile: profile})
		if err != nil {
			t.Fatal(err)
		}
		model = wrapped
		ref := wrapped.Reference()
		input.Budget = &ref
	}
	if withTrace {
		prepared := input
		prepared.Budget = nil
		_, span, err := trace.Start(ctx, "prepared", prepared)
		if err != nil {
			t.Fatal(err)
		}
		if err = span.End(nil, nil); err != nil {
			t.Fatal(err)
		}
		_, span, err = trace.Start(ctx, "baseline_check", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = span.End(&BaselineReport{Status: "pass"}, nil); err != nil {
			t.Fatal(err)
		}
	}
	loop := agent.Loop{ModelName: input.ModelName, Model: model, Tools: repotools.Specifications([]string{"spec", "workspace"}, true), Limits: agent.Limits{MaxModelCalls: 3, MaxToolCalls: 2}, Executor: replayFixtureExecutor{}}
	result, err := loop.Run(ctx, []agent.Message{{Role: agent.RoleSystem, Content: "Available roots: workspace (writable), spec (read-only)."}, {Role: agent.RoleUser, Content: "task"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(root, "artifacts/agent.json"), result); err != nil {
		t.Fatal(err)
	}
	policy := quality.Policy{SpecSHA256: input.SpecSHA256, Requirements: []quality.Requirement{{ID: "build", RuleIDs: []string{"BUILD-1"}}}}
	if err := writeJSON(filepath.Join(root, "evidence/policy.json"), policy); err != nil {
		t.Fatal(err)
	}
	material, err := os.ReadFile(filepath.Join(root, "evidence/policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	binding := quality.Binding{SpecSHA256: input.SpecSHA256, BaselineSHA256: input.BaselineSHA256, CandidateSHA256: record.CandidateSHA256}
	evidence := []quality.Evidence{{ID: "policy", Kind: "host_policy", Path: "policy.json", SHA256: digest(material), Binding: binding}}
	check := quality.Check{Status: quality.StatusPass, Detail: "fixture result", Applicability: quality.Applicable, RuleIDs: []string{"BUILD-1"}, EvidenceIDs: []string{"policy"}, Findings: []quality.Finding{{Code: "fixture", Detail: "fixture only", RuleIDs: []string{"BUILD-1"}, EvidenceIDs: []string{"policy"}}}}
	decision, err := quality.Decide(filepath.Join(root, "evidence"), policy, binding, map[string]quality.Check{"build": check}, evidence)
	if err != nil {
		t.Fatal(err)
	}
	validation := checks.Evaluation{SchemaVersion: 3, SpecSHA256: binding.SpecSHA256, BaselineSHA256: binding.BaselineSHA256, CandidateSHA256: binding.CandidateSHA256, Evidence: evidence, Checks: map[string]checks.Check{"build": check}, Verdict: decision.Verdict, Gaps: decision.Gaps}
	record.Verdict = decision.Verdict
	if withTrace {
		_, gate, err := trace.Start(ctx, "gate_check", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = gate.End(validation, nil); err != nil {
			t.Fatal(err)
		}
	}
	report := Report{RunID: record.ID, Status: record.Status, Input: input, AgentRecord: "agent.json", CandidateSHA256: record.CandidateSHA256, Baseline: &BaselineReport{Status: "pass"}, Validation: &validation}
	for name, value := range map[string]any{"inputs/task.json": input, "artifacts/report.json": report, "run.json": record} {
		if err := writeJSON(filepath.Join(root, name), value); err != nil {
			t.Fatal(err)
		}
	}
	if withTrace {
		if err := runSpan.End(record, nil); err != nil {
			t.Fatal(err)
		}
		if err := rec.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return root, ledgerPath
}

type replayFixtureExecutor struct{}

func (replayFixtureExecutor) Execute(context.Context, agent.ToolCall) (agent.ToolResult, error) {
	return agent.ToolResult{Content: "package orders\n"}, nil
}

func TestReplayTraceAndLegacyRecomputeAndDoNotMutate(t *testing.T) {
	for _, withTrace := range []bool{false, true} {
		for _, withBudget := range []bool{false, true} {
			name := map[bool]string{false: "legacy", true: "trace"}[withTrace] + map[bool]string{false: "-unbudgeted", true: "-ledger"}[withBudget]
			t.Run(name, func(t *testing.T) {
				root, ledger := replayFixture(t, withTrace, withBudget)
				before := mustReadTree(t, root)
				got, err := Replay(context.Background(), root, ledger)
				if err != nil {
					t.Fatal(err)
				}
				if got.Status != StatusCompleted || got.Verdict != quality.ReadyForReview || got.ModelCalls != 2 || got.ToolCalls != 1 || got.KnownTokens != 8 || got.UnknownCalls != 0 || got.LedgerMatched != withBudget {
					t.Fatalf("unexpected replay %+v", got)
				}
				if got.Mode != map[bool]string{false: "legacy-completed", true: "trace-v1"}[withTrace] || (!withTrace && len(got.DerivedFields) == 0) {
					t.Fatal(got)
				}
				if after := mustReadTree(t, root); !reflect.DeepEqual(before, after) {
					t.Fatal("replay modified source materials")
				}
			})
		}
	}
}
func TestReplayRejectsChangedMaterials(t *testing.T) {
	for _, change := range []string{"tool-result", "quality-verdict", "policy", "candidate", "ledger-usage", "trace-tail", "trace-missing-gate", "erased-budget-reference"} {
		t.Run(change, func(t *testing.T) {
			root, ledger := replayFixture(t, true, true)
			switch change {
			case "tool-result":
				var a agent.Result
				if err := replayJSON(root, "artifacts/agent.json", &a); err != nil {
					t.Fatal(err)
				}
				for i := range a.Messages {
					if a.Messages[i].Role == agent.RoleTool {
						a.Messages[i].Content = "changed"
						break
					}
				}
				if err := writeJSON(filepath.Join(root, "artifacts/agent.json"), a); err != nil {
					t.Fatal(err)
				}
			case "quality-verdict":
				var r Report
				if err := replayJSON(root, "artifacts/report.json", &r); err != nil {
					t.Fatal(err)
				}
				r.Validation.Verdict = quality.Rejected
				if err := writeJSON(filepath.Join(root, "artifacts/report.json"), r); err != nil {
					t.Fatal(err)
				}
			case "policy":
				if err := os.WriteFile(filepath.Join(root, "evidence/policy.json"), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "candidate":
				if err := os.WriteFile(filepath.Join(root, "workspace/orders/main.go"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "ledger-usage":
				var b budget.Snapshot
				if err := replayJSON(filepath.Dir(ledger), filepath.Base(ledger), &b); err != nil {
					t.Fatal(err)
				}
				b.Calls[0].Usage.TotalTokens++
				if err := writeJSON(ledger, b); err != nil {
					t.Fatal(err)
				}
			case "trace-tail":
				f, err := os.OpenFile(filepath.Join(root, "trace.jsonl"), os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.WriteString("{"); err != nil {
					t.Fatal(err)
				}
				if err = f.Close(); err != nil {
					t.Fatal(err)
				}
			case "erased-budget-reference":
				var input Input
				var report Report
				if err := replayJSON(root, "inputs/task.json", &input); err != nil {
					t.Fatal(err)
				}
				if err := replayJSON(root, "artifacts/report.json", &report); err != nil {
					t.Fatal(err)
				}
				input.Budget = nil
				report.Input = input
				if err := writeJSON(filepath.Join(root, "inputs/task.json"), input); err != nil {
					t.Fatal(err)
				}
				if err := writeJSON(filepath.Join(root, "artifacts/report.json"), report); err != nil {
					t.Fatal(err)
				}
				ledger = ""
			case "trace-missing-gate":
				if err := os.WriteFile(filepath.Join(root, "trace.jsonl"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Replay(context.Background(), root, ledger); err == nil {
				t.Fatal("changed source was accepted")
			}
		})
	}
}
func TestReplayRequiresLocalLedgerAndRejectsLegacyFailure(t *testing.T) {
	root, _ := replayFixture(t, false, true)
	// Move only the Run; replay must not follow its saved absolute ledger path.
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	if _, err := Replay(context.Background(), moved, ""); err == nil {
		t.Fatal("missing local ledger was ignored")
	}
	root, _ = replayFixture(t, false, false)
	var r Record
	if err := replayJSON(root, "run.json", &r); err != nil {
		t.Fatal(err)
	}
	r.Status = StatusFailed
	r.Error = "unknown failure"
	r.Verdict = ""
	var report Report
	if err := replayJSON(root, "artifacts/report.json", &report); err != nil {
		t.Fatal(err)
	}
	report.Status = r.Status
	report.Error = r.Error
	report.Validation.Verdict = ""
	if err := writeJSON(filepath.Join(root, "run.json"), r); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(root, "artifacts/report.json"), report); err != nil {
		t.Fatal(err)
	}
	if _, err := Replay(context.Background(), root, ""); err == nil {
		t.Fatal("legacy failure was guessed")
	}
}
func TestReplayControlledRunModelFailure(t *testing.T) {
	failure := errors.New("controlled provider failure")
	config := configForTest(t, modelFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
		return agent.Reply{}, failure
	}))
	p, err := lookupProfile(ProfileT1T2)
	if err != nil {
		t.Fatal(err)
	}
	config.Backend = baselineBackend{run: func(context.Context, execution.Request) (execution.Result, error) {
		return execution.Result{ExitCode: 0, Stdout: passedBaselineEvents(p)}, nil
	}}
	record, err := Run(context.Background(), config)
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	result, err := Replay(context.Background(), record.Directory, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusFailed || result.Verdict != "" || result.ModelCalls != 1 || result.UnknownCalls != 1 || result.FailureCategory != "model" {
		t.Fatal(result)
	}
}

func TestReplayControlledRunBudgetDenial(t *testing.T) {
	ctx := context.Background()
	profile := budget.Profile{Provider: "controlled", Endpoint: "in-process", Model: "scripted-test-model", ContextTokens: 90, MaxOutputTokens: 10, SourceSHA256: strings.Repeat("f", 64), VerifiedAt: time.Now().UTC()}
	ledgerPath := filepath.Join(t.TempDir(), "budget", "budget.json")
	ledger, err := budget.Create(budget.Config{Directory: filepath.Dir(ledgerPath), CampaignID: "denial", Profile: profile, Limits: budget.Limits{MaxCalls: 1, MaxTokens: 50}, Targets: map[string]budget.Limits{"orders": {MaxCalls: 1, MaxTokens: 100}}})
	if err != nil {
		t.Fatal(err)
	}
	config := configForTest(t, budgetTestModel{profile: profile, modelFunc: func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
		t.Fatal("budget-denied provider was called")
		return agent.Reply{}, nil
	}})
	config.Budget = &budget.Target{Ledger: ledger, ID: "orders"}
	p, err := lookupProfile(ProfileT1T2)
	if err != nil {
		t.Fatal(err)
	}
	config.Backend = baselineBackend{run: func(context.Context, execution.Request) (execution.Result, error) {
		return execution.Result{ExitCode: 0, Stdout: passedBaselineEvents(p)}, nil
	}}
	record, err := Run(ctx, config)
	if !errors.Is(err, agent.ErrModelBudget) {
		t.Fatal(err)
	}
	result, err := Replay(ctx, record.Directory, ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	log, readErr := trace.Read(filepath.Join(record.Directory, "trace.jsonl"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if log.Events[len(log.Events)-1].ErrorCategory != "budget" {
		t.Fatal("Run span lost budget failure category")
	}
	if result.Status != StatusFailed || result.FailureCategory != "budget" || result.KnownTokens != 0 || !result.LedgerMatched {
		t.Fatal(result)
	}
}

func TestReplayControlledCancellationAndBaselineFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failure  error
		baseline bool
		status   Status
		category string
	}{
		{"cancellation", context.Canceled, false, StatusCancelled, "cancelled"},
		{"deadline", context.DeadlineExceeded, false, StatusFailed, "timeout"},
		{"baseline-facility", errors.New("controlled baseline executor error"), true, StatusFailed, "infrastructure"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := configForTest(t, modelFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
				if tc.baseline {
					t.Fatal("model called after failed baseline")
				}
				return agent.Reply{}, tc.failure
			}))
			p, err := lookupProfile(ProfileT1T2)
			if err != nil {
				t.Fatal(err)
			}
			config.Backend = baselineBackend{run: func(context.Context, execution.Request) (execution.Result, error) {
				if tc.baseline {
					return execution.Result{}, tc.failure
				}
				return execution.Result{ExitCode: 0, Stdout: passedBaselineEvents(p)}, nil
			}}
			record, err := Run(context.Background(), config)
			if !errors.Is(err, tc.failure) {
				t.Fatal(err)
			}
			got, err := Replay(context.Background(), record.Directory, "")
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != tc.status || got.FailureCategory != tc.category || got.Verdict != "" {
				t.Fatal(got)
			}
		})
	}
}

func TestReplayRejectsHiddenLegacyModelFailuresAndRejections(t *testing.T) {
	for _, change := range []string{"outcome", "rejection"} {
		t.Run(change, func(t *testing.T) {
			root, path := replayFixture(t, false, true)
			var ledger budget.Snapshot
			if err := replayJSON(filepath.Dir(path), filepath.Base(path), &ledger); err != nil {
				t.Fatal(err)
			}
			if change == "outcome" {
				ledger.Calls[0].Outcome = "model_error"
			} else {
				ledger.Rejections = append(ledger.Rejections, budget.Rejection{RunID: ledger.Calls[0].RunID, TargetID: ledger.Calls[0].TargetID, InputSHA256: ledger.Calls[0].InputSHA256, Scope: "target", Resource: "tokens", At: time.Now().UTC()})
			}
			if err := writeJSON(path, ledger); err != nil {
				t.Fatal(err)
			}
			if _, err := Replay(context.Background(), root, path); err == nil {
				t.Fatal("legacy evidence contradiction accepted")
			}
		})
	}
}
