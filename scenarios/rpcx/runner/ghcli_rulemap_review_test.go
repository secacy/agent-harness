package runner

import (
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
	"github.com/secacy/agent-harness/scenarios/rpcx/ghcli"
	"html"
	"strings"
	"testing"
)

func TestGHCLIReviewRuleMappingEvidence(t *testing.T) {
	p, err := lookupProfile(ProfileGHCLI)
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != ghcli.ProfileID {
		t.Fatal("mapping profile binding differs from Runner")
	}
	report := Report{Status: StatusCompleted, Input: Input{RepositoryName: p.RepositoryName}, Validation: &checks.Evaluation{Verdict: quality.NeedsAttention, Checks: map[string]quality.Check{"context": {Status: quality.StatusUnknown, RuleIDs: []string{"CTX-001"}, EvidenceIDs: []string{"rule-map"}}}, Evidence: []quality.Evidence{{ID: "rule-map", Kind: "host_rule_mapping", Path: "rule-map.json"}}}}
	rendered := string(renderReview(report, tree{}, tree{}, nil))
	for _, text := range []string{"规则标识", "并非规范原文自带编号", "../evidence/rule-map.json", "规范段落、行号或宿主策略位置"} {
		if !strings.Contains(rendered, text) {
			t.Fatal("review mapping explanation or locator missing")
		}
	}
}

func TestGHCLIReviewBehaviorObservations(t *testing.T) {
	refs := []string{"behavior-observations", "behavior-stdout", "rule-map"}
	report := Report{Status: StatusCompleted, Validation: &checks.Evaluation{Verdict: quality.Rejected, Checks: map[string]quality.Check{"semantic_behavior": {Status: quality.StatusFail, EvidenceIDs: refs, Findings: []quality.Finding{{Code: "response_missing_on_redirect_error", Detail: "TestHarnessApplicationReturns/redirect-declined", RuleIDs: []string{"BEHAVIOR-001"}, EvidenceIDs: refs}}}}, Evidence: []quality.Evidence{{ID: "behavior-observations", Path: "behavior-observations.json"}, {ID: "behavior-stdout", Path: "logs/behavior.stdout.log"}, {ID: "rule-map", Path: "rule-map.json"}}}}
	rendered := html.UnescapeString(string(renderReview(report, tree{}, tree{}, nil)))
	for _, text := range []string{"response_missing_on_redirect_error", "TestHarnessApplicationReturns/redirect-declined", "BEHAVIOR-001", "../evidence/behavior-observations.json", "../evidence/logs/behavior.stdout.log", "../evidence/rule-map.json"} {
		if !strings.Contains(rendered, text) {
			t.Fatalf("review lacks behavior locator: %s", text)
		}
	}
}
