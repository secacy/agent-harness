package agent

import (
	"fmt"
	"reflect"
)

// CompletionPolicy 决定预算边界的交接方式；空值保留旧行为。
type CompletionPolicy string

const FinalResponseV1 CompletionPolicy = "final-response-v1"

func (p CompletionPolicy) Validate() error {
	if p != "" && p != FinalResponseV1 {
		return fmt.Errorf("%w: unsupported completion policy %q", ErrInvalidInput, p)
	}
	return nil
}

func (p CompletionPolicy) finalOnly(modelCalls, toolCalls int) bool {
	return p == FinalResponseV1 && (modelCalls == 1 || toolCalls == 0)
}

func completionBudgetMessage(p CompletionPolicy, modelCalls, toolCalls int) Message {
	if p == "" {
		return budgetMessage(modelCalls, toolCalls)
	}
	content := fmt.Sprintf("Harness budget (current run):\nModel calls remaining: %d (including this call).\nTool calls remaining: %d.", modelCalls, toolCalls)
	turns := modelCalls - 1
	if toolCalls == 0 {
		turns = 0
	}
	content += fmt.Sprintf("\nCompletion policy: %s. The last model call is reserved for a final response. Tool-enabled model calls remaining: %d.", p, turns)
	if p.finalOnly(modelCalls, toolCalls) {
		content += "\nTools are disabled for this call. Give a final response describing completed changes, actual checks and unresolved or unverified issues. Your final response submits the current candidate for independent verification; unresolved issues do not prevent submission."
	} else if modelCalls <= wrapUpModelCalls {
		content += "\nBudget is low. Prioritize necessary fixes, verification and a final response; avoid expanding scope."
	}
	return Message{Role: RoleUser, Content: content}
}

// ValidateCompletionResult 核对新策略的历史和本地计数，不证明供应商收到请求或候选正确。
// 旧策略不追溯添加历史要求，但不能携带新策略的末轮标记。
func ValidateCompletionResult(p CompletionPolicy, limits Limits, result Result) error {
	if err := p.Validate(); err != nil {
		return err
	}
	invalid := func(reason string) error { return fmt.Errorf("invalid completion record: %s", reason) }
	if p == "" {
		if result.FinalResponseAttempt != nil {
			return invalid("attempt without completion policy")
		}
		return nil
	}
	if limits.MaxModelCalls < 1 || limits.MaxToolCalls < 1 || result.ModelCalls < 0 || result.ModelCalls > limits.MaxModelCalls || result.ToolCalls < 0 || result.ToolCalls > limits.MaxToolCalls || len(result.Usage) != result.ModelCalls {
		return invalid("limits or call counts do not match")
	}
	if result.ModelCalls == 0 {
		if result.FinalResponseAttempt != nil || result.ToolCalls != 0 || result.Reason == ExitCompleted {
			return invalid("execution claimed without model attempts")
		}
		return nil
	}
	// 初始上下文可能包含旧预算通知；本次完整预算通知在它们之后。
	first := completionBudgetMessage(p, limits.MaxModelCalls, limits.MaxToolCalls)
	i := -1
	for index, message := range result.Messages {
		if reflect.DeepEqual(message, first) {
			i = index
		}
	}
	if i < 0 {
		return invalid("missing initial budget notice")
	}
	if _, err := validateHistory(result.Messages[:i]); err != nil {
		return invalid("invalid initial history")
	}
	executed := 0
	var expected *FinalResponseAttempt
	for call := 1; call <= result.ModelCalls; call++ {
		remaining, tools := limits.MaxModelCalls-call+1, limits.MaxToolCalls-executed
		if i >= len(result.Messages) || !reflect.DeepEqual(result.Messages[i], completionBudgetMessage(p, remaining, tools)) {
			return invalid("missing or inconsistent budget notice")
		}
		if _, err := validateHistory(result.Messages[:i+1]); err != nil {
			return invalid("request has unmatched tool history")
		}
		i++
		final := p.finalOnly(remaining, tools)
		if final {
			expected = &FinalResponseAttempt{ModelCall: call, MessageCount: i}
			if call != result.ModelCalls {
				return invalid("model request after final-response attempt")
			}
		}
		if i == len(result.Messages) && call == result.ModelCalls {
			break // 模型错误或准入拒绝可以没有助手回复。
		}
		if i >= len(result.Messages) || result.Messages[i].Role != RoleAssistant {
			return invalid("missing assistant reply")
		}
		reply := result.Messages[i]
		i++
		count := 0
		for i < len(result.Messages) && result.Messages[i].Role == RoleTool {
			if final || count >= len(reply.ToolCalls) || result.Messages[i].ToolCallID != reply.ToolCalls[count].ID {
				return invalid("unexpected tool execution")
			}
			count++
			executed++
			i++
		}
	}
	if i != len(result.Messages) || executed != result.ToolCalls || !reflect.DeepEqual(expected, result.FinalResponseAttempt) {
		return invalid("attempt marker or execution counts do not match history")
	}
	if result.Reason == ExitCompleted {
		last := result.Messages[len(result.Messages)-1]
		if last.Role != RoleAssistant || len(last.ToolCalls) != 0 {
			return invalid("completed result lacks a final response")
		}
	}
	return nil
}
