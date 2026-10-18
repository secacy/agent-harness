package checks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/analysis"
	"github.com/secacy/agent-harness/scenarios/rpcx/behavior"
	"github.com/secacy/agent-harness/scenarios/rpcx/testintegrity"
)

const T3SpecSHA256 = "5f096ec6bae18525fb5113781c096b2393707f95aa62f2ac9f9e4e4f94c8cad7"

func t3Policy() quality.Policy {
	return quality.Policy{SpecSHA256: T3SpecSHA256, Requirements: []quality.Requirement{
		{ID: "dependencies", RuleIDs: []string{"DEP-001"}},
		{ID: "protected_paths", RuleIDs: []string{"PATH-001"}},
		{ID: "build", RuleIDs: []string{"API-001", "CTX-001"}},
		{ID: "candidate_tests", RuleIDs: []string{"TEST-001", "BEHAVIOR-001"}},
		{ID: "test_structure", RuleIDs: []string{"TEST-001"}},
		{ID: "request_scope", RuleIDs: []string{"CTX-001", "CTX-002", "CTX-003", "BEHAVIOR-001", "SCOPE-001"}},
		{ID: "migration_analysis", RuleIDs: []string{"API-001", "CTX-001", "TIME-001", "TIME-003", "SCOPE-001"}},
		{ID: "trusted_behavior", RuleIDs: []string{"CTX-002", "CTX-003", "TIME-001", "TIME-002", "TIME-003", "TIME-004", "BEHAVIOR-001"}},
	}}
}

func t3Check(id, status, detail string, evidence ...string) Check {
	for _, req := range t3Policy().Requirements {
		if req.ID == id {
			return Check{Status: status, Detail: detail, Applicability: quality.Applicable, RuleIDs: req.RuleIDs, EvidenceIDs: evidence,
				Findings: []quality.Finding{{Code: id + "_" + status, Detail: detail, RuleIDs: req.RuleIDs, EvidenceIDs: evidence}}}
		}
	}
	panic("unknown host T3 check")
}

func structureCheck(r testintegrity.Report) Check {
	status := map[string]string{"preserved": "pass", "violated": "fail", "unknown": "unknown"}[r.Status]
	c := t3Check("test_structure", status, "frozen original test and helper structure: "+r.Status, "test-structure")
	for _, f := range r.Findings {
		finding := quality.Finding{Code: f.Code, Detail: f.Detail, RuleIDs: f.RuleIDs, EvidenceIDs: c.EvidenceIDs}
		for _, loc := range f.Locations {
			// adapted 行号不能冒充原文件位置；实际适配材料由证据引用定位。
			if loc.Tree == "candidate" || loc.Tree == "baseline" {
				finding.Locations = append(finding.Locations, quality.Location{Tree: loc.Tree, Path: loc.File, Line: loc.Line})
			}
		}
		c.Findings = append(c.Findings, finding)
	}
	return c
}

func analysisCheck(before, after analysis.Report) (Check, *analysis.Comparison, error) {
	c := t3Check("migration_analysis", "unknown", "type analysis or configuration coverage is incomplete", "analysis-baseline/analysis.json", "analysis-candidate/analysis.json")
	if before.Status != "complete" || after.Status != "complete" {
		return c, nil, nil
	}
	comparison, err := analysis.Compare(before, after)
	if err != nil {
		return Check{}, nil, err
	}
	c.Status, c.Detail = "pass", "all baseline RPC sites correspond to covered v2 boundaries in the declared configuration"
	c.EvidenceIDs = append(c.EvidenceIDs, "analysis-comparison")
	for _, change := range comparison.Changes {
		c.Status = "unknown"
		finding := quality.Finding{Code: change.Reason, Detail: "RPC correspondence is unproven: " + change.Reason, RuleIDs: []string{"SCOPE-001"}, EvidenceIDs: c.EvidenceIDs}
		for _, side := range []struct {
			tree  string
			sites []analysis.Site
		}{{"baseline", change.Baseline}, {"candidate", change.Candidate}} {
			for _, site := range side.sites {
				finding.Locations = append(finding.Locations, quality.Location{Tree: side.tree, Path: site.File, Line: site.Line})
			}
		}
		c.Findings = append(c.Findings, finding)
	}
	for _, gap := range comparison.Gaps {
		c.Status = "unknown"
		c.Findings = append(c.Findings, quality.Finding{Code: gap.Reason, Detail: "analysis coverage gap: " + gap.Reason, RuleIDs: []string{"SCOPE-001"},
			Locations: []quality.Location{{Tree: gap.Tree, Path: gap.File, Line: gap.Line}}, EvidenceIDs: c.EvidenceIDs})
	}
	for _, call := range after.Calls {
		if call.Status == "violation" {
			c.Status = "fail"
			c.Findings = append(c.Findings, quality.Finding{Code: call.Reason, Detail: "resolved timeout differs from 30 seconds", RuleIDs: []string{"TIME-001"},
				Locations: []quality.Location{{Tree: "candidate", Path: call.File, Line: call.Line}}, EvidenceIDs: c.EvidenceIDs})
		} else if call.Status != "covered" && c.Status != "fail" {
			c.Status = "unknown"
		}
	}
	// fail 优先但 unknown 仍通过 Findings 和 quality.Gaps 保留。
	c.Detail = "RPC correspondence and timeout coverage: " + c.Status
	c.Findings[0].Code, c.Findings[0].Detail = "migration_analysis_"+c.Status, c.Detail
	return c, &comparison, nil
}

