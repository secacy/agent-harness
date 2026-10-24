package agent

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestFinalResponseBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name             string
		limits           Limits
		replies          []Reply
		want             ExitReason
		marker, executed int
	}{
		{"last-call", Limits{2, 4}, []Reply{calls("a"), {Finish: FinishStop}}, ExitCompleted, 2, 1},
		{"tools-exhausted", Limits{5, 1}, []Reply{calls("a"), {Finish: FinishStop}}, ExitCompleted, 2, 1},
		{"early-stop", Limits{4, 4}, []Reply{{Finish: FinishStop}}, ExitCompleted, 0, 0},
		{"single-call", Limits{1, 4}, []Reply{{Finish: FinishStop}}, ExitCompleted, 1, 0},
		{"illegal-tools", Limits{1, 4}, []Reply{calls("a", "b")}, ExitFailed, 1, 0},
		{"stop-with-tools", Limits{1, 4}, []Reply{{Finish: FinishStop, ToolCalls: calls("a").ToolCalls}}, ExitFailed, 1, 0},
		{"truncated", Limits{1, 4}, []Reply{{Finish: FinishLength}}, ExitFailed, 1, 0},
		{"unknown-finish", Limits{1, 4}, []Reply{{Finish: "other"}}, ExitFailed, 1, 0},
		{"oversized-batch", Limits{3, 1}, []Reply{calls("a", "b")}, ExitLimitReached, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, executed := 0, 0
			loop := Loop{CompletionPolicy: FinalResponseV1, Limits: tc.limits, Tools: testTools(), Executor: executorFunc(func(context.Context, ToolCall) (ToolResult, error) { executed++; return ToolResult{}, nil })}
			loop.Model = modelFunc(func(_ context.Context, m []Message, tools []ToolSpec) (Reply, error) {
				n++
				if n > len(tc.replies) {
					t.Fatal("unexpected extra call")
				}
				if n == tc.marker {
					if tools != nil {
						t.Fatal("tools advertised at boundary")
					}
				} else if !reflect.DeepEqual(tools, testTools()) {
					t.Fatal("tools removed before boundary")
				}
				return tc.replies[n-1], nil
			})
			result, err := loop.Run(context.Background(), testInitial())
			if result.Reason != tc.want || executed != tc.executed || result.ToolCalls != executed || result.ModelCalls != len(tc.replies) {
				t.Fatalf("result=%+v err=%v executions=%d", result, err, executed)
			}
			if tc.want == ExitFailed && err == nil {
				t.Fatal("missing protocol failure")
			}
			if tc.marker == 0 {
				if result.FinalResponseAttempt != nil {
					t.Fatal("false boundary marker")
				}
			} else if result.FinalResponseAttempt == nil || result.FinalResponseAttempt.ModelCall != tc.marker {
				t.Fatal("missing boundary marker")
			}
			if err := ValidateCompletionResult(FinalResponseV1, tc.limits, result); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFinalResponseFailureAndRecord(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want ExitReason
	}{
		{"budget-denied", ErrModelBudget, ExitLimitReached}, {"cancelled", context.Canceled, ExitCancelled}, {"deadline", context.DeadlineExceeded, ExitDeadlineExceeded}, {"provider", errors.New("provider failure"), ExitFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loop := Loop{CompletionPolicy: FinalResponseV1, Limits: Limits{1, 1}, Executor: executorFunc(func(context.Context, ToolCall) (ToolResult, error) {
				t.Fatal("executed tool")
				return ToolResult{}, nil
			}), Model: modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) { return Reply{}, tc.err })}
			result, err := loop.Run(context.Background(), testInitial())
			if !errors.Is(err, tc.err) || result.Reason != tc.want || result.FinalResponseAttempt == nil {
				t.Fatal(result, err)
			}
			if err := ValidateCompletionResult(FinalResponseV1, loop.Limits, result); err != nil {
				t.Fatal(err)
			}
		})
	}
	loop := Loop{CompletionPolicy: FinalResponseV1, Limits: Limits{1, 1}, Model: modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) { return Reply{Finish: FinishStop}, nil }), Executor: executorFunc(func(context.Context, ToolCall) (ToolResult, error) { return ToolResult{}, nil })}
	result, err := loop.Run(context.Background(), testInitial())
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Result){
		func(r *Result) { r.FinalResponseAttempt = nil },
		func(r *Result) { r.FinalResponseAttempt.ModelCall++ },
		func(r *Result) { r.FinalResponseAttempt.MessageCount-- },
		func(r *Result) { r.Messages = append(r.Messages, Message{Role: RoleTool, ToolCallID: "fake"}) },
		func(r *Result) { r.ToolCalls++ },
		func(r *Result) { r.Messages[1].Content = "forged notice" },
	} {
		changed := result
		changed.Messages = cloneMessages(result.Messages)
		marker := *result.FinalResponseAttempt
		changed.FinalResponseAttempt = &marker
		mutate(&changed)
		if ValidateCompletionResult(FinalResponseV1, loop.Limits, changed) == nil {
			t.Fatal("corrupt record accepted", changed)
		}
	}
	if ValidateCompletionResult("", loop.Limits, result) == nil {
		t.Fatal("legacy accepted marker")
	}
	loop.CompletionPolicy = "unknown"
	if _, err := loop.Run(context.Background(), testInitial()); !errors.Is(err, ErrInvalidInput) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	loop.CompletionPolicy = FinalResponseV1
	result, err = loop.Run(ctx, testInitial())
	if !errors.Is(err, context.Canceled) || result.ModelCalls != 0 || result.FinalResponseAttempt != nil {
		t.Fatal(result, err)
	}
}

func TestFinalResponseCancellationAfterReply(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop := Loop{CompletionPolicy: FinalResponseV1, Limits: Limits{1, 1}, Tools: testTools(), Executor: executorFunc(func(context.Context, ToolCall) (ToolResult, error) {
		t.Fatal("cancelled final tool executed")
		return ToolResult{}, nil
	}), Model: modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) { cancel(); return calls("late"), nil })}
	result, err := loop.Run(ctx, testInitial())
	if !errors.Is(err, context.Canceled) || result.Reason != ExitCancelled || result.ToolCalls != 0 || result.FinalResponseAttempt == nil || len(result.Messages[len(result.Messages)-1].ToolCalls) != 1 {
		t.Fatal(result, err)
	}
	if err := ValidateCompletionResult(FinalResponseV1, loop.Limits, result); err != nil {
		t.Fatal(err)
	}
}

func TestFinalResponseInitialHistoryNotCounted(t *testing.T) {
	initial := append(testInitial(), completionBudgetMessage(FinalResponseV1, 1, 1), Message{Role: RoleAssistant, Content: "old response"})
	loop := Loop{CompletionPolicy: FinalResponseV1, Limits: Limits{1, 1}, Model: modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) { return Reply{Finish: FinishStop}, nil }), Executor: executorFunc(func(context.Context, ToolCall) (ToolResult, error) { return ToolResult{}, nil })}
	result, err := loop.Run(context.Background(), initial)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateCompletionResult(FinalResponseV1, loop.Limits, result); err != nil {
		t.Fatal(err)
	}
}
