package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
)

func TestFailureSummaryKeepsDenominatorsAndIncompleteRuns(t *testing.T) {
	var directories []string
	now := time.Now().UTC()
	cases := []struct {
		record     Record
		validation *checks.Evaluation
		want       string
	}{
		{Record{Status: StatusCompleted, Verdict: "ReadyForReview", FinishedAt: &now}, nil, "success"},
		{Record{Status: StatusCompleted, Verdict: "Rejected", FinishedAt: &now}, &checks.Evaluation{Checks: map[string]checks.Check{"build": {Status: "fail"}}}, "compile"},
		{Record{Status: StatusCompleted, Verdict: "Rejected", FinishedAt: &now}, &checks.Evaluation{Checks: map[string]checks.Check{"candidate_tests": {Status: "fail"}}}, "test"},
		{Record{Status: StatusFailed, AgentExitReason: agent.ExitLimitReached, FinishedAt: &now}, nil, "budget"},
		{Record{Status: StatusCompleted, Verdict: "NeedsAttention", FinishedAt: &now}, nil, "gate_incomplete"},
		{Record{Status: StatusFailed, Error: "generate candidate: tool edit (a): tool execution failed: disk", FinishedAt: &now}, nil, "tool"},
		{Record{Status: StatusFailed, Error: "publish report: disk", FinishedAt: &now}, nil, "infrastructure"},
		{Record{Status: StatusRunning}, nil, "incomplete"},
	}
	for _, tc := range cases {
		dir := t.TempDir()
		directories = append(directories, dir)
		r := tc.record
		r.ID = filepath.Base(dir)
		if err := writeJSON(filepath.Join(dir, "run.json"), r); err != nil {
			t.Fatal(err)
		}
		if r.Status != StatusRunning {
			if err := os.Mkdir(filepath.Join(dir, "artifacts"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := writeJSON(filepath.Join(dir, "artifacts/report.json"), Report{RunID: r.ID, Status: r.Status, Validation: tc.validation}); err != nil {
				t.Fatal(err)
			}
		}
	}
	result, err := SummarizeFailures(context.Background(), directories)
	if err != nil {
		t.Fatal(err)
	}
	if result.Runs != 8 || result.Failures != 6 || result.Incomplete != 1 {
		t.Fatalf("lost denominator: %+v", result)
	}
	for i, o := range result.Outcomes {
		if o.Category != cases[i].want {
			t.Fatalf("case %d: %+v", i, o)
		}
	}
	for _, c := range result.Counts {
		if c.FractionOfRuns != 0.125 {
			t.Fatal(c)
		}
		if c.Category == "success" || c.Category == "incomplete" {
			if c.FractionOfFailures != nil {
				t.Fatal(c)
			}
		} else if c.FractionOfFailures == nil || *c.FractionOfFailures != 1.0/6.0 {
			t.Fatal(c)
		}
	}
	if _, err := SummarizeFailures(context.Background(), append(directories, directories[0])); err == nil {
		t.Fatal("duplicate source changed denominator")
	}
	if err := os.Remove(filepath.Join(directories[0], "artifacts/report.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := SummarizeFailures(context.Background(), directories); err == nil {
		t.Fatal("missing terminal report was silently dropped")
	}
}
func TestFailureSummaryZeroFailureIsUndefined(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	if err := os.Mkdir(filepath.Join(dir, "artifacts"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(dir, "run.json"), Record{ID: "r", Status: StatusCompleted, Verdict: "ReadyForReview", FinishedAt: &now}); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(dir, "artifacts/report.json"), Report{RunID: "r", Status: StatusCompleted}); err != nil {
		t.Fatal(err)
	}
	s, err := SummarizeFailures(context.Background(), []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if s.Failures != 0 || len(s.Counts) != 1 || s.Counts[0].FractionOfFailures != nil {
		t.Fatal(s)
	}
	if err := writeJSON(filepath.Join(dir, "artifacts/report.json"), Report{RunID: "other", Status: StatusCompleted}); err != nil {
		t.Fatal(err)
	}
	if _, err := SummarizeFailures(context.Background(), []string{dir}); err == nil {
		t.Fatal("mixed run/report accepted")
	}
}
