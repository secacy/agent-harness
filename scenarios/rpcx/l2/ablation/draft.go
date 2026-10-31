package ablation

import (
	"path/filepath"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/task"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
	"github.com/secacy/agent-harness/scenarios/rpcx/runner"
)

// Draft binds current public task/reference inputs without issuing a request or
// starting Docker. Model capability and engine identities remain deliberately
// unset until measured, so this draft cannot pass Validate or launch spending.
func Draft(project string) (Protocol, error) {
	project, err := filepath.Abs(project)
	if err != nil {
		return Protocol{}, err
	}
	p := Protocol{SchemaVersion: 1, ID: "p1-baseline-v0_1_0", Kind: "formal", Repetitions: 3, Limits: agent.Limits{MaxModelCalls: 40, MaxToolCalls: 80}, AttemptBudget: budget.Limits{MaxCalls: 40, MaxTokens: 2500000}, TotalBudget: budget.Limits{MaxCalls: 1440, MaxTokens: 90000000}, PreparationTimeout: 2 * time.Minute, GenerationTimeout: 10 * time.Minute, ValidationTimeout: 5 * time.Minute, ReferenceTimeout: 5 * time.Minute, HostTimeout: 16 * time.Hour, Analysis: "independent-generations", FailurePolicy: "retain-all-no-retry-stop-on-shared-integrity-failure"}
	p.Model = budget.Profile{Provider: "deepseek", Endpoint: "https://api.deepseek.com/chat/completions", Model: "deepseek-flash", ContextTokens: 1048576, MaxOutputTokens: 4096}
	p.Execution.Kind = "docker"
	p.Execution.CPUs = 2
	p.Execution.MemoryBytes = 2 << 30
	p.Execution.Pids = 128
	for _, c := range []struct {
		id, dataset, module, spec, version string
		build                              func(string, string, string) (task.Target, error)
	}{
		{"goddd", "rpcx-l2-goddd-v1", "goddd", "v0.3.0", "rpcx-l2-reference-goddd-data-v2", runner.TargetFromGoddd},
		{"translation", "rpcx-l2-translation-v1", "translation", "v0.4.0", "rpcx-l2-reference-translation-data-v2", runner.TargetFromTranslationScope},
		{"workouts", "rpcx-l2-workouts-v1", "workouts", "v0.5.0", "rpcx-l2-reference-workouts-data-v1", runner.TargetFromWorkouts},
		{"ghcli", "rpcx-l2-ghcli-v1", "ghcli", "v0.6.0", evaluation.GHCLIDataVersion, runner.TargetFromGHCLI},
	} {
		fixtures := filepath.Join(project, "scenarios/rpcx/l2/data", c.dataset, "fixtures")
		spec := filepath.Join(project, "scenarios/rpcx", c.module, "specs", c.spec+".md")
		input, err := c.build(c.id, fixtures, spec)
		if err != nil {
			return p, err
		}
		identity, err := evaluation.PublishedIdentity(c.version)
		if err != nil {
			return p, err
		}
		limit := 0
		if c.id == "translation" || c.id == "workouts" {
			limit = 2
		}
		p.Tasks = append(p.Tasks, Task{ID: c.id, Input: input, DiagnosticLimit: limit, ReferenceVersion: identity.Version, ReferenceSHA256: identity.ReferenceSHA256, ReferenceToolSHA256: identity.ToolSHA256})
	}
	p.Schedule = Schedule(p.Tasks, p.Repetitions)
	return p, nil
}
