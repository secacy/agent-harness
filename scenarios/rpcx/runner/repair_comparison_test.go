package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
)

func repairInputs(t *testing.T) (string, repairProtocol, tree, tree, string) {
	t.Helper()
	project, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	p, seed, prompt, err := loadRepairProtocol(project)
	if err != nil {
		t.Fatal(err)
	}
	base := mustReadTree(t, filepath.Join(project, "scenarios/rpcx/l2/data/rpcx-l2-translation-v1/fixtures/translation"))
	return project, p, base, seed, prompt
}

func repairReplace(t *testing.T, files tree, path, old, new string) {
	t.Helper()
	if bytes.Count(files[path], []byte(old)) != 1 {
		t.Fatal("repair mutation must match exactly once", path, old)
	}
	files[path] = bytes.Replace(files[path], []byte(old), []byte(new), 1)
}

func TestRepairContentVerdicts(t *testing.T) {
	_, _, base, seed, _ := repairInputs(t)
	const bad = "if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second {"
	good := strings.Replace(bad, "<= 0", "< 29*time.Second", 1)
	for _, mode := range []string{"original-error", "restored-new-message", "restored-old-message", "only-message", "wrong-lower-bound", "equivalent-condition", "deleted-test", "new-test-file", "deleted-added-test", "changed-production", "changed-import", "changed-helper", "skip-test", "extra-guard-effect", "format"} {
		t.Run(mode, func(t *testing.T) {
			candidate := maps.Clone(seed)
			wantAssertion, wantTests, wantScope := quality.StatusPass, quality.StatusPass, quality.StatusPass
			if mode != "original-error" && mode != "only-message" {
				repairReplace(t, candidate, repairTestPath, bad, good)
			}
			switch mode {
			case "original-error":
				wantAssertion = quality.StatusFail
			case "restored-old-message":
				candidate[repairTestPath] = bytes.Replace(candidate[repairTestPath], []byte("30s RPC deadline missing"), []byte("v1 default deadline missing"), 1)
			case "only-message":
				candidate[repairTestPath] = bytes.Replace(candidate[repairTestPath], []byte("30s RPC deadline missing"), []byte("v1 default deadline missing"), 1)
				wantAssertion = quality.StatusUnknown
			case "wrong-lower-bound":
				repairReplace(t, candidate, repairTestPath, good, strings.Replace(good, "29*time.Second", "time.Second", 1))
				wantAssertion = quality.StatusUnknown
			case "equivalent-condition":
				repairReplace(t, candidate, repairTestPath, good, strings.Replace(good, "!ok", "ok == false", 1))
				wantAssertion = quality.StatusUnknown
			case "deleted-test":
				delete(candidate, repairTestPath)
				wantAssertion, wantTests = quality.StatusUnknown, quality.StatusFail
			case "new-test-file":
				candidate["new_test.go"] = []byte("package translation\n")
				wantTests = quality.StatusFail
			case "deleted-added-test":
				repairReplace(t, candidate, repairTestPath, "func TestTranslationContextPropagation(", "func helperContextPropagation(")
				wantTests = quality.StatusFail
			case "changed-production":
				candidate["go.mod"] = append(bytes.Clone(candidate["go.mod"]), '\n')
				wantScope = quality.StatusFail
			case "changed-import":
				repairReplace(t, candidate, repairTestPath, `"reflect"`, `alias "reflect"`)
				wantTests = quality.StatusFail
			case "changed-helper":
				candidate[repairTestPath] = append(bytes.Clone(candidate[repairTestPath]), []byte("\nfunc hiddenHelper() {}\n")...)
				wantTests = quality.StatusFail
			case "skip-test":
				repairReplace(t, candidate, repairTestPath, "func TestTranslationRoundTrip(t *testing.T) {", "func TestTranslationRoundTrip(t *testing.T) {\n t.Skip()\n")
				wantTests = quality.StatusFail
			case "extra-guard-effect":
				repairReplace(t, candidate, repairTestPath, good, "if reset(); "+strings.TrimPrefix(good, "if "))
				wantAssertion = quality.StatusUnknown
			case "format":
				candidate[repairTestPath] = append([]byte("\n\n"), candidate[repairTestPath]...)
			}
			got := repairContent(base, seed, candidate)
			if got.Assertion != wantAssertion || got.Tests != wantTests || got.Scope != wantScope {
				t.Fatalf("got %+v; expected %s/%s/%s", got, wantAssertion, wantTests, wantScope)
			}
			if got.success() {
				t.Fatal("content alone cannot establish complete repair success")
			}
		})
	}
}

