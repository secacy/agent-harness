package ghcli

import "github.com/secacy/agent-harness/internal/quality"

func policy() quality.Policy {
	return quality.Policy{SpecSHA256: SpecSHA256(), Requirements: []quality.Requirement{
		{ID: "dependencies", RuleIDs: []string{"DEP-001", "API-001"}},
		{ID: "protected_paths", RuleIDs: []string{"PATH-001"}},
		{ID: "build", RuleIDs: []string{"BUILD-001"}},
		{ID: "baseline_tests", RuleIDs: []string{"TEST-001"}},
		{ID: "test_preservation", RuleIDs: []string{"TEST-001"}},
		{ID: "request_scope", RuleIDs: []string{"CTX-001", "BEHAVIOR-001", "SCOPE-001"}},
		{ID: "migration_completeness", RuleIDs: []string{"CTX-001", "TIME-001", "TIME-002", "BUILD-001", "SCOPE-001"}},
		{ID: "semantic_behavior", RuleIDs: []string{"CTX-001", "TIME-001", "TIME-002", "BEHAVIOR-001"}},
	}}
}
func rules(id string) []string {
	for _, r := range policy().Requirements {
		if r.ID == id {
			return r.RuleIDs
		}
	}
	return nil
}
func check(id, status, detail string, refs ...string) quality.Check {
	refs = append(append([]string{}, refs...), "rule-map")
	return quality.Check{Status: status, Detail: detail, Applicability: quality.Applicable, RuleIDs: rules(id), EvidenceIDs: refs, Findings: []quality.Finding{{Code: id + "_" + status, Detail: detail, RuleIDs: rules(id), EvidenceIDs: refs}}}
}
