package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/secacy/agent-harness/internal/trace"
)

func recordLoop(t *testing.T, ctx context.Context, loop Loop) (Recording, Result, error, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	recorder, err := trace.New(path, "recorded-run")
	if err != nil {
		t.Fatal(err)
	}
	result, runErr := loop.Run(trace.Context(ctx, recorder), testInitial())
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	log, err := trace.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	recording, err := RecordingFromTrace(log)
	if err != nil {
		t.Fatal(err)
	}
	return recording, result, runErr, path
}

func traceLoop(model Model, executor ToolExecutor) Loop {
	return Loop{ModelName: "offline-test-model", Model: model, Executor: executor, Tools: testTools(), Limits: Limits{MaxModelCalls: 3, MaxToolCalls: 5}}
}

func TestTraceReplayProductionLoopOutcomes(t *testing.T) {
	for _, test := range []struct {
		name    string
		build   func(context.CancelFunc, *int, *int) Loop
		want    ExitReason
		wantErr error
	}{
		{"multiple tools including unknown", func(_ context.CancelFunc, modelCalls, toolCalls *int) Loop {
			return traceLoop(modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) {
				*modelCalls++
				if *modelCalls == 1 {
					reply := calls("read-one", "read-two")
					reply.ToolCalls = append(reply.ToolCalls, ToolCall{ID: "unknown", Name: "not_advertised", Arguments: `{"x":1}`})
					reply.Usage = &Usage{InputTokens: 11, OutputTokens: 5, TotalTokens: 16}
					return reply, nil
				}
				return Reply{Content: "submitted", Finish: FinishStop, Usage: &Usage{InputTokens: 20, OutputTokens: 2, TotalTokens: 22}}, nil
			}), executorFunc(func(_ context.Context, c ToolCall) (ToolResult, error) {
				*toolCalls++
				return ToolResult{Content: "data:" + c.ID}, nil
			}))
		}, ExitCompleted, nil},
		{"model length", func(_ context.CancelFunc, mc, tc *int) Loop {
			return traceLoop(modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) {
				*mc++
				return Reply{Content: "partial", Finish: FinishLength}, nil
			}), executorFunc(func(context.Context, ToolCall) (ToolResult, error) { *tc++; return ToolResult{}, nil }))
		}, ExitFailed, ErrOutputTruncated},
		{"model protocol", func(_ context.CancelFunc, mc, tc *int) Loop {
			return traceLoop(modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) {
				*mc++
				reply := calls("read")
				reply.Finish = FinishStop
				return reply, nil
			}), executorFunc(func(context.Context, ToolCall) (ToolResult, error) { *tc++; return ToolResult{}, nil }))
		}, ExitFailed, ErrProtocol},
		{"tool infrastructure", func(_ context.CancelFunc, mc, tc *int) Loop {
			return traceLoop(modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) { *mc++; return calls("read"), nil }), executorFunc(func(context.Context, ToolCall) (ToolResult, error) {
				*tc++
				return ToolResult{Content: "partial result"}, errors.New("executor connection lost")
			}))
		}, ExitFailed, nil},
		{"budget denial", func(_ context.CancelFunc, mc, tc *int) Loop {
			return traceLoop(modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) { *mc++; return Reply{}, ErrModelBudget }), executorFunc(func(context.Context, ToolCall) (ToolResult, error) { *tc++; return ToolResult{}, nil }))
		}, ExitLimitReached, ErrModelBudget},
		{"final-only violation", func(_ context.CancelFunc, mc, tc *int) Loop {
			loop := traceLoop(modelFunc(func(_ context.Context, _ []Message, tools []ToolSpec) (Reply, error) {
				*mc++
				if len(tools) != 0 {
					t.Error("final-only request exposed tools")
				}
				return calls("illegal"), nil
			}), executorFunc(func(context.Context, ToolCall) (ToolResult, error) { *tc++; return ToolResult{}, nil }))
			loop.CompletionPolicy = FinalResponseV1
			loop.Limits.MaxModelCalls = 1
			return loop
		}, ExitFailed, ErrProtocol},
		{"cancel after model", func(cancel context.CancelFunc, mc, tc *int) Loop {
			return traceLoop(modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) {
				*mc++
				cancel()
				return calls("read"), nil
			}), executorFunc(func(context.Context, ToolCall) (ToolResult, error) { *tc++; return ToolResult{}, nil }))
		}, ExitCancelled, context.Canceled},
		{"cancel after tool", func(cancel context.CancelFunc, mc, tc *int) Loop {
			return traceLoop(modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) {
				*mc++
				return calls("read", "unreached"), nil
			}), executorFunc(func(context.Context, ToolCall) (ToolResult, error) {
				*tc++
				cancel()
				return ToolResult{Content: "completed write"}, nil
			}))
		}, ExitCancelled, context.Canceled},
		{"model usage unknown", func(_ context.CancelFunc, mc, tc *int) Loop {
			return traceLoop(modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) {
				*mc++
				return Reply{Finish: FinishStop}, nil
			}), executorFunc(func(context.Context, ToolCall) (ToolResult, error) { *tc++; return ToolResult{}, nil }))
		}, ExitCompleted, nil},
		{"tool batch budget", func(_ context.CancelFunc, mc, tc *int) Loop {
			loop := traceLoop(modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) { *mc++; return calls("one", "two"), nil }), executorFunc(func(context.Context, ToolCall) (ToolResult, error) { *tc++; return ToolResult{}, nil }))
			loop.Limits.MaxToolCalls = 1
			return loop
		}, ExitLimitReached, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			modelCalls, toolCalls := 0, 0
			loop := test.build(cancel, &modelCalls, &toolCalls)
			recording, result, runErr, _ := recordLoop(t, ctx, loop)
			if result.Reason != test.want {
				t.Fatalf("reason=%s err=%v", result.Reason, runErr)
			}
			if test.wantErr != nil && !errors.Is(runErr, test.wantErr) {
				t.Fatalf("expected %v got %v", test.wantErr, runErr)
			}
			beforeModel, beforeTool := modelCalls, toolCalls
			replayed, err := ReplayRecording(context.Background(), recording)
			if err != nil || !reflect.DeepEqual(result, replayed) {
				t.Fatalf("result=%+v replay=%+v err=%v", result, replayed, err)
			}
			if modelCalls != beforeModel || toolCalls != beforeTool {
				t.Fatal("replay executed a live model or tool")
			}
		})
	}
}

