package quality

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type sample struct {
	root     string
	policy   Policy
	binding  Binding
	checks   map[string]Check
	evidence []Evidence
}

func evidenceSample(t *testing.T) sample {
	t.Helper()
	root := t.TempDir()
	data := []byte("verified output\n")
	if err := os.WriteFile(filepath.Join(root, "result.log"), data, 0644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	binding := Binding{SpecSHA256: strings.Repeat("a", 64), BaselineSHA256: strings.Repeat("b", 64), CandidateSHA256: strings.Repeat("c", 64)}
	evidence := []Evidence{{ID: "result", Kind: "test_log", Path: "result.log", SHA256: hex.EncodeToString(sum[:]), Binding: binding}}
	policy := Policy{SpecSHA256: binding.SpecSHA256, Requirements: []Requirement{{ID: "build", RuleIDs: []string{"BUILD-001"}}, {ID: "behavior", RuleIDs: []string{"TIME-001"}}}}
	checks := map[string]Check{}
	for _, r := range policy.Requirements {
		checks[r.ID] = Check{Status: StatusPass, Detail: "verified", Applicability: Applicable, RuleIDs: r.RuleIDs, EvidenceIDs: []string{"result"}, Findings: []Finding{{Code: "verified", Detail: "actual command succeeded", RuleIDs: r.RuleIDs}}}
	}
	return sample{root, policy, binding, checks, evidence}
}

func TestDecisionRequiresCompleteEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*sample)
		want   string
		gap    string
	}{
		{"all-pass", func(*sample) {}, ReadyForReview, ""},
		{"empty-results", func(s *sample) { s.checks = nil }, NeedsAttention, "check_missing"},
		{"missing-required", func(s *sample) { delete(s.checks, "behavior") }, NeedsAttention, "check_missing"},
		{"unresolved-reference", func(s *sample) {
			c := s.checks["behavior"]
			c.EvidenceIDs = []string{"not-produced"}
			s.checks["behavior"] = c
		}, NeedsAttention, "evidence_missing"},
		{"no-reference", func(s *sample) { c := s.checks["behavior"]; c.EvidenceIDs = nil; s.checks["behavior"] = c }, NeedsAttention, "evidence_missing"},
		{"no-finding", func(s *sample) { c := s.checks["behavior"]; c.Findings = nil; s.checks["behavior"] = c }, NeedsAttention, "finding_missing"},
		{"no-clause-association", func(s *sample) { c := s.checks["behavior"]; c.RuleIDs = nil; s.checks["behavior"] = c }, NeedsAttention, "rule_missing"},
		{"known-failure", func(s *sample) { c := s.checks["behavior"]; c.Status = StatusFail; s.checks["behavior"] = c }, Rejected, ""},
		{"failure-without-evidence", func(s *sample) {
			c := s.checks["behavior"]
			c.Status = StatusFail
			c.EvidenceIDs = nil
			s.checks["behavior"] = c
		}, NeedsAttention, "evidence_missing"},
		{"failure-and-missing-check", func(s *sample) {
			c := s.checks["build"]
			c.Status = StatusFail
			s.checks["build"] = c
			delete(s.checks, "behavior")
		}, Rejected, "check_missing"},
		{"failure-and-uncovered-clause", func(s *sample) {
			s.policy.Requirements[0].RuleIDs = append(s.policy.Requirements[0].RuleIDs, "BUILD-002")
			c := s.checks["build"]
			c.Status = StatusFail
			s.checks["build"] = c
		}, Rejected, "rule_missing"},
		{"host-disallows-not-applicable", func(s *sample) {
			c := s.checks["behavior"]
			c.Applicability = NotApplicable
			c.Status = StatusSkipped
			s.checks["behavior"] = c
		}, NeedsAttention, "not_applicable_disallowed"},
		{"host-allows-not-applicable", func(s *sample) {
			s.policy.Requirements[1].AllowNotApplicable = true
			c := s.checks["behavior"]
			c.Applicability = NotApplicable
			c.Status = StatusSkipped
			s.checks["behavior"] = c
		}, ReadyForReview, ""},
		{"not-applicable-without-reason", func(s *sample) {
			s.policy.Requirements[1].AllowNotApplicable = true
			c := s.checks["behavior"]
			c.Applicability = NotApplicable
			c.Status = StatusSkipped
			c.Findings = nil
			s.checks["behavior"] = c
		}, NeedsAttention, "finding_missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := evidenceSample(t)
			tc.change(&s)
			got, err := Decide(s.root, s.policy, s.binding, s.checks, s.evidence)
			if err != nil || got.Verdict != tc.want {
				t.Fatalf("decision=%+v err=%v", got, err)
			}
			if tc.gap != "" && !slices.ContainsFunc(got.Gaps, func(g Gap) bool { return g.Code == tc.gap }) {
				t.Fatalf("missing gap %s: %+v", tc.gap, got)
			}
		})
	}
	for _, status := range []string{StatusUnknown, StatusBlocked, StatusNotRun, StatusSkipped} {
		t.Run(status, func(t *testing.T) {
			s := evidenceSample(t)
			c := s.checks["behavior"]
			c.Status = status
			s.checks["behavior"] = c
			decision, err := Decide(s.root, s.policy, s.binding, s.checks, s.evidence)
			if err != nil || decision.Verdict != NeedsAttention {
				t.Fatalf("%+v %v", decision, err)
			}
			c = s.checks["build"]
			c.Status = StatusFail
			s.checks["build"] = c
			slices.Reverse(s.policy.Requirements)
			decision, err = Decide(s.root, s.policy, s.binding, s.checks, s.evidence)
			if err != nil || decision.Verdict != Rejected || len(decision.Gaps) == 0 {
				t.Fatalf("failure lost priority: %+v %v", decision, err)
			}
		})
	}
}

