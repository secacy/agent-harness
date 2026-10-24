package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/audit"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/task"
	"github.com/secacy/agent-harness/scenarios/rpcx/testintegrity"
)

func diagnosticState(t *testing.T, workouts bool) *runState {
	t.Helper()
	config := translationConfig(t)
	if workouts {
		config = workoutsConfig(t)
	}
	config.DiagnosticLimit = 2
	config, err := validateConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	p, err := lookupProfile(config.Target.ProfileID)
	if err != nil {
		t.Fatal(err)
	}
	r := &runState{config: config, profile: p, record: Record{ID: "run-1", TargetID: "orders", Directory: t.TempDir()}, input: Input{Target: config.Target, DiagnosticLimit: 2, ProfileSHA256: p.hash(), RepositoryName: p.RepositoryName}}
	if err := r.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	return r
}

func diagnosticCall(id string) agent.ToolCall {
	return agent.ToolCall{ID: id, Name: diagnosticTool, Arguments: `{}`}
}

func TestDiagnosticStatesAndSnapshots(t *testing.T) {
	for _, workouts := range []bool{false, true} {
		t.Run(map[bool]string{false: "translation", true: "workouts"}[workouts], func(t *testing.T) {
			r := diagnosticState(t, workouts)
			workspace := r.path("workspace/" + r.profile.RepositoryName)
			scenario := "translation"
			if workouts {
				scenario = "workouts"
			}
			patch, _ := filepath.Abs("../" + scenario + "/cases/patches/correct.patch")
			applyPatch(t, workspace, patch)
			good := mustReadTree(t, workspace)
			d := &diagnosticTools{run: r}
			first, err := d.Execute(context.Background(), diagnosticCall("one"))
			if err != nil || first.IsError {
				t.Fatal(first, err)
			}
			var feedback diagnosticFeedback
			if json.Unmarshal([]byte(first.Content), &feedback) != nil || feedback.Status != "preserved" || !strings.Contains(feedback.Scope, "Production deadlines") {
				t.Fatal(first)
			}
			var testPath string
			for _, name := range sortedKeys(good) {
				if strings.HasSuffix(name, "_test.go") {
					testPath = name
					break
				}
			}
			if err := os.Remove(filepath.Join(workspace, testPath)); err != nil {
				t.Fatal(err)
			}
			second, err := d.Execute(context.Background(), diagnosticCall("two"))
			if err != nil || second.IsError || json.Unmarshal([]byte(second.Content), &feedback) != nil || feedback.Status != "violated" {
				t.Fatal(second, err)
			}
			third, err := d.Execute(context.Background(), diagnosticCall("three"))
			if err != nil || !third.IsError || len(r.diagnostics) != 2 {
				t.Fatal(third, err)
			}
			for i, ref := range r.diagnostics {
				report := readJSON[diagnosticReport](t, r.path(ref.Path))
				candidate := mustReadTree(t, filepath.Join(filepath.Dir(r.path(ref.Path)), "candidate"))
				if report.Sequence != i+1 || candidate.hash() != report.Binding.CandidateSHA256 || report.Binding.BaselineSHA256 != r.input.BaselineSHA256 {
					t.Fatal("diagnostic snapshot binding mismatch")
				}
			}
			if before := mustReadTree(t, filepath.Join(filepath.Dir(r.path(r.diagnostics[0].Path)), "candidate")); before.hash() != good.hash() {
				t.Fatal("earlier snapshot changed")
			}
		})
	}
}

