// Package round 在单候选参考完成后关联原运行记录，生成一次尝试轮次的离线汇总。
package round

import (
	"time"

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
)

const Version = "rpcx-l2-round-v2"
const Calibration = "development_calibration"
const Development = "development_evaluation"

// Manifest 固定纳入范围与参考身份，不是包含模型授权的完整实验协议。
type Manifest struct {
	SchemaVersion int         `json:"schema_version"`
	ID            string      `json:"id"`
	Kind          string      `json:"kind"`
	CampaignID    string      `json:"campaign_id"`
	PlanSHA256    string      `json:"plan_sha256"`
	Targets       []TargetPin `json:"targets"`
	Protocol      *Protocol   `json:"protocol,omitempty"`
}

type TargetPin struct {
	ID               string `json:"id"`
	ProfileID        string `json:"profile_id"`
	BuildConfig      string `json:"build_config"`
	BaselineSHA256   string `json:"baseline_sha256"`
	SpecSHA256       string `json:"spec_sha256"`
	ReferenceVersion string `json:"reference_version"`
	ReferenceSHA256  string `json:"reference_sha256"`
	ToolSHA256       string `json:"tool_sha256"`
}

type Artifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type Reference struct {
	TargetID string    `json:"target_id"`
	Report   Artifact  `json:"report"`
	Review   *Artifact `json:"review,omitempty"`
}

type Request struct {
	ManifestPath string      `json:"manifest_path"`
	CampaignDir  string      `json:"campaign_dir"`
	EvidenceDir  string      `json:"evidence_dir"`
	References   []Reference `json:"references"`
}

type Issue struct {
	Code     string `json:"code"`
	TargetID string `json:"target_id,omitempty"`
	Detail   string `json:"detail"`
}

type Source struct {
	SHA256  string `json:"sha256,omitempty"`
	Missing bool   `json:"missing,omitempty"`
	Copy    string `json:"copy,omitempty"`
}

type Result struct {
	TargetID              string            `json:"target_id"`
	RunID                 string            `json:"run_id,omitempty"`
	RunStatus             string            `json:"run_status"`
	Verdict               string            `json:"verdict,omitempty"`
	FailureCode           string            `json:"failure_code,omitempty"`
	CandidateSHA256       string            `json:"candidate_sha256,omitempty"`
	DeliveryVerified      bool              `json:"delivery_verified"`
	Automatic             evaluation.Status `json:"automatic"`
	Reference             evaluation.Status `json:"reference"`
	FailureCategories     []string          `json:"failure_categories,omitempty"`
	Gaps                  []string          `json:"gaps,omitempty"`
	Cost                  *budget.Totals    `json:"cost,omitempty"`
	LoopModelCalls        *int              `json:"loop_model_calls"`
	ToolCalls             *int              `json:"tool_calls"`
	AdmittedCalls         *int              `json:"admitted_calls,omitempty"`
	Rejections            *int              `json:"rejections,omitempty"`
	DurationMS            *int64            `json:"duration_ms,omitempty"`
	ReferenceReportSHA256 string            `json:"reference_report_sha256,omitempty"`
	ReviewSHA256          string            `json:"review_sha256,omitempty"`
}

type Counts struct {
	Targets                int                       `json:"targets"`
	Runs                   int                       `json:"runs"`
	Candidates             int                       `json:"candidates"`
	Released               int                       `json:"released"`
	NeedsAttention         int                       `json:"needs_attention"`
	Success                int                       `json:"success"`
	UnknownSuccess         int                       `json:"unknown_success"`
	WrongReleases          int                       `json:"wrong_releases"`
	UnknownReleases        int                       `json:"unknown_releases"`
	Execution              map[string]int            `json:"execution"`
	Verdicts               map[string]int            `json:"verdicts"`
	Reference              map[evaluation.Status]int `json:"reference"`
	Failures               map[string]int            `json:"failures"`
	WrongReleaseCategories map[string]int            `json:"wrong_release_categories"`
}

// Interval 保留原始分子/分母；分母为零时不创建对象，不伪造百分比。
type Interval struct {
	Lower       int `json:"lower_numerator"`
	Upper       int `json:"upper_numerator"`
	Denominator int `json:"denominator"`
}

type Metrics struct {
	Success             *Interval `json:"effective_automatic_success"`
	WrongRelease        *Interval `json:"wrong_release"`
	AttentionTargets    *Interval `json:"attention_targets"`
	AttentionCandidates *Interval `json:"attention_candidates"`
}

type Cost struct {
	Profile           budget.Profile `json:"profile"`
	Limits            budget.Limits  `json:"limits"`
	Totals            budget.Totals  `json:"totals"`
	KnownInputTokens  int64          `json:"known_input_tokens"`
	KnownOutputTokens int64          `json:"known_output_tokens"`
	Rejections        int            `json:"rejections"`
}

type Report struct {
	SchemaVersion     int               `json:"schema_version"`
	Version           string            `json:"version"`
	ID                string            `json:"id"`
	Kind              string            `json:"kind"`
	Protocol          *Protocol         `json:"protocol,omitempty"`
	CampaignID        string            `json:"campaign_id"`
	CampaignStatus    campaign.Status   `json:"campaign_status,omitempty"`
	ManifestSHA256    string            `json:"manifest_sha256"`
	ToolSHA256        string            `json:"tool_sha256"`
	PlanSHA256        string            `json:"plan_sha256"`
	StartedAt         time.Time         `json:"started_at"`
	FinishedAt        time.Time         `json:"finished_at"`
	InputsUnchanged   bool              `json:"inputs_unchanged"`
	Valid             bool              `json:"valid"`
	ExecutionComplete bool              `json:"execution_complete"`
	Counts            Counts            `json:"counts"`
	Metrics           *Metrics          `json:"metrics"`
	Cost              *Cost             `json:"cost"`
	Targets           []Result          `json:"targets"`
	Issues            []Issue           `json:"issues,omitempty"`
	Sources           map[string]Source `json:"sources"`
}
