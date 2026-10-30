package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/secacy/agent-harness/internal/trace"
)

var (
	ErrInvalidInput    = errors.New("invalid agent input")
	ErrProtocol        = errors.New("invalid model reply")
	ErrOutputTruncated = errors.New("model output truncated")
)

// 临近上限时提示收尾，硬预算仍由循环统一检查。
const wrapUpModelCalls = 3

// Loop 管理一次运行的对话与调用预算。
// 模型与执行器必须响应上下文取消；循环同步等待调用结束，不遗留后台任务。
type Loop struct {
	replayBoundary   func(string) error
	replayUnknown    func(ToolCall, ToolResult) error
	ModelName        string
	CompletionPolicy CompletionPolicy
	Model            Model
	Executor         ToolExecutor
	Tools            []ToolSpec
	Limits           Limits
}

// Run 在正常完成或预算耗尽时返回空错误，调用方必须检查退出原因。
// 正常完成仅表示模型提交了候选，不代表通过独立验证；失败时保留已有对话。
// 初始历史中的工具调用必须已有对应结果，当前不支持续跑中断的批次。
// 调用计数仅覆盖本次运行，不包含初始历史。
// 每次请求前追加宿主预算通知，剩余模型调用次数包含即将发起的本次调用。
func (l Loop) Run(ctx context.Context, initial []Message) (result Result, err error) {
	result = Result{Messages: cloneMessages(initial)}
	ctx, loopSpan, traceErr := trace.Start(ctx, "agent", LoopInput{Initial: initial, Tools: l.Tools, Limits: l.Limits, CompletionPolicy: l.CompletionPolicy, ModelName: l.ModelName})
	if traceErr != nil {
		return stopWithError(ctx, result, traceErr)
	}
	var stepSpan *trace.Span
	defer func() {
		if closeErr := stepSpan.End(LoopOutput{Result: result, Failure: failure(err), ContextError: contextFailure(ctx)}, modelTraceError(err)); closeErr != nil {
			result, err = stopWithError(ctx, result, errors.Join(err, closeErr))
		}
		if closeErr := loopSpan.End(LoopOutput{Result: result, Failure: failure(err), ContextError: contextFailure(ctx)}, modelTraceError(err)); closeErr != nil {
			result, err = stopWithError(ctx, result, errors.Join(err, closeErr))
		}
	}()
	if err := l.checkContext(ctx, "initial"); err != nil {
		return stopWithError(ctx, result, err)
	}
	tools := cloneTools(l.Tools)
	if l.Model == nil || l.Executor == nil || l.Limits.MaxModelCalls <= 0 || l.Limits.MaxToolCalls <= 0 {
		return stopWithError(ctx, result, fmt.Errorf("%w: model, executor and positive limits are required", ErrInvalidInput))
	}
	if err := l.CompletionPolicy.Validate(); err != nil {
		return stopWithError(ctx, result, err)
	}
	allowed, err := validateTools(tools)
	if err != nil {
		return stopWithError(ctx, result, err)
	}
	seenIDs, err := validateHistory(result.Messages)
	if err != nil {
		return stopWithError(ctx, result, err)
	}
	loopContext := ctx
	for {
		if stepSpan != nil {
			if e := stepSpan.End(map[string]any{"model_calls": result.ModelCalls, "tool_calls": result.ToolCalls}, nil); e != nil {
				return stopWithError(ctx, result, e)
			}
			stepSpan = nil
		}
		ctx = loopContext
		if err := l.checkContext(ctx, "before_step"); err != nil {
			return stopWithError(ctx, result, err)
		}
		if result.ModelCalls >= l.Limits.MaxModelCalls {
			result.Reason = ExitLimitReached
			return result, nil
		}
		var e error
		ctx, stepSpan, e = trace.Start(ctx, "step", map[string]int{"index": result.ModelCalls + 1})
		if e != nil {
			return stopWithError(ctx, result, e)
		}
		modelRemaining := l.Limits.MaxModelCalls - result.ModelCalls
		toolRemaining := l.Limits.MaxToolCalls - result.ToolCalls
		result.Messages = append(result.Messages, completionBudgetMessage(l.CompletionPolicy, modelRemaining, toolRemaining))
		result.ModelCalls++
		requestTools := tools
		finalOnly := l.CompletionPolicy.finalOnly(modelRemaining, toolRemaining)
		if finalOnly {
			requestTools = nil
			result.FinalResponseAttempt = &FinalResponseAttempt{ModelCall: result.ModelCalls, MessageCount: len(result.Messages)}
		}
		modelCtx, modelSpan, e := trace.Start(ctx, "llm_call", ModelInput{Messages: result.Messages, Tools: requestTools, ModelName: l.ModelName})
		if e != nil {
			return stopWithError(ctx, result, e)
		}
		reply, err := l.Model.Complete(modelCtx, result.Messages, requestTools)
		err = errors.Join(err, modelSpan.End(ModelOutput{Reply: reply, Failure: failure(err), ContextError: contextFailure(ctx)}, modelTraceError(err)))
		result.Usage = append(result.Usage, cloneUsage(reply.Usage))
		if err != nil {
			return stopWithError(ctx, result, fmt.Errorf("model call %d: %w", result.ModelCalls, err))
		}
		// 先保留完整回复，使异常退出时仍有可供审查的记录。
		message := Message{
			Role:      RoleAssistant,
			Content:   reply.Content,
			ToolCalls: append([]ToolCall(nil), reply.ToolCalls...),
		}
		result.Messages = append(result.Messages, message)
		if err := l.checkContext(ctx, "after_model"); err != nil {
			return stopWithError(ctx, result, err)
		}
		if finalOnly && len(message.ToolCalls) != 0 {
			return stopWithError(ctx, result, fmt.Errorf("%w: tools are disabled for the final response", ErrProtocol))
		}
		switch reply.Finish {
		case FinishStop:
			if len(message.ToolCalls) != 0 {
				return stopWithError(ctx, result, fmt.Errorf("%w: stop with tool calls", ErrProtocol))
			}
			result.Reason = ExitCompleted
			return result, nil
		case FinishLength:
			return stopWithError(ctx, result, ErrOutputTruncated)
		case FinishToolCalls:
			if len(message.ToolCalls) == 0 {
				return stopWithError(ctx, result, fmt.Errorf("%w: tool_calls without calls", ErrProtocol))
			}
		default:
			return stopWithError(ctx, result, fmt.Errorf("%w: unsupported finish reason %q", ErrProtocol, reply.Finish))
		}
		if err := validateCallIDs(message.ToolCalls, seenIDs); err != nil {
			return stopWithError(ctx, result, fmt.Errorf("%w: %v", ErrProtocol, err))
		}
		// 执行前检查整批预算，避免部分写入后才发现预算不足。
		if len(message.ToolCalls) > l.Limits.MaxToolCalls-result.ToolCalls {
			result.Reason = ExitLimitReached
			return result, nil
		}
		for _, call := range message.ToolCalls {
			if err := l.checkContext(ctx, "before_tool"); err != nil {
				return stopWithError(ctx, result, err)
			}
			result.ToolCalls++
			toolCtx, toolSpan, e := trace.Start(ctx, "tool_call", ToolInput{Call: call, Executed: allowed[call.Name]})
			if e != nil {
				return stopWithError(ctx, result, e)
			}
			var output ToolResult
			var execErr error
			if !allowed[call.Name] {
				output = ToolResult{Content: fmt.Sprintf("unknown tool %q; use an advertised tool", call.Name), IsError: true}
				if l.replayUnknown != nil {
					execErr = l.replayUnknown(call, output)
				}
			} else {
				output, execErr = l.Executor.Execute(toolCtx, call)
			}
			execErr = errors.Join(execErr, toolSpan.End(ToolOutput{Result: output, Failure: failure(execErr), ContextError: contextFailure(ctx)}, trace.Categorize(execErr, "tool")))
			if execErr != nil {
				output.IsError = true
				if output.Content != "" {
					output.Content += "\n"
				}
				output.Content += "tool execution failed: " + execErr.Error()
			}
			result.Messages = append(result.Messages, Message{
				Role:       RoleTool,
				Content:    output.Content,
				ToolCallID: call.ID,
				IsError:    output.IsError,
			})
			if execErr != nil {
				return stopWithError(ctx, result, fmt.Errorf("tool %s (%s): %w", call.Name, call.ID, toolTraceError(execErr)))
			}
			if err := l.checkContext(ctx, "after_tool"); err != nil {
				return stopWithError(ctx, result, err)
			}
		}
	}
}

