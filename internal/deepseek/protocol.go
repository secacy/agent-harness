package deepseek

import (
	"encoding/json"
	"fmt"

	"github.com/secacy/agent-harness/internal/agent"
)

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

type chatTool struct {
	Type     string           `json:"type"`
	Function chatFunctionSpec `json:"function"`
}

type chatFunctionSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type chatRequest struct {
	Model     string         `json:"model"`
	Messages  []chatMessage  `json:"messages"`
	Tools     []chatTool     `json:"tools,omitempty"`
	MaxTokens int            `json:"max_tokens"`
	Stream    bool           `json:"stream"`
	Thinking  thinkingConfig `json:"thinking"`
}

type thinkingConfig struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
	Usage   *chatUsage   `json:"usage"`
}

type chatChoice struct {
	Message      chatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type chatUsage struct {
	incomplete            bool
	PromptTokens          int64  `json:"prompt_tokens"`
	CompletionTokens      int64  `json:"completion_tokens"`
	TotalTokens           int64  `json:"total_tokens"`
	PromptCacheHitTokens  *int64 `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens *int64 `json:"prompt_cache_miss_tokens"`
}

// UnmarshalJSON 避免将缺失的必需用量字段当作零消耗。
func (u *chatUsage) UnmarshalJSON(data []byte) error {
	type wireUsage struct {
		Prompt     *int64 `json:"prompt_tokens"`
		Completion *int64 `json:"completion_tokens"`
		Total      *int64 `json:"total_tokens"`
		Hit        *int64 `json:"prompt_cache_hit_tokens"`
		Miss       *int64 `json:"prompt_cache_miss_tokens"`
	}
	var wire wireUsage
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	u.incomplete = wire.Prompt == nil || wire.Completion == nil || wire.Total == nil
	if !u.incomplete {
		u.PromptTokens = *wire.Prompt
		u.CompletionTokens = *wire.Completion
		u.TotalTokens = *wire.Total
	}
	u.PromptCacheHitTokens = wire.Hit
	u.PromptCacheMissTokens = wire.Miss
	return nil
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

// encodeTools 包装工具定义；参数规则仅供读取，编码时保持为 JSON 对象。
func encodeTools(tools []agent.ToolSpec) []chatTool {
	encoded := make([]chatTool, len(tools))
	for i, tool := range tools {
		encoded[i] = chatTool{
			Type: toolTypeFunction,
			Function: chatFunctionSpec{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		}
	}
	return encoded
}

func decodeReply(response chatResponse) (agent.Reply, error) {
	var reply agent.Reply
	if usage := response.Usage; usage != nil && !usage.incomplete {
		reply.Usage = &agent.Usage{
			InputTokens:     usage.PromptTokens,
			OutputTokens:    usage.CompletionTokens,
			TotalTokens:     usage.TotalTokens,
			CacheHitTokens:  usage.PromptCacheHitTokens,
			CacheMissTokens: usage.PromptCacheMissTokens,
		}
	}
	if len(response.Choices) != 1 {
		return reply, fmt.Errorf("expected one completion choice, got %d", len(response.Choices))
	}
	choice := response.Choices[0]
	if choice.Message.Role != string(agent.RoleAssistant) {
		return reply, fmt.Errorf("expected assistant role, got %q", choice.Message.Role)
	}
	reply.Content = choice.Message.Content
	switch agent.FinishReason(choice.FinishReason) {
	case agent.FinishStop, agent.FinishToolCalls, agent.FinishLength:
		reply.Finish = agent.FinishReason(choice.FinishReason)
	default:
		return reply, fmt.Errorf("unsupported finish reason %q", choice.FinishReason)
	}
	for _, call := range choice.Message.ToolCalls {
		// 截断回复可能含不完整调用，保留原文交给循环按截断退出，不执行工具。
		if reply.Finish != agent.FinishLength && call.Type != toolTypeFunction {
			return reply, fmt.Errorf("unsupported tool call type %q", call.Type)
		}
		reply.ToolCalls = append(reply.ToolCalls, agent.ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		})
	}
	return reply, nil
}
