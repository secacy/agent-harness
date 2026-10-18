// Package quality 依据宿主检查要求和绑定证据聚合候选质量判定。
package quality

const (
	StatusPass     = "pass"
	StatusFail     = "fail"
	StatusUnknown  = "unknown"
	StatusBlocked  = "blocked"
	StatusSkipped  = "skipped"
	StatusNotRun   = "not_run"
	Applicable     = "applicable"
	NotApplicable  = "not_applicable"
	ReadyForReview = "ReadyForReview"
	NeedsAttention = "NeedsAttention"
	Rejected       = "Rejected"
)

type Binding struct {
	SpecSHA256      string `json:"spec_sha256"`
	BaselineSHA256  string `json:"baseline_sha256"`
	CandidateSHA256 string `json:"candidate_sha256"`
}

type Location struct {
	Tree string `json:"tree"` // baseline 或 candidate，删除位置可指向基线。
	Path string `json:"path"`
	Line int    `json:"line,omitempty"` // 0 表示文件级结果。
}

type Finding struct {
	Code        string     `json:"code"`
	Detail      string     `json:"detail"`
	RuleIDs     []string   `json:"rule_ids"`
	Locations   []Location `json:"locations,omitempty"`
	EvidenceIDs []string   `json:"evidence_ids,omitempty"`
}

type Check struct {
	Status        string    `json:"status"`
	Detail        string    `json:"detail"`
	Applicability string    `json:"applicability"`
	RuleIDs       []string  `json:"rule_ids"`
	Findings      []Finding `json:"findings"`
	EvidenceIDs   []string  `json:"evidence_ids"`
}

type Evidence struct {
	ID      string  `json:"id"`
	Kind    string  `json:"kind"`
	Path    string  `json:"path"`
	SHA256  string  `json:"sha256"`
	Binding Binding `json:"binding"`
}

// Requirement 由可信宿主定义；候选和用例期望不得改变必需集合。
type Requirement struct {
	ID                 string
	RuleIDs            []string
	AllowNotApplicable bool
}

type Policy struct {
	SpecSHA256   string
	Requirements []Requirement
}

type Gap struct {
	CheckID string `json:"check_id"`
	Code    string `json:"code"`
	Detail  string `json:"detail"`
}

type Decision struct {
	Verdict string `json:"verdict"`
	Gaps    []Gap  `json:"gaps"`
}
