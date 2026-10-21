package campaign

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/task"
)

func TestCampaignOriginalGenerationDeadlineIsNotExtended(t *testing.T) {
	config := testConfig(t, 1)
	deadline := time.Now().Add(200 * time.Millisecond).UTC()
	config.Plan.Recovery = &RecoveryPolicy{MaxAttempts: 2, GenerationDeadline: deadline}
	config.Execute = func(ctx context.Context, _ Target, _ Services) (Attempt, error) {
		actual, ok := ctx.Deadline()
		if !ok || !actual.Equal(deadline) {
			t.Error("original absolute deadline not inherited", actual)
		}
		<-ctx.Done()
		return Attempt{Status: Failed, FailureCode: "deadline"}, ctx.Err()
	}
	record, _, err := Run(context.Background(), config)
	if !errors.Is(err, context.DeadlineExceeded) || record.Status != Cancelled {
		t.Fatal(record, err)
	}
}

func TestRecoveryReceiptsPreserveOriginalPermission(t *testing.T) {
	now := time.Now().UTC()
	for _, mode := range []string{"allowed", "deadline", "one-attempt", "no-policy", "completed", "unknown", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			p := Plan{ID: "batch", Recovery: &RecoveryPolicy{MaxAttempts: 2, GenerationDeadline: now.Add(time.Minute)}, Targets: []Target{{Input: task.Target{ID: "a"}}}}
			c := RecoveryCheckpoint{CampaignID: p.ID, After: RecoveryState{Targets: []RecoveredTarget{{ID: "a", Attempts: []HistoricalAttempt{{RunID: "first", Resolution: "Interrupted"}}}}}}
			selected := []string{"a"}
			switch mode {
			case "deadline":
				p.Recovery.GenerationDeadline = now
			case "one-attempt":
				p.Recovery.MaxAttempts = 1
			case "no-policy":
				p.Recovery = nil
			case "completed":
				c.After.Targets[0].Current = &Attempt{Status: Completed, Verdict: "NeedsAttention"}
			case "unknown":
				selected[0] = "unknown"
			case "duplicate":
				selected = append(selected, "a")
			}
			r, err := RecoveryReceipts(p, c, selected, now)
			if mode == "allowed" {
				if err != nil || len(r) != 1 || r[0].Ordinal != 2 || r[0].PreviousRunID != "first" {
					t.Fatal(r, err)
				}
			} else if err == nil {
				t.Fatal("original permission was expanded")
			}
		})
	}
}
