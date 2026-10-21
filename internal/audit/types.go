// Package audit 只读检查批次记录的归属、发布状态和预算一致性。
package audit

import (
	"context"
	"errors"
	"time"

	"github.com/secacy/agent-harness/internal/task"
)

const Version = "campaign-audit-v5"

var ErrConfig = errors.New("invalid audit configuration")

type Request struct {
	CampaignDir string
	OutputDir   string
}

type Severity string

const (
	Violation   Severity = "violation"
	Gap         Severity = "gap"
	Observation Severity = "observation"
)

// Finding 的 Sources 必须包含得出结论所依赖的材料；复核失败时据此降级。
type Finding struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	TargetID string   `json:"target_id,omitempty"`
	RunID    string   `json:"run_id,omitempty"`
	Path     string   `json:"path"`
	Field    string   `json:"field,omitempty"`
	Reason   string   `json:"reason"`
	Sources  []string `json:"sources"`
}

type Source struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256,omitempty"`
	// Verified 为空表示复核未完成，false 表示与首次读取不一致。
	Verified *bool `json:"verified"`
}

type Coverage struct {
	Area     string `json:"area"`
	Complete bool   `json:"complete"`
}

type Report struct {
	SchemaVersion          int        `json:"schema_version"`
	ToolVersion            string     `json:"tool_version"`
	CampaignDir            string     `json:"campaign_dir"`
	Consistency            string     `json:"consistency"`
	ExecutionState         string     `json:"execution_state"`
	RecoveryExecutionState string     `json:"recovery_execution_state,omitempty"`
	ResourceState          string     `json:"resource_state,omitempty"`
	ScanComplete           bool       `json:"scan_complete"`
	InputsUnchanged        *bool      `json:"inputs_unchanged"`
	WriterState            string     `json:"writer_state"`
	Violations             []Finding  `json:"violations"`
	Gaps                   []Finding  `json:"gaps"`
	Observations           []Finding  `json:"observations"`
	Sources                []Source   `json:"sources"`
	Coverage               []Coverage `json:"coverage"`
}

// RunRecord 是 Run v2 的归属与状态信封；业务交付证据由宿主检查。
type RunRecord struct {
	SchemaVersion   int        `json:"schema_version"`
	ID              string     `json:"id"`
	TargetID        string     `json:"target_id,omitempty"`
	Directory       string     `json:"directory"`
	Status          string     `json:"status"`
	Phase           string     `json:"phase"`
	StartedAt       time.Time  `json:"started_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	FinishedAt      *time.Time `json:"finished_at,omitempty"`
	AgentExitReason string     `json:"agent_exit_reason,omitempty"`
	CandidateSHA256 string     `json:"candidate_sha256,omitempty"`
	PatchSHA256     string     `json:"patch_sha256,omitempty"`
	Verdict         string     `json:"verdict,omitempty"`
	Error           string     `json:"error,omitempty"`
	FailureCode     string     `json:"failure_code,omitempty"`
}

type RunRef struct {
	Path         string
	Target       task.Target
	CampaignID   string
	OriginalRoot string
	Record       RunRecord
	BudgetPath   string // 为空表示原根账本；恢复代次由宿主审计固定路径。
}

type RunInspection struct {
	Complete bool
	Findings []Finding
}

// RunInspector 是可信宿主函数，读取材料必须经过同一个 View。
type RunInspector func(context.Context, *View, RunRef) (RunInspection, error)
