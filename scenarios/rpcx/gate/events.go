package gate

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"strings"

	"github.com/secacy/agent-harness/internal/quality"
)

// Observe 要求必需测试及包的完整事件，失败不等于设施错误已得到业务归因。
func Observe(data []byte, required []string, exit int, rules []string, prefix string) (string, []quality.Finding) {
	runs, passes := map[string]int{}, map[string]int{}
	packages := map[string]string{}
	packageEnds := map[string]int{}
	owners := map[string]string{}
	findings := []quality.Finding{}
	failed := false
	d := json.NewDecoder(bytes.NewReader(data))
	for {
		var e struct{ Action, Package, Test, Output string }
		if err := d.Decode(&e); err == io.EOF {
			break
		} else if err != nil {
			return quality.StatusUnknown, findings
		}
		id := e.Package + "/" + e.Test
		if e.Test != "" {
			owners[id] = e.Package
			switch e.Action {
			case "run":
				runs[id]++
			case "pass":
				passes[id]++
			case "fail":
				failed = true
			case "skip":
				if slices.Contains(required, id) {
					return quality.StatusUnknown, findings
				}
			}
		} else if e.Action == "pass" || e.Action == "fail" || e.Action == "skip" {
			packages[e.Package] = e.Action
			packageEnds[e.Package]++
			if e.Action == "fail" {
				failed = true
			}
		}
		if at := strings.Index(e.Output, prefix); at >= 0 {
			var f struct{ Rule, Detail string }
			if json.Unmarshal([]byte(strings.TrimSpace(e.Output[at+len(prefix):])), &f) == nil && slices.Contains(rules, f.Rule) && f.Detail != "" {
				findings = append(findings, quality.Finding{Code: "behavior_violation", Detail: f.Detail, RuleIDs: []string{f.Rule}})
			}
		}
	}
	if failed {
		if exit != 0 {
			return quality.StatusFail, findings
		}
		return quality.StatusUnknown, findings
	}
	if exit != 0 || len(required) == 0 {
		return quality.StatusUnknown, findings
	}
	for _, id := range required {
		if runs[id] != 1 || passes[id] != 1 || packages[owners[id]] != "pass" || packageEnds[owners[id]] != 1 {
			return quality.StatusUnknown, findings
		}
	}
	return quality.StatusPass, findings
}
