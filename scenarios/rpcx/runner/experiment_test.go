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
	"github.com/secacy/agent-harness/internal/trace"
)

func experimentForTest(t *testing.T, group ExperimentGroup, model agent.Model) ExperimentConfig {
	t.Helper()
	config := configForTest(t, model)
	target, e := TargetFromScenario("attempt", config.ScenarioDir, "v0.1.0")
	if e != nil {
		t.Fatal(e)
	}
	config.Target = &target
	config.ScenarioDir = ""
	config.Limits = agent.Limits{MaxModelCalls: 3, MaxToolCalls: 5}
	p, _ := lookupProfile(ProfileT1T2)
	config.Backend = baselineBackend{run: func(_ context.Context, req execution.Request) (execution.Result, error) {
		if strings.Contains(strings.Join(req.Args, " "), "mod edit") {
			return execution.Result{Stdout: []byte(`{}`)}, nil
		}
		return execution.Result{Stdout: passedBaselineEvents(p)}, nil
	}}
	profile := repairControlledProfile(config.ModelName)
	config.Model = budgetTestModel{modelFunc: model.Complete, profile: profile}
	ledger, e := budget.Create(budget.Config{Directory: filepath.Join(filepath.Dir(config.RunsDir), "budget"), CampaignID: "experiment", Profile: profile, Limits: budget.Limits{MaxCalls: 3, MaxTokens: 1000}, Targets: map[string]budget.Limits{target.ID: {MaxCalls: 3, MaxTokens: 1000}}})
	if e != nil {
		t.Fatal(e)
	}
	config.Budget = &budget.Target{ID: target.ID, Ledger: ledger}
	return ExperimentConfig{Config: config, Group: group, PreparationTimeout: time.Minute, GenerationTimeout: time.Minute, ValidationTimeout: time.Minute}
}

func experimentReply(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
	return agent.Reply{Finish: agent.FinishStop, Content: "Submitted; migration unresolved.", Usage: &agent.Usage{InputTokens: 15, OutputTokens: 5, TotalTokens: 20}}, nil
}

func TestExperimentUngatedPoliciesAndReplay(t *testing.T) {
	for _, group := range []ExperimentGroup{ExperimentA, ExperimentB} {
		t.Run(string(group), func(t *testing.T) {
			calls := 0
			c := experimentForTest(t, group, modelFunc(func(ctx context.Context, messages []agent.Message, tools []agent.ToolSpec) (agent.Reply, error) {
				calls++
				coached := strings.Contains(messages[0].Content, "Review each RPC boundary")
				if coached != (group == ExperimentB) {
					t.Fatal("wrong group prompt", messages[0].Content)
				}
				return experimentReply(ctx, messages, tools)
			}))
			commands := 0
			base := c.Config.Backend
			c.Config.Backend = baselineBackend{run: func(ctx context.Context, req execution.Request) (execution.Result, error) {
				commands++
				return base.Run(ctx, req)
			}}
			a, e := RunExperiment(context.Background(), c)
			if e != nil {
				t.Fatal(e)
			}
			if calls != 1 || commands != 2 || !a.Submitted || !a.Released || a.Record.Status != StatusCompleted || a.Record.Verdict != "" || a.Validation != nil {
				t.Fatalf("bad ungated attempt %+v commands=%d", a, commands)
			}
			if a.CandidatePath == "" || a.Record.PatchSHA256 == "" {
				t.Fatal("candidate missing")
			}
			if _, e = os.Stat(filepath.Join(a.Record.Directory, "run.json")); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("experiment wrote production receipt", e)
			}
			// Explicit ledger path authenticates the original unique accounting.
			replay, e := ReplayExperiment(context.Background(), a.Record.Directory, a.Input.Budget.LedgerPath)
			if e != nil {
				t.Fatal(e)
			}
			if replay.ModelCalls != 1 || replay.KnownTokens != 20 || !replay.LedgerMatched {
				t.Fatalf("bad replay %+v", replay)
			}
			if _, e = RunExperiment(context.Background(), ExperimentConfig{Config: Config{}, Group: group}); e == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}

func TestExperimentFailureRetainsPartialCandidate(t *testing.T) {
	stop := errors.New("model unavailable")
	c := experimentForTest(t, ExperimentA, modelFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
		return agent.Reply{}, stop
	}))
	a, e := RunExperiment(context.Background(), c)
	if !errors.Is(e, stop) || a.Submitted || a.Released || a.Record.Status != StatusFailed || a.CandidatePath == "" || a.Agent == nil {
		t.Fatalf("bad failure %+v %v", a, e)
	}
	if _, e = ReplayExperiment(context.Background(), a.Record.Directory, a.Input.Budget.LedgerPath); e != nil {
		t.Fatal(e)
	}
}