func repairMockBackend(t *testing.T) execution.Executor {
	t.Helper()
	p, err := lookupProfile(ProfileTranslation)
	if err != nil {
		t.Fatal(err)
	}
	return baselineBackend{run: func(_ context.Context, req execution.Request) (execution.Result, error) {
		if slices.Contains(req.Args, "list") {
			file, ignored := "translation_google.go", "translation_rpcx_alternate.go"
			if slices.Contains(req.Args, "-tags=rpcx_alternate") {
				file, ignored = "translation_rpcx_alternate.go", "translation_google.go"
			}
			selected, _ := json.Marshal(map[string]any{"ImportPath": "github.com/evrone/go-clean-template/internal/repo/webapi", "GoFiles": []string{file}, "IgnoredGoFiles": []string{ignored}})
			mocks, _ := json.Marshal(map[string]any{"ImportPath": "github.com/evrone/go-clean-template/internal/usecase", "XTestGoFiles": []string{"translation_test.go", "mocks_repo_test.go"}})
			return execution.Result{Stdout: append(append(selected, '\n'), mocks...)}, nil
		}
		if slices.Contains(req.Args, "test") {
			return execution.Result{Stdout: passedBaselineEvents(p)}, nil
		}
		return execution.Result{}, nil
	}}
}

func repairControlledProfile(model string) budget.Profile {
	return budget.Profile{Provider: "controlled", Endpoint: "in-process", Model: model, ContextTokens: 90, MaxOutputTokens: 10, SourceSHA256: strings.Repeat("e", 64), VerifiedAt: time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)}
}

// 此替身只验证实验编排，测试名和证据不声称实际运行业务检查。
func repairMockEvaluation(t *testing.T, req checks.EvaluationRequest) (checks.Evaluation, error) {
	t.Helper()
	p, _ := lookupProfile(ProfileTranslation)
	spec, err := os.ReadFile(req.SpecPath)
	if err != nil {
		return checks.Evaluation{}, err
	}
	return checks.Evaluation{VerifierVersion: p.Verifier, SpecSHA256: digest(spec), BaselineSHA256: mustReadTree(t, req.BaselineDir).hash(), CandidateSHA256: mustReadTree(t, req.CandidateDir).hash(), Verdict: quality.NeedsAttention, Checks: map[string]quality.Check{"build_configurations": {Status: quality.StatusPass}, "build": {Status: quality.StatusPass}, "baseline_tests": {Status: quality.StatusPass}}}, nil
}

