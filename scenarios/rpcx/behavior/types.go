// Package behavior 在候选生产副本中执行可信业务测试和独立 T3 探针。
package behavior

import (
	"github.com/secacy/agent-harness/internal/execution"
)

const Version = "rpcx-trusted-behavior-v1"

type Request struct {
	BaselineDir  string
	CandidateDir string
	SDKDir       string
	EvidenceDir  string // 必须为空，且与三个输入目录互不包含。
	Backend      execution.Executor
}

type Binding struct {
	SHA256 string            `json:"sha256"`
	Files  map[string]string `json:"files"`
}

type Observation struct {
	ID      string   `json:"id"` // 完整包路径＋测试名。
	Status  string   `json:"status"`
	RuleIDs []string `json:"rule_ids"`
}

// Report 的 pass 只覆盖列出的配置和测试，不能独自支持最终质量放行。
type Report struct {
	SchemaVersion int                `json:"schema_version"`
	Version       string             `json:"version"`
	SpecSHA256    string             `json:"spec_sha256"`
	Material      Binding            `json:"material"`
	Inputs        map[string]Binding `json:"inputs"`
	Production    Binding            `json:"candidate_production"`
	Assembly      Binding            `json:"assembly"`
	Backend       execution.Info     `json:"backend"`
	Status        string             `json:"status"`
	Tests         []Observation      `json:"tests"`
	Gaps          []string           `json:"gaps"`
}
