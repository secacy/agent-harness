package runner

import (
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
	"github.com/secacy/agent-harness/scenarios/rpcx/ghcli"
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
