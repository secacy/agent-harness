// Package budget 管理模型调用的两级额度、前置预留和持久化结算。
package budget

import (
	"errors"
	"fmt"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
)

var (
	ErrHalted      = errors.New("budget admission halted")
	ErrIntegrity   = errors.New("budget integrity violation")
	ErrPersistence = errors.New("persist budget ledger")
)

type Limits struct {
	MaxCalls  int64 `json:"max_calls"`
	MaxTokens int64 `json:"max_tokens"`
}

// Profile 由可信宿主核实，来源摘要绑定上界依据；本包不认证供应商声明。
type Profile struct {
	Provider        string    `json:"provider"`
	Endpoint        string    `json:"endpoint"`
	Model           string    `json:"model"`
	ContextTokens   int64     `json:"context_tokens"`
	MaxOutputTokens int64     `json:"max_output_tokens"`
	SourceSHA256    string    `json:"source_sha256"`
	VerifiedAt      time.Time `json:"verified_at"`
}

type Config struct {
	Directory  string
	CampaignID string
	Limits     Limits
	Targets    map[string]Limits
	Profile    Profile
}

type Totals struct {
	Calls          int64 `json:"calls"`
	KnownTokens    int64 `json:"known_tokens"`
	ReservedTokens int64 `json:"reserved_tokens"`
	UnknownTokens  int64 `json:"unknown_tokens"`
}

type Account struct {
	Limits Limits   `json:"limits"`
	RunID  string   `json:"run_id,omitempty"`
	RunIDs []string `json:"run_ids,omitempty"`
	Totals Totals   `json:"totals"`
}

type CallState string

const (
	StateReserved CallState = "reserved"
	StateSettled  CallState = "settled"
	StateUnknown  CallState = "unknown"
	StateInvalid  CallState = "invalid"
)

type Call struct {
	ID             string       `json:"id"`
	TargetID       string       `json:"target_id"`
	RunID          string       `json:"run_id"`
	InputSHA256    string       `json:"input_sha256"`
	ReservedTokens int64        `json:"reserved_tokens"`
	State          CallState    `json:"state"`
	Usage          *agent.Usage `json:"usage,omitempty"`
	Outcome        string       `json:"outcome,omitempty"`
	RecoveryID     string       `json:"recovery_id,omitempty"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
}

type Rejection struct {
	TargetID    string    `json:"target_id"`
	RunID       string    `json:"run_id"`
	InputSHA256 string    `json:"input_sha256"`
	Scope       string    `json:"scope"`
	Resource    string    `json:"resource"`
	At          time.Time `json:"at"`
}

type Snapshot struct {
	SchemaVersion    int                `json:"schema_version"`
	CampaignID       string             `json:"campaign_id"`
	Revision         int64              `json:"revision"`
	Profile          Profile            `json:"profile"`
	Limits           Limits             `json:"limits"`
	Totals           Totals             `json:"totals"`
	Targets          map[string]Account `json:"targets"`
	Calls            []Call             `json:"calls"`
	Rejections       []Rejection        `json:"rejections"`
	HaltReason       string             `json:"halt_reason,omitempty"`
	RecoveryIDs      []string           `json:"recovery_ids,omitempty"`
	RecoveryRevision int64              `json:"recovery_revision,omitempty"`
}

// Denied 表示调用尚未准入，原因与供应商执行错误分开记录。
type Denied struct{ Scope, Resource string }

func (e *Denied) Error() string { return fmt.Sprintf("%s %s budget exhausted", e.Scope, e.Resource) }
func (e *Denied) Unwrap() error { return agent.ErrModelBudget }

// Reference 供运行报告定位共享账本，不复制会过期的余额。
type Reference struct {
	CampaignID    string `json:"campaign_id"`
	TargetID      string `json:"target_id"`
	RunID         string `json:"run_id"`
	LedgerPath    string `json:"ledger_path"`
	ProfileSHA256 string `json:"profile_sha256"`
}

// Target 由宿主配置，Runner 创建 Run 后才能绑定模型。
type Target struct {
	Ledger *Ledger
	ID     string
}
