// Package campaign 调度独立仓库任务，管理共享预算、执行槽位与交付索引。
package campaign

import (
	"context"
	"errors"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/task"
)

var (
	ErrConfig  = errors.New("invalid campaign configuration")
	ErrControl = errors.New("campaign control failure")
	ErrResult  = errors.New("invalid target result")
)

type Plan struct {
	ID                   string         `json:"id"`
	Concurrency          int            `json:"concurrency"`
	ExecutionConcurrency int            `json:"execution_concurrency"`
	Budget               budget.Limits  `json:"budget"`
	Profile              budget.Profile `json:"profile"`
	Targets              []Target       `json:"targets"`
}

type Target struct {
	Input   task.Target   `json:"input"`
	Budget  budget.Limits `json:"budget"`
	Limits  agent.Limits  `json:"agent_limits"`
	Timeout time.Duration `json:"timeout_ns"`
}

// Services 只交给可信宿主适配器；适配器必须在绑定 Run 后包装模型。
type Services struct {
	RunsDir string
	Model   budget.ProfiledModel
	Budget  budget.Target
	Backend execution.Executor
}

type Execute func(context.Context, Target, Services) (Attempt, error)

type Config struct {
	Directory string // 必须是尚不存在、父目录已存在的目录。
	Plan      Plan
	Backend   execution.Executor
	// 每个 Target 独立创建模型，调用前不得消费生成额度。
	NewModel func(task.Target) (budget.ProfiledModel, error)
	Execute  Execute
}

type Status string

const (
	Running   Status = "Running"
	Completed Status = "Completed"
	Failed    Status = "Failed"
	Cancelled Status = "Cancelled"
)

type TargetState string

const (
	Pending  TargetState = "Pending"
	Active   TargetState = "Running"
	Finished TargetState = "Finished"
	Skipped  TargetState = "Skipped"
)

type Artifact struct {
	Path   string `json:"path"` // 执行函数返回绝对路径；持久化时转为相对 Campaign 的路径。
	SHA256 string `json:"sha256"`
}

// Attempt 引用已发布的 Run；执行函数返回绝对目录，汇总保存相对目录。
// 失败且尚未创建 Run 时身份字段可以为空。
type Attempt struct {
	RunID       string   `json:"run_id,omitempty"`
	Directory   string   `json:"directory,omitempty"`
	Status      Status   `json:"status"`
	Verdict     string   `json:"verdict,omitempty"`
	Report      Artifact `json:"report"`
	FailureCode string   `json:"failure_code,omitempty"`
}

type TargetRecord struct {
	ID      string      `json:"id"`
	State   TargetState `json:"state"`
	Attempt *Attempt    `json:"attempt,omitempty"`
	Reason  string      `json:"reason,omitempty"`
	Error   string      `json:"error,omitempty"`
}

type Record struct {
	SchemaVersion int        `json:"schema_version"`
	ID            string     `json:"id"`
	Directory     string     `json:"directory"`
	Status        Status     `json:"status"`
	PlanSHA256    string     `json:"plan_sha256"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	Summary       Artifact   `json:"summary"`
	Error         string     `json:"error,omitempty"`
}

type Counts struct {
	Completed int            `json:"completed"`
	Failed    int            `json:"failed"`
	Cancelled int            `json:"cancelled"`
	Skipped   int            `json:"skipped"`
	Verdicts  map[string]int `json:"verdicts"`
}

// Summary 是运行索引，不重判质量，也不把放行数量当作独立评测成功数。
type Summary struct {
	CampaignID string          `json:"campaign_id"`
	PlanSHA256 string          `json:"plan_sha256"`
	Targets    []TargetRecord  `json:"targets"`
	Counts     Counts          `json:"counts"`
	Budget     budget.Snapshot `json:"budget"`
}
