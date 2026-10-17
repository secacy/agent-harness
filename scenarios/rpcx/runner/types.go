// Package runner 组织 rpcx 场景的一次候选生成、独立验证和交付。
package runner

import (
	"errors"
	"time"

	"example.com/agent-harness/internal/agent"
	"example.com/agent-harness/scenarios/rpcx/checks"
)

var (
	ErrInvalidConfig   = errors.New("invalid runner configuration")
	ErrAgentIncomplete = errors.New("agent did not submit a candidate")
)

// Config 由宿主配置。Model 只负责模型调用，不参与目录准备和质量判定。
type Config struct {
	ScenarioDir string
	RunsDir     string
	Model       agent.Model
	ModelName   string
	Limits      agent.Limits
	Timeout     time.Duration
	// 当前后端在宿主执行代码，调用方必须明确选择可信本地执行。
	TrustedLocal bool
}

type Status string

const (
	StatusRunning   Status = "Running"
	StatusCompleted Status = "Completed"
	StatusFailed    Status = "Failed"
	StatusCancelled Status = "Cancelled"
)

type Phase string

const (
	PhasePreparing  Phase = "Preparing"
	PhaseGenerating Phase = "Generating"
	PhaseValidating Phase = "Validating"
	PhasePublishing Phase = "Publishing"
)

// Record 是 run.json 的内容；只有 Completed 才表示产物已经完整交付。
type Record struct {
	SchemaVersion   int              `json:"schema_version"`
	ID              string           `json:"id"`
	Directory       string           `json:"directory"`
	Status          Status           `json:"status"`
	Phase           Phase            `json:"phase"`
	StartedAt       time.Time        `json:"started_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
	FinishedAt      *time.Time       `json:"finished_at,omitempty"`
	AgentExitReason agent.ExitReason `json:"agent_exit_reason,omitempty"`
	CandidateSHA256 string           `json:"candidate_sha256,omitempty"`
	PatchSHA256     string           `json:"patch_sha256,omitempty"`
	Verdict         string           `json:"verdict,omitempty"`
	Error           string           `json:"error,omitempty"`
}

// Input 只保存可审计的任务配置与输入摘要，不序列化模型客户端或密钥。
type Input struct {
	ScenarioDir    string            `json:"scenario_dir"`
	ModelName      string            `json:"model_name"`
	ExecutionMode  string            `json:"execution_mode"`
	MaxModelCalls  int               `json:"max_model_calls"`
	MaxToolCalls   int               `json:"max_tool_calls"`
	Timeout        string            `json:"timeout"`
	SpecVersion    string            `json:"spec_version"`
	SpecSHA256     string            `json:"spec_sha256"`
	BaselineSHA256 string            `json:"baseline_sha256"`
	Files          map[string]string `json:"files"`
}

// Report 保存实际证据。发布是否成功以本次运行根目录的 run.json 为准。
type Report struct {
	RunID           string             `json:"run_id"`
	Input           Input              `json:"input"`
	CandidateSHA256 string             `json:"candidate_sha256,omitempty"`
	PatchSHA256     string             `json:"patch_sha256,omitempty"`
	AgentRecord     string             `json:"agent_record,omitempty"`
	Validation      *checks.Evaluation `json:"validation,omitempty"`
	Error           string             `json:"error,omitempty"`
}