func completeRecording(t *testing.T) Recording {
	t.Helper()
	index := 0
	loop := traceLoop(modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) {
		index++
		if index == 1 {
			reply := calls("known")
			reply.ToolCalls = append(reply.ToolCalls, ToolCall{ID: "unknown", Name: "missing", Arguments: `{"path":"x"}`})
			reply.Usage = &Usage{InputTokens: 2, OutputTokens: 3, TotalTokens: 5}
			return reply, nil
		}
		return Reply{Content: "done", Finish: FinishStop}, nil
	}), executorFunc(func(context.Context, ToolCall) (ToolResult, error) { return ToolResult{Content: "known output"}, nil }))
	r, _, err, _ := recordLoop(t, context.Background(), loop)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestReplayRejectsChangedInteractionAndControlData(t *testing.T) {
	original := completeRecording(t)
	for _, test := range []struct {
		name   string
		change func(*Recording)
	}{
		{"request", func(r *Recording) {
			for _, op := range r.Operations {
				if op.Model != nil {
					op.Model.Input.Messages[0].Content = "changed"
					return
				}
			}
		}},
		{"usage", func(r *Recording) {
			for _, op := range r.Operations {
				if op.Model != nil {
					op.Model.Output.Reply.Usage.TotalTokens++
					return
				}
			}
		}},
		{"finish", func(r *Recording) {
			for _, op := range r.Operations {
				if op.Model != nil {
					op.Model.Output.Reply.Finish = FinishLength
					return
				}
			}
		}},
		{"unknown ID", func(r *Recording) {
			for _, op := range r.Operations {
				if op.Tool != nil && !op.Tool.Input.Executed {
					op.Tool.Input.Call.ID = "other"
					return
				}
			}
		}},
		{"unknown arguments", func(r *Recording) {
			for _, op := range r.Operations {
				if op.Tool != nil && !op.Tool.Input.Executed {
					op.Tool.Input.Call.Arguments = `{"path":"other"}`
					return
				}
			}
		}},
		{"unknown result", func(r *Recording) {
			for _, op := range r.Operations {
				if op.Tool != nil && !op.Tool.Input.Executed {
					op.Tool.Output.Result.Content = "forged rejection"
					return
				}
			}
		}},
		{"missing boundary", func(r *Recording) { r.Operations = r.Operations[1:] }},
		{"changed boundary", func(r *Recording) { r.Operations[0].Boundary.Name = "other" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, err := json.Marshal(original)
			if err != nil {
				t.Fatal(err)
			}
			var changed Recording
			if err := json.Unmarshal(data, &changed); err != nil {
				t.Fatal(err)
			}
			test.change(&changed)
			if _, err := ReplayRecording(context.Background(), changed); err == nil {
				t.Fatal("changed interaction was accepted")
			}
		})
	}
}

