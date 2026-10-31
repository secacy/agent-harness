package runner

import (
	"context"
	"fmt"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/repotools"
	"github.com/secacy/agent-harness/internal/task"
)

// ExperimentDefinition is the model-visible policy for one frozen task/group.
// It is derived from the same prompt and wire-tool definitions as execution;
// describing a policy never creates workspaces or dispatches external work.
type ExperimentDefinition struct {
	Messages         []agent.Message        `json:"messages"`
	Tools            []agent.ToolSpec       `json:"tools"`
	CompletionPolicy agent.CompletionPolicy `json:"completion_policy"`
	DiagnosticLimit  int                    `json:"generation_diagnostic_limit"`
}

// DescribeExperiment authenticates all task inputs and returns the exact initial
// messages and tools used by RunExperiment, before dynamic budget notices. The
// baseline and SDK aliases expose only the same task material as production.
func DescribeExperiment(ctx context.Context, target task.Target, group ExperimentGroup, diagnosticLimit int) (out ExperimentDefinition, err error) {
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if group != ExperimentA && group != ExperimentB && group != ExperimentC {
		return out, fmt.Errorf("%w: unsupported experiment group", ErrInvalidConfig)
	}
	if group == ExperimentA && diagnosticLimit != 0 {
		return out, fmt.Errorf("%w: plain group cannot receive migration diagnostics", ErrInvalidConfig)
	}
	if err = target.Validate(); err != nil {
		return out, err
	}
	p, err := lookupProfile(target.ProfileID)
	if err != nil {
		return out, err
	}
	if err = p.validate(target); err != nil {
		return out, err
	}
	if err = ValidateDiagnostics(target.ProfileID, diagnosticLimit); err != nil {
		return out, err
	}
	_, spec, err := readTarget(ctx, target, p)
	if err != nil {
		return out, fmt.Errorf("%w: %w", ErrInputChanged, err)
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	roots := map[string]string{"baseline": "", "spec": ""}
	for name := range p.Dependencies {
		roots[name] = ""
	}
	r := runState{profile: p, input: Input{SpecVersion: p.SpecVersion}, spec: spec}
	out.Messages = r.generationMessages(sortedKeys(roots), group == ExperimentA)
	roots["workspace"] = ""
	out.Tools = repotools.Specifications(sortedKeys(roots), true)
	if diagnosticLimit != 0 {
		out.Tools = append(out.Tools, diagnosticSpec())
	}
	out.CompletionPolicy, out.DiagnosticLimit = agent.FinalResponseV1, diagnosticLimit
	return out, nil
}
