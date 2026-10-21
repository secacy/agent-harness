package round

import (
	"fmt"
	"strings"

	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
)

func interval(lower, upper, total int) *Interval {
	if total == 0 {
		return nil
	}
	return &Interval{Lower: lower, Upper: upper, Denominator: total}
}

func count(results []Result) (Counts, Metrics) {
	c := Counts{Targets: len(results), Execution: map[string]int{}, Verdicts: map[string]int{}, Reference: map[evaluation.Status]int{}, Failures: map[string]int{}, WrongReleaseCategories: map[string]int{}}
	for _, r := range results {
		c.Execution[r.RunStatus]++
		c.Reference[r.Reference]++
		if r.RunID != "" {
			c.Runs++
		}
		if r.FailureCode != "" {
			c.Failures[r.FailureCode]++
		}
		if r.Verdict != "" {
			c.Candidates++
			c.Verdicts[r.Verdict]++
		}
		if r.Verdict == "NeedsAttention" {
			c.NeedsAttention++
		}
		if r.Verdict != "ReadyForReview" {
			continue
		}
		c.Released++
		switch r.Reference {
		case evaluation.Fail:
			c.WrongReleases++
			for _, category := range r.FailureCategories {
				c.WrongReleaseCategories[category]++
			}
		case evaluation.Inconclusive:
			c.UnknownReleases++
			if r.RunStatus == "Completed" && r.DeliveryVerified {
				c.UnknownSuccess++
			}
		case evaluation.Pass:
			if r.RunStatus == "Completed" && r.DeliveryVerified {
				c.Success++
			}
		}
	}
	return c, Metrics{Success: interval(c.Success, c.Success+c.UnknownSuccess, c.Targets), WrongRelease: interval(c.WrongReleases, c.WrongReleases+c.UnknownReleases, c.Released), AttentionTargets: interval(c.NeedsAttention, c.NeedsAttention, c.Targets), AttentionCandidates: interval(c.NeedsAttention, c.NeedsAttention, c.Candidates)}
}

func markdown(report Report) []byte {
	var s strings.Builder
	fmt.Fprintf(&s, "# 轮次 %s\n\n类型：%s。记录有效：%t；批次记录完整终结：%t。\n\n", report.ID, report.Kind, report.Valid, report.ExecutionComplete)
	if report.Kind == Calibration {
		fmt.Fprintln(&s, "开发校准结果，不代表真实模型迁移效果。")
	}
	if report.Protocol != nil {
		fmt.Fprintf(&s, "\n协议：%s；分区：%s；每任务 %d 次尝试。协议绑定不认证样本独立性或人员签署，不表示 M5 已验收。\n", report.Protocol.ID, report.Protocol.Split, report.Protocol.AttemptsPerTarget)
	}
	fmt.Fprintln(&s, "\n| Target | Run 状态 | 原判定 | 自动参考 | 完整参考 |\n| --- | --- | --- | --- | --- |")
	for _, r := range report.Targets {
		fmt.Fprintf(&s, "| %s | %s | %s | %s | %s |\n", r.TargetID, r.RunStatus, r.Verdict, r.Automatic, r.Reference)
	}
	c := report.Counts
	fmt.Fprintf(&s, "\nN=%d；R=%d；Q=%d；A=%d；H=%d；确认自动成功=%d；确认错误放行=%d；未知放行=%d。\n", c.Targets, c.Runs, c.Candidates, c.Released, c.NeedsAttention, c.Success, c.WrongReleases, c.UnknownReleases)
	if report.Metrics == nil {
		fmt.Fprintln(&s, "\n记录冲突或输入不稳定，效果指标未发布。")
	} else {
		for _, item := range []struct {
			name  string
			value *Interval
		}{{"有效自动成功", report.Metrics.Success}, {"错误放行", report.Metrics.WrongRelease}, {"需人工确认 / Target", report.Metrics.AttentionTargets}, {"需人工确认 / 候选", report.Metrics.AttentionCandidates}} {
			if item.value == nil {
				fmt.Fprintf(&s, "\n%s：未定义（零分母）。\n", item.name)
			} else {
				v := item.value
				fmt.Fprintf(&s, "\n%s：[%d/%d, %d/%d]。\n", item.name, v.Lower, v.Denominator, v.Upper, v.Denominator)
			}
		}
	}
	if report.Cost == nil {
		fmt.Fprintln(&s, "\n成本材料缺失或不一致，用量未知。")
	} else {
		t := report.Cost.Totals
		fmt.Fprintf(&s, "\n准入 %d 次；已知输入/输出 %d/%d Token；未知保守占用 %d；在途预留 %d。Target 明细不再叠加到此总计。\n", t.Calls, report.Cost.KnownInputTokens, report.Cost.KnownOutputTokens, t.UnknownTokens, t.ReservedTokens)
	}
	fmt.Fprintln(&s, "\n费用金额及人工活动耗时未知。区间表示待确认结果范围，不是统计置信区间。原始身份、缺口与诊断见 [round.json](round.json)。")
	return []byte(s.String())
}
