// Package testintegrity 比较冻结夹具的测试结构，提供原断言保留与缺口证据。
package testintegrity

type Binding struct {
	SHA256 string            `json:"sha256"`
	Files  map[string]string `json:"files"`
}

// Report 的 preserved 仅指结构保留，不表示测试已执行或业务已经正确。
type Report struct {
	SchemaVersion   int           `json:"schema_version"`
	VerifierVersion string        `json:"verifier_version"`
	Status          string        `json:"status"`
	SpecSHA256      string        `json:"spec_sha256"`
	MaterialSHA256  string        `json:"material_sha256"`
	GoVersion       string        `json:"go_version"`
	Baseline        Binding       `json:"baseline_tests"`
	Expected        Binding       `json:"adapted_tests"`
	Candidate       Binding       `json:"candidate_tests"`
	Declarations    []Declaration `json:"declarations"`
	AddedTests      []Location    `json:"added_tests"`
	Findings        []Finding     `json:"findings"`
}

type Declaration struct {
	ID             string `json:"id"` // 相对包目录＋接收者类型摘要（方法）＋名称；模块身份由外层确认。
	Kind           string `json:"kind"`
	ExpectedSHA256 string `json:"expected_sha256"`
	ActualSHA256   string `json:"actual_sha256,omitempty"`
	Status         string `json:"status"`
}

type Location struct {
	Tree string `json:"tree"`
	File string `json:"file"`
	Line int    `json:"line"`
	Name string `json:"name,omitempty"`
}

type Finding struct {
	Status    string     `json:"status"`
	Code      string     `json:"code"`
	Detail    string     `json:"detail"`
	RuleIDs   []string   `json:"rule_ids"`
	Locations []Location `json:"locations"`
}
