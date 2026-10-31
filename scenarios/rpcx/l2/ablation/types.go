// Package ablation defines the frozen P1 experiment and its accounting rules.
// Production gate verdicts are observations, never independent truth labels.
package ablation

import (
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/task"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
)

type Group string

const (
	A Group = "A"
	B Group = "B"
	C Group = "C"
)

type Protocol struct {
	SchemaVersion      int                      `json:"schema_version"`
	ID                 string                   `json:"id"`
	Kind               string                   `json:"kind"` // formal or development_calibration
	Model              budget.Profile           `json:"model"`
	Execution          execution.Info           `json:"execution"`
	Resources          execution.ResourcePolicy `json:"resources"`
	Limits             agent.Limits             `json:"agent_limits"`
	AttemptBudget      budget.Limits            `json:"attempt_budget"`
	TotalBudget        budget.Limits            `json:"total_budget"`
	PreparationTimeout time.Duration            `json:"preparation_timeout_ns"`
	GenerationTimeout  time.Duration            `json:"generation_timeout_ns"`
	ValidationTimeout  time.Duration            `json:"validation_timeout_ns"`
	ReferenceTimeout   time.Duration            `json:"reference_timeout_ns"`
	HostTimeout        time.Duration            `json:"host_timeout_ns"`
	Repetitions        int                      `json:"repetitions"`
	Tasks              []Task                   `json:"tasks"`
	Schedule           []Slot                   `json:"schedule"`
	Price              *Price                   `json:"price"`    // nil means money is unknown, never free.
	Analysis           string                   `json:"analysis"` // independent-generations; no paired efficacy claim
	FailurePolicy      string                   `json:"failure_policy"`
}

type Task struct {
	ID                  string      `json:"id"`
	Input               task.Target `json:"input"`
	DiagnosticLimit     int         `json:"generation_diagnostic_limit"`
	ReferenceVersion    string      `json:"reference_version"`
	ReferenceSHA256     string      `json:"reference_sha256"`
	ReferenceToolSHA256 string      `json:"reference_tool_sha256"`
}

type Slot struct {
	ID         string `json:"id"`
	TaskID     string `json:"task_id"`
	Group      Group  `json:"group"`
	Repetition int    `json:"repetition"`
}

// Price is a frozen estimate basis, not a supplier invoice or monetary limiter.
// Token admission is hard; its conservative monetary bound assumes these rates.
type Price struct {
	Basis                 string  `json:"basis,omitempty"` // e.g. peak-rate estimate, not an invoice
	Currency              string  `json:"currency"`
	Source                string  `json:"source"`
	RecordedAt            string  `json:"recorded_at"`
	InputPerMillion       float64 `json:"input_per_million"`
	CachedInputPerMillion float64 `json:"cached_input_per_million"`
	OutputPerMillion      float64 `json:"output_per_million"`
}

// Result is a verified observation for one scheduled slot. Missing observations
// remain explicit not_started rows when summarized; they never reduce N.
type Result struct {
	Slot            Slot              `json:"slot"`
	RunID           string            `json:"run_id,omitempty"`
	State           string            `json:"state"`
	Submitted       bool              `json:"submitted"`
	Released        bool              `json:"released"`
	Verdict         string            `json:"verdict,omitempty"`
	Failure         string            `json:"failure,omitempty"`
	CandidateSHA256 string            `json:"candidate_sha256,omitempty"`
	Truth           evaluation.Status `json:"truth"`
	Delivery        evaluation.Status `json:"delivery"`
	Metrics         Measurements      `json:"metrics"`
}

type Measurements struct {
	KnownTokens     *int64   `json:"known_tokens"`
	ReservedTokens  *int64   `json:"reserved_tokens"`
	UnknownTokens   *int64   `json:"unknown_tokens"`
	ModelCalls      *int64   `json:"model_calls"`
	ToolCalls       *int64   `json:"tool_calls"`
	Steps           *int64   `json:"steps"`
	DurationSeconds *float64 `json:"duration_seconds"`
	Amount          *float64 `json:"amount"`
}

type Ratio struct {
	Numerator   int      `json:"numerator"`
	Denominator int      `json:"denominator"`
	Value       *float64 `json:"value"`
}

type Distribution struct {
	Observed int      `json:"observed"`
	Missing  int      `json:"missing"`
	Mean     *float64 `json:"mean"`
	Min      *float64 `json:"min"`
	Max      *float64 `json:"max"`
}

type Counts struct {
	Scheduled           int            `json:"scheduled"`
	NotStarted          int            `json:"not_started"`
	Submitted           int            `json:"submitted"`
	Released            int            `json:"released"`
	TruthPass           int            `json:"truth_pass"`
	TruthFail           int            `json:"truth_fail"`
	TruthUnknown        int            `json:"truth_unknown"`
	ReleasedUnknown     int            `json:"released_unknown"`
	Success             int            `json:"success"`
	WrongRelease        int            `json:"wrong_release"`
	FalseRejection      int            `json:"false_rejection"`
	FullDeliverySuccess int            `json:"full_delivery_success"`
	BlockedReasons      map[string]int `json:"correct_candidate_blocked_reasons"`
}

type Aggregate struct {
	Counts                Counts                  `json:"counts"`
	AutomaticSuccess      Ratio                   `json:"automatic_success"`
	AutomaticSuccessUpper Ratio                   `json:"automatic_success_unknown_upper"`
	WrongRelease          Ratio                   `json:"wrong_release"`
	WrongReleaseUpper     Ratio                   `json:"wrong_release_unknown_upper"`
	FalseRejection        Ratio                   `json:"false_rejection"`
	Measurements          map[string]Distribution `json:"measurements"`
}

type Summary struct {
	ProtocolSHA256    string                         `json:"protocol_sha256,omitempty"`
	Price             *Price                         `json:"price"`
	ProtocolID        string                         `json:"protocol_id"`
	Kind              string                         `json:"kind"`
	AllSlotsObserved  bool                           `json:"all_slots_observed"`
	ExecutionComplete bool                           `json:"execution_complete"`
	Gaps              []string                       `json:"gaps"`
	Results           []Result                       `json:"results"`
	Groups            map[Group]Aggregate            `json:"groups"`
	Tasks             map[string]map[Group]Aggregate `json:"tasks"`
}
