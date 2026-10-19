package campaign

import (
	"fmt"
	"strings"

	"github.com/secacy/agent-harness/internal/budget"
)

func summarize(id, planSHA string, targets []TargetRecord, ledger budget.Snapshot) Summary {
	summary := Summary{CampaignID: id, PlanSHA256: planSHA, Targets: targets, Budget: ledger, Counts: Counts{Verdicts: map[string]int{}}}
	for _, target := range targets {
		if target.State == Skipped {
			summary.Counts.Skipped++
			continue
		}
		if target.Attempt == nil {
			continue
		}
		switch target.Attempt.Status {
		case Completed:
			summary.Counts.Completed++
			summary.Counts.Verdicts[target.Attempt.Verdict]++
		case Failed:
			summary.Counts.Failed++
		case Cancelled:
			summary.Counts.Cancelled++
		}
	}
	return summary
}
func markdown(summary Summary) []byte {
	var text strings.Builder
	fmt.Fprintf(&text, "# Campaign %s\n\n执行完成 %d；执行失败 %d；取消 %d；未启动 %d。质量放行数不代表独立评测成功数。\n\n", summary.CampaignID, summary.Counts.Completed, summary.Counts.Failed, summary.Counts.Cancelled, summary.Counts.Skipped)
	fmt.Fprintln(&text, "| Target | 状态 | 判定 | 报告 |\n| --- | --- | --- | --- |")
	for _, target := range summary.Targets {
		status, verdict, report := string(target.State), "—", "—"
		if target.Attempt != nil {
			status = string(target.Attempt.Status)
			if target.Attempt.Verdict != "" {
				verdict = target.Attempt.Verdict
			}
			if target.Attempt.Report.Path != "" {
				report = "[report.json](" + target.Attempt.Report.Path + ")"
			}
		}
		fmt.Fprintf(&text, "| %s | %s | %s | %s |\n", target.ID, status, verdict, report)
	}
	t := summary.Budget.Totals
	fmt.Fprintf(&text, "\n准入请求 %d；已知 Token %d；在途预留 %d；未知占用 %d。详细错误与逐请求账目见 [summary.json](summary.json)。\n", t.Calls, t.KnownTokens, t.ReservedTokens, t.UnknownTokens)
	return []byte(text.String())
}
