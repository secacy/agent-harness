package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

type modelFunc func(context.Context, []Message, []ToolSpec) (Reply, error)

func (f modelFunc) Complete(ctx context.Context, messages []Message, tools []ToolSpec) (Reply, error) {
	return f(ctx, messages, tools)
}

type executorFunc func(context.Context, ToolCall) (ToolResult, error)

func (f executorFunc) Execute(ctx context.Context, call ToolCall) (ToolResult, error) {
	return f(ctx, call)
}

func testTools() []ToolSpec {
	return []ToolSpec{{Name: "read_file", Description: "Read a file", Parameters: json.RawMessage(`{"type":"object"}`)}}
}
func testInitial() []Message { return []Message{{Role: RoleUser, Content: "Migrate the repository"}} }
func calls(ids ...string) Reply {
	reply := Reply{Content: "Inspecting files", Finish: FinishToolCalls}
	for _, id := range ids {
		reply.ToolCalls = append(reply.ToolCalls, ToolCall{ID: id, Name: "read_file", Arguments: `{"path":"client.go"}`})
	}
	return reply
}

func TestLoopPreservesBatchAndFeedsResultsBeforeNextModelCall(t *testing.T) {
	requested := calls("read-client", "read-service")
	executed := []string{}
	modelCalls := 0
	loop := Loop{
		Tools: testTools(), Limits: Limits{MaxModelCalls: 2, MaxToolCalls: 2},
		Model: modelFunc(func(_ context.Context, messages []Message, tools []ToolSpec) (Reply, error) {
			modelCalls++
			if modelCalls == 1 {
				return requested, nil
			}
			if !reflect.DeepEqual(executed, []string{"read-client", "read-service"}) {
				t.Fatalf("model called before entire batch finished: %v", executed)
			}
			if len(messages) != 6 || messages[2].Content != requested.Content || !reflect.DeepEqual(messages[2].ToolCalls, requested.ToolCalls) {
				t.Fatalf("assistant message lost: %+v", messages)
			}
			for i, id := range executed {
				message := messages[i+3]
				if message.Role != RoleTool || message.ToolCallID != id || message.Content != "contents:"+id || message.IsError {
					t.Fatalf("wrong tool result: %+v", message)
				}
			}
			if messages[5].Role != RoleUser || !strings.Contains(messages[5].Content, "Tool calls remaining: 0.") {
				t.Fatalf("budget notice missing after complete batch: %+v", messages[5])
			}
			return Reply{Content: "Candidate ready", Finish: FinishStop}, nil
		}),
		Executor: executorFunc(func(_ context.Context, call ToolCall) (ToolResult, error) {
			executed = append(executed, call.ID)
			return ToolResult{Content: "contents:" + call.ID}, nil
		}),
	}
	result, err := loop.Run(context.Background(), testInitial())
	if err != nil || result.Reason != ExitCompleted || result.ModelCalls != 2 || result.ToolCalls != 2 || len(result.Messages) != 7 {
		t.Fatalf("%+v %v", result, err)
	}
	if _, err := validateHistory(result.Messages); err != nil {
		t.Fatalf("budget notice broke tool pairing: %v", err)
	}
}

