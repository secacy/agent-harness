package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/audit"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/repotools"
	"github.com/secacy/agent-harness/internal/task"
)

func TestCompletionAuditBindings(t *testing.T) {
	for _, mode := range []string{"valid", "policy", "unknown-policy", "missing-marker", "ordinal", "prefix", "tool-result", "input-hash", "missing-admission"} {
		t.Run(mode, func(t *testing.T) {
			f := newAuditFixture(t, ProfileT1T2)
			plan := readJSON[campaign.Plan](t, filepath.Join(f.root, "plan.json"))
			plan.Targets[0].CompletionPolicy = agent.FinalResponseV1
			f.report.Input.CompletionPolicy = agent.FinalResponseV1
			f.report.Input.MaxModelCalls = 1
			f.report.Input.MaxToolCalls = 1
			executor, err := repotools.New(repotools.Config{Workspace: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			loop := agent.Loop{CompletionPolicy: agent.FinalResponseV1, Limits: agent.Limits{MaxModelCalls: 1, MaxToolCalls: 1}, Executor: executor, Model: modelFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
				return agent.Reply{Finish: agent.FinishStop}, nil
			})}
			result, err := loop.Run(context.Background(), []agent.Message{{Role: agent.RoleUser, Content: "submit"}})
			if err != nil {
				t.Fatal(err)
			}
			input, _ := json.Marshal(struct {
				Messages []agent.Message
				Tools    []agent.ToolSpec
			}{Messages: result.Messages[:result.FinalResponseAttempt.MessageCount]})
			ledger := readJSON[budget.Snapshot](t, filepath.Join(f.root, "budget/budget.json"))
			ledger.Calls = []budget.Call{{ID: "call-1", TargetID: "orders", RunID: "run-1", InputSHA256: digest(input), ReservedTokens: 100, State: budget.StateSettled, Usage: &agent.Usage{}, Outcome: "reply", CreatedAt: ledger.Profile.VerifiedAt, UpdatedAt: ledger.Profile.VerifiedAt}}
			ledger.Totals = budget.Totals{Calls: 1}
			account := ledger.Targets["orders"]
			account.Totals = ledger.Totals
			ledger.Targets["orders"] = account
			switch mode {
			case "policy":
				f.report.Input.CompletionPolicy = ""
			case "unknown-policy":
				f.report.Input.CompletionPolicy = "unknown"
				plan.Targets[0].CompletionPolicy = "unknown"
			case "missing-marker":
				result.FinalResponseAttempt = nil
			case "ordinal":
				result.FinalResponseAttempt.ModelCall++
			case "prefix":
				result.FinalResponseAttempt.MessageCount--
			case "tool-result":
				result.Messages = append(result.Messages, agent.Message{Role: agent.RoleTool, ToolCallID: "unrequested"})
			case "input-hash":
				ledger.Calls[0].InputSHA256 = strings.Repeat("a", 64)
			case "missing-admission":
				ledger.Calls = nil
			}
			auditPut(t, f.root, "plan.json", plan)
			raw, _ := os.ReadFile(filepath.Join(f.root, "plan.json"))
			f.record.PlanSHA256 = digest(raw)
			auditPut(t, f.dir, "inputs/task.json", f.report.Input)
			auditPut(t, f.dir, "artifacts/agent.json", result)
			auditPut(t, f.root, "budget/budget.json", ledger)
			f.publish(t)
			inspected := inspectAuditFixture(t, f)
			if mode == "valid" {
				if inspected.Consistency != "pass" {
					t.Fatal(inspected.Violations)
				}
			} else if !auditHas(inspected.Violations, "completion_policy_mismatch") {
				t.Fatal("completion corruption accepted", inspected.Violations)
			}
		})
	}
}

