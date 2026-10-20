package goddd

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
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
func modEntries(data []byte) (map[string]string, error) { return gate.ModEntries(data) }

func requestScope(base, candidate map[string][]byte, a adaptations) (quality.Check, map[string]string, *routingScopeReport) {
	out := check("request_scope", quality.StatusPass, "production changes match the authenticated API/context adaptation", "input-snapshots", "scope", "adaptations")
	matches := map[string]string{}
	var routing *routingScopeReport
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
			expected, e1 := syntax(name, a.production[name])
			b, e2 := syntax(name, candidate[name])
			proved = e1 == nil && e2 == nil && expected == b
			if proved {
				matches[name] = "production-canonical"
			} else if name == mockPath && e2 == nil {
				forwarded, err := syntax(name, a.forwarded)
				proved = err == nil && forwarded == b
				if proved {
					matches[name] = "production-forwarded"
				}
			}
		}
		if !proved && name == routingPath && base[name] != nil && candidate[name] != nil {
			report := routingScope(base, candidate, a.production[name])
			routing = &report
			proved = report.Matched
			if proved {
				out.Findings = append(out.Findings, quality.Finding{Code: "bounded_routing_structure", Detail: "bounded routing changes preserve the remaining production structure", RuleIDs: requirement("request_scope"), Locations: []quality.Location{{Tree: "candidate", Path: name}}, EvidenceIDs: []string{"routing-scope"}})
			}
		}
		if !proved {
			out.Status = quality.StatusUnknown
			out.Findings = append(out.Findings, quality.Finding{Code: "unmodeled_production_change", Detail: "production structure requires review: " + name, RuleIDs: requirement("request_scope"), Locations: []quality.Location{{Tree: "candidate", Path: name}}, EvidenceIDs: out.EvidenceIDs})
		}
	}
	out.Detail = "request-chain and business structure: " + out.Status
	out.Findings[0].Detail = out.Detail
	out.Findings[0].Code = "request_scope_" + out.Status
	return out, matches, routing
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
	return gate.Observe(data, required, exit, requirement("semantic_behavior"), "GODDD_FINDING ")
}