func TestLoopFeedsRecoverableErrorsAndBlocksUnadvertisedTools(t *testing.T) {
	step, executions := 0, 0
	loop := Loop{Tools: testTools(), Limits: Limits{MaxModelCalls: 4, MaxToolCalls: 3}}
	loop.Model = modelFunc(func(_ context.Context, messages []Message, _ []ToolSpec) (Reply, error) {
		step++
		if step > 1 {
			last := messages[len(messages)-2]
			if last.Role != RoleTool || last.ToolCallID != fmt.Sprint(step-1) || last.IsError != (step <= 3) {
				t.Fatalf("feedback lost: %+v", last)
			}
		}
		if step == 4 {
			return Reply{Finish: FinishStop}, nil
		}
		reply := calls(fmt.Sprint(step))
		if step == 1 {
			reply.ToolCalls[0].Name = "unadvertised_shell"
		}
		if step == 2 {
			reply.ToolCalls[0].Arguments = "not JSON"
		}
		return reply, nil
	})
	loop.Executor = executorFunc(func(_ context.Context, call ToolCall) (ToolResult, error) {
		executions++
		if call.Name != "read_file" {
			t.Fatal("unknown tool reached executor")
		}
		if !json.Valid([]byte(call.Arguments)) {
			return ToolResult{Content: "arguments must be JSON", IsError: true}, nil
		}
		return ToolResult{Content: "file contents"}, nil
	})
	result, err := loop.Run(context.Background(), testInitial())
	if err != nil || result.Reason != ExitCompleted || result.ToolCalls != 3 || executions != 2 {
		t.Fatalf("%+v %v executions=%d", result, err, executions)
	}
}

func TestLoopBudgets(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		replies               []Reply
		limits                Limits
		reason                ExitReason
		modelCalls, toolCalls int
	}{
		{"finish-on-final-model-call", []Reply{{Finish: FinishStop}}, Limits{1, 1}, ExitCompleted, 1, 0},
		{"finish-after-last-tool", []Reply{calls("1"), {Finish: FinishStop}}, Limits{2, 1}, ExitCompleted, 2, 1},
		{"model-limit", []Reply{calls("1")}, Limits{1, 2}, ExitLimitReached, 1, 1},
		{"batch-exceeds-tool-budget", []Reply{calls("1", "2")}, Limits{2, 1}, ExitLimitReached, 1, 0},
		{"remaining-tool-budget", []Reply{calls("1"), calls("2", "3")}, Limits{3, 2}, ExitLimitReached, 2, 1},
		{"no-tools-remain", []Reply{calls("1"), calls("2")}, Limits{3, 1}, ExitLimitReached, 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modelCalls, executions := 0, 0
			loop := Loop{Tools: testTools(), Limits: tc.limits,
				Model: modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) {
					if modelCalls >= len(tc.replies) {
						t.Fatal("model budget exceeded")
					}
					reply := tc.replies[modelCalls]
					modelCalls++
					return reply, nil
				}),
				Executor: executorFunc(func(context.Context, ToolCall) (ToolResult, error) {
					executions++
					return ToolResult{}, nil
				}),
			}
			result, err := loop.Run(context.Background(), testInitial())
			if err != nil || result.Reason != tc.reason || result.ModelCalls != tc.modelCalls || result.ToolCalls != tc.toolCalls || executions != tc.toolCalls {
				t.Fatalf("%+v %v executions=%d", result, err, executions)
			}
		})
	}
}

