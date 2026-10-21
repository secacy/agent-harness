package campaign

import (
	"time"

	"github.com/secacy/agent-harness/internal/budget"
)

// RecoveryPin 绑定原始控制及 Run 材料；目录摘要沿用审计的成员清单格式。
type RecoveryPin struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256,omitempty"`
}

type HistoricalAttempt struct {
	RunID          string   `json:"run_id,omitempty"`
	Directory      string   `json:"directory,omitempty"`
	ObservedStatus string   `json:"observed_status,omitempty"`
	Resolution     string   `json:"resolution"`
	RecordSHA256   string   `json:"record_sha256,omitempty"`
	Result         *Attempt `json:"result,omitempty"`
}

type RecoveredTarget struct {
	ID         string              `json:"id"`
	Resolution string              `json:"resolution"`
	Attempts   []HistoricalAttempt `json:"attempts"`
	Current    *Attempt            `json:"current,omitempty"`
}

type RecoveryState struct {
	Budget  budget.Snapshot   `json:"budget"`
	Targets []RecoveredTarget `json:"targets"`
}

// RecoveryCheckpoint 的 Before 保留原文字节，After 仅为待激活状态。
// 它不修改 Campaign v2 的单 Run 契约，也不能单独作为继续生成的凭证。
type RecoveryCheckpoint struct {
	SchemaVersion int               `json:"schema_version"`
	ID            string            `json:"id"`
	CampaignID    string            `json:"campaign_id"`
	PlanSHA256    string            `json:"plan_sha256"`
	CreatedAt     time.Time         `json:"created_at"`
	Sources       []RecoveryPin     `json:"sources"`
	Before        map[string][]byte `json:"before"`
	After         RecoveryState     `json:"after"`
}

type RecoveryHead struct {
	SchemaVersion int      `json:"schema_version"`
	CampaignID    string   `json:"campaign_id"`
	PlanSHA256    string   `json:"plan_sha256"`
	Checkpoint    Artifact `json:"checkpoint"`
}
