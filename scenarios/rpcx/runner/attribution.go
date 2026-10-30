package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/secacy/agent-harness/internal/agent"
)

// Attribution distinguishes execution failures from completed but unreleased
// candidates. These counts describe records, not an independent correctness truth.
type Attribution struct {
	RunID     string   `json:"run_id"`
	Directory string   `json:"directory"`
	Status    Status   `json:"status"`
	Verdict   string   `json:"verdict"`
	Category  string   `json:"category"`
	Secondary []string `json:"secondary,omitempty"`
}
type FailureCount struct {
	Category           string   `json:"category"`
	Count              int      `json:"count"`
	FractionOfRuns     float64  `json:"fraction_of_runs"`
	FractionOfFailures *float64 `json:"fraction_of_failures"`
}
type FailureSummary struct {
	Runs       int            `json:"runs"`
	Failures   int            `json:"failures"`
	Incomplete int            `json:"incomplete"`
	Outcomes   []Attribution  `json:"outcomes"`
	Counts     []FailureCount `json:"counts"`
}

func readRunJSON(path string, value any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular record: %s", path)
	}
	if info.Size() > 64*1024*1024 {
		return errors.New("record exceeds 64 MiB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}
func classifyRun(record Record, report Report) (string, []string) {
	secondary := []string{}
	if report.Validation != nil {
		keys := []string{}
		for id := range report.Validation.Checks {
			keys = append(keys, id)
		}
		sort.Strings(keys)
		for _, id := range keys {
			c := report.Validation.Checks[id]
			if c.Status == "fail" {
				secondary = append(secondary, id)
			}
		}
	}
	if record.Status == StatusRunning || record.FinishedAt == nil {
		return "incomplete", secondary
	}
	if record.Status == StatusCancelled {
		return "cancelled", secondary
	}
	if record.Status == StatusFailed {
		switch record.AgentExitReason {
		case agent.ExitLimitReached:
			return "budget", secondary
		case agent.ExitDeadlineExceeded:
			return "timeout", secondary
		case agent.ExitCancelled:
			return "cancelled", secondary
		}
		if record.FailureCode == "input_invalid" {
			return "input", secondary
		}
		if record.FailureCode == "baseline_failed" && report.Baseline != nil && report.Baseline.Status == "fail" {
			for i := len(report.Baseline.Commands) - 1; i >= 0; i-- {
				command := report.Baseline.Commands[i]
				if command.ExitCode <= 0 {
					continue
				}
				if slices.Contains(command.Args, "build") {
					return "compile", secondary
				}
				if slices.Contains(command.Args, "test") {
					return "test", secondary
				}
			}
		}
		if strings.Contains(record.Error, "tool execution failed") || strings.Contains(record.Error, "tool ") && strings.Contains(record.Error, "generate candidate") {
			return "tool", secondary
		}
		if strings.Contains(record.Error, "model call ") {
			return "model", secondary
		}
		return "infrastructure", secondary
	}
	for _, id := range secondary {
		if id == "build" {
			return "compile", secondary
		}
	}
	for _, id := range secondary {
		if strings.Contains(id, "test") || id == "trusted_behavior" {
			return "test", secondary
		}
	}
	if record.Verdict == "Rejected" {
		return "gate_rejected", secondary
	}
	if record.Verdict == "NeedsAttention" {
		return "gate_incomplete", secondary
	}
	if record.Status == StatusCompleted && record.Verdict == "ReadyForReview" {
		return "success", secondary
	}
	return "infrastructure", secondary
}

// SummarizeFailures reads explicit run directories, including incomplete runs.
// Invalid/missing terminal reports fail the command rather than vanish from N.
func SummarizeFailures(ctx context.Context, directories []string) (FailureSummary, error) {
	summary := FailureSummary{Outcomes: []Attribution{}, Counts: []FailureCount{}}
	seen := map[string]bool{}
	counts := map[string]int{}
	for _, dir := range directories {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		root, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return summary, err
		}
		root, err = filepath.Abs(root)
		if err != nil {
			return summary, err
		}
		if seen[root] {
			return summary, errors.New("duplicate run directory")
		}
		seen[root] = true
		var record Record
		if err := readRunJSON(filepath.Join(root, "run.json"), &record); err != nil {
			return summary, err
		}
		if record.ID == "" {
			return summary, errors.New("record lacks run identity")
		}
		var report Report
		err = readRunJSON(filepath.Join(root, "artifacts/report.json"), &report)
		if err != nil && !(errors.Is(err, os.ErrNotExist) && record.Status == StatusRunning) {
			return summary, err
		}
		if err == nil && (report.RunID != record.ID || report.Status != record.Status) {
			return summary, errors.New("run/report identity or status mismatch")
		}
		category, secondary := classifyRun(record, report)
		summary.Outcomes = append(summary.Outcomes, Attribution{record.ID, root, record.Status, record.Verdict, category, secondary})
		counts[category]++
		summary.Runs++
		if category == "incomplete" {
			summary.Incomplete++
		} else if category != "success" {
			summary.Failures++
		}
	}
	keys := []string{}
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		c := FailureCount{Category: k, Count: counts[k], FractionOfRuns: float64(counts[k]) / float64(summary.Runs)}
		if summary.Failures > 0 && k != "success" && k != "incomplete" {
			v := float64(c.Count) / float64(summary.Failures)
			c.FractionOfFailures = &v
		}
		summary.Counts = append(summary.Counts, c)
	}
	return summary, nil
}
