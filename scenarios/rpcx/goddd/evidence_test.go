package goddd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
)

type backendFunc func(context.Context, execution.Request) (execution.Result, error)

func (b backendFunc) Info() execution.Info { return execution.Local{}.Info() }
func (b backendFunc) Run(ctx context.Context, r execution.Request) (execution.Result, error) {
	return b(ctx, r)
}

func TestHostModulesAreBoundBeforeAndAfterExecution(t *testing.T) {
	for _, mode := range []string{"unchanged", "before", "during"} {
		t.Run(mode, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "rpcx-goddd-modules")
			calls := 0
			backend, err := PrepareBackend(context.Background(), root, backendFunc(func(_ context.Context, r execution.Request) (execution.Result, error) {
				calls++
				if len(r.Args) < 3 || !strings.HasPrefix(r.Args[1], "GOMODCACHE=") || len(r.ReadOnlyDirs) != 1 || r.ReadOnlyDirs[0] != root {
					t.Fatalf("missing environment: %+v", r)
				}
				if mode == "during" {
					if err := os.WriteFile(filepath.Join(root, "extra"), []byte("changed"), 0644); err != nil {
						t.Fatal(err)
					}
				}
				return execution.Result{ExitCode: 0}, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			if mode == "before" {
				if err := os.WriteFile(filepath.Join(root, "extra"), []byte("changed"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			_, err = backend.Run(context.Background(), execution.Request{Args: []string{"go", "test", "./..."}})
			if (err == nil) != (mode == "unchanged") {
				t.Fatalf("integrity error=%v", err)
			}
			want := 1
			if mode == "before" {
				want = 0
			}
			if calls != want {
				t.Fatalf("calls=%d", calls)
			}
			if _, err = PrepareBackend(context.Background(), root, execution.Local{}); err == nil {
				t.Fatal("existing materials reused")
			}
		})
	}
}

func TestTestEvidenceRequiresCompleteEvents(t *testing.T) {
	events := func(rows ...map[string]string) []byte {
		var data []byte
		for _, row := range rows {
			line, _ := json.Marshal(row)
			data = append(append(data, line...), '\n')
		}
		return data
	}
	start := map[string]string{"Action": "run", "Package": "p", "Test": "TestX/sub"}
	pass := map[string]string{"Action": "pass", "Package": "p", "Test": "TestX/sub"}
	pkg := map[string]string{"Action": "pass", "Package": "p"}
	fail := map[string]string{"Action": "fail", "Package": "p", "Test": "TestX/sub"}
	for _, c := range []struct {
		name string
		data []byte
		exit int
		want string
	}{
		{"complete", events(start, pass, pkg), 0, quality.StatusPass},
		{"no-run", events(pass, pkg), 0, quality.StatusUnknown},
		{"no-package", events(start, pass), 0, quality.StatusUnknown},
		{"duplicate", events(start, pass, pkg, pkg), 0, quality.StatusUnknown},
		{"fail-with-success-exit", events(start, fail, pass, pkg), 0, quality.StatusUnknown},
		{"assertion-failed", events(start, fail), 1, quality.StatusFail},
		{"truncated", []byte(`{"Action":`), 0, quality.StatusUnknown},
	} {
		t.Run(c.name, func(t *testing.T) {
			status, _ := observe(c.data, []string{"p/TestX/sub"}, c.exit)
			if status != c.want {
				t.Fatal(status)
			}
		})
	}
}

func TestVerificationInfrastructureCannotProduceVerdict(t *testing.T) {
	fixtures, err := LoadFixtures("../l2/data/rpcx-l2-goddd-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing-events", "mutated-copy", "mutated-input"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			for _, name := range []string{"baseline", "candidate"} {
				if err := copyFiles(filepath.Join(root, name), fixtures); err != nil {
					t.Fatal(err)
				}
			}
			spec := filepath.Join(root, "spec.md")
			if err := write(spec, Spec()); err != nil {
				t.Fatal(err)
			}
			calls := 0
			backend := backendFunc(func(_ context.Context, r execution.Request) (execution.Result, error) {
				calls++
				if calls == 1 && mode != "missing-events" {
					path := filepath.Join(r.Directory, "cargo.go")
					if mode == "mutated-input" {
						path = filepath.Join(root, "baseline/goddd/cargo.go")
					}
					if err := os.WriteFile(path, []byte("package shipping\n"), 0644); err != nil {
						t.Fatal(err)
					}
				}
				return execution.Result{ExitCode: 0}, nil
			})
			result, err := Evaluate(context.Background(), checks.EvaluationRequest{BaselineDir: filepath.Join(root, "baseline/goddd"), CandidateDir: filepath.Join(root, "candidate/goddd"), SpecPath: spec, EvidenceDir: filepath.Join(root, "evidence"), Backend: backend})
			if err == nil || result.Verdict != "" {
				t.Fatalf("invalid evidence produced verdict: %s %v", result.Verdict, err)
			}
		})
	}
}
