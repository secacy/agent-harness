// Package l1 离线交叉验收门禁与独立参考标准，不向 Agent 暴露预期或 oracle。
package l1

import (
	"time"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
)

type Config struct {
	Directory    string
	T3Directory  string
	RunsDir      string
	Backend      execution.Executor
	TrustedLocal bool
	Development  bool
	Quick        bool
	Cases        []string // 子集只能用于开发，不能作为完整验收。
}

type Manifest struct {
	SchemaVersion int    `json:"schema_version"`
	Version       string `json:"version"`
	SpecSHA256    string `json:"spec_sha256"`
	Cases         []Case `json:"cases"`
}

type Case struct {
	ID          string   `json:"id"`
	Patch       string   `json:"patch"`
	PatchSHA256 string   `json:"patch_sha256"`
	Review      string   `json:"review"`
	Expected    Expected `json:"expected"`
}

type Expected struct {
	Verdict        string            `json:"verdict"`
	Checks         map[string]string `json:"checks"`
	Reasons        []Reason          `json:"reasons"`
	ReferenceBuild string            `json:"reference_build"`
	ReferenceFast  string            `json:"reference_fast"`
	ReferenceRules []string          `json:"reference_rules"`
}

type Reason struct {
	Check string `json:"check"`
	Code  string `json:"code"`
	Rule  string `json:"rule"`
}

type Observation struct {
	Status string            `json:"status"`
	Tests  map[string]string `json:"tests"`
	Rules  []string          `json:"rules"`
}

type Reference struct {
	ProductionSHA256 string      `json:"production_sha256"`
	MaterialSHA256   string      `json:"material_sha256"`
	Build            string      `json:"build"`
	Fast             Observation `json:"fast"`
	Slow             Observation `json:"slow"`
	Scope            string      `json:"scope"`
}

type Command struct {
	Args        []string `json:"args"`
	Directory   string   `json:"directory"`
	Stdout      string   `json:"stdout"`
	Stderr      string   `json:"stderr"`
	ExitCode    int      `json:"exit_code"`
	ContainerID string   `json:"container_id,omitempty"`
	DurationMS  int64    `json:"duration_ms"`
}

type Result struct {
	ID               string            `json:"id"`
	PatchSHA256      string            `json:"patch_sha256"`
	PatchReplayed    bool              `json:"patch_replayed"`
	CandidateSHA256  string            `json:"candidate_sha256"`
	ProductionSHA256 string            `json:"production_sha256"`
	Gate             checks.Evaluation `json:"gate"`
	Reference        Reference         `json:"reference"`
	Commands         []Command         `json:"reference_commands"`
	Mismatches       []string          `json:"mismatches"`
}

type Report struct {
	SchemaVersion   int               `json:"schema_version"`
	Directory       string            `json:"directory"`
	Mode            string            `json:"mode"`
	Backend         execution.Info    `json:"backend"`
	StartedAt       time.Time         `json:"started_at"`
	FinishedAt      time.Time         `json:"finished_at"`
	DataSHA256      string            `json:"data_sha256"`
	ToolSHA256      string            `json:"tool_sha256"`
	T3SHA256        string            `json:"t3_sha256"`
	FreezeMatched   bool              `json:"freeze_matched"`
	InputsUnchanged bool              `json:"inputs_unchanged"`
	FullSelection   bool              `json:"full_selection"`
	SlowEnabled     bool              `json:"slow_enabled"`
	Matched         bool              `json:"matched"`
	Accepted        bool              `json:"accepted"`
	Counts          map[string]int    `json:"counts"` // 构造任务计数，不是生产成功率或留出成绩。
	Results         []Result          `json:"results"`
	Artifacts       map[string]string `json:"artifacts"`
	Error           string            `json:"error,omitempty"`
}
