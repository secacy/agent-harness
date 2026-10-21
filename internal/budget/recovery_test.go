package budget

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"
)

func recoverySourceSnapshot(t *testing.T) Snapshot {
	t.Helper()
	l := newLedger(t, 10, 1000)
	bind(t, l, "a")
	bind(t, l, "b")
	settled := reserve(t, l, "a")
	if err := l.settle(settled.ID, usage(15, 5), "reply"); err != nil {
		t.Fatal(err)
	}
	reserve(t, l, "a")
	unknown := reserve(t, l, "b")
	if err := l.settle(unknown.ID, nil, "model_error"); err != nil {
		t.Fatal(err)
	}
	return l.Snapshot()
}

func TestRecoverSnapshotPreservesOccupancyAndProvenance(t *testing.T) {
	s := recoverySourceSnapshot(t)
	before, _ := json.Marshal(s)
	at := time.Date(2026, 10, 20, 5, 0, 0, 0, time.UTC)
	next, err := RecoverSnapshot(s, "recovery-1", at)
	if err != nil {
		t.Fatal(err)
	}
	if next.Totals != (Totals{Calls: 3, KnownTokens: 20, UnknownTokens: 200}) || next.Targets["a"].Totals != (Totals{Calls: 2, KnownTokens: 20, UnknownTokens: 100}) || next.Targets["b"].Totals != (Totals{Calls: 1, UnknownTokens: 100}) {
		t.Fatal(next)
	}
	if next.SchemaVersion != 2 || next.Revision != s.Revision+1 || next.RecoveryRevision != next.Revision || next.Targets["a"].RunID != "" || !reflect.DeepEqual(next.Targets["a"].RunIDs, []string{"run-a"}) {
		t.Fatal(next)
	}
	if next.Calls[1].RecoveryID != "recovery-1" || next.Calls[1].Outcome != "interrupted" || next.Calls[1].UpdatedAt != at || !reflect.DeepEqual(next.Calls[0], s.Calls[0]) || !reflect.DeepEqual(next.Calls[2], s.Calls[2]) {
		t.Fatal(next.Calls)
	}
	again, err := RecoverSnapshot(next, "recovery-1", at.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(again, next) {
		t.Fatal("same recovery was not idempotent", err)
	}
	after, _ := json.Marshal(s)
	if string(before) != string(after) {
		t.Fatal("input snapshot was modified")
	}
	again.Targets["a"].RunIDs[0] = "changed"
	again.RecoveryIDs[0] = "changed"
	if next.Targets["a"].RunIDs[0] != "run-a" || next.RecoveryIDs[0] != "recovery-1" {
		t.Fatal("snapshot results share mutable slices")
	}
}

func TestRecoverySnapshotSupportsMultipleRunsWithoutReset(t *testing.T) {
	s, err := RecoverSnapshot(recoverySourceSnapshot(t), "first", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	a := s.Targets["a"]
	a.RunIDs = append(a.RunIDs, "run-a2")
	a.RunID = "run-a2"
	a.Totals.Calls++
	a.Totals.KnownTokens += 10
	s.Targets["a"] = a
	s.Totals.Calls++
	s.Totals.KnownTokens += 10
	s.Revision++
	c := s.Calls[0]
	c.ID, c.RunID, c.Usage = "call-new", "run-a2", usage(7, 3)
	s.Calls = append(s.Calls, c)
	if err := ValidateSnapshot(s); err != nil {
		t.Fatal(err)
	}
	if _, err := RecoverSnapshot(s, "first", time.Now()); !errors.Is(err, ErrIntegrity) {
		t.Fatal("old checkpoint ID was reused after new activity", err)
	}
	next, err := RecoverSnapshot(s, "second", time.Now())
	if err != nil || next.Totals != (Totals{Calls: 4, KnownTokens: 30, UnknownTokens: 200}) || !reflect.DeepEqual(next.Targets["a"].RunIDs, []string{"run-a", "run-a2"}) || next.Targets["a"].RunID != "" {
		t.Fatal(next, err)
	}
	if _, err := RecoverSnapshot(next, "first", time.Now()); !errors.Is(err, ErrIntegrity) {
		t.Fatal("older recovery ID was accepted", err)
	}
}

func TestRecoveryRejectsCorruptOrHaltedSnapshots(t *testing.T) {
	base := recoverySourceSnapshot(t)
	for _, mode := range []string{"campaign-total", "target-total", "duplicate-call", "missing-run", "negative", "overflow", "halt", "version", "usage", "provenance", "revision", "duplicate-run"} {
		t.Run(mode, func(t *testing.T) {
			s := cloneSnapshot(base)
			switch mode {
			case "campaign-total":
				s.Totals.UnknownTokens = 0
			case "target-total":
				a := s.Targets["a"]
				a.Totals.Calls = 0
				s.Targets["a"] = a
			case "duplicate-call":
				s.Calls[1].ID = s.Calls[0].ID
			case "missing-run":
				s.Calls[1].RunID = "other"
			case "negative":
				s.Totals.KnownTokens = -1
			case "overflow":
				s.Totals.UnknownTokens = math.MaxInt64
			case "halt":
				s.HaltReason = "persistence uncertain"
			case "version":
				s.SchemaVersion = 99
			case "usage":
				s.Calls[0].Usage.TotalTokens = 1
			case "provenance":
				s.Calls[2].RecoveryID, s.Calls[2].Outcome = "unrecorded", "interrupted"
			case "revision":
				s.Revision = math.MaxInt64
			case "duplicate-run":
				s, _ = RecoverSnapshot(s, "first", time.Now())
				a := s.Targets["a"]
				a.RunIDs = append(a.RunIDs, a.RunIDs[0])
				s.Targets["a"] = a
			}
			if _, err := RecoverSnapshot(s, "recovery", time.Now()); !errors.Is(err, ErrIntegrity) {
				t.Fatal("invalid snapshot accepted", err)
			}
		})
	}
}