func TestLoopReportsRemainingBudgetAndPreservesNotices(t *testing.T) {
	initial := append(testInitial(),
		Message{Role: RoleAssistant, ToolCalls: calls("old").ToolCalls},
		Message{Role: RoleTool, ToolCallID: "old", Content: "previous run"},
	)
	remainingTools := []int{5, 3, 2, 1, 0}
	notices := []string{}
	attempts, executions := 0, 0
	loop := Loop{Tools: testTools(), Limits: Limits{5, 5},
		Model: modelFunc(func(_ context.Context, messages []Message, _ []ToolSpec) (Reply, error) {
			remaining := 5 - attempts
			notice := messages[len(messages)-1]
			wantCounts := fmt.Sprintf("Model calls remaining: %d (including this call).\nTool calls remaining: %d.", remaining, remainingTools[attempts])
			if notice.Role != RoleUser || !strings.Contains(notice.Content, wantCounts) {
				t.Fatalf("incorrect budget before call %d: %+v", attempts+1, notice)
			}
			if got := strings.Contains(notice.Content, "Budget is low."); got != (remaining == 2 || remaining == 3) {
				t.Fatalf("incorrect wrap-up reminder: %s", notice.Content)
			}
			if got := strings.Contains(notice.Content, "last model call"); got != (remaining == 1) {
				t.Fatalf("incorrect final-call reminder: %s", notice.Content)
			}
			if got := strings.Contains(notice.Content, "No tool calls remain."); got != (remainingTools[attempts] == 0) {
				t.Fatalf("incorrect tool-budget reminder: %s", notice.Content)
			}
			if _, err := validateHistory(messages); err != nil {
				t.Fatalf("notice interrupted tool results: %v", err)
			}
			notices = append(notices, notice.Content)
			attempts++
			switch attempts {
			case 1:
				return calls("1", "2"), nil
			case 2:
				reply := calls("3")
				reply.ToolCalls[0].Name = "unknown_tool"
				return reply, nil
			case 3, 4:
				return calls(fmt.Sprint(attempts + 1)), nil
			default:
				return Reply{Finish: FinishStop, Content: "Submitted with unresolved issues."}, nil
			}
		}),
		Executor: executorFunc(func(context.Context, ToolCall) (ToolResult, error) {
			executions++
			return ToolResult{}, nil
		}),
	}
	result, err := loop.Run(context.Background(), initial)
	if err != nil || result.Reason != ExitCompleted || result.ModelCalls != 5 || result.ToolCalls != 5 || executions != 4 {
		t.Fatalf("budget or final submission changed: %+v %v executions=%d", result, err, executions)
	}
	saved := []string{}
	for _, message := range result.Messages[len(initial):] {
		if message.Role == RoleUser {
			saved = append(saved, message.Content)
		}
	}
	if !reflect.DeepEqual(saved, notices) {
		t.Fatalf("notices changed or were not saved: %v, want %v", saved, notices)
	}
}

func TestLoopRejectsInvalidReplyBeforeAnyBatchSideEffect(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply Reply
		want  error
	}{
		{"stop-with-calls", Reply{Finish: FinishStop, ToolCalls: calls("1").ToolCalls}, ErrProtocol},
		{"empty-batch", Reply{Finish: FinishToolCalls}, ErrProtocol},
		{"unknown-finish", Reply{Finish: "unknown"}, ErrProtocol},
		{"truncated-text", Reply{Content: "incomplete", Finish: FinishLength}, ErrOutputTruncated},
		{"truncated-calls", Reply{Finish: FinishLength, ToolCalls: calls("1").ToolCalls}, ErrOutputTruncated},
		{"duplicate-id", calls("1", "1"), ErrProtocol},
		{"empty-second-id", calls("1", ""), ErrProtocol},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loop := Loop{Tools: testTools(), Limits: Limits{2, 2},
				Model: modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) { return tc.reply, nil }),
				Executor: executorFunc(func(context.Context, ToolCall) (ToolResult, error) {
					t.Fatal("invalid batch executed")
					return ToolResult{}, nil
				}),
			}
			result, err := loop.Run(context.Background(), testInitial())
			if !errors.Is(err, tc.want) || result.Reason != ExitFailed || result.ToolCalls != 0 || len(result.Messages) != 3 || result.Messages[2].Content != tc.reply.Content {
				t.Fatalf("%+v %v", result, err)
			}
		})
	}
}

func TestLoopRejectsReusedIDsAcrossTurnsAndHistory(t *testing.T) {
	initial := append(testInitial(), Message{Role: RoleAssistant, ToolCalls: calls("old").ToolCalls}, Message{Role: RoleTool, ToolCallID: "old", Content: "old result"})
	for _, history := range [][]Message{testInitial(), initial} {
		executions := 0
		loop := Loop{Tools: testTools(), Limits: Limits{3, 3},
			Model: modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) { return calls("old"), nil }),
			Executor: executorFunc(func(context.Context, ToolCall) (ToolResult, error) {
				executions++
				return ToolResult{}, nil
			}),
		}
		result, err := loop.Run(context.Background(), history)
		want := 1
		if len(history) > 1 {
			want = 0
		}
		if !errors.Is(err, ErrProtocol) || result.Reason != ExitFailed || executions != want {
			t.Fatalf("%+v %v executions=%d", result, err, executions)
		}
	}
}

