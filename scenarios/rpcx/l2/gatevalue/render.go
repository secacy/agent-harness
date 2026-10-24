package gatevalue

import (
	"fmt"
	"sort"
	"strings"
)

func sortSources(s []Source) { sort.Slice(s, func(i, j int) bool { return s[i].Path < s[j].Path }) }
func markdown(r Report) []byte {
	var s strings.Builder
	fmt.Fprintln(&s, "# 历史候选回顾\n\n同一历史报告的证据投影；已见样本，非独立消融、当前版本评测或生产概率。\n\n| 轮次/任务 | 构建测试证据 | 原门禁 | 原自动参考/本次参考消费 | 变更覆盖/交付依据 | 记录命令数/耗时和(ms) |\n| --- | --- | --- | --- | --- | --- |")
	for _, row := range r.Rows {
		var count int
		var ms int64
		for _, c := range row.Costs {
			count += c.Count
			ms += c.Milliseconds
		}
		fmt.Fprintf(&s, "| %s/%s | %s | %s | %s/%s | %s/%s | %d/%d |\n", row.Case.Cohort, row.Case.Target, row.BuildTests, row.OriginalVerdict, row.Automatic, row.Assessment, row.Review["change_coverage"].Status, row.Review["delivery_evidence"].Status, count, ms)
	}
	fmt.Fprintln(&s, "\n命令时间是已记录的墙钟区间之和，包含真实期限等待；不等于 CPU、批次墙钟、完整验证成本或独立简化分支耗时。人类审核活动时间与接入工时未知。\n\n原始身份、检查条款、复核理由、来源路径、命令分类与缺口均保存在 report.json。两轮分别展示；两个 GHCLI 属于同源，不合并总体比例。")
	for _, row := range r.Rows {
		if row.ReferenceGap != "" {
			fmt.Fprintf(&s, "\n- %s/%s：%s\n", row.Case.Cohort, row.Case.Target, row.ReferenceGap)
		}
		if row.Error != "" {
			fmt.Fprintf(&s, "\n- %s/%s：%s\n", row.Case.Cohort, row.Case.Target, strings.ReplaceAll(row.Error, "\n", " "))
		}
	}
	fmt.Fprintf(&s, "\n消费源复核保持：%t。\n", r.InputsUnchanged)
	return []byte(s.String())
}
