package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/quality"
)

// renderReview 只呈现已保存的事实，不重算判定，也不把模型自述转换为验证证据。
func renderReview(report Report, baseline, candidate tree, trace *agent.Result) []byte {
	module := report.Input.RepositoryName
	if module == "" {
		module = "demo-orders"
	}
	var out bytes.Buffer
	fmt.Fprint(&out, "# 候选审核材料\n\n")
	fmt.Fprintf(&out, "Run：%s；执行状态：%s。最终发布状态以 [run.json](../run.json) 为准。\n\n", textValue(report.RunID), textValue(string(report.Status)))
	verdict := "无最终质量判定"
	if report.Error == "" && report.Status == StatusCompleted && report.Validation != nil && report.Validation.Verdict != "" {
		verdict = report.Validation.Verdict
	}
	fmt.Fprintf(&out, "质量判定：**%s**。\n\n", verdict)
	if report.Error != "" {
		fmt.Fprintf(&out, "执行或交付错误：%s。下方仅保留部分诊断材料，不能视为完成验收。\n\n", textValue(report.Error))
	}
	fmt.Fprint(&out, "ReadyForReview 表示当前门禁有充分证据，可进入常规人工审核；NeedsAttention 表示仍需确认缺口；Rejected 表示发现明确违规。判定只适用于列明的输入、构建配置和支持形态，不证明任意调用图或业务等价性。\n\n")
	if report.Input.Target != nil {
		fmt.Fprintf(&out, "Target：%s；宿主 Profile：%s；配置 SHA-256：%s。\n\n", textValue(report.Input.Target.ID), textValue(report.Input.Target.ProfileID), textValue(report.Input.ProfileSHA256))
	}
	if report.Input.SpecSHA256 != "" && report.Baseline != nil {
		fmt.Fprintf(&out, "规范：%s（[原文](../inputs/spec/spec.md)）；后端：%s；镜像：%s。\n\n", textValue(report.Input.SpecVersion), textValue(report.Input.ExecutionMode), textValue(report.Input.Backend.Image))
	} else {
		fmt.Fprint(&out, "规范及输入状态见结构化报告。\n\n")
	}
	if report.Baseline != nil {
		if report.Baseline.Status == "pass" || report.Baseline.Status == "fail" {
			fmt.Fprintf(&out, "生成前基线检查：%s；详情见 [baseline.json](../preflight/baseline.json)。该检查不替代候选独立门禁。\n\n", textValue(report.Baseline.Status))
		} else {
			fmt.Fprintf(&out, "生成前基线检查：%s；详情见结构化报告的 baseline_check。该检查不替代候选独立门禁。\n\n", textValue(report.Baseline.Status))
		}
	}

	if ref := report.Input.Budget; ref != nil {
		fmt.Fprintf(&out, "共享预算：Campaign %s，Target %s，Run %s；账本 %s；模型配置 SHA-256 %s。准入请求及已知/未知消耗以共享账本为准。\n\n", textValue(ref.CampaignID), textValue(ref.TargetID), textValue(ref.RunID), textValue(ref.LedgerPath), textValue(ref.ProfileSHA256))
	}
	fmt.Fprint(&out, "| 对象 | SHA-256 |\n| --- | --- |\n")
	for _, item := range [][2]string{{"规范", report.Input.SpecSHA256}, {"基线", report.Input.BaselineSHA256}, {"候选", report.CandidateSHA256}, {"补丁", report.PatchSHA256}} {
		fmt.Fprintf(&out, "| %s | %s |\n", item[0], textValue(item[1]))
	}
	fmt.Fprint(&out, "\n[结构化报告](report.json)")
	if report.PatchSHA256 != "" {
		fmt.Fprint(&out, " · [候选补丁](candidate.patch)")
	}
	fmt.Fprint(&out, "\n\n")
	fmt.Fprint(&out, "## 相关修改\n\n以下为文件级变化；条款对应的具体发现和行号在后续检查中。补丁以 baseline/"+module+" 为基线重放。输入逐文件摘要见 inputs/task.json。\n\n| 文件 | 变化 | 基线 SHA-256 | 候选 SHA-256 |\n| --- | --- | --- | --- |\n")
	paths := map[string]bool{}
	for path := range baseline {
		paths[path] = true
	}
	for path := range candidate {
		paths[path] = true
	}
	for _, path := range sortedKeys(paths) {
		before, existed := baseline[path]
		after, exists := candidate[path]
		if existed == exists && bytes.Equal(before, after) {
			continue
		}
		kind := "修改"
		if !existed {
			kind = "新增"
		} else if !exists {
			kind = "删除"
		}
		oldSHA, newSHA := "—", "—"
		var links []string
		if existed {
			oldSHA = digest(before)
			links = append(links, link("基线", "../baseline/"+module+"/"+path))
		}
		if exists {
			newSHA = digest(after)
			links = append(links, link("候选", "../workspace/"+module+"/"+path))
		}
		fmt.Fprintf(&out, "| %s（%s） | %s | %s | %s |\n", textValue(path), strings.Join(links, " / "), kind, oldSHA, newSHA)
	}
	if report.Validation == nil {
		fmt.Fprint(&out, "\n## 独立检查\n\n未获得独立验证结果；不得把 Agent 的提交或本地检查视为最终质量判定。\n")
	} else {
		renderValidation(&out, report, module)
	}
	fmt.Fprint(&out, "\n## Agent 自述（未经独立确认）\n\n下述文字仅来自模型，不能替代以上检查依据。\n")
	if trace != nil {
		fmt.Fprint(&out, "\n完整工具反馈、预算通知及服务返回用量见 [agent.json](agent.json)。\n")
		fmt.Fprintf(&out, "\n模型调用尝试 %d 次，工具尝试 %d 次；退出原因 %s。未知 Token 用量保留为未知，不计为零。\n", trace.ModelCalls, trace.ToolCalls, textValue(string(trace.Reason)))
		for i := len(trace.Messages) - 1; i >= 0; i-- {
			m := trace.Messages[i]
			if m.Role == agent.RoleAssistant && len(m.ToolCalls) == 0 && m.Content != "" {
				// 转义并逐行引用，避免模型文字伪造报告标题、链接或门禁结论。
				for _, line := range strings.Split(m.Content, "\n") {
					fmt.Fprintln(&out, "> "+escapeMarkdown(line))
				}
				break
			}
		}
	} else {
		fmt.Fprint(&out, "\n没有 Agent 运行记录。\n")
	}
	return out.Bytes()
}

