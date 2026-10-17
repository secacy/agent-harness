package checks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Prepare inputs outside the scenario tree, with no cases/manifest or oracle.
// Patch application is deliberately a caller responsibility in this test too.
func preparedRequest(t *testing.T) EvaluationRequest {
	t.Helper()
	scenario, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	baseline := filepath.Join(root, "inputs/baseline")
	candidate := filepath.Join(root, "inputs/candidate")
	if err := copyTree(filepath.Join(scenario, "fixtures"), baseline); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(filepath.Join(scenario, "fixtures"), candidate); err != nil {
		t.Fatal(err)
	}
	spec, err := os.ReadFile(filepath.Join(scenario, "specs/v0.1.0.md"))
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "spec.md", string(spec))
	cacheDir := os.Getenv("GOCACHE")
	if cacheDir == "" {
		cacheDir = filepath.Join(root, "cache")
	}
	req := EvaluationRequest{
		BaselineDir:  filepath.Join(baseline, "demo-orders"),
		CandidateDir: filepath.Join(candidate, "demo-orders"),
		SpecPath:     filepath.Join(root, "spec.md"),
		EvidenceDir:  filepath.Join(root, "evidence"),
		CacheDir:     cacheDir,
	}
	meta, _, err := command(context.Background(), cacheDir, req.CandidateDir, filepath.Join(root, "patch.log"), "git", "apply", filepath.Join(scenario, "cases/patches/correct.patch"))
	if err != nil || meta.ExitCode != 0 {
		t.Fatalf("prepare patch: %+v %v", meta, err)
	}
	return req
}

func TestEvaluatePreparedCandidateWithoutManifest(t *testing.T) {
	req := preparedRequest(t)
	before, err := snapshot(filepath.Dir(filepath.Dir(req.CandidateDir)))
	if err != nil {
		t.Fatal(err)
	}
	result, err := Evaluate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Verdict != "ReadyForReview" || result.SpecSHA256 != SupportedSpecSHA256 || result.SpecVersion != "v0.1.0" {
		t.Fatalf("unexpected evaluation: %+v", result)
	}
	for _, name := range checkNames {
		if result.Checks[name].Status != "pass" {
			t.Fatalf("%s: %+v", name, result.Checks[name])
		}
	}
	after, err := snapshot(filepath.Dir(filepath.Dir(req.CandidateDir)))
	if err != nil {
		t.Fatal(err)
	}
	if treeDigest(before) != treeDigest(after) {
		t.Fatal("evaluation changed input modules")
	}
	if len(result.Commands) == 0 {
		t.Fatal("missing command evidence")
	}
	evidenceDir, err := canonicalPath(req.EvidenceDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, meta := range result.Commands {
		if !within(meta.Directory, evidenceDir) {
			t.Fatalf("command executed against an input: %+v", meta)
		}
		if _, err := os.Stat(meta.Log); err != nil {
			t.Fatal(err)
		}
	}
	// A real compiler failure is a candidate verdict, not an execution error.
	writeTestFile(t, req.CandidateDir, "internal/inventory/broken.go", "package inventory\nvar _ = missingIdentifier\n")
	req.EvidenceDir += "-broken"
	result, err = Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("compiler failure became execution failure: %v", err)
	}
	if result.Verdict != "Rejected" || result.Checks["build"].Status != "fail" || result.Checks["context_propagation"].Status != "blocked" {
		t.Fatalf("unexpected broken-candidate result: %+v", result)
	}
	// Test-only compiler diagnostics go to stderr. They must stay in the log
	// without corrupting the JSON event stream or becoming infrastructure errors.
	if err := os.Remove(filepath.Join(req.CandidateDir, "internal/inventory/broken.go")); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, req.CandidateDir, "internal/inventory/broken_test.go", "package inventory\nvar _ = missingTestIdentifier\n")
	req.EvidenceDir += "-test-compile"
	result, err = Evaluate(context.Background(), req)
	if err != nil || result.Verdict != "Rejected" || result.Checks["build"].Status != "pass" || result.Checks["baseline_tests"].Status != "fail" {
		t.Fatalf("test compile failure: %+v %v", result, err)
	}
	log, err := os.ReadFile(filepath.Join(req.EvidenceDir, "business-tests.log"))
	if err != nil || !strings.Contains(string(log), "missingTestIdentifier") {
		t.Fatalf("missing compiler diagnostics: %s %v", log, err)
	}
}

