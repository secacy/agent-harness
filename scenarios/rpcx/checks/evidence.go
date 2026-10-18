package checks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"example.com/agent-harness/internal/quality"
)

const verifierVersion = "rpcx-t1t2-evidence-v1"

// t1t2Policy 保持旧六项要求；T3 不能借用此策略宣称已经验证。
func t1t2Policy() quality.Policy {
	return quality.Policy{SpecSHA256: SupportedSpecSHA256, Requirements: []quality.Requirement{
		{ID: "dependencies", RuleIDs: []string{"DEP-001"}},
		{ID: "protected_paths", RuleIDs: []string{"PATH-001"}},
		{ID: "build", RuleIDs: []string{"API-001", "CTX-001"}},
		{ID: "baseline_tests", RuleIDs: []string{"TEST-001", "BEHAVIOR-001"}},
		{ID: "test_preservation", RuleIDs: []string{"TEST-001"}},
		{ID: "context_propagation", RuleIDs: []string{"CTX-001", "CTX-002", "CTX-003", "BEHAVIOR-001"}},
	}}
}

func (result *Evaluation) binding() quality.Binding {
	return quality.Binding{SpecSHA256: result.SpecSHA256, BaselineSHA256: result.BaselineSHA256, CandidateSHA256: result.CandidateSHA256}
}

func (result *Evaluation) saveEvidence(root, id, kind, path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(root, path), append(data, '\n'), 0644); err != nil {
		return err
	}
	return result.addEvidence(root, id, kind, path)
}

func (result *Evaluation) addEvidence(root, id, kind, path string) error {
	data, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		return err
	}
	result.Evidence = append(result.Evidence, quality.Evidence{ID: id, Kind: kind, Path: path, SHA256: digest(data), Binding: result.binding()})
	return nil
}

// annotateChecks 只关联现有检查的事实，不从 case 期望或日志文字推断结论。
func (result *Evaluation) annotateChecks() {
	refs := map[string][]string{
		"dependencies":        {"input-snapshots", "dependencies"},
		"protected_paths":     {"input-snapshots"},
		"build":               {"build"},
		"baseline_tests":      {"business-tests"},
		"test_preservation":   {"test-inventory", "baseline-tests", "business-tests"},
		"context_propagation": {"context-probes", "context-policy", "probe-material"},
	}
	for _, req := range t1t2Policy().Requirements {
		check, ok := result.Checks[req.ID]
		if !ok {
			continue
		}
		check.Applicability = quality.Applicable
		check.RuleIDs = req.RuleIDs
		code := req.ID + "_" + check.Status
		if check.Status == quality.StatusPass || check.Status == quality.StatusFail {
			check.EvidenceIDs = refs[req.ID]
		}
		if len(check.Findings) == 0 {
			check.Findings = []quality.Finding{{Code: code, Detail: check.Detail, RuleIDs: req.RuleIDs, EvidenceIDs: check.EvidenceIDs}}
		}
		result.Checks[req.ID] = check
	}
}

func (result *Evaluation) decide(root string) error {
	result.annotateChecks()
	decision, err := quality.Decide(root, t1t2Policy(), result.binding(), result.Checks, result.Evidence)
	if err != nil {
		return fmt.Errorf("assess quality evidence: %w", err)
	}
	result.Verdict, result.Gaps = decision.Verdict, decision.Gaps
	return nil
}
