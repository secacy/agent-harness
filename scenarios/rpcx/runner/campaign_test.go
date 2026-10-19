package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/task"
)

func campaignConfig(t *testing.T, ids []string, concurrency int, backend execution.Executor) campaign.Config {
	t.Helper()
	scenario, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	scenario, err = filepath.EvalSymlinks(scenario)
	if err != nil {
		t.Fatal(err)
	}
	baseline := mustReadTree(t, filepath.Join(scenario, "fixtures/demo-orders"))
	migrated := migratedTree(t, scenario)
	var paths []string
	for path, contents := range migrated {
		if !bytes.Equal(contents, baseline[path]) {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	var edits []agent.ToolCall
	for i, path := range paths {
		edits = append(edits, replacement(t, fmt.Sprint(i), path, baseline[path], migrated[path]))
	}
	profile := budget.Profile{Provider: "controlled", Endpoint: "in-process", Model: "scripted-test-model", ContextTokens: 90, MaxOutputTokens: 10, SourceSHA256: strings.Repeat("e", 64), VerifiedAt: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)}
	parent := t.TempDir()
	if root := os.Getenv("HARNESS_CAMPAIGN_ARTIFACTS"); root != "" {
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		parent, err = os.MkdirTemp(root, "rpcx-campaign-")
		if err != nil {
			t.Fatal(err)
		}
	}
	config := campaign.Config{Directory: filepath.Join(parent, "campaign"), Backend: backend, Execute: CampaignExecutor(backend.Info().Kind == execution.KindLocal), Plan: campaign.Plan{ID: "rpcx-controlled", Concurrency: concurrency, ExecutionConcurrency: concurrency, Budget: budget.Limits{MaxCalls: 100, MaxTokens: 10000}, Profile: profile}}
	for _, id := range ids {
		input, err := TargetFromScenario(id, scenario, "v0.1.0")
		if err != nil {
			t.Fatal(err)
		}
		if id == "missing-input" {
			input.Repository.Directory = filepath.Join(t.TempDir(), "missing-source")
		}
		quota := budget.Limits{MaxCalls: 5, MaxTokens: 500}
		if id == "budget-denied" {
			quota.MaxTokens = 50
		}
		config.Plan.Targets = append(config.Plan.Targets, campaign.Target{Input: input, Budget: quota, Limits: agent.Limits{MaxModelCalls: 5, MaxToolCalls: 30}, Timeout: 3 * time.Minute})
	}
	config.NewModel = func(input task.Target) (budget.ProfiledModel, error) {
		calls := 0
		return budgetTestModel{profile: profile, modelFunc: func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
			calls++
			reply := agent.Reply{Finish: agent.FinishStop, Content: "submitted", Usage: &agent.Usage{InputTokens: 15, OutputTokens: 5, TotalTokens: 20}}
			if input.ID == "baseline-red" || input.ID == "budget-denied" || input.ID == "execution-killed" || input.ID == "missing-input" {
				t.Errorf("%s reached underlying model", input.ID)
			}
			if input.ID == "garbage" {
				reply.Finish = agent.FinishReason("invalid-reply")
			}
			if input.ID == "correct" && calls == 1 {
				reply.Finish = agent.FinishToolCalls
				reply.ToolCalls = edits
			}
			return reply, nil
		}}, nil
	}
	return config
}

func TestCampaignRPCXSerialParallelFaultIsolation(t *testing.T) {
	solo := campaignConfig(t, []string{"correct"}, 1, execution.Local{})
	record, single, err := campaign.Run(context.Background(), solo)
	if err != nil || record.Status != campaign.Completed || single.Counts.Verdicts["ReadyForReview"] != 1 {
		t.Fatalf("%+v %+v %v", record, single.Counts, err)
	}
	soloReport := campaignReport(t, record.Directory, single.Targets[0])
	var baselineFaults atomic.Int32
	backend := baselineBackend{run: func(ctx context.Context, req execution.Request) (execution.Result, error) {
		if strings.Contains(filepath.ToSlash(req.Directory), "/targets/baseline-red/") {
			baselineFaults.Add(1)
			return execution.Result{ExitCode: 1, Stderr: []byte("injected red baseline command")}, nil
		}
		return (execution.Local{}).Run(ctx, req)
	}}
	parallel := campaignConfig(t, []string{"baseline-red", "correct", "garbage", "budget-denied", "unchanged", "missing-input"}, 3, backend)
	record, batch, err := campaign.Run(context.Background(), parallel)
	if err != nil || record.Status != campaign.Completed || batch.Counts.Completed != 2 || batch.Counts.Failed != 4 || batch.Counts.Verdicts["ReadyForReview"] != 1 || batch.Counts.Verdicts["Rejected"] != 1 || baselineFaults.Load() != 1 || batch.Budget.Totals.Calls != 4 {
		t.Fatalf("%+v %+v budget=%+v err=%v", record, batch.Counts, batch.Budget, err)
	}
	for _, target := range batch.Targets {
		report := campaignReport(t, record.Directory, target)
		if report.Input.Target.ID != target.ID || report.RunID != target.Attempt.RunID || report.Status != Status(target.Attempt.Status) {
			t.Fatal("report binding mismatch", target)
		}
		switch target.ID {
		case "correct":
			compareCampaignEvidence(t, soloReport, report)
		case "baseline-red":
			if report.FailureCode != "baseline_failed" || report.AgentRecord != "" || batch.Budget.Targets[target.ID].Totals.Calls != 0 {
				t.Fatal(report)
			}
		case "missing-input":
			if report.FailureCode != "input_invalid" || report.Baseline != nil || batch.Budget.Targets[target.ID].Totals.Calls != 0 {
				t.Fatal(report)
			}
		case "budget-denied":
			if batch.Budget.Targets[target.ID].Totals.Calls != 0 || len(batch.Budget.Rejections) != 1 {
				t.Fatal(batch.Budget)
			}
		}
	}
	t.Logf("solo=%s batch=%s completed=%d failed=%d admitted=%d", solo.Directory, record.Directory, batch.Counts.Completed, batch.Counts.Failed, batch.Budget.Totals.Calls)
}
func campaignReport(t *testing.T, root string, target campaign.TargetRecord) Report {
	t.Helper()
	if target.Attempt == nil || target.Attempt.Report.Path == "" {
		t.Fatalf("missing report: %+v", target)
	}
	return readJSON[Report](t, filepath.Join(root, filepath.FromSlash(target.Attempt.Report.Path)))
}
func compareCampaignEvidence(t *testing.T, a, b Report) {
	t.Helper()
	if a.Validation == nil || b.Validation == nil {
		t.Fatal("missing validation")
	}
	if a.CandidateSHA256 != b.CandidateSHA256 || a.PatchSHA256 != b.PatchSHA256 || a.Input.SpecSHA256 != b.Input.SpecSHA256 || a.Input.BaselineSHA256 != b.Input.BaselineSHA256 || a.Validation.Verdict != b.Validation.Verdict || a.Validation.VerifierVersion != b.Validation.VerifierVersion || a.Validation.MaterialsSHA256 != b.Validation.MaterialsSHA256 {
		t.Fatal("parallel run changed bound inputs, candidate or verdict")
	}
	statuses := func(report Report) map[string]string {
		out := map[string]string{}
		for name, check := range report.Validation.Checks {
			out[name] = check.Status
		}
		return out
	}
	if !reflect.DeepEqual(statuses(a), statuses(b)) {
		t.Fatal("parallel run changed independent check outcomes")
	}
}
