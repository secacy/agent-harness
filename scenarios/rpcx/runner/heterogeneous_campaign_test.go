package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
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

type heterogeneousCase struct {
	input     task.Target
	before    tree
	candidate tree
	edits     []agent.ToolCall
	spec      string
}

// 参考补丁仅用于测试期构造脚本化模型，Campaign 与 Agent 均不能读取补丁目录。
func heterogeneousCases(t *testing.T) map[string]heterogeneousCase {
	t.Helper()
	scenario, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	definitions := []struct {
		id, dataset, spec, module, patch string
		makeTarget                       func(string, string, string) (task.Target, error)
	}{
		{"goddd", "rpcx-l2-goddd-v1", "goddd/specs/v0.3.0.md", "goddd", "goddd/cases/patches/correct.patch", TargetFromGoddd},
		{"translation", "rpcx-l2-translation-v1", "translation/specs/v0.4.0.md", "translation", "translation/cases/patches/correct.patch", TargetFromTranslation},
		{"workouts", "rpcx-l2-workouts-v1", "workouts/specs/v0.5.0.md", "trainings", "workouts/cases/patches/correct.patch", TargetFromWorkouts},
	}
	cases := make(map[string]heterogeneousCase, len(definitions))
	for _, definition := range definitions {
		fixtures := filepath.Join(scenario, "l2/data", definition.dataset, "fixtures")
		input, err := definition.makeTarget(definition.id, fixtures, filepath.Join(scenario, definition.spec))
		if err != nil {
			t.Fatal(err)
		}
		profile, err := lookupProfile(input.ProfileID)
		if err != nil {
			t.Fatal(err)
		}
		material, _, err := readTarget(context.Background(), input, profile)
		if err != nil {
			t.Fatal(err)
		}
		layout := t.TempDir()
		if err := material.write(context.Background(), layout); err != nil {
			t.Fatal(err)
		}
		applyPatch(t, filepath.Join(layout, definition.module), filepath.Join(scenario, definition.patch))
		before := material.module(definition.module)
		candidate := mustReadTree(t, filepath.Join(layout, definition.module))
		var paths []string
		for path, contents := range candidate {
			if !bytes.Equal(contents, before[path]) {
				paths = append(paths, path)
			}
		}
		sort.Strings(paths)
		var edits []agent.ToolCall
		for i, path := range paths {
			edits = append(edits, replacement(t, fmt.Sprint(i), path, before[path], candidate[path]))
		}
		if len(edits) == 0 {
			t.Fatalf("%s has no scripted edits", definition.id)
		}
		cases[definition.id] = heterogeneousCase{input: input, before: before, candidate: candidate, edits: edits, spec: strings.TrimSuffix(filepath.Base(definition.spec), ".md")}
	}
	return cases
}

func heterogeneousCampaign(t *testing.T, name string, ids []string, cases map[string]heterogeneousCase, backend execution.Executor) campaign.Config {
	t.Helper()
	parent := t.TempDir()
	if root := os.Getenv("HARNESS_HETEROGENEOUS_ARTIFACTS"); root != "" {
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		var err error
		parent, err = os.MkdirTemp(root, name+"-")
		if err != nil {
			t.Fatal(err)
		}
	}
	profile := budget.Profile{Provider: "controlled", Endpoint: "in-process", Model: "scripted-test-model", ContextTokens: 90, MaxOutputTokens: 10, SourceSHA256: strings.Repeat("e", 64), VerifiedAt: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)}
	config := campaign.Config{
		Directory: filepath.Join(parent, "campaign"), Backend: backend,
		Execute: CampaignExecutor(backend.Info().Kind == execution.KindLocal),
		Plan: campaign.Plan{ID: name, Concurrency: 2, ExecutionConcurrency: 2,
			Budget: budget.Limits{MaxCalls: 2 * int64(len(ids)), MaxTokens: 200 * int64(len(ids))}, Profile: profile},
	}
	for _, id := range ids {
		caseData, ok := cases[id]
		if !ok {
			t.Fatalf("unknown target %q", id)
		}
		config.Plan.Targets = append(config.Plan.Targets, campaign.Target{
			Input: caseData.input, Budget: budget.Limits{MaxCalls: 2, MaxTokens: 200},
			Limits: agent.Limits{MaxModelCalls: 2, MaxToolCalls: 30}, Timeout: 10 * time.Minute,
		})
	}
	config.NewModel = func(input task.Target) (budget.ProfiledModel, error) {
		caseData, ok := cases[input.ID]
		if !ok {
			return nil, fmt.Errorf("unknown target %q", input.ID)
		}
		calls := 0
		return budgetTestModel{profile: profile, modelFunc: func(_ context.Context, messages []agent.Message, _ []agent.ToolSpec) (agent.Reply, error) {
			calls++
			for _, message := range messages {
				if message.Role == agent.RoleTool && message.IsError {
					return agent.Reply{}, fmt.Errorf("%s tool failed: %s", input.ID, message.Content)
				}
			}
			reply := agent.Reply{Usage: &agent.Usage{InputTokens: 15, OutputTokens: 5, TotalTokens: 20}}
			switch calls {
			case 1:
				if len(messages) < 2 || !strings.Contains(messages[1].Content, caseData.spec) {
					return agent.Reply{}, fmt.Errorf("%s task prompt missing migration context", input.ID)
				}
				reply.Finish, reply.ToolCalls = agent.FinishToolCalls, caseData.edits
			case 2:
				reply.Finish, reply.Content = agent.FinishStop, "Scripted candidate submitted."
			default:
				return agent.Reply{}, fmt.Errorf("%s exceeded scripted calls", input.ID)
			}
			return reply, nil
		}}, nil
	}
	return config
}