func budgetMessage(modelCalls, toolCalls int) Message {
	content := fmt.Sprintf("Harness budget (current run):\nModel calls remaining: %d (including this call).\nTool calls remaining: %d.", modelCalls, toolCalls)
	switch {
	case modelCalls == 1:
		content += "\nThis is your last model call; there will be no follow-up after tool execution. Prefer a final response describing completed work, checks and unresolved issues."
	case modelCalls <= wrapUpModelCalls:
		content += "\nBudget is low. Prioritize necessary fixes, verification and a final response; avoid expanding scope."
	}
	if toolCalls == 0 {
		content += "\nNo tool calls remain. Give a final response describing completed work and any unverified or unresolved issues."
	}
	return Message{Role: RoleUser, Content: content}
}

func stopWithError(ctx context.Context, result Result, err error) (Result, error) {
	if hook, ok := ctx.Value(replayStopKey{}).(func(string) error); ok {
		err = errors.Join(err, hook("error_stop"))
	}
	_, span, traceErr := trace.Start(ctx, "agent_boundary", RecordedBoundary{Name: "error_stop"})
	observed := ctx.Err()
	traceErr = errors.Join(traceErr, span.End(RecordedBoundary{Name: "error_stop", ContextError: contextErrorKind(observed)}, observed))
	err = errors.Join(err, traceErr)
	cause := err
	if observed != nil {
		cause = observed
		err = errors.Join(cause, err)
	}
	result.Reason = ExitFailed
	switch {
	case errors.Is(cause, ErrModelBudget):
		result.Reason = ExitLimitReached
	case errors.Is(cause, context.DeadlineExceeded):
		result.Reason = ExitDeadlineExceeded
	case errors.Is(cause, context.Canceled):
		result.Reason = ExitCancelled
	}
	return result, err
}

