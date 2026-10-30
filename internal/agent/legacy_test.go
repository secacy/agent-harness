package agent

import (
	"context"
	"reflect"
	"testing"
)

func legacyFixture(t *testing.T, policy CompletionPolicy) (Result, Limits) {
	t.Helper()
	limits := Limits{MaxModelCalls: 3, MaxToolCalls: 2}
	step := 0
	loop := Loop{CompletionPolicy: policy, Limits: limits, Tools: testTools(),
		Model: modelFunc(func(context.Context, []Message, []ToolSpec) (Reply, error) {
			step++
			if step == 1 {
				reply := calls("one", "two")
				reply.Usage = &Usage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5}
				return reply, nil
			}
			return Reply{Content: "done", Finish: FinishStop}, nil
		}),
		Executor: executorFunc(func(_ context.Context, call ToolCall) (ToolResult, error) {
			return ToolResult{Content: call.ID}, nil
		}),
	}
	// An old complete conversation may contain the same initial allowance.
	initial := append(testInitial(), completionBudgetMessage(policy, limits.MaxModelCalls, limits.MaxToolCalls))
	result, err := loop.Run(context.Background(), initial)
	if err != nil || result.Reason != ExitCompleted {
		t.Fatalf("fixture: %+v %v", result, err)
	}
	return result, limits
}

func TestLegacyRecordingPreservesCompleteConversation(t *testing.T) {
	result, limits := legacyFixture(t, "")
	recording, err := LegacyRecording(result, testTools(), limits, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recording.Output.Result, result) || len(recording.Input.Initial) != 2 || len(recording.Operations) != 4 {
		t.Fatalf("recording differs: %+v", recording)
	}
	if replayed, err := ReplayRecording(context.Background(), recording); err != nil || !reflect.DeepEqual(replayed, result) {
		t.Fatalf("legacy replay: %v", err)
	}
	first, last := recording.Operations[0].Model, recording.Operations[3].Model
	if first.Output.Reply.Finish != FinishToolCalls || last.Output.Reply.Finish != FinishStop || last.Output.Reply.Usage != nil {
		t.Fatal("incorrect derived finish reasons or unknown usage")
	}
	if len(first.Input.Messages) != 3 || len(last.Input.Messages) != 7 || !recording.Operations[1].Tool.Input.Executed {
		t.Fatal("request prefix or tool operation lost")
	}
	result.Messages[0].Content = "changed"
	result.Usage[0].TotalTokens = 100
	if recording.Input.Initial[0].Content == "changed" || first.Output.Reply.Usage.TotalTokens != 5 || recording.Output.Result.Usage[0].TotalTokens != 5 {
		t.Fatal("recording aliases mutable source")
	}
}

func TestLegacyRecordingRejectsIncompleteOrInconsistentHistory(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Result)
	}{
		{"failed", func(r *Result) { r.Reason = ExitFailed }},
		{"budget-exhausted", func(r *Result) { r.Reason = ExitLimitReached }},
		{"call-count", func(r *Result) { r.ModelCalls++ }},
		{"tool-count", func(r *Result) { r.ToolCalls-- }},
		{"usage-count", func(r *Result) { r.Usage = r.Usage[:1] }},
		{"budget-notice", func(r *Result) { r.Messages[len(r.Messages)-2].Content = "wrong remaining budget" }},
		{"tool-pair", func(r *Result) { r.Messages[4].ToolCallID = "wrong-id" }},
		{"reordered-tool-results", func(r *Result) { r.Messages[4], r.Messages[5] = r.Messages[5], r.Messages[4] }},
		{"missing-final-response", func(r *Result) { r.Messages = r.Messages[:len(r.Messages)-1] }},
		{"trailing-content", func(r *Result) { r.Messages = append(r.Messages, Message{Role: RoleUser, Content: "extra"}) }},
		{"finish-before-tools", func(r *Result) { r.Messages[3].ToolCalls = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, limits := legacyFixture(t, "")
			tc.edit(&result)
			if _, err := LegacyRecording(result, testTools(), limits, ""); err == nil {
				t.Fatal("accepted inconsistent history")
			}
		})
	}
}

func TestLegacyRecordingFinalResponsePolicy(t *testing.T) {
	result, limits := legacyFixture(t, FinalResponseV1)
	recording, err := LegacyRecording(result, testTools(), limits, FinalResponseV1)
	if err != nil {
		t.Fatal(err)
	}
	if replayed, err := ReplayRecording(context.Background(), recording); err != nil || !reflect.DeepEqual(replayed, result) {
		t.Fatalf("final-only legacy replay: %v", err)
	}
	last := recording.Operations[len(recording.Operations)-1].Model
	if last.Input.Tools != nil || result.FinalResponseAttempt == nil || last.Output.Reply.Finish != FinishStop {
		t.Fatal("final-only request not preserved")
	}
	result.FinalResponseAttempt = nil
	if _, err := LegacyRecording(result, testTools(), limits, FinalResponseV1); err == nil {
		t.Fatal("accepted missing final-only marker")
	}
}

func TestLegacyRecordingUnknownToolMustMatchHostRejection(t *testing.T) {
	result, limits := legacyFixture(t, "")
	result.Messages[3].ToolCalls[0].Name = "not-advertised"
	result.Messages[4].Content = `unknown tool "not-advertised"; use an advertised tool`
	result.Messages[4].IsError = true
	recording, err := LegacyRecording(result, testTools(), limits, "")
	if err != nil {
		t.Fatal(err)
	}
	if replayed, err := ReplayRecording(context.Background(), recording); err != nil || !reflect.DeepEqual(replayed, result) {
		t.Fatalf("unknown-tool legacy replay: %v", err)
	}
	if recording.Operations[1].Tool.Input.Executed {
		t.Fatal("unknown tool was marked executed")
	}
	result.Messages[4].Content = "invented result"
	if _, err := LegacyRecording(result, testTools(), limits, ""); err == nil {
		t.Fatal("accepted invented unknown-tool feedback")
	}
}