func TestInvalidEvidenceNeverProducesVerdict(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*sample)
	}{
		{"other-candidate", func(s *sample) { s.evidence[0].Binding.CandidateSHA256 = strings.Repeat("d", 64) }},
		{"other-baseline", func(s *sample) { s.evidence[0].Binding.BaselineSHA256 = strings.Repeat("d", 64) }},
		{"other-specification", func(s *sample) { s.evidence[0].Binding.SpecSHA256 = strings.Repeat("d", 64) }},
		{"wrong-policy", func(s *sample) { s.policy.SpecSHA256 = strings.Repeat("d", 64) }},
		{"duplicate-id", func(s *sample) { s.evidence = append(s.evidence, s.evidence[0]) }},
		{"changed-file", func(s *sample) {
			if err := os.WriteFile(filepath.Join(s.root, "result.log"), []byte("changed"), 0644); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing-file", func(s *sample) { s.evidence[0].Path = "missing.log" }},
		{"path-escape", func(s *sample) { s.evidence[0].Path = "../result.log" }},
		{"absolute-path", func(s *sample) { s.evidence[0].Path = filepath.Join(s.root, "result.log") }},
		{"symlink", func(s *sample) {
			if err := os.Symlink(filepath.Join(s.root, "result.log"), filepath.Join(s.root, "link")); err != nil {
				t.Fatal(err)
			}
			s.evidence[0].Path = "link"
		}},
		{"symlink-directory", func(s *sample) {
			if err := os.Symlink(s.root, filepath.Join(s.root, "linkdir")); err != nil {
				t.Fatal(err)
			}
			s.evidence[0].Path = "linkdir/result.log"
		}},
		{"invalid-status", func(s *sample) { c := s.checks["behavior"]; c.Status = "probably-pass"; s.checks["behavior"] = c }},
		{"invalid-rule", func(s *sample) { c := s.checks["behavior"]; c.RuleIDs = []string{"FAKE-001"}; s.checks["behavior"] = c }},
		{"invalid-finding-rule", func(s *sample) {
			c := s.checks["behavior"]
			c.Findings[0].RuleIDs = []string{"FAKE-001"}
			s.checks["behavior"] = c
		}},
		{"invalid-location", func(s *sample) {
			c := s.checks["behavior"]
			c.Findings[0].Locations = []Location{{Tree: "candidate", Path: "../outside.go"}}
			s.checks["behavior"] = c
		}},
		{"empty-policy", func(s *sample) { s.policy.Requirements = nil }},
		{"unexpected-check", func(s *sample) { s.checks["invented"] = s.checks["build"] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := evidenceSample(t)
			tc.change(&s)
			decision, err := Decide(s.root, s.policy, s.binding, s.checks, s.evidence)
			if err == nil || decision.Verdict != "" {
				t.Fatalf("invalid evidence became quality result: %+v %v", decision, err)
			}
		})
	}
}