func TestDiagnosticErrorsAndBoundedJSON(t *testing.T) {
	for _, mode := range []string{"arguments", "baseline-drift", "write-failure", "cancelled", "candidate-boundary", "candidate-drift", "cancel-after-snapshot"} {
		t.Run(mode, func(t *testing.T) {
			r := diagnosticState(t, false)
			d := &diagnosticTools{run: r}
			call := diagnosticCall("check")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "arguments":
				call.Arguments = `{"path":"../../oracle"}`
			case "baseline-drift":
				auditBytes(t, r.record.Directory, "baseline/translation/extra.txt", []byte("changed"))
			case "write-failure":
				auditBytes(t, r.record.Directory, "generation-diagnostics", []byte("blocked"))
			case "cancelled":
				cancel()
			case "candidate-drift":
				d.beforePublish = func() { auditBytes(t, r.record.Directory, "workspace/translation/changed.txt", []byte("drift")) }
			case "cancel-after-snapshot":
				d.beforePublish = cancel
			case "candidate-boundary":
				if err := os.Symlink(os.DevNull, r.path("workspace/translation/link")); err != nil {
					t.Fatal(err)
				}
			}
			result, err := d.Execute(ctx, call)
			if mode == "arguments" || mode == "candidate-boundary" {
				if err != nil || !result.IsError {
					t.Fatal(result, err)
				}
			} else if err == nil {
				t.Fatal("host failure was not fatal")
			}
			if len(r.diagnostics) != 0 {
				t.Fatal("failed check published a valid reference")
			}
			if mode == "arguments" && d.attempts != 1 {
				t.Fatal("invalid argument did not consume an attempt")
			}
		})
	}
	large := testintegrity.Report{Status: "unknown"}
	for range 100 {
		large.Findings = append(large.Findings, testintegrity.Finding{Detail: strings.Repeat("x", 1000)})
	}
	result, err := renderDiagnostic(diagnosticReport{}.Binding, large)
	var feedback diagnosticFeedback
	if err != nil || len(result.Content) > diagnosticOutputLimit || json.Unmarshal([]byte(result.Content), &feedback) != nil || feedback.Total != 100 || feedback.Omitted == 0 {
		t.Fatal("invalid bounded diagnostic JSON", err)
	}
	if ValidateDiagnostics(ProfileT3, 2) == nil || ValidateDiagnostics(ProfileTranslation, 1) == nil || ValidateDiagnostics(ProfileWorkouts, 2) != nil {
		t.Fatal("unsupported diagnostic policy accepted")
	}
}

func TestDiagnosticAuditBindings(t *testing.T) {
	r := diagnosticState(t, false)
	d := &diagnosticTools{run: r}
	response, err := d.Execute(context.Background(), diagnosticCall("one"))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"valid", "snapshot", "cross-run", "response", "hidden-reference", "hidden-config", "report-digest"} {
		t.Run(mode, func(t *testing.T) {
			f := newAuditFixture(t, ProfileTranslation)
			files := mustReadTree(t, r.path("generation-diagnostics"))
			if err := files.write(context.Background(), filepath.Join(f.dir, "generation-diagnostics")); err != nil {
				t.Fatal(err)
			}
			f.report.Input.DiagnosticLimit = 2
			f.report.Diagnostics = append([]DiagnosticReference{}, r.diagnostics...)
			plan := readJSON[campaign.Plan](t, filepath.Join(f.root, "plan.json"))
			plan.Targets[0].DiagnosticLimit = 2
			auditPut(t, f.root, "plan.json", plan)
			raw, _ := os.ReadFile(filepath.Join(f.root, "plan.json"))
			f.record.PlanSHA256 = digest(raw)
			auditPut(t, f.dir, "inputs/task.json", f.report.Input)
			history := agent.Result{Reason: agent.ExitCompleted, ModelCalls: 1, ToolCalls: 1, Messages: []agent.Message{{Role: agent.RoleAssistant, ToolCalls: []agent.ToolCall{diagnosticCall("one")}}, {Role: agent.RoleTool, ToolCallID: "one", Content: response.Content}}}
			ref := r.diagnostics[0]
			switch mode {
			case "snapshot":
				auditBytes(t, f.dir, filepath.Dir(ref.Path)+"/candidate/extra.txt", []byte("changed"))
			case "cross-run":
				entry := readJSON[diagnosticReport](t, filepath.Join(f.dir, ref.Path))
				entry.RunID = "another-run"
				auditPut(t, f.dir, ref.Path, entry)
				raw, _ = os.ReadFile(filepath.Join(f.dir, ref.Path))
				f.report.Diagnostics[0].SHA256 = digest(raw)
			case "response":
				history.Messages[1].Content = "forged"
			case "hidden-reference":
				f.report.Diagnostics = nil
			case "hidden-config":
				f.report.Input.DiagnosticLimit = 0
				f.report.Diagnostics = nil
				auditPut(t, f.dir, "inputs/task.json", f.report.Input)
			case "report-digest":
				f.report.Diagnostics[0].SHA256 = strings.Repeat("0", 64)
			}
			auditPut(t, f.dir, "artifacts/agent.json", history)
			f.publish(t)
			result := inspectAuditFixture(t, f)
			if mode == "valid" {
				if result.Consistency != "pass" {
					t.Fatalf("valid diagnostic audit: %+v", result.Violations)
				}
			} else if !auditHas(result.Violations, "generation_diagnostic_mismatch") {
				t.Fatal("diagnostic corruption was not detected", result.Violations)
			}
		})
	}
}

