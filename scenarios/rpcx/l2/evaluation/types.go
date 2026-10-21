// Package evaluation 对已固定的迁移候选执行独立离线参考检查。
package evaluation

import (
	"time"

	"github.com/secacy/agent-harness/internal/execution"
)

type Status string

const (
	Pass         Status = "pass"
	Fail         Status = "fail"
	Inconclusive Status = "inconclusive"
)

// Request 不接受门禁结论、校准预期或 Agent 自述。
type Request struct {
	TargetID, RunID           string
	FixturesDir, CandidateDir string
	CandidateSHA256, SpecPath string
	EvidenceDir               string
	Backend                   execution.Executor
	TrustedLocal              bool
}

type Finding struct {
	Rule   string `json:"rule"`
	Detail string `json:"detail"`
}

type Observation struct {
	Status   Status            `json:"status"`
	Reason   string            `json:"reason,omitempty"`
	Findings []Finding         `json:"findings,omitempty"`
	Tests    map[string]string `json:"tests,omitempty"`
}

type Command struct {
	ID          string   `json:"id"`
	Args        []string `json:"args"`
	Directory   string   `json:"directory"`
	ExitCode    int      `json:"exit_code"`
	Stdout      string   `json:"stdout"`
	Stderr      string   `json:"stderr"`
	ContainerID string   `json:"container_id,omitempty"`
	DurationMS  int64    `json:"duration_ms"`
	Error       string   `json:"error,omitempty"`
}

type Report struct {
	SchemaVersion       int                            `json:"schema_version"`
	Version             string                         `json:"version"`
	TargetID            string                         `json:"target_id"`
	RunID               string                         `json:"run_id"`
	CandidateSHA256     string                         `json:"candidate_sha256"`
	ProductionSHA256    string                         `json:"production_sha256,omitempty"`
	BaselineSHA256      string                         `json:"baseline_sha256,omitempty"`
	SpecSHA256          string                         `json:"spec_sha256,omitempty"`
	ReferenceVersion    string                         `json:"reference_version,omitempty"`
	ReferenceSHA256     string                         `json:"reference_sha256,omitempty"`
	ToolSHA256          string                         `json:"tool_sha256,omitempty"`
	HostMaterialsSHA256 string                         `json:"host_materials_sha256,omitempty"`
	SDKs                map[string]string              `json:"sdks,omitempty"`
	Backend             execution.Info                 `json:"backend"`
	StartedAt           time.Time                      `json:"started_at"`
	FinishedAt          time.Time                      `json:"finished_at"`
	InputsUnchanged     bool                           `json:"inputs_unchanged"`
	Automatic           Status                         `json:"automatic"`
	Checks              map[string]Observation         `json:"checks"`
	Configurations      map[string]ConfigurationReport `json:"configurations,omitempty"`
	LocalModules        map[string]ModuleReport        `json:"local_modules,omitempty"`
	Commands            []Command                      `json:"commands"`
	Artifacts           map[string]string              `json:"artifacts"`
	Error               string                         `json:"error,omitempty"`
}

// ModuleReport 保存只读本地依赖自身的构建与测试，不能由业务模块成功推断。
type ModuleReport struct {
	SHA256 string                 `json:"sha256"`
	Checks map[string]Observation `json:"checks"`
}

// ConfigurationReport 记录实际执行配置，不把一套构建事件用于另一套配置。
type ConfigurationReport struct {
	Tags          string                 `json:"tags"`
	SelectedFiles map[string][]string    `json:"selected_files"`
	Checks        map[string]Observation `json:"checks"`
}

// Review 由复核者另行填写；模板不预填通过结果或人员身份。
type Review struct {
	SchemaVersion   int                   `json:"schema_version"`
	Kind            string                `json:"kind"`
	ReportSHA256    string                `json:"report_sha256"`
	TargetID        string                `json:"target_id"`
	RunID           string                `json:"run_id"`
	CandidateSHA256 string                `json:"candidate_sha256"`
	Reviewer        string                `json:"reviewer"`
	RecordedAt      string                `json:"recorded_at"`
	Items           map[string]ReviewItem `json:"items"`
}

type ReviewItem struct {
	Status   Status   `json:"status"`
	Reason   string   `json:"reason"`
	Files    []string `json:"files"`
	Evidence []string `json:"evidence"`
}

type Assessment struct {
	Kind            string `json:"kind"`
	TargetID        string `json:"target_id"`
	RunID           string `json:"run_id"`
	CandidateSHA256 string `json:"candidate_sha256"`
	ReportSHA256    string `json:"report_sha256"`
	ReviewSHA256    string `json:"review_sha256"`
	Status          Status `json:"status"`
}
