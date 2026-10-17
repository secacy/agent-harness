package agent

import (
	"context"
	"encoding/json"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall // 仅助手消息使用，一条消息可以包含多个工具调用。
	ToolCallID string     // 仅工具结果消息使用，一条结果只对应一个工具调用。
	IsError    bool
}

type ToolSpec struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

type ToolResult struct {
	Content string
	IsError bool // 可修复的工具错误，追加到对话后允许模型继续。
}

type FinishReason string

const (
	FinishStop      FinishReason = "stop"
	FinishToolCalls FinishReason = "tool_calls"
	FinishLength    FinishReason = "length"
)

type Reply struct {
	Content   string
	ToolCalls []ToolCall
	Finish    FinishReason
}

type Model interface {
	// 输入由循环持有，实现只能读取消息、工具及其嵌套切片，
	// 不得修改或在调用结束后继续使用。构造请求时所需的转换由实现负责。
	Complete(ctx context.Context, messages []Message, tools []ToolSpec) (Reply, error)
}

type ToolExecutor interface {
	// 执行器负责解析并校验参数。可修复错误通过工具结果反馈，
	// 执行基础设施错误通过错误返回值上报。实现必须响应上下文取消。
	Execute(ctx context.Context, call ToolCall) (ToolResult, error)
}

type Limits struct {
	MaxModelCalls int
	MaxToolCalls  int
}

type ExitReason string

const (
	ExitCompleted        ExitReason = "completed"
	ExitLimitReached     ExitReason = "limit_reached"
	ExitCancelled        ExitReason = "cancelled"
	ExitDeadlineExceeded ExitReason = "deadline_exceeded"
	ExitFailed           ExitReason = "failed"
)

type Result struct {
	Reason     ExitReason
	Messages   []Message
	ModelCalls int
	ToolCalls  int // 工具尝试数，包括循环直接拒绝的未知工具调用。
}