// 所有动态值均转义；原始内容保留在 JSON 和源码中。
func textValue(value string) string {
	value = html.EscapeString(strings.ReplaceAll(strings.ReplaceAll(value, "\r", ""), "\n", " "))
	value = strings.NewReplacer("|", "&#124;", "`", "&#96;", "*", "&#42;", "_", "&#95;", "[", "&#91;", "]", "&#93;", "\\", "&#92;").Replace(value)
	return "<code>" + value + "</code>"
}

func escapeMarkdown(value string) string {
	r := strings.NewReplacer("\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "#", "\\#", ">", "&gt;", "<", "&lt;", "|", "\\|", "!", "\\!", "&", "&amp;")
	return r.Replace(value)
}

func link(label, path string) string {
	parts := strings.Split(path, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(parts[i], "(", "%28"), ")", "%29")
	}
	return "[" + escapeMarkdown(label) + "](" + strings.Join(parts, "/") + ")"
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func evidenceLinks(ids []string, evidence map[string]quality.Evidence) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		if e, ok := evidence[id]; ok {
			parts = append(parts, link(id, "../evidence/"+e.Path))
		} else {
			parts = append(parts, textValue(id)+"（缺少证据）")
		}
	}
	return strings.Join(parts, "，")
}

func renderValidation(out *bytes.Buffer, report Report, module string) {
	result := report.Validation
	fmt.Fprintf(out, "\n## 检查与规则依据\n\n验证器：%s；材料集合 SHA-256：%s。每项状态仅适用于该检查的支持范围，不把 blocked、unknown 或未执行计为通过。\n", textValue(result.VerifierVersion), textValue(result.MaterialsSHA256))
	evidence := map[string]quality.Evidence{}
	used := map[string]bool{}
	for _, e := range result.Evidence {
		evidence[e.ID] = e
	}
	if _, ok := evidence["rule-map"]; ok {
		fmt.Fprint(out, "\n规则标识由宿主定义，并非规范原文自带编号；规范段落、行号或宿主策略位置的映射见每项检查所引的 rule-map 证据。\n")
	}
	for _, id := range sortedKeys(result.Checks) {
		check := result.Checks[id]
		fmt.Fprintf(out, "\n### %s：%s\n\n规则标识：%s；适用性：%s。\n\n%s\n\n依据：%s。\n", escapeMarkdown(id), escapeMarkdown(check.Status), textValue(strings.Join(check.RuleIDs, ", ")), textValue(check.Applicability), textValue(check.Detail), evidenceLinks(check.EvidenceIDs, evidence))
		for _, ref := range check.EvidenceIDs {
			used[ref] = true
		}
		for _, f := range check.Findings {
			fmt.Fprintf(out, "\n- %s；规则标识 %s：%s。依据：%s。", textValue(f.Code), textValue(strings.Join(f.RuleIDs, ", ")), textValue(f.Detail), evidenceLinks(f.EvidenceIDs, evidence))
			for _, ref := range f.EvidenceIDs {
				used[ref] = true
			}
			for _, loc := range f.Locations {
				tree := "baseline"
				if loc.Tree == "candidate" {
					tree = "workspace"
				}
				fmt.Fprintf(out, " 位置：%s，第 %d 行（0 为文件级）。", link(loc.Tree+":"+loc.Path, "../"+tree+"/"+module+"/"+loc.Path), loc.Line)
			}
			fmt.Fprintln(out)
		}
	}
	fmt.Fprint(out, "\n## 未解决问题与人工确认\n\n")
	if len(result.Gaps) == 0 {
		fmt.Fprint(out, "结构化结果没有报告额外缺口；仍受上述规范和支持范围限制。\n")
	}
	for _, gap := range result.Gaps {
		fmt.Fprintf(out, "- %s / %s：%s。\n", textValue(gap.CheckID), textValue(gap.Code), textValue(gap.Detail))
	}
	fmt.Fprint(out, "\n## 实际执行记录\n\n以下为门禁直接命令；类型加载、构建配置、可信行为的内层命令与测量记录见对应检查引用的 JSON 及其日志。未执行项没有成功记录。\n\n| 命令参数（JSON） | 后端 | 退出码 | 耗时 ms | 日志 |\n| --- | --- | --- | --- | --- |\n")
	for _, command := range result.Commands {
		args, _ := json.Marshal(command.Args)
		// 日志路径由宿主保存；只把 evidence 内的路径转成链接。
		log := textValue(command.Log)
		for _, e := range result.Evidence {
			if e.Kind == "command_log" && filepath.Base(command.Log) == e.Path {
				log = link(e.Path, "../evidence/"+e.Path)
				break
			}
		}
		fmt.Fprintf(out, "| %s | %s | %d | %d | %s |\n", textValue(string(args)), textValue(command.Backend.Kind), command.ExitCode, command.DurationMS, log)
	}
	fmt.Fprint(out, "\n## 引用证据索引\n\n所有证据都绑定本报告的规范、基线和候选摘要。此处列出检查直接引用的证据；完整索引及内层材料摘要在 report.json 的 validation.evidence 中。\n\n| ID | 材料 | SHA-256 |\n| --- | --- | --- |\n")
	for _, id := range sortedKeys(used) {
		if e, ok := evidence[id]; ok {
			fmt.Fprintf(out, "| %s | %s | %s |\n", textValue(id), link(e.Path, "../evidence/"+e.Path), textValue(e.SHA256))
		}
	}
}
