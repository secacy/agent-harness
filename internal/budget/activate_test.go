package budget

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
)

func TestActivationRevisionOverflowCannotBindOrCall(t *testing.T) {
	seed, _ := RecoverSnapshot(recoverySourceSnapshot(t), "recovery", time.Now())
	seed.Revision = math.MaxInt64
	l, err := Activate(filepath.Join(t.TempDir(), "budget"), seed, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.BindRun("a", "new-run"); !errors.Is(err, ErrIntegrity) {
		t.Fatal("overflowing revision committed", err)
	}
	if l.Err() == nil || !reflect.DeepEqual(seed, disk(t, l)) {
		t.Fatal("overflow changed the persisted budget")
	}
}

func TestActivateInheritsCostsAndRejectsHistoricalRun(t *testing.T) {
	seed, err := RecoverSnapshot(recoverySourceSnapshot(t), "recovery", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	l, err := Activate(filepath.Join(t.TempDir(), "budget"), seed, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.BindRun("a", "run-a"); err == nil {
		t.Fatal("historical run rebound")
	}
	if !reflect.DeepEqual(seed, disk(t, l)) {
		t.Fatal("activation reset costs")
	}
	called := 0
	m, err := NewModel(l, "a", "next-run", controlledModel{profile: seed.Profile, complete: func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
		called++
		return agent.Reply{Usage: usage(15, 5)}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Complete(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	s := disk(t, l)
	if called != 1 || s.Totals != (Totals{Calls: 4, KnownTokens: 40, UnknownTokens: 200}) || ValidateSnapshot(s) != nil || !reflect.DeepEqual(s.Targets["a"].RunIDs, []string{"run-a", "next-run"}) {
		t.Fatal(s)
	}
	if seed.Targets["a"].RunID != "" || len(seed.Targets["a"].RunIDs) != 1 {
		t.Fatal("activation mutated its checkpoint")
	}
	if _, err := Activate(filepath.Dir(l.path), seed, time.Now().Add(time.Minute)); !errors.Is(err, os.ErrExist) {
		t.Fatal("existing ledger reopened", err)
	}
}

func TestActivatedModelDeadlineAppliesToAdmissionAndInflightCall(t *testing.T) {
	seed, _ := RecoverSnapshot(recoverySourceSnapshot(t), "recovery", time.Now())
	l, err := Activate(filepath.Join(t.TempDir(), "budget"), seed, time.Now().Add(250*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	m, err := NewModel(l, "a", "next", controlledModel{profile: seed.Profile, complete: func(ctx context.Context, _ []agent.Message, _ []agent.ToolSpec) (agent.Reply, error) {
		called++
		<-ctx.Done()
		return agent.Reply{}, ctx.Err()
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Complete(context.Background(), nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	before := l.Snapshot()
	if _, err := m.Complete(context.Background(), nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if called != 1 || !reflect.DeepEqual(before, disk(t, l)) || before.Totals != (Totals{Calls: 4, KnownTokens: 20, UnknownTokens: 300}) {
		t.Fatal("deadline admitted another call or refunded the unknown request", called, before.Totals)
	}
}

func TestActivationRejectsInvalidCheckpointAndExpiredDeadline(t *testing.T) {
	for _, mode := range []string{"expired", "v1", "reserved", "halt", "active"} {
		t.Run(mode, func(t *testing.T) {
			seed, _ := RecoverSnapshot(recoverySourceSnapshot(t), "recovery", time.Now())
			deadline := time.Now().Add(time.Minute)
			switch mode {
			case "expired":
				deadline = time.Now().Add(-time.Second)
			case "v1":
				seed = recoverySourceSnapshot(t)
			case "reserved":
				seed.Totals.ReservedTokens = 100
			case "halt":
				seed.HaltReason = "integrity failure"
			case "active":
				a := seed.Targets["a"]
				a.RunID = "run-a"
				seed.Targets["a"] = a
			}
			dir := filepath.Join(t.TempDir(), "budget")
			if _, err := Activate(dir, seed, deadline); err == nil {
				t.Fatal("invalid activation accepted")
			}
			if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("invalid activation created a ledger")
			}
		})
	}
}
