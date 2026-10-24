package runner

import (
	"encoding/json"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/audit"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
)

func (a *runAudit) checkCompletion(report Report) {
	issue := func(reason string, sources ...string) {
		a.issue(audit.Violation, "completion_policy_mismatch", "artifacts/agent.json", "FinalResponseAttempt", reason, sources...)
	}
	var plan campaign.Plan
	raw, err := a.view.ReadFile("plan.json")
	matched := false
	var planned agent.CompletionPolicy
	var limits agent.Limits
	if err == nil && json.Unmarshal(raw, &plan) == nil {
		for _, target := range plan.Targets {
			if target.Input.ID == a.ref.Target.ID {
				planned, limits = target.CompletionPolicy, target.Limits
				matched = planned == report.Input.CompletionPolicy
			}
		}
	}
	p := report.Input.CompletionPolicy
	if p != "" || planned != "" {
		if !matched || p.Validate() != nil || limits.MaxModelCalls != report.Input.MaxModelCalls || limits.MaxToolCalls != report.Input.MaxToolCalls {
			issue("completion policy or limits differ from campaign plan", "plan.json", a.file("inputs/task.json"))
			return
		}
	}
	if report.AgentRecord != "agent.json" {
		return // 基线失败时尚未进入生成阶段。
	}
	var result agent.Result
	if !a.read("artifacts/agent.json", &result, 0, true) {
		return
	}
	if err := agent.ValidateCompletionResult(p, limits, result); err != nil {
		issue(err.Error())
		return
	}
	attempt := result.FinalResponseAttempt
	if attempt == nil || report.Input.Budget == nil {
		return
	}
	// 此摘要对应模型接口输入，不能解释为供应商收到的 HTTP 字节。
	input, _ := json.Marshal(struct {
		Messages []agent.Message
		Tools    []agent.ToolSpec
	}{Messages: result.Messages[:attempt.MessageCount]})
	expected := digest(input)
	budgetPath, version := a.ref.BudgetPath, 2
	if budgetPath == "" {
		budgetPath, version = "budget/budget.json", 1
	}
	var ledger budget.Snapshot
	raw, err = a.view.ReadFile(budgetPath)
	if err != nil || audit.DecodeJSON(raw, &ledger, version) != nil {
		a.issue(audit.Gap, "completion_budget_unverified", "artifacts/agent.json", "FinalResponseAttempt", "ledger is unavailable or unsupported", budgetPath)
		return
	}
	var calls []budget.Call
	for _, call := range ledger.Calls {
		if call.TargetID == a.ref.Target.ID && call.RunID == a.ref.Record.ID {
			calls = append(calls, call)
		}
	}
	if len(calls) == attempt.ModelCall {
		if calls[len(calls)-1].InputSHA256 != expected {
			issue("final-response input differs from admitted request", budgetPath)
		}
		return
	}
	if len(calls) != attempt.ModelCall-1 || len(result.Messages) != attempt.MessageCount || result.Reason == agent.ExitCompleted {
		issue("final-response reply or attempt count lacks matching budget admission", budgetPath)
		return
	}
	for _, rejected := range ledger.Rejections {
		if rejected.TargetID == a.ref.Target.ID && rejected.RunID == a.ref.Record.ID {
			if rejected.InputSHA256 != expected {
				issue("final-response input differs from rejected request", budgetPath)
			}
			return
		}
	}
	a.issue(audit.Observation, "completion_not_admitted", "artifacts/agent.json", "FinalResponseAttempt", "local attempt has no admission or rejection record; cancellation or infrastructure failure may precede admission", budgetPath)
}
