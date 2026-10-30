package runner

import (
	"context"
	"errors"
	"testing"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/execution"
)

// Failures originate in the production Runner; only supplier/command execution
// is replaced. This checks that replay and batch attribution use the same cause.
func TestFailureAttributionAgreesForActualRunnerFailures(t *testing.T) {
	for _, test := range []struct {
		name     string
		command  string
		facility bool
		want     string
	}{
		{"baseline compile", "build", false, "compile"},
		{"baseline test", "test", false, "test"},
		{"provider", "", false, "model"},
		{"baseline facility", "", true, "infrastructure"},
	} {
		t.Run(test.name, func(t *testing.T) {
			failure := errors.New("controlled " + test.name + " failure")
			modelCalls, nonzeroCommands, buildPasses := 0, 0, 0
			config := configForTest(t, modelFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
				modelCalls++
				if test.command != "" || test.facility {
					t.Fatal("failed baseline dispatched model")
				}
				return agent.Reply{}, failure
			}))
			profile, err := lookupProfile(ProfileT1T2)
			if err != nil {
				t.Fatal(err)
			}
			config.Backend = baselineBackend{run: func(_ context.Context, request execution.Request) (execution.Result, error) {
				if test.facility {
					return execution.Result{}, failure
				}
				if len(request.Args) < 2 {
					t.Fatalf("unexpected command: %v", request.Args)
				}
				if request.Args[1] == test.command {
					nonzeroCommands++
					return execution.Result{ExitCode: 1, Stderr: []byte("controlled nonzero " + test.command)}, nil
				}
				if request.Args[1] == "build" {
					buildPasses++
				}
				return execution.Result{ExitCode: 0, Stdout: passedBaselineEvents(profile)}, nil
			}}
			record, runErr := Run(context.Background(), config)
			if runErr == nil || record.Status != StatusFailed || record.Verdict != "" {
				t.Fatalf("record=%+v err=%v", record, runErr)
			}
			if test.command != "" && nonzeroCommands != 1 {
				t.Fatalf("nonzero commands=%d", nonzeroCommands)
			}
			if test.command == "test" && buildPasses == 0 {
				t.Fatal("test failure did not follow successful build")
			}
			if test.command == "" && !errors.Is(runErr, failure) {
				t.Fatalf("lost cause: %v", runErr)
			}
			if test.want == "model" && modelCalls != 1 {
				t.Fatalf("provider calls=%d", modelCalls)
			}
			replayed, err := Replay(context.Background(), record.Directory, "")
			if err != nil {
				t.Fatal(err)
			}
			summary, err := SummarizeFailures(context.Background(), []string{record.Directory})
			if err != nil {
				t.Fatal(err)
			}
			if replayed.FailureCategory != test.want || summary.Runs != 1 || summary.Failures != 1 || summary.Incomplete != 0 || len(summary.Outcomes) != 1 || summary.Outcomes[0].Category != test.want {
				t.Fatalf("replay=%+v summary=%+v", replayed, summary)
			}
		})
	}
}
