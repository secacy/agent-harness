//go:build darwin || linux

package campaign

import (
	"context"
	"errors"
	"testing"

	"github.com/secacy/agent-harness/internal/writer"
)

func TestCampaignWriterLifecycle(t *testing.T) {
	for _, mode := range []string{"completed", "prepare-error", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			config := testConfig(t, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			assertHeld := func() {
				l, err := writer.Open(config.Directory)
				if !errors.Is(err, writer.ErrBusy) {
					if l != nil {
						_ = l.Close()
					}
					t.Errorf("campaign did not hold writer ownership: %v", err)
				}
			}
			config.Prepare = func(context.Context, Artifact) error {
				assertHeld()
				if mode == "prepare-error" {
					return errors.New("controlled preparation failure")
				}
				if mode == "cancel" {
					cancel()
				}
				return nil
			}
			execute := config.Execute
			config.Execute = func(ctx context.Context, target Target, services Services) (Attempt, error) {
				assertHeld()
				return execute(ctx, target, services)
			}
			record, _, err := Run(ctx, config)
			if (mode == "completed") != (err == nil) || record.SchemaVersion != 2 || record.WriterProtocol != writer.Protocol {
				t.Fatal(record, err)
			}
			l, err := writer.Open(config.Directory)
			if err != nil {
				t.Fatal("campaign did not release writer ownership", err)
			}
			_ = l.Close()
		})
	}
}
