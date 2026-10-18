// Package acceptance 运行离线 T3 场景验收，不提供运行时质量判定。
package acceptance

import (
	"time"

	"github.com/secacy/agent-harness/internal/execution"
)

type Config struct {
	ScenarioDir  string
	RunsDir      string
	Backend      execution.Executor
	TrustedLocal bool
	Development  bool
	Quick        bool
}

type Check struct {
	Status string            `json:"status"`
	Rules  []string          `json:"rules"`
	Tests  map[string]string `json:"tests"`
}

type Command struct {
	Label       string   `json:"label"`
	Args        []string `json:"args"`
	Directory   string   `json:"directory"`
	Stdout      string   `json:"stdout"`
	Stderr      string   `json:"stderr"`
	ExitCode    int      `json:"exit_code"`
	DurationMS  int64    `json:"duration_ms"`
	ContainerID string   `json:"container_id,omitempty"`
	Error       string   `json:"error,omitempty"`
}

type CaseResult struct {
	ID                        string `json:"id"`
	CandidateSHA256           string `json:"candidate_sha256"`
	ProductionSHA256          string `json:"production_sha256"`
	ReferenceProductionSHA256 string `json:"reference_production_sha256"`
	Build                     Check  `json:"build"`
	Business                  Check  `json:"business_tests"`
	Reference                 Check  `json:"reference_checks"`
	Slow                      *Check `json:"actual_expiry,omitempty"`
	PatchReplayed             bool   `json:"patch_replayed"`
	InputsUnchanged           bool   `json:"inputs_unchanged"`
	ExpectationMatched        bool   `json:"expectation_matched"`
}

type Report struct {
	SchemaVersion   int               `json:"schema_version"`
	DataVersion     string            `json:"data_version"`
	ToolSHA256      string            `json:"tool_sha256"`
	Directory       string            `json:"directory"`
	StartedAt       time.Time         `json:"started_at"`
	FinishedAt      time.Time         `json:"finished_at"`
	Mode            string            `json:"mode"`
	Backend         execution.Info    `json:"backend"`
	GoVersion       string            `json:"go_version"`
	SourceRevision  string            `json:"source_revision"`
	Inputs          map[string]string `json:"input_files"`
	InputsSHA256    string            `json:"inputs_sha256"`
	FreezeMatched   bool              `json:"freeze_matched"`
	InputsUnchanged bool              `json:"inputs_unchanged"`
	SlowEnabled     bool              `json:"slow_enabled"`
	V1Expiry        *Check            `json:"v1_actual_expiry,omitempty"`
	Cases           []CaseResult      `json:"cases"`
	Commands        []Command         `json:"commands"`
	Matched         bool              `json:"expectations_matched"`
	Accepted        bool              `json:"accepted"`
	Error           string            `json:"error,omitempty"`
}

type expected struct {
	Build            string   `json:"build"`
	Business         string   `json:"business_tests"`
	Reference        string   `json:"reference_checks"`
	Rules            []string `json:"failure_rules"`
	RequiredFailures []string `json:"required_failures"`
}

type scenarioCase struct {
	ID       string   `json:"id"`
	Patch    string   `json:"patch"`
	Expected expected `json:"expected"`
}

type manifest struct {
	SchemaVersion int            `json:"schema_version"`
	Spec          string         `json:"spec"`
	Cases         []scenarioCase `json:"cases"`
}