func TestLoopInfrastructureErrorsStopWithPartialEvidence(t *testing.T) {
	want := errors.New("service unavailable")
	for _, stage := range []string{"model", "tool"} {
		t.Run(stage, func(t *testing.T) {
			loop := Loop{Tools: testTools(), Limits: Limits{3, 3},
				Model: modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) {
					if stage == "model" {
						return Reply{}, want
					}
					return calls("1", "2"), nil
				}),
				Executor: executorFunc(func(context.Context, ToolCall) (ToolResult, error) {
					return ToolResult{Content: "partial diagnostic"}, want
				}),
			}
			result, err := loop.Run(context.Background(), testInitial())
			if !errors.Is(err, want) || result.Reason != ExitFailed || result.ModelCalls != 1 {
				t.Fatalf("%+v %v", result, err)
			}
			if result.Messages[1].Role != RoleUser || !strings.Contains(result.Messages[1].Content, "Model calls remaining: 3 (including this call).") {
				t.Fatalf("request budget evidence lost: %+v", result.Messages)
			}
			if stage == "tool" {
				last := result.Messages[len(result.Messages)-1]
				if result.ToolCalls != 1 || last.ToolCallID != "1" || !last.IsError || last.Content != "partial diagnostic\ntool execution failed: service unavailable" {
					t.Fatalf("partial evidence lost: %+v", result)
				}
			}
		})
	}
}

func TestLoopCancellation(t *testing.T) {
	for _, stage := range []string{"before-start", "model", "after-model-success", "tool", "after-tool-success", "deadline"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if stage == "deadline" {
				var deadlineCancel context.CancelFunc
				ctx, deadlineCancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer deadlineCancel()
			}
			if stage == "before-start" {
				cancel()
			}
			started := make(chan struct{})
			loop := Loop{Tools: testTools(), Limits: Limits{3, 3},
				Model: modelFunc(func(got context.Context, _ []Message, _ []ToolSpec) (Reply, error) {
					if got != ctx {
						t.Error("model received a different context")
					}
					if stage == "model" || stage == "deadline" {
						close(started)
						<-got.Done()
						return Reply{}, got.Err()
					}
					if stage == "after-model-success" {
						cancel()
					}
					return calls("1", "2"), nil
				}),
				Executor: executorFunc(func(got context.Context, call ToolCall) (ToolResult, error) {
					if got != ctx || call.ID != "1" {
						t.Error("context lost or second tool ran after cancellation")
					}
					if stage == "after-tool-success" {
						cancel()
						return ToolResult{Content: "edit completed"}, nil
					}
					close(started)
					<-got.Done()
					return ToolResult{}, got.Err()
				}),
			}
			type outcome struct {
				result Result
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				result, err := loop.Run(ctx, testInitial())
				done <- outcome{result, err}
			}()
			if stage == "model" || stage == "tool" {
				select {
				case <-started:
					cancel()
				case <-time.After(2 * time.Second):
					t.Fatal("dependency never started")
				}
			}
			select {
			case out := <-done:
				wantReason, wantErr := ExitCancelled, context.Canceled
				if stage == "deadline" {
					wantReason, wantErr = ExitDeadlineExceeded, context.DeadlineExceeded
				}
				if out.result.Reason != wantReason || !errors.Is(out.err, wantErr) || out.result.ModelCalls > 1 || out.result.ToolCalls > 1 {
					t.Fatalf("%+v %v", out.result, out.err)
				}
				if stage == "before-start" && out.result.ModelCalls != 0 {
					t.Fatal("pre-cancelled loop called model")
				}
				if stage == "after-model-success" && (out.result.ToolCalls != 0 || len(out.result.Messages) != 3) {
					t.Fatal("model response lost or tool started after cancellation")
				}
				if stage == "after-tool-success" && out.result.Messages[len(out.result.Messages)-1].Content != "edit completed" {
					t.Fatal("completed tool evidence lost")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("loop ignored cancellation")
			}
		})
	}
}

