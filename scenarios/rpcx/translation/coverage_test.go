package translation

import (
	"testing"

	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/analysis"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
)

func TestInactiveFileRequiresKnownCounterpart(t *testing.T) {
	makeComparison := func() analysis.Comparison {
		return analysis.Comparison{Matches: []analysis.Match{{Baseline: analysis.Site{File: adapters[0]}, Candidate: analysis.Site{File: adapters[0]}, Operation: "translate"}}, Gaps: []analysis.ComparisonGap{{Tree: "baseline", File: adapters[1], Reason: "inactive_build_file"}, {Tree: "candidate", File: adapters[1], Reason: "inactive_build_file"}}}
	}
	if !configuredCoverage("default", makeComparison()) {
		t.Fatal("known matrix not recognized")
	}
	for _, mode := range []string{"extra", "wrong-file", "no-candidate", "wrong-active", "wrong-operation"} {
		t.Run(mode, func(t *testing.T) {
			c := makeComparison()
			switch mode {
			case "extra":
				c.Gaps = append(c.Gaps, analysis.ComparisonGap{Tree: "candidate", File: "extra.go", Reason: "inactive_build_file"})
			case "wrong-file":
				c.Gaps[0].File = "extra.go"
			case "no-candidate":
				c.Gaps = c.Gaps[:1]
			case "wrong-active":
				c.Matches[0].Candidate.File = adapters[1]
			case "wrong-operation":
				c.Matches[0].Operation = "other"
			}
			if configuredCoverage("default", c) {
				t.Fatal("unproven configuration coverage accepted")
			}
		})
	}
}
func TestConfigurationMergeCannotEraseMissingEvidence(t *testing.T) {
	for _, initial := range []string{quality.StatusBlocked, quality.StatusUnknown, quality.StatusFail} {
		t.Run(initial, func(t *testing.T) {
			e := evaluation{merged: map[string]bool{}, result: checks.Evaluation{Checks: map[string]quality.Check{"build": check("build", quality.StatusBlocked, "not started", "configurations")}}}
			e.merge("build", "default", check("build", initial, "missing or failed", "configurations"))
			e.merge("build", "rpcx_alternate", check("build", quality.StatusPass, "passed", "configurations"))
			if e.result.Checks["build"].Status != initial {
				t.Fatal("pass erased required evidence gap")
			}
		})
	}
}
