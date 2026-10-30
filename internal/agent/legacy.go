package agent

import (
	"fmt"
	"reflect"
)

// LegacyRecording reconstructs a replay recording from a completed, persisted
// conversation. Finish reasons were not stored by the legacy recorder: they are
// derived from the successful Loop protocol, never presented as raw responses.
// The caller must authenticate request prefixes, tools and usage against the
// original ledger. Unknown usage stays unknown; failed/incomplete runs cannot
// establish which external operation failed and are deliberately rejected.
func LegacyRecording(result Result, tools []ToolSpec, limits Limits, policy CompletionPolicy) (Recording, error) {
	invalid := func(reason string) (Recording, error) {
		return Recording{}, fmt.Errorf("invalid legacy recording: %s", reason)
	}
	if result.Reason != ExitCompleted {
		return invalid("only completed conversations establish legacy response finish reasons")
	}
	if limits.MaxModelCalls < 1 || limits.MaxToolCalls < 1 || result.ModelCalls < 1 || result.ModelCalls > limits.MaxModelCalls || result.ToolCalls < 0 || result.ToolCalls > limits.MaxToolCalls || len(result.Usage) != result.ModelCalls {
		return invalid("limits, counters or usage entries do not match")
	}
	if err := ValidateCompletionResult(policy, limits, result); err != nil {
		return invalid(err.Error())
	}
	allowed, err := validateTools(tools)
	if err != nil {
		return invalid(err.Error())
	}
	if _, err := validateHistory(result.Messages); err != nil {
		return invalid(err.Error())
	}
	// Initial history may itself contain earlier budget notices. The current
	// run's first notice is the last exact occurrence of its initial allowance.
	first := completionBudgetMessage(policy, limits.MaxModelCalls, limits.MaxToolCalls)
	start := -1
	for i, message := range result.Messages {
		if reflect.DeepEqual(message, first) {
			start = i
		}
	}
	if start < 1 {
		return invalid("missing initial history or first budget notice")
	}
	if _, err := validateHistory(result.Messages[:start]); err != nil {
		return invalid(err.Error())
	}
	recording := Recording{Input: LoopInput{
		Initial: cloneMessages(result.Messages[:start]), Tools: cloneTools(tools), Limits: limits, CompletionPolicy: policy,
	}}
	i, executed := start, 0
	for number := 1; number <= result.ModelCalls; number++ {
		modelRemaining, toolRemaining := limits.MaxModelCalls-number+1, limits.MaxToolCalls-executed
		if i >= len(result.Messages) || !reflect.DeepEqual(result.Messages[i], completionBudgetMessage(policy, modelRemaining, toolRemaining)) {
			return invalid("missing or inconsistent budget notice")
		}
		i++
		request := ModelInput{Messages: cloneMessages(result.Messages[:i]), Tools: cloneTools(tools)}
		finalOnly := policy.finalOnly(modelRemaining, toolRemaining)
		if finalOnly {
			request.Tools = nil
		}
		if i >= len(result.Messages) || result.Messages[i].Role != RoleAssistant {
			return invalid("missing assistant response")
		}
		message := result.Messages[i]
		i++
		reply := Reply{Content: message.Content, ToolCalls: append([]ToolCall(nil), message.ToolCalls...), Usage: cloneUsage(result.Usage[number-1])}
		if len(message.ToolCalls) == 0 {
			if number != result.ModelCalls || i != len(result.Messages) {
				return invalid("conversation continues after final response")
			}
			reply.Finish = FinishStop
		} else {
			if finalOnly || number == result.ModelCalls || len(message.ToolCalls) > toolRemaining {
				return invalid("tool batch cannot complete under recorded limits")
			}
			reply.Finish = FinishToolCalls
		}
		recording.Operations = append(recording.Operations, Operation{Model: &RecordedModel{Input: request, Output: ModelOutput{Reply: reply}}})
		for _, call := range message.ToolCalls {
			if i >= len(result.Messages) || result.Messages[i].Role != RoleTool || result.Messages[i].ToolCallID != call.ID {
				return invalid("missing or reordered tool result")
			}
			message := result.Messages[i]
			i++
			executed++
			output := ToolResult{Content: message.Content, IsError: message.IsError}
			if !allowed[call.Name] {
				expected := ToolResult{Content: fmt.Sprintf("unknown tool %q; use an advertised tool", call.Name), IsError: true}
				if output != expected {
					return invalid("unknown tool result differs from Loop rejection")
				}
			}
			recording.Operations = append(recording.Operations, Operation{Tool: &RecordedTool{Input: ToolInput{Call: call, Executed: allowed[call.Name]}, Output: ToolOutput{Result: output}}})
		}
	}
	if i != len(result.Messages) || executed != result.ToolCalls {
		return invalid("trailing messages or tool counter mismatch")
	}
	// Own all nested slices and usage pointers so callers cannot change a
	// recording by subsequently editing their decoded legacy transcript.
	result.Messages = cloneMessages(result.Messages)
	usage := make([]*Usage, len(result.Usage))
	for i := range result.Usage {
		usage[i] = cloneUsage(result.Usage[i])
	}
	result.Usage = usage
	if result.FinalResponseAttempt != nil {
		attempt := *result.FinalResponseAttempt
		result.FinalResponseAttempt = &attempt
	}
	recording.Output = LoopOutput{Result: result}
	return recording, nil
}