func TestLoopParentCancellationTakesPrecedence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop := Loop{Tools: testTools(), Limits: Limits{1, 1},
		Model: modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) {
			cancel()
			return Reply{}, context.DeadlineExceeded
		}),
		Executor: executorFunc(func(context.Context, ToolCall) (ToolResult, error) {
			t.Fatal("tool ran")
			return ToolResult{}, nil
		}),
	}
	result, err := loop.Run(ctx, testInitial())
	if result.Reason != ExitCancelled || !errors.Is(err, context.Canceled) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestLoopRejectsInvalidInputsWithoutCallingDependencies(t *testing.T) {
	for _, mode := range []string{"no-messages", "bad-role", "unpaired-result", "pending-call", "non-assistant-calls", "non-tool-error", "zero-model-limit", "zero-tool-limit", "duplicate-tool", "bad-schema", "missing-model", "missing-executor"} {
		t.Run(mode, func(t *testing.T) {
			initial := testInitial()
			loop := Loop{Tools: testTools(), Limits: Limits{2, 2},
				Model: modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) {
					t.Fatal("invalid input reached model")
					return Reply{}, nil
				}),
				Executor: executorFunc(func(context.Context, ToolCall) (ToolResult, error) {
					t.Fatal("invalid input reached tool")
					return ToolResult{}, nil
				}),
			}
			switch mode {
			case "no-messages":
				initial = nil
			case "bad-role":
				initial[0].Role = "other"
			case "unpaired-result":
				initial = append(initial, Message{Role: RoleTool, ToolCallID: "missing"})
			case "pending-call":
				initial = append(initial, Message{Role: RoleAssistant, ToolCalls: calls("1").ToolCalls})
			case "non-assistant-calls":
				initial[0].ToolCalls = calls("1").ToolCalls
			case "non-tool-error":
				initial[0].IsError = true
			case "zero-model-limit":
				loop.Limits.MaxModelCalls = 0
			case "zero-tool-limit":
				loop.Limits.MaxToolCalls = 0
			case "duplicate-tool":
				loop.Tools = append(loop.Tools, loop.Tools[0])
			case "bad-schema":
				loop.Tools[0].Parameters = json.RawMessage(`null`)
			case "missing-model":
				loop.Model = nil
			case "missing-executor":
				loop.Executor = nil
			}
			result, err := loop.Run(context.Background(), initial)
			if !errors.Is(err, ErrInvalidInput) || result.Reason != ExitFailed || result.ModelCalls != 0 || result.ToolCalls != 0 {
				t.Fatalf("%+v %v", result, err)
			}
		})
	}
}