func TestRepairComparisonMechanism(t *testing.T) {
	project, p, _, seed, prompt := repairInputs(t)
	profile := repairControlledProfile(p.Model)
	services := repairServices{Backend: repairMockBackend(t), Profile: profile, Evaluate: func(_ context.Context, req checks.EvaluationRequest) (checks.Evaluation, error) {
		return repairMockEvaluation(t, req)
	}}
	var order []string
	services.NewModel = func(id string) (budget.ProfiledModel, error) {
		order = append(order, id)
		script := repairScript(profile)
		return budgetTestModel{profile: profile, modelFunc: func(ctx context.Context, messages []agent.Message, tools []agent.ToolSpec) (agent.Reply, error) {
			shown := strings.Contains(messages[2].Content, `"total_findings"`)
			if shown != (id == "B") {
				t.Fatal("wrong feedback visibility", id)
			}
			for _, tool := range tools {
				if tool.Name == diagnosticTool {
					t.Fatal("model can request extra diagnostics")
				}
			}
			if id == "A" {
				return agent.Reply{Finish: agent.FinishReason("garbage"), Usage: &agent.Usage{InputTokens: 15, OutputTokens: 5, TotalTokens: 20}}, nil
			}
			return script.Complete(ctx, messages, tools)
		}}, nil
	}
	output := filepath.Join(t.TempDir(), "comparison")
	summary, err := runRepairComparison(context.Background(), project, output, p, seed, prompt, services)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "A,B" || summary.Status != "Completed" || summary.Attempts[0].Verdict.Submitted != quality.StatusFail || summary.Attempts[0].Verdict.Assertion != quality.StatusFail || summary.Attempts[1].Verdict.Assertion != quality.StatusPass || summary.Attempts[1].Verdict.Tests != quality.StatusPass || summary.Budget.Totals.Calls != 4 || summary.Budget.Totals.KnownTokens != 80 {
		t.Fatalf("unexpected controlled result %+v", summary)
	}
	if summary.Attempts[1].Verdict.Delivery != quality.StatusUnknown || summary.Attempts[1].Verdict.success() {
		t.Fatal("missing review was counted as successful")
	}
	for _, id := range []string{"A", "B"} {
		if mustReadTree(t, filepath.Join(output, id, "starting-candidate")).hash() != p.SeedSHA256 {
			t.Fatal("starting candidate changed")
		}
		if _, err := os.Stat(filepath.Join(output, id, "run.json")); !os.IsNotExist(err) {
			t.Fatal("experiment impersonates standard Run")
		}
	}
	before, err := os.ReadFile(filepath.Join(output, "experiment.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = runRepairComparison(context.Background(), project, output, p, seed, prompt, services); err == nil {
		t.Fatal("existing output was reused")
	}
	after, readErr := os.ReadFile(filepath.Join(output, "experiment.json"))
	if readErr != nil || !bytes.Equal(before, after) {
		t.Fatal("refused rerun changed old records")
	}
}

func TestRepairToolsKeepPrivateMaterialsHidden(t *testing.T) {
	project, p, _, seed, prompt := repairInputs(t)
	r, feedback, err := prepareRepair(context.Background(), project, filepath.Join(t.TempDir(), "A"), "A", p, seed, repairMockBackend(t))
	if err != nil {
		t.Fatal(err)
	}
	tools, roots, err := r.repositoryTools()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(roots, ",") != "baseline,rpcx-v1,rpcx-v2,spec" {
		t.Fatal(roots)
	}
	for _, path := range []string{"../generation-diagnostics/check-0001/report.json", "../starting-candidate/go.mod", "../inputs/profile.json"} {
		args, _ := json.Marshal(map[string]string{"root": "workspace", "path": path})
		result, err := tools.Execute(context.Background(), agent.ToolCall{ID: "escape", Name: "read_file", Arguments: string(args)})
		if err != nil || !result.IsError {
			t.Fatal("private material exposed", path, result, err)
		}
	}
	a, b := repairMessages(r, prompt, feedback, false, roots), repairMessages(r, prompt, feedback, true, roots)
	if !reflectMessagesEqual(a[:2], b[:2]) || strings.Contains(a[2].Content, feedback.Content) || !strings.Contains(b[2].Content, feedback.Content) {
		t.Fatal("messages differ beyond the frozen feedback condition")
	}
	for _, root := range []string{"baseline", "rpcx-v1", "rpcx-v2", "spec"} {
		args, _ := json.Marshal(map[string]string{"root": root, "path": "../inputs/profile.json"})
		result, err := tools.Execute(context.Background(), agent.ToolCall{ID: "escape-root", Name: "read_file", Arguments: string(args)})
		if err != nil || !result.IsError {
			t.Fatal("read-only root escaped", root, result, err)
		}
	}
}

func reflectMessagesEqual(a, b []agent.Message) bool {
	aRaw, _ := json.Marshal(a)
	bRaw, _ := json.Marshal(b)
	return bytes.Equal(aRaw, bRaw)
}

func TestRepairBudgetDenialAndUnknownCost(t *testing.T) {
	for _, mode := range []string{"denied", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			project, p, _, seed, prompt := repairInputs(t)
			profile := repairControlledProfile(p.Model)
			calls := 0
			if mode == "denied" {
				p.TokensPerAttempt = 50
			}
			services := repairServices{Backend: repairMockBackend(t), Profile: profile, Evaluate: func(_ context.Context, r checks.EvaluationRequest) (checks.Evaluation, error) {
				return repairMockEvaluation(t, r)
			}, NewModel: func(string) (budget.ProfiledModel, error) {
				return budgetTestModel{profile: profile, modelFunc: func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
					calls++
					return agent.Reply{Finish: agent.FinishStop, Content: "Unresolved; submitted unchanged."}, nil
				}}, nil
			}}
			summary, err := runRepairComparison(context.Background(), project, filepath.Join(t.TempDir(), "comparison"), p, seed, prompt, services)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "denied" && (calls != 0 || summary.Budget.Totals.Calls != 0 || len(summary.Budget.Rejections) != 2) {
				t.Fatalf("denial reached provider %+v", summary.Budget)
			}
			if mode == "unknown" && (calls != 2 || summary.Budget.Totals.UnknownTokens != 200 || summary.Budget.Totals.KnownTokens != 0) {
				t.Fatal("unknown cost released", summary.Budget)
			}
		})
	}
}

