package ablation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/runner"
)

type unusedBackend struct{ info execution.Info }

func (b unusedBackend) Info() execution.Info { return b.info }
func (b unusedBackend) Run(context.Context, execution.Request) (execution.Result, error) {
	panic("factory-failure test must never execute candidate code")
}

func runnableProtocol(t *testing.T) Protocol {
	t.Helper()
	p, err := Draft("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	template := testProtocol()
	p.Model = template.Model
	p.Execution = template.Execution
	p.Resources = template.Resources
	if err = p.Validate(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestP1FactoryFailuresKeepThirtySixRowsAndNeverDispatch(t *testing.T) {
	p := runnableProtocol(t)
	root := filepath.Join(t.TempDir(), "experiment")
	calls := 0
	out, err := Run(context.Background(), root, p, Services{Backend: unusedBackend{p.Execution}, NewModel: func(Slot) (budget.ProfiledModel, error) { calls++; return nil, errors.New("model unavailable") }})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 36 || len(out.Slots) != 36 || out.FinishedAt == nil {
		t.Fatalf("schedule lost: %d %+v", calls, out)
	}
	summary, err := Collect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Results) != 36 || !summary.ExecutionComplete {
		t.Fatalf("incomplete accounting: %+v", summary)
	}
	for _, g := range []Group{A, B, C} {
		a := summary.Groups[g]
		if a.Counts.Scheduled != 12 || a.Counts.TruthUnknown != 12 || a.Counts.Released != 0 || a.WrongRelease.Value != nil {
			t.Fatalf("failure accounting: %+v", a)
		}
	}
	if _, err = Run(context.Background(), root, p, Services{Backend: unusedBackend{p.Execution}, NewModel: func(Slot) (budget.ProfiledModel, error) { t.Fatal("rerun admitted"); return nil, nil }}); err == nil {
		t.Fatal("overwrote experiment")
	}
	if _, err = os.Stat(filepath.Join(root, "resources/registry.json")); err != nil {
		t.Fatal(err)
	}
}

type profileModel struct{ profile budget.Profile }

func (m profileModel) BudgetProfile() (budget.Profile, error) { return m.profile, nil }
func (m profileModel) Complete(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
	panic("model identity mismatch must not dispatch")
}

func TestP1ModelIdentityChangeStopsRemainingSchedule(t *testing.T) {
	p := runnableProtocol(t)
	root := filepath.Join(t.TempDir(), "experiment")
	calls := 0
	other := p.Model
	other.Model = "changed"
	out, err := Run(context.Background(), root, p, Services{Backend: unusedBackend{p.Execution}, NewModel: func(Slot) (budget.ProfiledModel, error) { calls++; return profileModel{other}, nil }})
	if err == nil || calls != 1 {
		t.Fatalf("shared drift did not stop: %d %v", calls, err)
	}
	if out.Slots[0].State != "failed_before_run" || out.Slots[1].State != "not_started" {
		t.Fatalf("wrong terminal rows: %+v", out.Slots[:2])
	}
	summary, err := Collect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if summary.AllSlotsObserved || summary.ExecutionComplete || len(summary.Results) != 36 {
		t.Fatal("unstarted slots represented as completed")
	}
}

func TestP1AmountKeepsDisjointCachesAndUnknownReservations(t *testing.T) {
	price := &Price{Currency: "USD", Source: "https://example.com/pricing", RecordedAt: "2026-10-31T00:00:00Z", InputPerMillion: 1, CachedInputPerMillion: .1, OutputPerMillion: 3}
	hit, miss := int64(600000), int64(400000)
	calls := []budget.Call{{TargetID: "one", State: budget.StateSettled, Usage: &agent.Usage{InputTokens: 1000000, OutputTokens: 200000, TotalTokens: 1200000, CacheHitTokens: &hit, CacheMissTokens: &miss}}, {TargetID: "other", State: budget.StateUnknown, ReservedTokens: 999999}}
	amount, err := Amount(price, calls, "one")
	if err != nil || amount == nil || *amount != 1.06 {
		t.Fatalf("cache double counting: %v %v", amount, err)
	}
	calls[0].Usage.CacheMissTokens = nil
	if amount, err = Amount(price, calls, "one"); err != nil || amount != nil {
		t.Fatal("missing cache accounted as free")
	}
	calls[0].State = budget.StateUnknown
	if amount, err = Amount(price, calls, "one"); err != nil || amount != nil {
		t.Fatal("unknown call accounted as free")
	}
}

func TestP1InitialRunningReceiptRemainsUnknownWithoutBreakingDenominator(t *testing.T) {
	p := runnableProtocol(t)
	root := filepath.Join(t.TempDir(), "experiment")
	out, err := Run(context.Background(), root, p, Services{Backend: unusedBackend{p.Execution}, NewModel: func(Slot) (budget.ProfiledModel, error) { return nil, errors.New("unavailable") }})
	if err != nil {
		t.Fatal(err)
	}
	out.Slots[0].State = "starting"
	if err = gate.WriteJSON(filepath.Join(root, "execution.json"), out); err != nil {
		t.Fatal(err)
	}
	if err = gate.WriteJSON(filepath.Join(AttemptDirectory(root, p.Schedule[0].ID), "experiment-attempt.json"), runner.ExperimentAttempt{SchemaVersion: 1, Record: runner.Record{Status: runner.StatusRunning}}); err != nil {
		t.Fatal(err)
	}
	s, err := Collect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Results) != 36 || s.Results[0].Truth != "inconclusive" || s.Results[0].Released || s.ExecutionComplete || len(s.Gaps) == 0 {
		t.Fatalf("interrupted receipt became complete: %+v", s)
	}
}

func TestP1LaunchEvidenceMustPersistBeforeAnyModelConstruction(t *testing.T) {
	p := runnableProtocol(t)
	root := filepath.Join(t.TempDir(), "experiment")
	_, err := Run(context.Background(), root, p, Services{Backend: unusedBackend{p.Execution}, NewModel: func(Slot) (budget.ProfiledModel, error) {
		t.Fatal("model constructed before launch evidence was durable")
		return nil, nil
	}, Prepare: func(context.Context, string, string) error { return errors.New("launch evidence write failed") }})
	if err == nil {
		t.Fatal("failed launch persistence did not stop dispatch")
	}
	summary, err := Collect(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ExecutionComplete || summary.AllSlotsObserved || len(summary.Results) != 36 {
		t.Fatal("unstarted schedule lost after preparation failure")
	}
	for _, r := range summary.Results {
		if r.State != "not_started" {
			t.Fatal("admitted before launch evidence")
		}
	}
}
