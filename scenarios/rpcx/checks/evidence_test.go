package checks

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/secacy/agent-harness/internal/quality"
)

func TestPublicCasesHaveReproducibleClauseEvidence(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	runDir := t.TempDir()
	report, err := Verify(root, runDir)
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != 2 || !report.FixturesUnchanged {
		t.Fatalf("invalid report: %+v", report)
	}
	expected := map[string]struct{ check, rule string }{
		"correct":             {},
		"background-fallback": {"context_propagation", "CTX-003"},
		"missed-callsite":     {"build", "CTX-001"},
		"deleted-test":        {"test_preservation", "TEST-001"},
		"protected-path":      {"protected_paths", "PATH-001"},
	}
	if len(report.Results) != len(expected) {
		t.Fatal("public case set changed")
	}
	for _, result := range report.Results {
		want, exists := expected[result.ID]
		if !exists || !result.ExpectationMatched || len(result.Evidence) == 0 {
			t.Fatalf("case %s lost evidence or expected verdict", result.ID)
		}
		binding := quality.Binding{SpecSHA256: report.SpecSHA256, BaselineSHA256: result.BaselineSHA256, CandidateSHA256: result.CandidateSHA256}
		decision, err := quality.Decide(filepath.Join(runDir, result.EvidenceDirectory), t1t2Policy(), binding, result.Checks, result.Evidence)
		if err != nil || decision.Verdict != result.Verdict {
			t.Fatalf("case %s cannot be replayed: %+v %v", result.ID, decision, err)
		}
		if want.check == "" {
			continue
		}
		check := result.Checks[want.check]
		if check.Status != quality.StatusFail || !slices.Contains(check.RuleIDs, want.rule) || !slices.ContainsFunc(check.Findings, func(f quality.Finding) bool {
			return f.Code == want.check+"_fail" && slices.Contains(f.RuleIDs, want.rule)
		}) {
			t.Fatalf("case %s lacks clause reason: %+v", result.ID, check)
		}
		if result.ID == "protected-path" && len(check.Findings[0].Locations) == 0 {
			t.Fatal("protected-path change lost its file location")
		}
	}
}

func TestExecutionFailureKeepsPartialEvidenceWithoutVerdict(t *testing.T) {
	req := preparedRequest(t)
	req.Backend = failedExecution{cause: context.Canceled}
	result, err := Evaluate(context.Background(), req)
	if err == nil || result.Verdict != "" || len(result.Evidence) == 0 || len(result.Checks["build"].Findings) == 0 {
		t.Fatalf("lost partial evidence: %+v %v", result, err)
	}
}

func TestEvidenceSchemaDoesNotEnableUnsupportedT3Rules(t *testing.T) {
	req := preparedRequest(t)
	spec, err := filepath.Abs("../t3/specs/v0.2.0.md")
	if err != nil {
		t.Fatal(err)
	}
	req.SpecPath = spec
	result, err := Evaluate(context.Background(), req)
	if err == nil || result.Verdict != "" || len(result.Commands) != 0 {
		t.Fatalf("T3 bypassed explicit verifier support: %+v %v", result, err)
	}
}
