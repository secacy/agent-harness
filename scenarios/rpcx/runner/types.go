// Package runner 组织 rpcx 场景的一次候选生成、独立验证和交付。
package runner

import (
	"errors"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/task"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
)

var (
	ErrInvalidConfig   = errors.New("invalid runner configuration")
	ErrAgentIncomplete = errors.New("agent did not submit a candidate")
	ErrInputChanged    = errors.New("task input could not be authenticated")
	ErrBaseline        = errors.New("baseline checks did not pass")
)

// Config 由宿主配置。Model 只负责模型调用，不参与目录准备和质量判定。
type Config struct {
	// Target 与旧 ScenarioDir/SpecVersion 入口互斥。
	Target      *task.Target
	ScenarioDir string
	SpecVersion string // 为空时保留 v0.1.0；新场景必须显式选择。
	RunsDir     string
	RunID       string // 可信宿主预分配的收据身份，目录仍必须独占创建。
	Model       agent.Model
	ModelName   string
	Budget      *budget.Target
	Limits      agent.Limits
	Timeout     time.Duration
	// 仅使用宿主后端时启用，不能与容器后端同时设置。
	TrustedLocal  bool
	Backend       execution.Executor
	ResourceOwner *execution.ResourceOwner
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
	PhasePreparing        Phase = "Preparing"
	PhaseCheckingBaseline Phase = "CheckingBaseline"
	PhaseGenerating       Phase = "Generating"
	PhaseValidating       Phase = "Validating"
	PhasePublishing       Phase = "Publishing"
)

// Record 是 run.json 的内容；只有 Completed 才表示产物已经完整交付。
type Record struct {
	SchemaVersion   int              `json:"schema_version"`
	ID              string           `json:"id"`
	TargetID        string           `json:"target_id,omitempty"`
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
	FailureCode     string           `json:"failure_code,omitempty"`
}

// Input 只保存可审计的任务配置与输入摘要，不序列化模型客户端或密钥。
type Input struct {
	Target         *task.Target      `json:"target,omitempty"`
	ProfileSHA256  string            `json:"profile_sha256,omitempty"`
	RepositoryName string            `json:"repository_name,omitempty"`
	Budget         *budget.Reference `json:"budget,omitempty"`
	ScenarioDir    string            `json:"scenario_dir"`
	ModelName      string            `json:"model_name"`
	Backend        execution.Info    `json:"backend"`
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
	Baseline        *BaselineReport    `json:"baseline_check,omitempty"`
	FailureCode     string             `json:"failure_code,omitempty"`
	RunID           string             `json:"run_id"`
	Status          Status             `json:"status"`
	Review          string             `json:"review,omitempty"`
	Input           Input              `json:"input"`
	CandidateSHA256 string             `json:"candidate_sha256,omitempty"`
	PatchSHA256     string             `json:"patch_sha256,omitempty"`
	AgentRecord     string             `json:"agent_record,omitempty"`
	Validation      *checks.Evaluation `json:"validation,omitempty"`
	Error           string             `json:"error,omitempty"`
}