func TestExperimentFreezeAndReceiptTampering(t *testing.T) {
	c := experimentForTest(t, ExperimentB, modelFunc(experimentReply))
	a, e := RunExperiment(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(a.Record.Directory, filepath.FromSlash(a.CandidatePath), "go.mod")
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, append(raw, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = ReplayExperiment(context.Background(), a.Record.Directory, a.Input.Budget.LedgerPath); e == nil {
		t.Fatal("changed fixed candidate replayed")
	}
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	a.Released = false
	if e = writeJSON(filepath.Join(a.Record.Directory, "experiment-attempt.json"), a); e != nil {
		t.Fatal(e)
	}
	if _, e = ReplayExperiment(context.Background(), a.Record.Directory, a.Input.Budget.LedgerPath); e == nil {
		t.Fatal("changed release receipt replayed")
	}
}

func TestExperimentGenerationTimeoutPreservesBudgetAndNoRelease(t *testing.T) {
	c := experimentForTest(t, ExperimentB, modelFunc(func(ctx context.Context, _ []agent.Message, _ []agent.ToolSpec) (agent.Reply, error) {
		<-ctx.Done()
		return agent.Reply{}, ctx.Err()
	}))
	c.GenerationTimeout = 300 * time.Millisecond
	a, e := RunExperiment(context.Background(), c)
	if !errors.Is(e, context.DeadlineExceeded) || a.Released || a.Submitted || a.Record.Status != StatusFailed || a.Record.CandidateSHA256 == "" {
		t.Fatalf("bad timeout %+v %v", a, e)
	}
	if _, e = ReplayExperiment(context.Background(), a.Record.Directory, a.Input.Budget.LedgerPath); e != nil {
		t.Fatal(e)
	}
	if c.Config.Budget.Ledger.Snapshot().Totals.UnknownTokens != 100 {
		t.Fatal("unknown provider outcome lost")
	}
}

func TestExperimentPlainRejectsDiagnostics(t *testing.T) {
	c := experimentForTest(t, ExperimentA, modelFunc(experimentReply))
	c.Config.DiagnosticLimit = 2
	if _, e := RunExperiment(context.Background(), c); e == nil {
		t.Fatal("plain group accepted migration diagnostics")
	}
}

func TestExperimentCUsesExistingGateAndIndependentGeneration(t *testing.T) {
	var prompts [][]agent.Message
	var toolsets [][]agent.ToolSpec
	var ids []string
	for _, group := range []ExperimentGroup{ExperimentB, ExperimentC} {
		c := experimentForTest(t, group, modelFunc(func(ctx context.Context, m []agent.Message, tools []agent.ToolSpec) (agent.Reply, error) {
			prompts = append(prompts, append([]agent.Message(nil), m[:2]...))
			toolsets = append(toolsets, tools)
			return experimentReply(ctx, m, tools)
		}))
		commands := 0
		base := c.Config.Backend
		c.Config.Backend = baselineBackend{run: func(ctx context.Context, req execution.Request) (execution.Result, error) {
			commands++
			return base.Run(ctx, req)
		}}
		a, e := RunExperiment(context.Background(), c)
		if e != nil {
			t.Fatal(e)
		}
		ids = append(ids, a.Record.ID)
		if group == ExperimentC {
			if commands <= 2 || a.Validation == nil || a.Record.Verdict == "" {
				t.Fatalf("C skipped gate %+v commands=%d", a, commands)
			}
			if a.Released != (a.Record.Verdict == "ReadyForReview") {
				t.Fatal("C release rule differs")
			}
			if _, e = ReplayExperiment(context.Background(), a.Record.Directory, a.Input.Budget.LedgerPath); e != nil {
				t.Fatal(e)
			}
		}
	}
	if len(prompts) != 2 || !reflect.DeepEqual(prompts[0], prompts[1]) || !reflect.DeepEqual(toolsets[0], toolsets[1]) || ids[0] == ids[1] {
		t.Fatal("B/C do not use identical generation policies with independent attempts")
	}
}

func TestExperimentInputTamperingCannotRelease(t *testing.T) {
	c := experimentForTest(t, ExperimentB, modelFunc(experimentReply))
	c.Config.RunID = "input-change"
	profile := repairControlledProfile(c.Config.ModelName)
	c.Config.Model = budgetTestModel{profile: profile, modelFunc: func(ctx context.Context, m []agent.Message, tools []agent.ToolSpec) (agent.Reply, error) {
		path := filepath.Join(c.Config.RunsDir, c.Config.RunID, "baseline/demo-orders/go.mod")
		raw, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, append(raw, '\n'), 0600); e != nil {
			t.Fatal(e)
		}
		return experimentReply(ctx, m, tools)
	}}
	a, e := RunExperiment(context.Background(), c)
	if e == nil || a.Released || a.Record.Status != StatusFailed || !a.Submitted || a.Record.FailureCode != "input_invalid" {
		t.Fatalf("input drift released %+v %v", a, e)
	}
	if _, e = ReplayExperiment(context.Background(), a.Record.Directory, a.Input.Budget.LedgerPath); e == nil {
		t.Fatal("changed authenticated input replayed")
	}
}

func TestExperimentOverallTimeoutStopsPreparation(t *testing.T) {
	c := experimentForTest(t, ExperimentA, modelFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
		t.Fatal("overall timeout reached model")
		return agent.Reply{}, nil
	}))
	c.Config.Timeout = 150 * time.Millisecond
	c.Config.Backend = baselineBackend{run: func(ctx context.Context, _ execution.Request) (execution.Result, error) {
		<-ctx.Done()
		return execution.Result{}, ctx.Err()
	}}
	a, e := RunExperiment(context.Background(), c)
	if !errors.Is(e, context.DeadlineExceeded) || a.Released || a.Submitted || a.Record.Status != StatusFailed || a.Agent != nil {
		t.Fatalf("overall timeout ignored %+v %v", a, e)
	}
	if _, e = ReplayExperiment(context.Background(), a.Record.Directory, filepath.Join(filepath.Dir(c.Config.RunsDir), "budget/budget.json")); e != nil {
		t.Fatal(e)
	}
	if a.Record.FinishedAt == nil || a.Record.FinishedAt.Sub(a.Record.StartedAt) > time.Second {
		t.Fatal("overall timeout did not bound preparation")
	}
}

