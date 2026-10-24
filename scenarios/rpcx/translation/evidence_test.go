package translation

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

type backendFunc func(context.Context, execution.Request) (execution.Result, error)

func (backendFunc) Info() execution.Info { return execution.Local{}.Info() }
func (b backendFunc) Run(ctx context.Context, req execution.Request) (execution.Result, error) {
	return b(ctx, req)
}

// 在最后一条真实验证命令结束后注入故障，检查发布前取消与证明写入失败。
func TestScopePublicationFailureHasNoVerdict(t *testing.T) {
	if os.Getenv("HARNESS_TRANSLATION_SCOPE_FAILURES") != "1" {
		t.Skip("set HARNESS_TRANSLATION_SCOPE_FAILURES=1 for late publication failures")
	}
	fixtures, err := LoadFixtures("../l2/data/rpcx-l2-translation-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"scope-write", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			for _, side := range []string{"baseline", "candidate"} {
				if err := gate.CopyFiles(filepath.Join(root, side), fixtures); err != nil {
					t.Fatal(err)
				}
			}
			if err := gate.CopyFiles(filepath.Join(root, "candidate/translation"), controlCandidate(t, "correct")); err != nil {
				t.Fatal(err)
			}
			spec := filepath.Join(root, "spec.md")
			if err := gate.Write(spec, Spec()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
			defer cancel()
			injected := false
			backend := backendFunc(func(ctx context.Context, req execution.Request) (execution.Result, error) {
				result, err := (execution.Local{}).Run(ctx, req)
				if err == nil && slices.Contains(req.Args, "-run=^TestHarnessTranslationExpiry$") && slices.Contains(req.Args, "-tags=rpcx_alternate") {
					injected = true
					if mode == "cancel" {
						cancel()
					} else if err := os.Mkdir(filepath.Join(root, "evidence/scope-proof.json"), 0755); err != nil {
						t.Fatal(err)
					}
				}
				return result, err
			})
			result, err := EvaluateScope(ctx, checks.EvaluationRequest{BaselineDir: filepath.Join(root, "baseline/translation"), CandidateDir: filepath.Join(root, "candidate/translation"), SpecPath: spec, EvidenceDir: filepath.Join(root, "evidence"), Backend: backend})
			if !injected || err == nil || result.Verdict != "" {
				t.Fatalf("late failure produced a verdict or was not injected: injected=%t verdict=%s err=%v", injected, result.Verdict, err)
			}
		})
	}
}

func TestInvalidEvidenceHasNoVerdict(t *testing.T) {
	fixtures, err := LoadFixtures("../l2/data/rpcx-l2-translation-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing-selection", "mutated-copy", "mutated-input", "mutated-modules"} {
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
				case "mutated-copy":
					path = filepath.Join(req.Directory, "LICENSE")
				case "mutated-input":
					path = filepath.Join(root, "baseline/translation/LICENSE")
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
			result, err := Evaluate(context.Background(), checks.EvaluationRequest{BaselineDir: filepath.Join(root, "baseline/translation"), CandidateDir: filepath.Join(root, "candidate/translation"), SpecPath: spec, EvidenceDir: filepath.Join(root, "evidence"), Backend: backend})
			if err == nil || result.Verdict != "" {
				t.Fatalf("invalid evidence produced verdict: %s %v", result.Verdict, err)
			}
		})
	}
}
