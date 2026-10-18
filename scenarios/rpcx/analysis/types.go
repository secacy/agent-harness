// Package analysis 在执行后端内检查 RPC 边界，保存有限覆盖事实与缺口。
package analysis

import "example.com/agent-harness/internal/execution"

// Request 要求输入只含常规文件，证据目录位于输入外且为空。
// 本地后端仅用于可信夹具；调用方负责提供可信 SDK。
type Request struct {
	ModuleDir   string
	SDKDirs     []string
	EvidenceDir string
	CacheDir    string
	Tags        []string
	Backend     execution.Executor
}

// Report 是分析事实，不代表最终质量判定，也不证明任意控制流。
type Report struct {
	SchemaVersion int           `json:"schema_version"`
	Status        string        `json:"status"`
	LoadExitCode  int           `json:"load_exit_code"`
	InputSHA256   string        `json:"input_sha256"`
	ToolSHA256    string        `json:"tool_sha256"`
	Config        Configuration `json:"config"`
	Packages      []Package     `json:"packages"`
	Calls         []Call        `json:"calls"`
	Gaps          []Gap         `json:"gaps"`
	Diagnostic    string        `json:"diagnostic,omitempty"`
}

type Configuration struct {
	GoVersion string   `json:"go_version"`
	GOOS      string   `json:"goos"`
	GOARCH    string   `json:"goarch"`
	CGO       string   `json:"cgo"`
	Tags      []string `json:"tags"`
}

type Package struct {
	Path    string            `json:"path"`
	Files   map[string]string `json:"files"`
	Ignored []string          `json:"ignored"`
}

type Call struct {
	Package   string `json:"package"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Function  string `json:"function"`
	SDK       string `json:"sdk"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Reason    string `json:"reason"`
	TimeoutNS *int64 `json:"timeout_ns,omitempty"`
}

type Gap struct {
	File   string `json:"file"`
	Line   int    `json:"line,omitempty"`
	Reason string `json:"reason"`
}

type Result struct {
	Report  Report
	Command execution.Result
}