func TestDescribeExperimentMatchesActualAgentTrace(t *testing.T) {
	for _, group := range []ExperimentGroup{ExperimentA, ExperimentB, ExperimentC} {
		t.Run(string(group), func(t *testing.T) {
			c := experimentForTest(t, group, modelFunc(experimentReply))
			before := mustReadTree(t, filepath.Dir(c.Config.Target.Repository.Directory))
			definition, e := DescribeExperiment(context.Background(), *c.Config.Target, group, 0)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = os.Stat(c.Config.RunsDir); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("description created workspace", e)
			}
			if before.hash() != mustReadTree(t, filepath.Dir(c.Config.Target.Repository.Directory)).hash() {
				t.Fatal("description modified input")
			}
			a, e := RunExperiment(context.Background(), c)
			if e != nil {
				t.Fatal(e)
			}
			log, e := trace.Read(filepath.Join(a.Record.Directory, "trace.jsonl"))
			if e != nil {
				t.Fatal(e)
			}
			recording, e := agent.RecordingFromTrace(log)
			if e != nil {
				t.Fatal(e)
			}
			if !reflect.DeepEqual(definition.Messages, recording.Input.Initial) || !reflect.DeepEqual(definition.Tools, recording.Input.Tools) || definition.CompletionPolicy != recording.Input.CompletionPolicy || definition.DiagnosticLimit != a.Input.DiagnosticLimit {
				t.Fatal("frozen definition differs from actual generation")
			}
		})
	}
}