func TestDiagnosticObservedCandidates(t *testing.T) {
	if os.Getenv("HARNESS_DIAGNOSTICS_OBSERVED") != "1" {
		t.Skip("set HARNESS_DIAGNOSTICS_OBSERVED=1 to inspect the two pinned development candidates")
	}
	for _, item := range []struct {
		name, run, hash, status string
		workouts                bool
	}{
		{"translation", "rpcx-agent-1712004754", "19c4a36e7cdd83c0106fbdbbc3c221affe4dd471c0dd41df531491f48c4e6884", "unknown", false},
		{"workouts", "rpcx-agent-4062530435", "50fb602f1c7600e13a15e7f446088264d354e0da6721ecdba6b7f3a9032b6a56", "preserved", true},
	} {
		t.Run(item.name, func(t *testing.T) {
			r := diagnosticState(t, item.workouts)
			source := filepath.Join("../../../runs/l2-current-dev-v0.1.0/campaign/targets", item.name, "runs", item.run, "workspace", r.profile.RepositoryName)
			files := mustReadTree(t, source)
			if files.hash() != item.hash {
				t.Fatal("observed candidate identity changed")
			}
			workspace := r.path("workspace/" + r.profile.RepositoryName)
			if err := os.RemoveAll(workspace); err != nil {
				t.Fatal(err)
			}
			if err := files.write(context.Background(), workspace); err != nil {
				t.Fatal(err)
			}
			result, err := (&diagnosticTools{run: r}).Execute(context.Background(), diagnosticCall("observed"))
			var feedback diagnosticFeedback
			if err != nil || result.IsError || json.Unmarshal([]byte(result.Content), &feedback) != nil || feedback.Status != item.status {
				t.Fatal(result, err)
			}
			if !item.workouts && !strings.Contains(result.Content, "TestTranslationRoundTrip") {
				t.Fatal("changed original test declaration was not located")
			}
			if mustReadTree(t, source).hash() != item.hash {
				t.Fatal("original observation changed")
			}
			t.Log("DIAGNOSTIC_OBSERVED", item.name, result.Content)
		})
	}
}

func TestDiagnosticEnabledBaselineFailureHasNoAgentRecord(t *testing.T) {
	cases := heterogeneousCases(t)
	backend := baselineBackend{run: func(context.Context, execution.Request) (execution.Result, error) {
		return execution.Result{ExitCode: 1, Stderr: []byte("injected baseline failure")}, nil
	}}
	config := heterogeneousCampaign(t, "diagnostic-baseline-failure", []string{"translation"}, cases, backend)
	config.Plan.Targets[0].DiagnosticLimit = 2
	_, summary, err := campaign.Run(context.Background(), config)
	if err != nil || summary.Counts.Failed != 1 || len(summary.Budget.Calls) != 0 {
		t.Fatal(summary, err)
	}
	result, err := audit.Inspect(context.Background(), audit.Request{CampaignDir: config.Directory, OutputDir: filepath.Join(filepath.Dir(config.Directory), "audit")}, InspectRun)
	if err != nil || result.Consistency != "pass" {
		t.Fatal("baseline failure audit", result.Violations, result.Gaps, err)
	}
}