func behaviorCheck(r behavior.Report) Check {
	c := t3Check("trusted_behavior", r.Status, "trusted production copy business and T3 observations: "+r.Status, "behavior/report.json")
	for _, test := range r.Tests {
		if test.Status != "pass" {
			c.Findings = append(c.Findings, quality.Finding{Code: "behavior_" + test.Status, Detail: test.ID + ": " + test.Status, RuleIDs: test.RuleIDs, EvidenceIDs: c.EvidenceIDs})
		}
	}
	return c
}

// candidateTestCheck 独立核验候选测试执行；未知入口退出不能当成删除断言或业务失败。
func candidateTestCheck(data []byte, meta Command, names map[string]bool) Check {
	c := t3Check("candidate_tests", "pass", "all required original tests executed and passed in the candidate", "candidate-tests")
	runs, ends, packages := map[string]int{}, map[string][]string{}, map[string]string{}
	abnormal := false
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var e struct{ Action, Package, Test, Output string }
		if err := decoder.Decode(&e); err == io.EOF {
			break
		} else if err != nil {
			abnormal = true
			break
		}
		if strings.Contains(e.Output, "panic:") || strings.Contains(e.Output, "fatal error:") || strings.Contains(e.Output, "[build failed]") {
			abnormal = true
		}
		id := e.Package + "/" + e.Test
		if e.Test == "" {
			if e.Action == "pass" || e.Action == "fail" || e.Action == "skip" {
				packages[e.Package] = e.Action
			}
			continue
		}
		if e.Action == "run" {
			runs[id]++
		}
		if e.Action == "pass" || e.Action == "fail" || e.Action == "skip" {
			ends[id] = append(ends[id], e.Action)
		}
	}
	if abnormal {
		return t3Check("candidate_tests", "unknown", "candidate test events are malformed or execution ended abnormally", "candidate-tests")
	}
	for id, states := range ends {
		if runs[id] == 1 && len(states) == 1 && states[0] == "fail" && meta.ExitCode != 0 {
			c.Status = "fail"
			c.Findings = append(c.Findings, quality.Finding{Code: "candidate_test_failed", Detail: id + ": fail", RuleIDs: []string{"TEST-001"}, EvidenceIDs: c.EvidenceIDs})
		}
	}
	for _, id := range sortedPaths(names) {
		at := strings.Index(id, "/Test")
		pkg := id[:at]
		status := "unknown"
		if runs[id] == 1 && len(ends[id]) == 1 {
			if ends[id][0] == "pass" && packages[pkg] == "pass" {
				status = "pass"
			}
			if ends[id][0] == "skip" || (ends[id][0] == "fail" && packages[pkg] == "fail" && meta.ExitCode != 0) {
				status = "fail"
			}
		}
		if status != "pass" {
			if status == "fail" || c.Status != "fail" {
				c.Status = status
			}
			c.Findings = append(c.Findings, quality.Finding{Code: "original_test_" + status, Detail: id + ": " + status, RuleIDs: []string{"TEST-001"}, EvidenceIDs: c.EvidenceIDs})
		}
	}
	if meta.ExitCode != 0 && c.Status == "pass" {
		c.Status = "unknown"
	}
	c.Detail = fmt.Sprintf("candidate original test execution: %s", c.Status)
	c.Findings[0].Detail, c.Findings[0].Code = c.Detail, "candidate_tests_"+c.Status
	return c
}