func TestLoopSnapshotsInputsAndReply(t *testing.T) {
	initial := append(testInitial(), Message{Role: RoleAssistant, ToolCalls: calls("old").ToolCalls}, Message{Role: RoleTool, ToolCallID: "old"})
	initialContent := initial[0].Content
	tools := testTools()
	reply := calls("new")
	step := 0
	loop := Loop{Tools: tools, Limits: Limits{2, 1},
		Model: modelFunc(func(_ context.Context, messages []Message, specs []ToolSpec) (Reply, error) {
			step++
			if messages[0].Content != initialContent || messages[1].ToolCalls[0].ID != "old" || !json.Valid(specs[0].Parameters) {
				t.Fatal("caller mutation corrupted loop snapshots")
			}
			if step == 1 {
				return reply, nil
			}
			if messages[4].ToolCalls[0].Arguments != `{"path":"client.go"}` {
				t.Fatal("reply alias mutated transcript")
			}
			return Reply{Finish: FinishStop}, nil
		}),
		Executor: executorFunc(func(_ context.Context, call ToolCall) (ToolResult, error) {
			// 修改调用方的原始数据，验证入口快照和已接收回复仍保持独立。
			initial[0].Content = "caller update"
			initial[1].ToolCalls[0].ID = "caller update"
			tools[0].Parameters[0] = '!'
			reply.ToolCalls[0].Arguments = "mutated"
			if call.Arguments != `{"path":"client.go"}` {
				t.Fatal("call arguments corrupted")
			}
			return ToolResult{}, nil
		}),
	}
	result, err := loop.Run(context.Background(), initial)
	if err != nil || result.Reason != ExitCompleted {
		t.Fatalf("%+v %v", result, err)
	}
	result.Messages[1].ToolCalls[0].ID = "caller mutation"
	if initial[1].ToolCalls[0].ID != "caller update" {
		t.Fatal("result mutation changed caller-owned history")
	}
}

func ExampleLoop_Run() {
	// 使用预设回复的模型和工具替身演示循环，不读取真实文件或执行迁移。
	loop := Loop{Tools: testTools(), Limits: Limits{MaxModelCalls: 2, MaxToolCalls: 1},
		Model: modelFunc(func(_ context.Context, messages []Message, _ []ToolSpec) (Reply, error) {
			if len(messages) >= 2 && messages[len(messages)-2].Role == RoleTool {
				return Reply{Content: "Done", Finish: FinishStop}, nil
			}
			return calls("read-1"), nil
		}),
		Executor: executorFunc(func(context.Context, ToolCall) (ToolResult, error) {
			return ToolResult{Content: "package inventory"}, nil
		}),
	}
	result, err := loop.Run(context.Background(), []Message{{Role: RoleUser, Content: "Read client.go"}})
	fmt.Println(result.Reason, result.ModelCalls, result.ToolCalls, err)
	// Output: completed 2 1 <nil>
}

func TestLoopRecordsUsageForEachAttempt(t *testing.T) {
	for _, reportedOnFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(reportedOnFailure), func(t *testing.T) {
			hit, miss := int64(6), int64(4)
			usage := &Usage{InputTokens: 10, OutputTokens: 2, TotalTokens: 12, CacheHitTokens: &hit, CacheMissTokens: &miss}
			wantError := errors.New("model failed")
			attempts := 0
			loop := Loop{Tools: testTools(), Limits: Limits{MaxModelCalls: 2, MaxToolCalls: 1},
				Model: modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) {
					attempts++
					if attempts == 1 {
						reply := calls("read-1")
						reply.Usage = usage
						return reply, nil
					}
					if reportedOnFailure {
						return Reply{Usage: &Usage{InputTokens: 20, OutputTokens: 3, TotalTokens: 23}}, wantError
					}
					return Reply{}, wantError
				}),
				Executor: executorFunc(func(context.Context, ToolCall) (ToolResult, error) {
					usage.InputTokens, hit, miss = 999, 999, 999
					return ToolResult{}, nil
				}),
			}
			result, err := loop.Run(context.Background(), testInitial())
			if !errors.Is(err, wantError) || result.Reason != ExitFailed || len(result.Usage) != result.ModelCalls || result.ModelCalls != 2 {
				t.Fatalf("attempt usage missing: %+v %v", result, err)
			}
			first := result.Usage[0]
			if first == nil || first.InputTokens != 10 || first.OutputTokens != 2 || first.TotalTokens != 12 || *first.CacheHitTokens != 6 || *first.CacheMissTokens != 4 {
				t.Fatalf("usage snapshot changed: %+v", first)
			}
			if (result.Usage[1] != nil) != reportedOnFailure || reportedOnFailure && result.Usage[1].TotalTokens != 23 {
				t.Fatalf("failure usage lost or invented: %+v", result.Usage[1])
			}
		})
	}
}