func TestEvaluateRejectsUnsupportedSpecAndUnsafeEvidence(t *testing.T) {
	req := preparedRequest(t)
	t.Run("unsupported-spec", func(t *testing.T) {
		bad := req
		bad.SpecPath = filepath.Join(filepath.Dir(req.SpecPath), "unsupported.md")
		if err := os.WriteFile(bad.SpecPath, []byte("v0.1.0 but different acceptance rules"), 0644); err != nil {
			t.Fatal(err)
		}
		result, err := Evaluate(context.Background(), bad)
		if err == nil || !strings.Contains(err.Error(), "unsupported") || result.Verdict != "" || len(result.Commands) != 0 {
			t.Fatalf("%+v %v", result, err)
		}
		if _, err := os.Stat(bad.EvidenceDir); !os.IsNotExist(err) {
			t.Fatal("unsupported specification created evidence")
		}
	})
	for _, mode := range []string{"candidate", "alias", "existing-evidence"} {
		t.Run(mode, func(t *testing.T) {
			bad := req
			switch mode {
			case "candidate":
				bad.EvidenceDir = filepath.Join(req.CandidateDir, "logs")
			case "alias":
				alias := filepath.Join(t.TempDir(), "candidate-link")
				if err := os.Symlink(req.CandidateDir, alias); err != nil {
					t.Fatal(err)
				}
				bad.EvidenceDir = filepath.Join(alias, "logs")
			case "existing-evidence":
				bad.EvidenceDir = t.TempDir()
				writeTestFile(t, bad.EvidenceDir, "report.json", "existing evidence")
			}
			result, err := Evaluate(context.Background(), bad)
			if err == nil || result.Verdict != "" || len(result.Commands) != 0 {
				t.Fatalf("%+v %v", result, err)
			}
		})
	}
}

func TestEvaluateExecutionErrorsHaveNoVerdict(t *testing.T) {
	t.Run("cancelled-before-start", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		result, err := Evaluate(ctx, EvaluationRequest{})
		if !errors.Is(err, context.Canceled) || result.Verdict != "" || len(result.Commands) != 0 {
			t.Fatalf("%+v %v", result, err)
		}
	})
	t.Run("baseline-failed", func(t *testing.T) {
		req := preparedRequest(t)
		writeTestFile(t, req.BaselineDir, "broken.go", "invalid Go source")
		result, err := Evaluate(context.Background(), req)
		if err == nil || !strings.Contains(err.Error(), "baseline tests failed") || result.Verdict != "" {
			t.Fatalf("%+v %v", result, err)
		}
		if len(result.Commands) != 1 {
			t.Fatalf("candidate commands ran despite invalid baseline: %+v", result.Commands)
		}
	})
	t.Run("command-unavailable", func(t *testing.T) {
		req := preparedRequest(t)
		t.Setenv("PATH", t.TempDir())
		result, err := Evaluate(context.Background(), req)
		if err == nil || result.Verdict != "" || len(result.Commands) != 1 {
			t.Fatalf("%+v %v", result, err)
		}
		if result.Commands[0].ExitCode != -1 {
			t.Fatalf("unstarted command reported successful exit: %+v", result.Commands[0])
		}
		if _, err := os.Stat(result.Commands[0].Log); err != nil {
			t.Fatal(err)
		}
	})
}

func TestCommandCancellationHelper(t *testing.T) {
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--evaluation-command-helper" {
		return
	}
	ready := os.Args[len(os.Args)-1]
	if err := os.WriteFile(ready, []byte("ready"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Log("command is running")
	time.Sleep(time.Minute)
}

func TestCommandHonorsParentCancellationAndDeadline(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			ready := filepath.Join(dir, "ready")
			duration := 5 * time.Second
			if mode == "deadline" {
				duration = 2 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), duration)
			defer cancel()
			type outcome struct {
				meta Command
				err  error
			}
			done := make(chan outcome, 1)
			go func() {
				meta, _, err := command(ctx, dir, dir, filepath.Join(dir, "command.log"), os.Args[0], "-test.run=^TestCommandCancellationHelper$", "--", "--evaluation-command-helper", ready)
				done <- outcome{meta, err}
			}()
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				if _, err := os.Stat(ready); err == nil {
					break
				}
				select {
				case <-ticker.C:
				case out := <-done:
					t.Fatalf("command did not reach running state: %+v", out)
				case <-ctx.Done():
					t.Fatal("helper did not start")
				}
			}
			if mode == "cancel" {
				cancel()
			}
			select {
			case out := <-done:
				want := context.Canceled
				if mode == "deadline" {
					want = context.DeadlineExceeded
				}
				if !errors.Is(out.err, want) || out.meta.ExitCode == 0 {
					t.Fatalf("%+v", out)
				}
				if out.meta.Cancelled != (mode == "cancel") || out.meta.TimedOut != (mode == "deadline") {
					t.Fatalf("wrong cancellation metadata: %+v", out.meta)
				}
				if _, err := os.Stat(out.meta.Log); err != nil {
					t.Fatal(err)
				}
			case <-time.After(6 * time.Second):
				t.Fatal("command ignored parent context")
			}
		})
	}
}

func TestOfflineEnvDoesNotInheritServiceCredentials(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "must-not-reach-tests")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GOFLAGS", "-mod=mod")
	env := map[string]string{}
	for _, item := range offlineEnv(t.TempDir(), t.TempDir()) {
		key, value, _ := strings.Cut(item, "=")
		env[key] = value
	}
	if _, exists := env["DEEPSEEK_API_KEY"]; exists {
		t.Fatal("model API key was inherited")
	}
	if _, exists := env["GIT_CONFIG_COUNT"]; exists {
		t.Fatal("host Git configuration was inherited")
	}
	if env["GOFLAGS"] != "-mod=readonly" || env["GOENV"] != "off" || env["PATH"] != os.Getenv("PATH") {
		t.Fatalf("invalid controlled environment: %v", env)
	}
}
