package goddd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/secacy/agent-harness/internal/quality"
)

func policy() quality.Policy {
	return quality.Policy{SpecSHA256: SpecSHA256(), Requirements: []quality.Requirement{
		{ID: "dependencies", RuleIDs: []string{"DEP-001", "API-001"}},
		{ID: "protected_paths", RuleIDs: []string{"PATH-001"}},
		{ID: "build", RuleIDs: []string{"BUILD-001"}},
		{ID: "baseline_tests", RuleIDs: []string{"TEST-001"}},
		{ID: "test_preservation", RuleIDs: []string{"TEST-001"}},
		{ID: "request_scope", RuleIDs: []string{"CTX-001", "CTX-002", "BEHAVIOR-001", "SCOPE-001"}},
		{ID: "migration_completeness", RuleIDs: []string{"CTX-003", "TIME-001", "TIME-003", "SCOPE-001"}},
		{ID: "semantic_behavior", RuleIDs: []string{"CTX-003", "TIME-001", "TIME-002", "TIME-003", "BEHAVIOR-001"}},
	}}
}
func requirement(id string) []string {
	for _, r := range policy().Requirements {
		if r.ID == id {
			return r.RuleIDs
		}
	}
	return nil
}
func check(id, status, detail string, ids ...string) quality.Check {
	return quality.Check{Status: status, Detail: detail, Applicability: quality.Applicable, RuleIDs: requirement(id), EvidenceIDs: ids,
		Findings: []quality.Finding{{Code: id + "_" + status, Detail: detail, RuleIDs: requirement(id), EvidenceIDs: ids}}}
}

func migratedMod(data []byte) []byte {
	s := strings.Replace(string(data), "example.com/rpcx v1.0.0", "example.com/rpcx/v2 v2.0.0", 1)
	s = strings.Replace(s, "replace example.com/rpcx => ../rpcx-v1\n", "", 1)
	return []byte(s)
}

// modEntries 只接受已声明的模块语法，允许格式与块内顺序变化。
func modEntries(data []byte) (map[string]string, error) {
	out := map[string]string{}
	block := ""
	for _, line := range strings.Split(string(data), "\n") {
		line, _, _ = strings.Cut(line, "//")
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		for i, field := range fields {
			if strings.HasPrefix(field, "\"") {
				value, err := strconv.Unquote(field)
				if err != nil {
					return nil, fmt.Errorf("unsupported quoted module token: %w", err)
				}
				fields[i] = value
			}
		}
		if len(fields) == 1 && fields[0] == ")" {
			if block == "" {
				return nil, fmt.Errorf("unexpected module block end")
			}
			block = ""
			continue
		}
		directive := block
		if directive == "" {
			directive = fields[0]
			fields = fields[1:]
		}
		if len(fields) == 1 && fields[0] == "(" {
			if block != "" || directive != "require" && directive != "replace" {
				return nil, fmt.Errorf("unsupported module block")
			}
			block = directive
			continue
		}
		var key, value string
		switch directive {
		case "module", "go":
			if len(fields) != 1 {
				return nil, fmt.Errorf("invalid %s entry", directive)
			}
			key = directive
			value = fields[0]
		case "require":
			if len(fields) != 2 {
				return nil, fmt.Errorf("unsupported requirement")
			}
			key = "require " + fields[0]
			value = fields[1]
		case "replace":
			if len(fields) != 3 || fields[1] != "=>" {
				return nil, fmt.Errorf("unsupported replacement")
			}
			key = "replace " + fields[0]
			value = fields[2]
		default:
			return nil, fmt.Errorf("unsupported module directive: %s", directive)
		}
		if _, ok := out[key]; ok {
			return nil, fmt.Errorf("duplicate module entry: %s", key)
		}
		out[key] = value
	}
	if block != "" {
		return nil, fmt.Errorf("unclosed module block")
	}
	return out, nil
}

func requestScope(base, candidate map[string][]byte) quality.Check {
	out := check("request_scope", quality.StatusPass, "production changes match the authenticated API/context adaptation", "input-snapshots", "scope")
	expected, err := adapt(base, false)
	if err != nil {
		out.Status = quality.StatusUnknown
		out.Detail = err.Error()
		return out
	}
	paths := map[string]bool{}
	for p := range base {
		paths[p] = true
	}
	for p := range candidate {
		paths[p] = true
	}
	for _, name := range slices.Sorted(maps.Keys(paths)) {
		if strings.HasSuffix(name, "_test.go") || name == "go.mod" {
			continue
		}
		if !strings.HasSuffix(name, ".go") && bytes.Equal(base[name], candidate[name]) {
			continue
		}
		proved := false
		if strings.HasSuffix(name, ".go") && base[name] != nil && candidate[name] != nil && !directives(candidate[name]) {
			a, e1 := syntax(name, expected[name])
			b, e2 := syntax(name, candidate[name])
			proved = e1 == nil && e2 == nil && a == b
		}
		if !proved {
			out.Status = quality.StatusUnknown
			out.Findings = append(out.Findings, quality.Finding{Code: "unmodeled_production_change", Detail: "production structure requires review: " + name, RuleIDs: requirement("request_scope"), Locations: []quality.Location{{Tree: "candidate", Path: name}}, EvidenceIDs: out.EvidenceIDs})
		}
	}
	out.Detail = "request-chain and business structure: " + out.Status
	out.Findings[0].Detail = out.Detail
	out.Findings[0].Code = "request_scope_" + out.Status
	return out
}
func directives(data []byte) bool {
	for _, tag := range []string{"//go:", "// +build", "//line ", "/*line ", "//export "} {
		if bytes.Contains(data, []byte(tag)) {
			return true
		}
	}
	return false
}

func RequiredTests() ([]string, error) {
	data, err := baselineData()
	if err != nil {
		return nil, err
	}
	var inventory struct {
		Modules map[string]struct {
			Original []string `json:"original_tests"`
			Added    []string `json:"added_tests"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(data["tests.json"], &inventory); err != nil {
		return nil, err
	}
	m := inventory.Modules["goddd"]
	return append(m.Original, m.Added...), nil
}

// observe 要求原测试与子测试各自执行并终结，包终态也必须成功。
func observe(data []byte, required []string, exit int) (string, []quality.Finding) {
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
		if at := strings.Index(e.Output, "GODDD_FINDING "); at >= 0 {
			var f struct{ Rule, Detail string }
			if json.Unmarshal([]byte(strings.TrimSpace(e.Output[at+len("GODDD_FINDING "):])), &f) == nil && slices.Contains(requirement("semantic_behavior"), f.Rule) && f.Detail != "" {
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