func TestDescribeExperimentRejectsInputDriftAndInvalidPolicies(t *testing.T) {
	c := experimentForTest(t, ExperimentA, modelFunc(experimentReply))
	if _, e := DescribeExperiment(context.Background(), *c.Config.Target, ExperimentA, 2); e == nil {
		t.Fatal("description permits plain diagnostics")
	}
	if _, e := DescribeExperiment(context.Background(), *c.Config.Target, "unknown", 0); e == nil {
		t.Fatal("description permits unknown strategy")
	}
	target := *c.Config.Target
	spec, e := os.ReadFile(target.Specification.Path)
	if e != nil {
		t.Fatal(e)
	}
	target.Specification.Path = filepath.Join(t.TempDir(), "spec.md")
	if e = os.WriteFile(target.Specification.Path, append(spec, '\n'), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = DescribeExperiment(context.Background(), target, ExperimentB, 0); !errors.Is(e, ErrInputChanged) {
		t.Fatalf("description accepted source drift: %v", e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = DescribeExperiment(ctx, *c.Config.Target, ExperimentA, 0); !errors.Is(e, context.Canceled) {
		t.Fatal("description ignored cancellation", e)
	}
}

func TestDescribeExperimentIncludesActualSupportedDiagnostics(t *testing.T) {
	c := experimentForTest(t, ExperimentB, modelFunc(experimentReply))
	target := *translationConfig(t).Target
	target.ID = c.Config.Target.ID
	target.ProfileID = ProfileTranslationScope
	c.Config.Target = &target
	c.Config.DiagnosticLimit = 2
	c.Config.Backend = repairMockBackend(t)
	definition, e := DescribeExperiment(context.Background(), target, ExperimentB, 2)
	if e != nil {
		t.Fatal(e)
	}
	if definition.DiagnosticLimit != 2 || definition.Tools[len(definition.Tools)-1].Name != diagnosticTool {
		t.Fatal("diagnostic policy missing")
	}
	a, e := RunExperiment(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	log, e := trace.Read(filepath.Join(a.Record.Directory, "trace.jsonl"))
	if e != nil {
		t.Fatal(e)
	}
	recording, e := agent.RecordingFromTrace(log)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(definition.Messages, recording.Input.Initial) || !reflect.DeepEqual(definition.Tools, recording.Input.Tools) {
		t.Fatal("diagnostic definition differs from execution")
	}
	if _, e = ReplayExperiment(context.Background(), a.Record.Directory, a.Input.Budget.LedgerPath); e != nil {
		t.Fatal(e)
	}
}

func TestExperimentEarlyOwnerFailureHasReplayableFailureReceipt(t *testing.T) {
	c := experimentForTest(t, ExperimentA, modelFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
		t.Fatal("invalid owner reached model")
		return agent.Reply{}, nil
	}))
	c.Config.ResourceOwner = &execution.ResourceOwner{Root: t.TempDir(), TargetID: c.Config.Target.ID}
	a, e := RunExperiment(context.Background(), c)
	if e == nil || a.Record.Status != StatusFailed || a.Record.Directory == "" || a.Released || a.Submitted || a.Agent != nil || a.Record.CandidateSHA256 != "" {
		t.Fatalf("invalid early failure receipt %+v %v", a, e)
	}
	if _, e = os.Stat(filepath.Join(a.Record.Directory, "inputs/task.json")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("failure unexpectedly prepared inputs", e)
	}
	replay, e := ReplayExperiment(context.Background(), a.Record.Directory, filepath.Join(filepath.Dir(c.Config.RunsDir), "budget/budget.json"))
	if e != nil {
		t.Fatal(e)
	}
	if replay.Status != StatusFailed || !replay.LedgerMatched || replay.ModelCalls != 0 {
		t.Fatalf("bad early failure replay %+v", replay)
	}
}