func TestRecordingFromTraceRejectsTruncatedAndOpenLoop(t *testing.T) {
	loop := traceLoop(modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) { return Reply{Finish: FinishStop}, nil }), executorFunc(func(context.Context, ToolCall) (ToolResult, error) { return ToolResult{}, nil }))
	_, _, _, path := recordLoop(t, context.Background(), loop)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw[:len(raw)-3], 0600); err != nil {
		t.Fatal(err)
	}
	log, err := trace.Read(path)
	if err != nil || !log.TruncatedTail {
		t.Fatalf("log=%+v err=%v", log, err)
	}
	if _, err := RecordingFromTrace(log); err == nil {
		t.Fatal("truncated loop accepted")
	}
	log.TruncatedTail = false
	if _, err := RecordingFromTrace(log); err == nil {
		t.Fatal("open loop accepted")
	}
}

func TestTraceFailureBeforeLoopPreventsModelDispatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "closed.jsonl")
	recorder, err := trace.New(path, "closed-run")
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	called := false
	loop := traceLoop(modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) {
		called = true
		return Reply{Finish: FinishStop}, nil
	}), executorFunc(func(context.Context, ToolCall) (ToolResult, error) { called = true; return ToolResult{}, nil }))
	result, err := loop.Run(trace.Context(context.Background(), recorder), testInitial())
	if called || !errors.Is(err, trace.ErrClosed) || result.Reason != ExitFailed {
		t.Fatalf("called=%v result=%+v err=%v", called, result, err)
	}
}

func TestTraceReplayCancellationAtRecordedControlBoundary(t *testing.T) {
	for _, boundary := range []string{"initial", "before_step", "after_model", "before_tool", "after_tool"} {
		t.Run(boundary, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			loop := traceLoop(modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) {
				return Reply{Finish: FinishToolCalls, ToolCalls: []ToolCall{{ID: "unknown", Name: "missing", Arguments: `{}`}}}, nil
			}), executorFunc(func(context.Context, ToolCall) (ToolResult, error) {
				t.Fatal("unknown tool reached executor")
				return ToolResult{}, nil
			}))
			// Cancel exactly at the Loop check, after the previous operation's end
			// event was persisted. No model/tool output can explain this timing.
			loop.replayBoundary = func(name string) error {
				if name == boundary {
					cancel()
				}
				return nil
			}
			recording, result, runErr, _ := recordLoop(t, ctx, loop)
			if result.Reason != ExitCancelled || !errors.Is(runErr, context.Canceled) {
				t.Fatalf("result=%+v err=%v", result, runErr)
			}
			if _, err := ReplayRecording(context.Background(), recording); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTraceReplayDeadlineCause(t *testing.T) {
	ctx := &replayContext{Context: context.Background()}
	loop := traceLoop(modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) {
		ctx.cause = context.DeadlineExceeded
		return Reply{Content: "returned after timeout", Finish: FinishStop}, nil
	}), executorFunc(func(context.Context, ToolCall) (ToolResult, error) {
		t.Fatal("tool executed after timeout")
		return ToolResult{}, nil
	}))
	recording, result, runErr, _ := recordLoop(t, ctx, loop)
	if result.Reason != ExitDeadlineExceeded || !errors.Is(runErr, context.DeadlineExceeded) {
		t.Fatalf("result=%+v err=%v", result, runErr)
	}
	if _, err := ReplayRecording(context.Background(), recording); err != nil {
		t.Fatal(err)
	}
}

func TestTraceFailureAfterModelPreventsToolDispatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "closed-after-model.jsonl")
	recorder, err := trace.New(path, "closed-after-model")
	if err != nil {
		t.Fatal(err)
	}
	toolCalled := false
	loop := traceLoop(modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) {
		if err := recorder.Close(); err != nil {
			t.Fatal(err)
		}
		return calls("must-not-execute"), nil
	}), executorFunc(func(context.Context, ToolCall) (ToolResult, error) {
		toolCalled = true
		return ToolResult{}, nil
	}))
	result, runErr := loop.Run(trace.Context(context.Background(), recorder), testInitial())
	if toolCalled || result.Reason != ExitFailed || !errors.Is(runErr, trace.ErrClosed) {
		t.Fatalf("toolCalled=%v result=%+v err=%v", toolCalled, result, runErr)
	}
	log, err := trace.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecordingFromTrace(log); err == nil {
		t.Fatal("incomplete failed recording accepted")
	}
}

func TestToolFailureRetainsParentSpanCategory(t *testing.T) {
	loop := traceLoop(modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) { return calls("read"), nil }), executorFunc(func(context.Context, ToolCall) (ToolResult, error) { return ToolResult{}, errors.New("executor lost") }))
	_, _, _, path := recordLoop(t, context.Background(), loop)
	log, err := trace.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	matched := 0
	for _, e := range log.Events {
		if e.Phase == "end" && (e.Kind == "tool_call" || e.Kind == "step" || e.Kind == "agent") {
			matched++
			if e.ErrorCategory != "tool" {
				t.Fatalf("%s incorrectly classified as %s", e.Kind, e.ErrorCategory)
			}
		}
	}
	if matched != 3 {
		t.Fatalf("missing error spans: %d", matched)
	}
}
