package workouts

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

type backendFunc func(context.Context, execution.Request) (execution.Result, error)

func (backendFunc) Info() execution.Info { return execution.Local{}.Info() }
func (b backendFunc) Run(ctx context.Context, req execution.Request) (execution.Result, error) {
	return b(ctx, req)
}

func TestInvalidEvidenceHasNoVerdict(t *testing.T) {
	fixtures, err := LoadFixtures("../l2/data/rpcx-l2-workouts-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing-common-events", "common-build-failure", "mutated-common", "mutated-copy", "mutated-input", "mutated-modules"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			for _, name := range []string{"baseline", "candidate"} {
				if err := gate.CopyFiles(filepath.Join(root, name), fixtures); err != nil {
					t.Fatal(err)
				}
			}
			spec := filepath.Join(root, "spec.md")
			if err := gate.Write(spec, Spec()); err != nil {
				t.Fatal(err)
			}
			backend := backendFunc(func(_ context.Context, req execution.Request) (execution.Result, error) {
				path := ""
				switch mode {
				case "common-build-failure":
					return execution.Result{ExitCode: 1}, nil
				case "mutated-common":
					path = filepath.Join(req.Directory, "go.mod")
				case "mutated-copy":
					path = filepath.Join(req.Directory, "LICENSE")
				case "mutated-input":
					path = filepath.Join(root, "baseline/trainings/LICENSE")
				case "mutated-modules":
					path = filepath.Join(req.ReadOnlyDirs[len(req.ReadOnlyDirs)-1], "unexpected")
				}
				if path != "" {
					if err := os.WriteFile(path, []byte("changed\n"), 0644); err != nil {
						t.Fatal(err)
					}
				}
				return execution.Result{ExitCode: 0}, nil
			})
			result, err := Evaluate(context.Background(), checks.EvaluationRequest{BaselineDir: filepath.Join(root, "baseline/trainings"), CandidateDir: filepath.Join(root, "candidate/trainings"), SpecPath: spec, EvidenceDir: filepath.Join(root, "evidence"), Backend: backend})
			if err == nil || result.Verdict != "" {
				t.Fatalf("invalid evidence produced verdict: %s %v", result.Verdict, err)
			}
		})
	}
}