func assertHeterogeneousDelivery(t *testing.T, record campaign.Record, summary campaign.Summary, cases map[string]heterogeneousCase, solo map[string]Report) {
	t.Helper()
	if record.Status != campaign.Completed || summary.Counts.Completed != len(summary.Targets) || summary.Counts.Verdicts["ReadyForReview"] != len(summary.Targets) || summary.Budget.Totals.Calls != int64(2*len(summary.Targets)) || summary.Budget.Totals.KnownTokens != int64(40*len(summary.Targets)) {
		t.Fatalf("incomplete heterogeneous campaign: %+v %+v", record, summary.Counts)
	}
	for _, target := range summary.Targets {
		report := campaignReport(t, record.Directory, target)
		caseData := cases[target.ID]
		if report.Input.Target == nil || report.Input.Target.ID != target.ID || report.Input.Target.ProfileID != caseData.input.ProfileID || report.CandidateSHA256 != caseData.candidate.hash() || report.Input.Budget == nil || report.Input.Budget.TargetID != target.ID || report.Input.Budget.RunID != target.Attempt.RunID {
			t.Fatalf("%s input, candidate or budget binding mismatch", target.ID)
		}
		if summary.Budget.Targets[target.ID].Totals.Calls != 2 {
			t.Fatalf("%s budget does not match model calls", target.ID)
		}
		if reference, ok := solo[target.ID]; ok {
			compareCampaignEvidence(t, reference, report)
		}
		switch target.ID {
		case "translation":
			if len(report.Baseline.ConfigurationTests) != 2 {
				t.Fatal("translation lost a build configuration")
			}
		case "workouts":
			if len(report.Baseline.ModuleTests["common"]) != 3 {
				t.Fatal("workouts lost common prerequisite tests")
			}
		}
		replay := t.TempDir()
		if err := caseData.before.write(context.Background(), replay); err != nil {
			t.Fatal(err)
		}
		applyPatch(t, replay, filepath.Join(record.Directory, filepath.FromSlash(target.Attempt.Directory), "artifacts/candidate.patch"))
		if mustReadTree(t, replay).hash() != caseData.candidate.hash() {
			t.Fatalf("%s exported patch did not replay", target.ID)
		}
	}
}

func TestCampaignHeterogeneousDelivery(t *testing.T) {
	if os.Getenv("HARNESS_HETEROGENEOUS_ACCEPTANCE") != "1" {
		t.Skip("set HARNESS_HETEROGENEOUS_ACCEPTANCE=1 for heterogeneous campaign delivery")
	}
	cases := heterogeneousCases(t)
	ids := []string{"goddd", "translation", "workouts"}
	solo := make(map[string]Report, len(ids))
	for _, id := range ids {
		config := heterogeneousCampaign(t, "solo-"+id, []string{id}, cases, execution.Local{})
		record, summary, err := campaign.Run(context.Background(), config)
		if err != nil {
			t.Fatal(err)
		}
		assertHeterogeneousDelivery(t, record, summary, cases, nil)
		solo[id] = campaignReport(t, record.Directory, summary.Targets[0])
		t.Logf("solo %s: %s", id, record.Directory)
	}

	config := heterogeneousCampaign(t, "heterogeneous-local", ids, cases, execution.Local{})
	record, summary, err := campaign.Run(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	assertHeterogeneousDelivery(t, record, summary, cases, solo)
	t.Logf("local batch: %s", record.Directory)

	var baselineFaults atomic.Int32
	backend := baselineBackend{run: func(ctx context.Context, req execution.Request) (execution.Result, error) {
		if strings.Contains(filepath.ToSlash(req.Directory), "/targets/translation/") {
			baselineFaults.Add(1)
			return execution.Result{ExitCode: 1, Stderr: []byte("injected translation baseline failure")}, nil
		}
		return (execution.Local{}).Run(ctx, req)
	}}
	config = heterogeneousCampaign(t, "heterogeneous-fault", ids, cases, backend)
	record, summary, err = campaign.Run(context.Background(), config)
	if err != nil || record.Status != campaign.Completed || summary.Counts.Completed != 2 || summary.Counts.Failed != 1 || baselineFaults.Load() != 1 || summary.Budget.Totals.Calls != 4 {
		t.Fatalf("fault batch: %+v %+v %v", record, summary.Counts, err)
	}
	for _, target := range summary.Targets {
		report := campaignReport(t, record.Directory, target)
		if target.ID == "translation" {
			if report.FailureCode != "baseline_failed" || report.Validation != nil || summary.Budget.Targets[target.ID].Totals.Calls != 0 {
				t.Fatal("fault target entered generation or validation")
			}
			continue
		}
		if target.Attempt.Status != campaign.Completed {
			t.Fatalf("unaffected target %s failed", target.ID)
		}
		compareCampaignEvidence(t, solo[target.ID], report)
	}
	t.Logf("fault batch: %s", record.Directory)

}

func TestCampaignHeterogeneousDockerDelivery(t *testing.T) {
	if os.Getenv("HARNESS_HETEROGENEOUS_DOCKER") != "1" {
		t.Skip("set HARNESS_HETEROGENEOUS_DOCKER=1 for Docker campaign delivery")
	}
	cases := heterogeneousCases(t)
	docker, err := execution.NewDocker(context.Background(), execution.DockerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	config := heterogeneousCampaign(t, "heterogeneous-docker", []string{"goddd", "translation", "workouts"}, cases, docker)
	record, summary, err := campaign.Run(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	assertHeterogeneousDelivery(t, record, summary, cases, nil)
	t.Logf("docker batch: %s", record.Directory)
}
