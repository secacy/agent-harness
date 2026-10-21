package runner

import (
	"context"
	"errors"
	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/ghcli"
	"path/filepath"
	"testing"
	"time"
)

type ghcliFailureBackend struct {
	run func(context.Context, execution.Request) (execution.Result, error)
}

func (ghcliFailureBackend) Info() execution.Info { return execution.Info{Kind: execution.KindDocker} }
func (b ghcliFailureBackend) Run(ctx context.Context, r execution.Request) (execution.Result, error) {
	return b.run(ctx, r)
}

func ghcliConfig(t *testing.T) Config {
	t.Helper()
	c := configForTest(t, modelFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
		t.Fatal("invalid preparation reached model")
		return agent.Reply{}, nil
	}))
	target, err := TargetFromGHCLI("application", filepath.Join(c.ScenarioDir, "l2/data/rpcx-l2-ghcli-v1/fixtures"), filepath.Join(c.ScenarioDir, "ghcli/specs/v0.6.0.md"))
	if err != nil {
		t.Fatal(err)
	}
	c.Target = &target
	c.ScenarioDir = ""
	c.Timeout = 2 * time.Minute
	return c
}

func TestGHCLIProfileBindingAndDockerRestriction(t *testing.T) {
	c := ghcliConfig(t)
	p, err := lookupProfile(ProfileGHCLI)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.validate(*c.Target); err != nil {
		t.Fatal(err)
	}
	if _, _, err = readTarget(context.Background(), *c.Target, p); err != nil {
		t.Fatal(err)
	}
	required, err := ghcli.RequiredTests()
	if err != nil || len(required) != 11 {
		t.Fatal("required inventory", err)
	}
	if _, err = baselineEvents(passedBaselineEvents(p), required); err != nil {
		t.Fatal(err)
	}
	if record, err := Run(context.Background(), c); !errors.Is(err, ErrInvalidConfig) || record.Directory != "" {
		t.Fatal("trusted local application profile accepted")
	}
	c.TrustedLocal = false
	c.Backend = ghcliFailureBackend{run: func(context.Context, execution.Request) (execution.Result, error) {
		t.Fatal("bad identity executed")
		return execution.Result{}, nil
	}}
	c.Target.Repository.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	if record, err := Run(context.Background(), c); !errors.Is(err, ErrInvalidConfig) || record.Directory != "" {
		t.Fatal("wrong identity accepted")
	}
}

func TestGHCLIBaselineFailureNeverCallsModel(t *testing.T) {
	c := ghcliConfig(t)
	c.TrustedLocal = false
	calls := 0
	c.Backend = ghcliFailureBackend{run: func(ctx context.Context, req execution.Request) (execution.Result, error) {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 30*time.Second {
			t.Fatal("baseline command limit changed")
		}
		if len(req.ReadOnlyDirs) != 3 {
			t.Fatal("expected isolated modules and dependency cache")
		}
		return execution.Result{ExitCode: 1, Stderr: []byte("injected baseline failure")}, nil
	}}
	record, err := Run(context.Background(), c)
	report := readJSON[Report](t, filepath.Join(record.Directory, "artifacts/report.json"))
	if report.Validation != nil || report.AgentRecord != "" || report.Baseline == nil || report.Baseline.Status == "pass" {
		t.Fatal("baseline failure continued to generation")
	}
	if !errors.Is(err, ErrBaseline) || record.Status != StatusFailed || calls != 1 {
		t.Fatalf("baseline failure did not stop: %s %v commands=%d", record.Status, err, calls)
	}
}