func validateTools(tools []ToolSpec) (map[string]bool, error) {
	allowed := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if strings.TrimSpace(tool.Name) == "" || allowed[tool.Name] {
			return nil, fmt.Errorf("%w: empty or duplicate tool name %q", ErrInvalidInput, tool.Name)
		}
		var schema map[string]json.RawMessage
		if err := json.Unmarshal(tool.Parameters, &schema); err != nil || schema == nil {
			return nil, fmt.Errorf("%w: tool %s needs a JSON object schema", ErrInvalidInput, tool.Name)
		}
		allowed[tool.Name] = true
	}
	return allowed, nil
}

func validateCallIDs(calls []ToolCall, seen map[string]bool) error {
	batch := make(map[string]bool, len(calls))
	for _, call := range calls {
		if strings.TrimSpace(call.ID) == "" || seen[call.ID] || batch[call.ID] {
			return fmt.Errorf("empty or repeated tool call ID %q", call.ID)
		}
		batch[call.ID] = true
	}
	for id := range batch {
		seen[id] = true
	}
	return nil
}

func validateHistory(messages []Message) (map[string]bool, error) {
	if len(messages) == 0 {
		return nil, fmt.Errorf("%w: initial messages are required", ErrInvalidInput)
	}
	seen := map[string]bool{}
	pending := map[string]bool{}
	for i, message := range messages {
		invalid := func(reason string) (map[string]bool, error) {
			return nil, fmt.Errorf("%w: message %d: %s", ErrInvalidInput, i, reason)
		}
		if message.Role != RoleTool && (message.ToolCallID != "" || message.IsError) {
			return invalid("tool result fields on a non-tool message")
		}
		if message.Role != RoleAssistant && len(message.ToolCalls) != 0 {
			return invalid("tool calls on a non-assistant message")
		}
		if len(pending) > 0 && message.Role != RoleTool {
			return invalid("unanswered tool calls")
		}
		switch message.Role {
		case RoleSystem, RoleUser:
		case RoleAssistant:
			if err := validateCallIDs(message.ToolCalls, seen); err != nil {
				return invalid(err.Error())
			}
			for _, call := range message.ToolCalls {
				pending[call.ID] = true
			}
		case RoleTool:
			if !pending[message.ToolCallID] {
				return invalid("tool result has no pending call")
			}
			delete(pending, message.ToolCallID)
		default:
			return invalid("unsupported role")
		}
	}
	if len(pending) != 0 {
		return nil, fmt.Errorf("%w: initial history has unanswered tool calls", ErrInvalidInput)
	}
	return seen, nil
}

func cloneMessages(messages []Message) []Message {
	cloned := append([]Message(nil), messages...)
	for i := range cloned {
		cloned[i].ToolCalls = append([]ToolCall(nil), messages[i].ToolCalls...)
	}
	return cloned
}

func cloneTools(tools []ToolSpec) []ToolSpec {
	cloned := append([]ToolSpec(nil), tools...)
	for i := range cloned {
		cloned[i].Parameters = append(json.RawMessage(nil), tools[i].Parameters...)
	}
	return cloned
}

func cloneUsage(usage *Usage) *Usage {
	if usage == nil {
		return nil
	}
	cloned := *usage
	if usage.CacheHitTokens != nil {
		value := *usage.CacheHitTokens
		cloned.CacheHitTokens = &value
	}
	if usage.CacheMissTokens != nil {
		value := *usage.CacheMissTokens
		cloned.CacheMissTokens = &value
	}
	return &cloned
}