func TestRepairCancellationAndPublicationFailure(t *testing.T) {
	for _, mode := range []string{"cancel", "validation-error", "candidate-drift", "publication-error"} {
		t.Run(mode, func(t *testing.T) {
			project, p, _, seed, prompt := repairInputs(t)
			profile := repairControlledProfile(p.Model)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			output := filepath.Join(t.TempDir(), "comparison")
			services := repairServices{Backend: repairMockBackend(t), Profile: profile, NewModel: func(string) (budget.ProfiledModel, error) {
				return budgetTestModel{profile: profile, modelFunc: func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
					if mode == "cancel" {
						cancel()
						return agent.Reply{}, context.Canceled
					}
					if mode == "publication-error" {
						if err := os.Remove(filepath.Join(output, "A/attempt.json")); err != nil {
							t.Fatal(err)
						}
						if err := os.Mkdir(filepath.Join(output, "A/attempt.json"), 0700); err != nil {
							t.Fatal(err)
						}
					}
					return agent.Reply{Finish: agent.FinishStop, Usage: &agent.Usage{InputTokens: 15, OutputTokens: 5, TotalTokens: 20}}, nil
				}}, nil
			}}
			services.Evaluate = func(_ context.Context, req checks.EvaluationRequest) (checks.Evaluation, error) {
				if mode == "validation-error" {
					return checks.Evaluation{}, errors.New("injected validation infrastructure error")
				}
				value, err := repairMockEvaluation(t, req)
				if mode == "candidate-drift" {
					if err := os.WriteFile(filepath.Join(req.CandidateDir, "go.mod"), []byte("drift"), 0644); err != nil {
						t.Fatal(err)
					}
				}
				return value, err
			}
			summary, err := runRepairComparison(ctx, project, output, p, seed, prompt, services)
			if err == nil || summary.Status != "Aborted" || summary.Attempts[1].Agent != nil {
				t.Fatal("shared fault allowed another model attempt", summary, err)
			}
			diskB := readJSON[repairAttempt](t, filepath.Join(output, "B/attempt.json"))
			if diskB.Status != "NotStarted" || summary.Attempts[1].Status != diskB.Status {
				t.Fatal("unstarted attempt state differs from summary")
			}
			if summary.Attempts[0].CandidateSHA256 == "" {
				t.Fatal("partial candidate was lost")
			}
			if mode == "cancel" && summary.Budget.Totals.UnknownTokens != 100 {
				t.Fatal("cancelled request was released")
			}
		})
	}
}

func TestRepairProtocolRejectsChangedInputs(t *testing.T) {
	project, _, _, _, _ := repairInputs(t)
	for _, mode := range []string{"seed", "prompt", "limits", "unknown-field", "extra-json", "unsafe-path"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, repairProtocolDir)
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"protocol.json", "seed.json", "prompt.txt"} {
				raw, err := os.ReadFile(filepath.Join(project, repairProtocolDir, name))
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			name := "protocol.json"
			raw, _ := os.ReadFile(filepath.Join(dir, name))
			switch mode {
			case "seed":
				name = "seed.json"
				raw = []byte(`{}`)
			case "prompt":
				name = "prompt.txt"
				raw = []byte("changed")
			case "limits":
				raw = bytes.Replace(raw, []byte(`"max_model_calls": 12`), []byte(`"max_model_calls": 13`), 1)
			case "unknown-field":
				raw = bytes.Replace(raw, []byte(`"schema_version": 1`), []byte(`"schema_version": 1, "extra": true`), 1)
			case "extra-json":
				raw = append(raw, []byte(` {}`)...)
			case "unsafe-path":
				var p repairProtocol
				if err := json.Unmarshal(raw, &p); err != nil {
					t.Fatal(err)
				}
				seedRaw, _ := json.Marshal(tree{"../outside": []byte("unsafe")})
				if err := os.WriteFile(filepath.Join(dir, "seed.json"), seedRaw, 0600); err != nil {
					t.Fatal(err)
				}
				p.SeedFileSHA256 = digest(seedRaw)
				raw, _ = json.Marshal(p)
			}
			if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := loadRepairProtocol(root); err == nil {
				t.Fatal("changed input was accepted", mode)
			}
		})
	}
}

func TestRepairInfrastructureFailureStopsBeforeNextModel(t *testing.T) {
	project, p, _, seed, prompt := repairInputs(t)
	profile := repairControlledProfile(p.Model)
	ordinary := repairMockBackend(t)
	backend := baselineBackend{run: func(ctx context.Context, req execution.Request) (execution.Result, error) {
		if strings.Contains(filepath.ToSlash(req.Directory), "/workspace/translation") {
			return execution.Result{}, errors.New("injected executor startup error")
		}
		return ordinary.Run(ctx, req)
	}}
	started := 0
	services := repairServices{Backend: backend, Profile: profile, NewModel: func(string) (budget.ProfiledModel, error) { started++; return repairScript(profile), nil }}
	summary, err := runRepairComparison(context.Background(), project, filepath.Join(t.TempDir(), "comparison"), p, seed, prompt, services)
	if err == nil || summary.Status != "Aborted" || started != 1 || summary.Attempts[1].Status != "NotStarted" {
		t.Fatal("executor failure continued comparison", summary, err)
	}
	if summary.Attempts[0].Agent.ToolCalls != 2 || summary.Attempts[0].Verdict.Submitted != quality.StatusFail {
		t.Fatal("failed check was counted as a submission")
	}
}
