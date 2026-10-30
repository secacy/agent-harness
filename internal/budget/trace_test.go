package budget

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/trace"
)

func budgetTrace(t *testing.T) (*trace.Recorder, context.Context, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	recorder, err := trace.New(path, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recorder.Close() })
	return recorder, trace.Context(context.Background(), recorder), path
}

func TestBudgetTraceMatchesDurableReservationAndSettlement(t *testing.T) {
	providerFailure := errors.New("provider connection ended")
	for _, test := range []struct {
		name        string
		usage       *agent.Usage
		providerErr error
		state       CallState
		totals      Totals
	}{
		{"known", usage(12, 3), nil, StateSettled, Totals{Calls: 1, KnownTokens: 15}},
		{"unknown", nil, nil, StateUnknown, Totals{Calls: 1, UnknownTokens: 100}},
		{"provider error", usage(12, 3), providerFailure, StateSettled, Totals{Calls: 1, KnownTokens: 15}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ledger := newLedger(t, 3, 300)
			recorder, ctx, path := budgetTrace(t)
			providerCalls := 0
			model, err := NewModel(ledger, "a", "run-a", controlledModel{profile: testProfile(), complete: func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
				providerCalls++
				return agent.Reply{Content: "response", Finish: agent.FinishStop, Usage: test.usage}, test.providerErr
			}})
			if err != nil {
				t.Fatal(err)
			}
			messages := []agent.Message{{Role: agent.RoleUser, Content: "task"}}
			reply, err := model.Complete(ctx, messages, nil)
			if providerCalls != 1 || !errors.Is(err, test.providerErr) || !reflect.DeepEqual(reply.Usage, test.usage) {
				t.Fatalf("calls=%d reply=%+v err=%v", providerCalls, reply, err)
			}
			if err := recorder.Close(); err != nil {
				t.Fatal(err)
			}
			snapshot := disk(t, ledger)
			if snapshot.Totals != test.totals || len(snapshot.Calls) != 1 || snapshot.Calls[0].State != test.state {
				t.Fatalf("%+v", snapshot)
			}
			log, err := trace.Read(path)
			if err != nil || len(log.Events) != 4 {
				t.Fatalf("events=%d err=%v", len(log.Events), err)
			}
			var reserveInput struct {
				InputSHA256 string `json:"input_sha256"`
				Model       string `json:"model"`
			}
			if err := json.Unmarshal(log.Events[0].Data, &reserveInput); err != nil {
				t.Fatal(err)
			}
			requestDigest, _ := digestJSON(struct {
				Messages []agent.Message
				Tools    []agent.ToolSpec
			}{messages, nil})
			if reserveInput.InputSHA256 != requestDigest || reserveInput.Model != testProfile().Model {
				t.Fatalf("%+v", reserveInput)
			}
			var reserved, settled struct {
				Call     Call
				Revision int64
			}
			if err := json.Unmarshal(log.Events[1].Data, &reserved); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(log.Events[3].Data, &settled); err != nil {
				t.Fatal(err)
			}
			if log.Events[0].Kind != "budget_reserve" || log.Events[1].Phase != "end" || log.Events[2].Kind != "budget_settle" || log.Events[3].Phase != "end" {
				t.Fatalf("%+v", log.Events)
			}
			call := snapshot.Calls[0]
			if reserved.Call.ID != call.ID || reserved.Call.InputSHA256 != requestDigest || reserved.Call.State != StateReserved || reserved.Call.Usage != nil || reserved.Call.ReservedTokens != 100 || !reflect.DeepEqual(settled.Call, call) || reserved.Revision >= settled.Revision || settled.Revision != snapshot.Revision {
				t.Fatalf("reserved=%+v settled=%+v disk=%+v", reserved, settled, snapshot)
			}
			if call.Outcome != "reply" && test.providerErr == nil {
				t.Fatalf("%+v", call)
			}
			if call.Outcome != "model_error" && test.providerErr != nil {
				t.Fatalf("%+v", call)
			}
		})
	}
}