// 三类候选和非法末轮共用调度器，模型只重放固定编辑，不访问服务。
func TestFinalResponseTranslationCampaign(t *testing.T) {
	if os.Getenv("HARNESS_COMPLETION_ACCEPTANCE") != "1" {
		t.Skip("set HARNESS_COMPLETION_ACCEPTANCE=1 for controlled Translation delivery")
	}
	cases := heterogeneousCases(t)
	base := cases["translation"]
	ids := []string{"correct", "alternate-missing-timeout", "mapping-helper", "illegal"}
	for _, id := range ids {
		c := base
		c.input.ID = id
		if id != "correct" && id != "illegal" {
			layout := t.TempDir()
			if err := c.before.write(context.Background(), layout); err != nil {
				t.Fatal(err)
			}
			patch, err := filepath.Abs("../translation/cases/patches/" + id + ".patch")
			if err != nil {
				t.Fatal(err)
			}
			applyPatch(t, layout, patch)
			c.candidate = mustReadTree(t, layout)
			c.edits = nil
			for _, name := range sortedKeys(c.candidate) {
				if !bytes.Equal(c.before[name], c.candidate[name]) {
					c.edits = append(c.edits, replacement(t, fmt.Sprint(len(c.edits)), name, c.before[name], c.candidate[name]))
				}
			}
		}
		cases[id] = c
	}
	backend := execution.Executor(execution.Local{})
	config := heterogeneousCampaign(t, "final-response", ids, cases, backend)
	for i := range config.Plan.Targets {
		config.Plan.Targets[i].CompletionPolicy = agent.FinalResponseV1
	}
	original := config.NewModel
	config.NewModel = func(input task.Target) (budget.ProfiledModel, error) {
		underlying, err := original(input)
		if err != nil {
			return nil, err
		}
		calls := 0
		return budgetTestModel{profile: config.Plan.Profile, modelFunc: func(ctx context.Context, messages []agent.Message, tools []agent.ToolSpec) (agent.Reply, error) {
			calls++
			if calls == 2 && tools != nil {
				t.Error("final tools advertised")
			}
			if calls == 2 && input.ID == "illegal" {
				return agent.Reply{Finish: agent.FinishToolCalls, ToolCalls: []agent.ToolCall{{ID: "late", Name: "replace_text", Arguments: `{"path":"go.mod","old":"module","new":"corrupt"}`}}, Usage: &agent.Usage{InputTokens: 15, OutputTokens: 5, TotalTokens: 20}}, nil
			}
			return underlying.Complete(ctx, messages, tools)
		}}, nil
	}
	record, summary, err := campaign.Run(context.Background(), config)
	if err != nil || record.Status != campaign.Completed || summary.Counts.Completed != 3 || summary.Counts.Failed != 1 {
		t.Fatal(record, summary.Counts, err)
	}
	want := map[string]string{"correct": "ReadyForReview", "alternate-missing-timeout": "Rejected", "mapping-helper": "NeedsAttention", "illegal": ""}
	for _, target := range summary.Targets {
		report := campaignReport(t, config.Directory, target)
		dir := filepath.Join(config.Directory, filepath.FromSlash(target.Attempt.Directory))
		trace := readJSON[agent.Result](t, filepath.Join(dir, "artifacts/agent.json"))
		if report.Input.CompletionPolicy != agent.FinalResponseV1 || trace.FinalResponseAttempt == nil || trace.FinalResponseAttempt.ModelCall != 2 || target.Attempt.Verdict != want[target.ID] || report.CandidateSHA256 != cases[target.ID].candidate.hash() {
			t.Fatalf("%s: report=%+v trace=%+v", target.ID, report, trace)
		}
		if target.ID == "illegal" && report.Validation != nil {
			t.Fatal("illegal final request reached gate")
		}
		review, err := os.ReadFile(filepath.Join(dir, "artifacts/review.md"))
		if err != nil || !strings.Contains(string(review), "仅回复模式") {
			t.Fatal("missing completion review", err)
		}
		assertReviewLinks(t, dir, review)
	}
	checked, err := audit.Inspect(context.Background(), audit.Request{CampaignDir: config.Directory, OutputDir: filepath.Join(filepath.Dir(config.Directory), "audit")}, InspectRun)
	if err != nil || checked.Consistency != "pass" {
		t.Fatal("completion campaign audit", checked.Violations, err)
	}
	t.Log("FINAL_RESPONSE_CAMPAIGN", config.Directory)
}
