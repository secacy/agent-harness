package ablation

import (
	"context"
	"os"
	"path/filepath"

	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/runner"
)

// TaskReview is filled by an actual person after inspecting the public inputs
// and independent reference. Empty templates never authorize a formal run.
type TaskReview struct {
	Task         Task     `json:"task"`
	Reviewer     string   `json:"reviewer"`
	Relationship string   `json:"relationship"`
	RecordedAt   string   `json:"recorded_at"`
	Decision     string   `json:"decision"`
	Evidence     []string `json:"evidence"`
	Reason       string   `json:"reason"`
}

// PrepareDraft creates inspectable model-visible definitions and human review
// templates. It neither probes a provider nor starts an execution backend.
func PrepareDraft(ctx context.Context, project, output string) error {
	p, err := Draft(project)
	if err != nil {
		return err
	}
	definitions := map[string]runner.ExperimentDefinition{}
	var reviews []TaskReview
	for _, t := range p.Tasks {
		for _, g := range []Group{A, B, C} {
			limit := t.DiagnosticLimit
			if g == A {
				limit = 0
			}
			d, err := runner.DescribeExperiment(ctx, t.Input, runner.ExperimentGroup(g), limit)
			if err != nil {
				return err
			}
			definitions[t.ID+"/"+string(g)] = d
		}
		reviews = append(reviews, TaskReview{Task: t, Decision: "pending", Evidence: []string{}})
	}
	output, err = gate.Canonical(output)
	if err != nil {
		return err
	}
	for _, t := range p.Tasks {
		if err = runner.ValidateTarget(t.Input, output); err != nil {
			return err
		}
	}
	if err = os.Mkdir(output, 0700); err != nil {
		return err
	}
	if err = publishJSON(filepath.Join(output, "protocol.draft.json"), p); err != nil {
		return err
	}
	if err = publishJSON(filepath.Join(output, "generation-definitions.json"), definitions); err != nil {
		return err
	}
	return publishJSON(filepath.Join(output, "task-reviews.template.json"), reviews)
}
