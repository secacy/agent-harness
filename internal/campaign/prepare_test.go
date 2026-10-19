package campaign

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestPreparationPrecedesAdmission(t *testing.T) {
	config := testConfig(t, 2)
	var prepared atomic.Bool
	config.Prepare = func(ctx context.Context, plan Artifact) error {
		data, err := os.ReadFile(plan.Path)
		if err != nil || checksum(data) != plan.SHA256 || !filepath.IsAbs(plan.Path) {
			t.Errorf("preparation did not receive the persisted plan: %v", err)
		}
		if _, err := os.Stat(filepath.Join(config.Directory, "targets")); !os.IsNotExist(err) {
			t.Error("targets admitted before preparation")
		}
		prepared.Store(true)
		return ctx.Err()
	}
	original := config.Execute
	config.Execute = func(ctx context.Context, target Target, services Services) (Attempt, error) {
		if !prepared.Load() {
			t.Error("execution started without preparation")
		}
		return original(ctx, target, services)
	}
	record, _, err := Run(context.Background(), config)
	if err != nil || record.Status != Completed || !prepared.Load() {
		t.Fatalf("prepared campaign failed: %+v %v", record, err)
	}
}

func TestPreparationFailurePreventsAdmission(t *testing.T) {
	for _, mode := range []string{"error", "delete plan", "change plan", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			config := testConfig(t, 2)
			var executions atomic.Int32
			config.Execute = func(context.Context, Target, Services) (Attempt, error) {
				executions.Add(1)
				return Attempt{}, errors.New("unexpected execution")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			config.Prepare = func(_ context.Context, plan Artifact) error {
				switch mode {
				case "delete plan":
					return os.Remove(plan.Path)
				case "change plan":
					return os.WriteFile(plan.Path, []byte("changed"), 0600)
				case "cancel":
					cancel()
					return nil
				default:
					return errors.New("injected publication failure")
				}
			}
			record, _, err := Run(ctx, config)
			wantStatus := Failed
			if mode == "cancel" {
				wantStatus = Cancelled
			}
			if err == nil || record.Status != wantStatus || executions.Load() != 0 {
				t.Fatalf("preparation failure admitted targets: %+v %v", record, err)
			}
			for _, path := range []string{"targets", "budget"} {
				if _, err := os.Stat(filepath.Join(config.Directory, path)); !os.IsNotExist(err) {
					t.Errorf("preparation failure created %s", path)
				}
			}
			if disk := read[Record](t, filepath.Join(config.Directory, "campaign.json")); disk.Status != wantStatus || disk.FinishedAt == nil {
				t.Fatalf("failure was not published: %+v", disk)
			}
		})
	}
}
