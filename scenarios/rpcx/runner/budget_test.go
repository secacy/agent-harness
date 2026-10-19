package runner

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
)

type budgetTestModel struct {
	modelFunc
	profile budget.Profile
}

func (m budgetTestModel) BudgetProfile() (budget.Profile, error) { return m.profile, nil }

func TestRunBudgetBindingAndDenial(t *testing.T) {
	for _, deny := range []bool{false, true} {
		t.Run(map[bool]string{false: "delivery", true: "denied-before-generation"}[deny], func(t *testing.T) {
			profile := budget.Profile{Provider: "controlled", Endpoint: "in-process", Model: "scripted-test-model", ContextTokens: 90, MaxOutputTokens: 10, SourceSHA256: strings.Repeat("e", 64), VerifiedAt: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)}
			tokens := int64(100)
			if deny {
				tokens = 50
			}
			ledger, err := budget.Create(budget.Config{Directory: filepath.Join(t.TempDir(), "budget"), CampaignID: "single-run", Profile: profile, Limits: budget.Limits{MaxCalls: 1, MaxTokens: tokens}, Targets: map[string]budget.Limits{"orders": {MaxCalls: 1, MaxTokens: 100}}})
			if err != nil {
				t.Fatal(err)
			}
			var called atomic.Int32
			config := configForTest(t, budgetTestModel{modelFunc: func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
				called.Add(1)
				return agent.Reply{Finish: agent.FinishStop, Content: "submitted unchanged", Usage: &agent.Usage{InputTokens: 15, OutputTokens: 5, TotalTokens: 20}}, nil
			}, profile: profile})
			config.Budget = &budget.Target{Ledger: ledger, ID: "orders"}
			record, err := Run(context.Background(), config)
			if deny {
				if !errors.Is(err, agent.ErrModelBudget) || record.Status != StatusFailed || record.AgentExitReason != agent.ExitLimitReached || record.Verdict != "" || called.Load() != 0 {
					t.Fatalf("record=%+v err=%v calls=%d", record, err, called.Load())
				}
			} else {
				if err != nil || record.Status != StatusCompleted || record.Verdict != "Rejected" || called.Load() != 1 {
					t.Fatalf("record=%+v err=%v calls=%d", record, err, called.Load())
				}
			}
			report := readJSON[Report](t, filepath.Join(record.Directory, "artifacts/report.json"))
			input := readJSON[Input](t, filepath.Join(record.Directory, "inputs/task.json"))
			if input.Budget == nil || report.Input.Budget == nil || input.Budget.RunID != record.ID || input.Budget.TargetID != "orders" || input.Budget.CampaignID != "single-run" || input.Budget.ProfileSHA256 == "" {
				t.Fatalf("missing budget identity: %+v", input.Budget)
			}
			snapshot := readJSON[budget.Snapshot](t, input.Budget.LedgerPath)
			if deny {
				if len(snapshot.Calls) != 0 || len(snapshot.Rejections) != 1 || snapshot.Totals.Calls != 0 {
					t.Fatal(snapshot)
				}
			} else {
				if len(snapshot.Calls) != 1 || snapshot.Calls[0].RunID != record.ID || snapshot.Totals.KnownTokens != 20 {
					t.Fatal(snapshot)
				}
			}
		})
	}
}