func TestBudgetDenialTraceHasNoSupplierDispatchOrFalseCall(t *testing.T) {
	ledger := newLedger(t, 1, 300)
	recorder, ctx, path := budgetTrace(t)
	dispatches := 0
	model, err := NewModel(ledger, "a", "run-a", controlledModel{profile: testProfile(), complete: func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
		dispatches++
		return agent.Reply{Usage: usage(1, 1)}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := model.Complete(ctx, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := model.Complete(ctx, nil, nil); !errors.Is(err, agent.ErrModelBudget) {
		t.Fatalf("%v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot := disk(t, ledger)
	if dispatches != 1 || len(snapshot.Calls) != 1 || len(snapshot.Rejections) != 1 {
		t.Fatalf("dispatches=%d snapshot=%+v", dispatches, snapshot)
	}
	log, err := trace.Read(path)
	if err != nil || len(log.Events) != 6 {
		t.Fatalf("%+v %v", log, err)
	}
	denied := log.Events[5]
	var output struct {
		Call     Call
		Revision int64
	}
	if err := json.Unmarshal(denied.Data, &output); err != nil {
		t.Fatal(err)
	}
	if denied.Kind != "budget_reserve" || denied.Phase != "end" || denied.ErrorCategory != "budget" || denied.Error == "" || output.Call.ID != "" || output.Revision != snapshot.Revision {
		t.Fatalf("event=%+v output=%+v", denied, output)
	}
}

func TestClosedTracePreventsReservationAndDispatch(t *testing.T) {
	ledger := newLedger(t, 3, 300)
	recorder, ctx, _ := budgetTrace(t)
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	dispatched := false
	model, err := NewModel(ledger, "a", "run-a", controlledModel{profile: testProfile(), complete: func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
		dispatched = true
		return agent.Reply{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	before := disk(t, ledger)
	if _, err := model.Complete(ctx, nil, nil); !errors.Is(err, trace.ErrClosed) {
		t.Fatalf("%v", err)
	}
	if dispatched || !reflect.DeepEqual(before, disk(t, ledger)) {
		t.Fatal("trace failure admitted supplier work or changed ledger")
	}
}

func TestTraceFailureAfterProviderStillSettlesActualUsageOnce(t *testing.T) {
	ledger := newLedger(t, 3, 300)
	recorder, ctx, path := budgetTrace(t)
	dispatches := 0
	model, err := NewModel(ledger, "a", "run-a", controlledModel{profile: testProfile(), complete: func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
		dispatches++
		if err := recorder.Close(); err != nil {
			t.Fatal(err)
		}
		return agent.Reply{Usage: usage(10, 2)}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := model.Complete(ctx, nil, nil); !errors.Is(err, trace.ErrClosed) {
		t.Fatalf("%v", err)
	}
	snapshot := disk(t, ledger)
	if dispatches != 1 || snapshot.Totals != (Totals{Calls: 1, KnownTokens: 12}) || len(snapshot.Calls) != 1 || snapshot.Calls[0].State != StateSettled {
		t.Fatalf("dispatches=%d snapshot=%+v", dispatches, snapshot)
	}
	if _, err := model.Complete(ctx, nil, nil); !errors.Is(err, trace.ErrClosed) {
		t.Fatalf("retry: %v", err)
	}
	if dispatches != 1 || !reflect.DeepEqual(snapshot, disk(t, ledger)) {
		t.Fatal("retry repeated supplier charge or settlement")
	}
	log, err := trace.Read(path)
	if err != nil || len(log.Events) != 2 {
		t.Fatalf("events=%d err=%v", len(log.Events), err)
	}
}

func TestReservationTraceEndFailureRetainsReservationWithoutDispatch(t *testing.T) {
	ledger := newLedger(t, 3, 300)
	recorder, ctx, path := budgetTrace(t)
	dispatched := false
	model, err := NewModel(ledger, "a", "run-a", controlledModel{profile: testProfile(), complete: func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
		dispatched = true
		return agent.Reply{}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	// Close the recorder immediately after the reservation becomes durable,
	// deterministically exercising the commit-to-trace window without polling.
	persist := ledger.persist
	ledger.persist = func(snapshot Snapshot) error {
		if err := persist(snapshot); err != nil {
			return err
		}
		return recorder.Close()
	}
	if _, err := model.Complete(ctx, nil, nil); !errors.Is(err, trace.ErrClosed) {
		t.Fatalf("%v", err)
	}
	snapshot := disk(t, ledger)
	if dispatched || snapshot.Totals != (Totals{Calls: 1, ReservedTokens: 100}) || len(snapshot.Calls) != 1 || snapshot.Calls[0].State != StateReserved {
		t.Fatalf("dispatched=%v snapshot=%+v", dispatched, snapshot)
	}
	log, err := trace.Read(path)
	if err != nil || len(log.Events) != 1 || log.Events[0].Phase != "begin" {
		t.Fatalf("%+v %v", log, err)
	}
	// The source remains a real partial log rather than synthetic completion.
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		t.Fatalf("%v %v", info, err)
	}
}
