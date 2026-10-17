package deepseek

import "example.com/agent-harness/internal/agent"

const toolTypeFunction = "function"

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatFunctionCall `json:"function"`
}

type chatFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// encodeMessages 构造独立的协议消息，保留调用顺序、关联标识及参数原文。
func encodeMessages(messages []agent.Message) []chatMessage {
	encoded := make([]chatMessage, len(messages))
	for i, message := range messages {
		encoded[i] = chatMessage{
			Role:       string(message.Role),
			Content:    message.Content,
			ToolCallID: message.ToolCallID,
		}
		if message.Role == agent.RoleTool && message.IsError {
			encoded[i].Content = "Tool error: " + message.Content
		}
		if len(message.ToolCalls) == 0 {
			continue
		}
		encoded[i].ToolCalls = make([]chatToolCall, len(message.ToolCalls))
		for j, call := range message.ToolCalls {
			encoded[i].ToolCalls[j] = chatToolCall{
				ID:   call.ID,
				Type: toolTypeFunction,
				Function: chatFunctionCall{
					Name:      call.Name,
					Arguments: call.Arguments,
				},
			}
		}
	}
	return encoded
}