func TestDiagnosticRepairCampaign(t *testing.T) {
	if os.Getenv("HARNESS_DIAGNOSTICS_ACCEPTANCE") != "1" {
		t.Skip("set HARNESS_DIAGNOSTICS_ACCEPTANCE=1 for full diagnostic repair delivery")
	}
	cases := heterogeneousCases(t)
	c := cases["translation"]
	config := heterogeneousCampaign(t, "diagnostic-repair", []string{"translation"}, cases, execution.Local{})
	config.Plan.Budget = budget.Limits{MaxCalls: 4, MaxTokens: 400}
	config.Plan.Targets[0].Budget = config.Plan.Budget
	config.Plan.Targets[0].DiagnosticLimit = 2
	config.Plan.Targets[0].Limits = agent.Limits{MaxModelCalls: 4, MaxToolCalls: 100}
	const testPath = "internal/repo/webapi/translation_rpcx_test.go"
	good := c.candidate[testPath]
	assertion := []byte("\t\tif method != \"translate\" || request != (translateRequest{Text: \"hello\", Source: \"en\", Destination: \"de\"}) {\n\t\t\tt.Fatal(\"request mapping changed\")\n\t\t}\n")
	if bytes.Count(good, assertion) != 1 {
		t.Fatal("fixture assertion changed")
	}
	bad := bytes.Replace(good, assertion, nil, 1)
	step := 0
	config.NewModel = func(task.Target) (budget.ProfiledModel, error) {
		return budgetTestModel{profile: config.Plan.Profile, modelFunc: func(_ context.Context, messages []agent.Message, _ []agent.ToolSpec) (agent.Reply, error) {
			step++
			reply := agent.Reply{Usage: &agent.Usage{InputTokens: 15, OutputTokens: 5, TotalTokens: 20}, Finish: agent.FinishToolCalls}
			switch step {
			case 1:
				reply.ToolCalls = append([]agent.ToolCall{}, c.edits...)
				reply.ToolCalls = append(reply.ToolCalls, replacement(t, "weaken", testPath, good, bad), diagnosticCall("before"))
			case 2, 3:
				var feedback diagnosticFeedback
				last := messages[len(messages)-2]
				if json.Unmarshal([]byte(last.Content), &feedback) != nil || last.IsError || feedback.Status != map[int]string{2: "violated", 3: "preserved"}[step] {
					t.Fatal("unexpected repair feedback", last)
				}
				if step == 2 {
					reply.ToolCalls = []agent.ToolCall{replacement(t, "restore", testPath, bad, good), diagnosticCall("after")}
				} else {
					reply.ToolCalls = []agent.ToolCall{diagnosticCall("denied")}
				}
			case 4:
				if !messages[len(messages)-2].IsError {
					t.Fatal("third diagnostic attempt was accepted")
				}
				reply.Finish, reply.Content = agent.FinishStop, "Submitted after restoring the original assertion."
			}
			return reply, nil
		}}, nil
	}
	_, summary, err := campaign.Run(context.Background(), config)
	if err != nil || summary.Counts.Completed != 1 || summary.Counts.Verdicts["ReadyForReview"] != 1 || len(summary.Budget.Calls) != 4 {
		t.Fatalf("repair campaign: %+v, %v", summary, err)
	}
	a, err := audit.Inspect(context.Background(), audit.Request{CampaignDir: config.Directory, OutputDir: filepath.Join(filepath.Dir(config.Directory), "audit")}, InspectRun)
	if err != nil || a.Consistency != "pass" {
		t.Fatal("repair campaign audit", a.Violations, err)
	}
	dir := filepath.Join(config.Directory, filepath.FromSlash(summary.Targets[0].Attempt.Directory))
	review, err := os.ReadFile(filepath.Join(dir, "artifacts/review.md"))
	if err != nil {
		t.Fatal(err)
	}
	assertReviewLinks(t, dir, review)
	t.Log("DIAGNOSTIC_REPAIR_CAMPAIGN", config.Directory)
}

func TestDiagnosticReviewLinks(t *testing.T) {
	dir := t.TempDir()
	ref := DiagnosticReference{Path: "generation-diagnostics/check-0001/report.json", SHA256: strings.Repeat("a", 64)}
	for _, name := range []string{"run.json", "artifacts/report.json", ref.Path} {
		auditBytes(t, dir, name, []byte("{}\n"))
	}
	report := Report{Status: StatusFailed, Input: Input{RepositoryName: "translation"}, Diagnostics: []DiagnosticReference{ref}}
	review := renderReview(report, nil, nil, nil)
	assertReviewLinks(t, dir, review)
	if !bytes.Contains(review, []byte("[逐次报告](../generation-diagnostics/check-0001/report.json)")) {
		t.Fatal("missing navigable diagnostic link")
	}
}
