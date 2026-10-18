package checks

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/quality"
)

func preparedT3(t *testing.T, patch string) EvaluationRequest {
	t.Helper()
	scene, err := filepath.Abs("../t3")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, side := range []string{"baseline", "candidate"} {
		if err := copyTree(filepath.Join(scene, "fixtures"), filepath.Join(root, side)); err != nil {
			t.Fatal(err)
		}
	}
	spec, err := os.ReadFile(filepath.Join(scene, "specs/v0.2.0.md"))
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "spec.md", string(spec))
	req := EvaluationRequest{BaselineDir: filepath.Join(root, "baseline/demo-orders"), CandidateDir: filepath.Join(root, "candidate/demo-orders"),
		SpecPath: filepath.Join(root, "spec.md"), EvidenceDir: filepath.Join(root, "evidence"), CacheDir: os.Getenv("GOCACHE"), Backend: execution.Local{}}
	command := exec.Command("git", "apply", filepath.Join(scene, "cases/patches", patch+".patch"))
	command.Dir = req.CandidateDir
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("apply T3 patch: %v: %s", err, data)
	}
	return req
}

func changeT3(t *testing.T, path, old, replacement string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.ReplaceAll(string(data), old, replacement)
	if changed == string(data) {
		t.Fatalf("mutation did not match: %s", path)
	}
	if err := os.WriteFile(path, []byte(changed), 0644); err != nil {
		t.Fatal(err)
	}
}

func mutateT3(t *testing.T, req EvaluationRequest, sample string) {
	t.Helper()
	client := filepath.Join(req.CandidateDir, "internal/inventory/client.go")
	switch sample {
	case "opaque-helper":
		changeT3(t, client, "context.WithTimeout(ctx, 30*time.Second)", "rpcDeadline(ctx)")
		file, err := os.OpenFile(client, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, err = file.WriteString("\nfunc rpcDeadline(ctx context.Context) (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, 30*time.Second) }\n")
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	case "rewritten-assertion":
		changeT3(t, filepath.Join(req.CandidateDir, "cmd/server/handler_test.go"), "w.Code != http.StatusCreated", "http.StatusCreated != w.Code")
	case "deleted-assertion":
		changeT3(t, filepath.Join(req.CandidateDir, "cmd/server/handler_test.go"), "if w.Code != http.StatusCreated {\n\t\tt.Fatalf(\"status %d: %s\", w.Code, w.Body)\n\t}", "")
	case "skipped-test":
		changeT3(t, filepath.Join(req.CandidateDir, "internal/inventory/client_test.go"), "func TestReserve(t *testing.T) {", "func TestReserve(t *testing.T) {\n t.Skip(\"disabled\")")
	case "reset-parent":
		changeT3(t, client, "context.WithTimeout(ctx,", "context.WithTimeout(context.Background(),")
	case "swallowed-error":
		changeT3(t, client, `return fmt.Errorf("reserve inventory: %w", err)`, "return nil")
	case "unverified-build-tag", "fail-and-unknown":
		writeTestFile(t, req.CandidateDir, "internal/inventory/extra.go", "//go:build extra\n\npackage inventory\nfunc extra() {}\n")
	case "protected-path":
		file := filepath.Join(req.CandidateDir, "internal/pb/orders.pb.go")
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, append(data, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	case "missed-callsite":
		changeT3(t, filepath.Join(req.CandidateDir, "cmd/server/handler.go"), "h.service.Cancel(r.Context(),", "h.service.Cancel(")
	case "sdk-change":
		file := filepath.Join(filepath.Dir(req.CandidateDir), "rpcx-v2/client.go")
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, append(data, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	case "test-init-exit":
		writeTestFile(t, req.CandidateDir, "cmd/server/exit_test.go", "package main\nimport \"os\"\nfunc init() { os.Exit(0) }\n")
	case "unprobed-branch":
		changeT3(t, filepath.Join(req.CandidateDir, "cmd/server/handler.go"), "func (h *Handler) Place(w http.ResponseWriter, r *http.Request) {", "func (h *Handler) Place(w http.ResponseWriter, r *http.Request) {\n if r.URL.Query().Get(\"mode\") == \"alternate\" { w.WriteHeader(http.StatusTeapot); return }")
	}
}

func TestT3ProductionScope(t *testing.T) {
	for _, sample := range []struct{ name, want string }{
		{"correct-timeout", "pass"}, {"opaque-helper", "unknown"}, {"rewritten-assertion", "pass"},
		{"unverified-build-tag", "unknown"}, {"protected-path", "pass"},
		{"unprobed-branch", "unknown"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			req := preparedT3(t, "correct-timeout")
			mutateT3(t, req, sample.name)
			before, err := readT3Files(context.Background(), req.BaselineDir)
			if err != nil {
				t.Fatal(err)
			}
			after, err := readT3Files(context.Background(), req.CandidateDir)
			if err != nil {
				t.Fatal(err)
			}
			check := requestScope(before, after)
			if check.Status != sample.want {
				t.Fatalf("scope=%+v want %s", check, sample.want)
			}
		})
	}
}

func TestT3RejectsUnsupportedInputsBeforeExecution(t *testing.T) {
	for _, sample := range []string{"no-backend", "baseline-change", "trusted-sdk-change", "output-overlap", "large-file", "symbolic-link"} {
		t.Run(sample, func(t *testing.T) {
			req := preparedT3(t, "correct-timeout")
			req.Backend = failedExecution{cause: errors.New("must not execute")}
			switch sample {
			case "no-backend":
				req.Backend = nil
			case "baseline-change":
				writeTestFile(t, req.BaselineDir, "cmd/server/main.go", "package main\nfunc main(){panic(\"changed\")}\n")
			case "trusted-sdk-change":
				writeTestFile(t, filepath.Dir(req.BaselineDir), "rpcx-v2/client.go", "package rpcx")
			case "output-overlap":
				req.EvidenceDir = filepath.Join(req.CandidateDir, "output")
			case "large-file":
				writeTestFile(t, req.CandidateDir, "large.txt", strings.Repeat("a", 1<<20+1))
			case "symbolic-link":
				if err := os.Symlink("go.mod", filepath.Join(req.CandidateDir, "alias")); err != nil {
					t.Fatal(err)
				}
			}
			r, err := Evaluate(context.Background(), req)
			if err == nil || r.Verdict != "" || len(r.Commands) != 0 {
				t.Fatalf("invalid input executed: %+v %v", r, err)
			}
		})
	}
}

func TestT3ExecutionFailureHasNoVerdict(t *testing.T) {
	for _, cause := range []error{execution.ErrOOM, execution.ErrOutputLimit, context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			req := preparedT3(t, "correct-timeout")
			req.Backend = failedExecution{cause: cause}
			r, err := Evaluate(context.Background(), req)
			if !errors.Is(err, cause) || r.Verdict != "" || len(r.Commands) != 1 {
				t.Fatalf("execution failure became quality: %+v %v", r, err)
			}
			if data, err := os.ReadFile(r.Commands[0].Log); err != nil || !strings.Contains(string(data), "partial evidence") {
				t.Fatal("partial log lost", err)
			}
		})
	}
}

func TestT3BaselineFailureHasNoQualityVerdict(t *testing.T) {
	req := preparedT3(t, "correct-timeout")
	req.Backend = failedExecution{}
	r, err := Evaluate(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "baseline") || r.Verdict != "" || len(r.Commands) != 1 {
		t.Fatalf("ordinary baseline failure became candidate quality: %+v %v", r, err)
	}
}

func TestT3CandidateEventsDoNotInferSuccessFromExitCode(t *testing.T) {
	req := preparedT3(t, "correct-timeout")
	names, err := testNames(req.BaselineDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct{ name, want string }{{"pass", "pass"}, {"skip", "fail"}, {"missing", "unknown"}, {"duplicate", "unknown"}, {"panic", "unknown"}, {"added-failure", "fail"}} {
		t.Run(sample.name, func(t *testing.T) {
			var data []byte
			appendEvent := func(pkg, name, action, output string) {
				encoded, _ := json.Marshal(map[string]string{"Package": pkg, "Test": name, "Action": action, "Output": output})
				data = append(data, append(encoded, '\n')...)
			}
			packages := map[string]bool{}
			for _, id := range sortedPaths(names) {
				at := strings.Index(id, "/Test")
				pkg, name := id[:at], id[at+1:]
				packages[pkg] = true
				if sample.name == "missing" {
					continue
				}
				appendEvent(pkg, name, "run", "")
				action := "pass"
				if sample.name == "skip" {
					action = "skip"
				}
				appendEvent(pkg, name, action, "")
				if sample.name == "duplicate" {
					appendEvent(pkg, name, action, "")
				}
			}
			meta := Command{}
			if sample.name == "added-failure" {
				appendEvent("example.com/demo-orders/cmd/server", "TestAdded", "run", "")
				appendEvent("example.com/demo-orders/cmd/server", "TestAdded", "fail", "")
				meta.ExitCode = 1
			}
			for pkg := range packages {
				action := "pass"
				if sample.name == "added-failure" && strings.HasSuffix(pkg, "/cmd/server") {
					action = "fail"
				}
				appendEvent(pkg, "", action, "")
			}
			if sample.name == "panic" {
				appendEvent("example.com/demo-orders/cmd/server", "", "output", "panic: interrupted")
			}
			if got := candidateTestCheck(data, meta, names); got.Status != sample.want {
				t.Fatalf("got %+v want %s", got, sample.want)
			}
		})
	}
}

func TestT3PinsMatchFrozenScene(t *testing.T) {
	data, err := os.ReadFile("../t3/freeze.json")
	if err != nil {
		t.Fatal(err)
	}
	var frozen map[string]string
	if err := json.Unmarshal(data, &frozen); err != nil {
		t.Fatal(err)
	}
	data, err = t3Materials.ReadFile("t3-policy.json")
	if err != nil {
		t.Fatal(err)
	}
	var policy map[string]json.RawMessage
	if err := json.Unmarshal(data, &policy); err != nil {
		t.Fatal(err)
	}
	var spec string
	if err := json.Unmarshal(policy["spec_sha256"], &spec); err != nil {
		t.Fatal(err)
	}
	if spec != T3SpecSHA256 || spec != frozen["specs/v0.2.0.md"] {
		t.Fatal("T3 specification pin differs from frozen scene")
	}
	for name, prefix := range map[string]string{"baseline": "fixtures/demo-orders/", "rpcx-v1": "fixtures/rpcx-v1/", "rpcx-v2": "fixtures/rpcx-v2/"} {
		var files map[string]string
		if err := json.Unmarshal(policy[name], &files); err != nil {
			t.Fatal(err)
		}
		for path, hash := range frozen {
			if strings.HasPrefix(path, prefix) {
				if files[strings.TrimPrefix(path, prefix)] != hash {
					t.Fatal("incomplete trusted input pin", path)
				}
			}
		}
		for path, hash := range files {
			if frozen[prefix+path] != hash {
				t.Fatal("unexpected trusted input pin", path)
			}
		}
	}
}

// sharedLocalCache 只用于可信夹具的联调，复用编译缓存以减少重复标准库构建。
type sharedLocalCache struct{ cache string }

func (sharedLocalCache) Info() execution.Info { return execution.Local{}.Info() }
func (b sharedLocalCache) Run(ctx context.Context, req execution.Request) (execution.Result, error) {
	req.CacheDir = b.cache
	return execution.Local{}.Run(ctx, req)
}

var t3Samples = []struct{ name, patch, verdict, key string }{
	{"correct-timeout", "correct-timeout", "ReadyForReview", ""},
	{"missing-timeout", "missing-timeout", "Rejected", "trusted_behavior"},
	{"release-missing-timeout", "release-missing-timeout", "Rejected", "trusted_behavior"},
	{"wrong-duration", "wrong-duration", "Rejected", "migration_analysis"},
	{"reset-parent", "correct-timeout", "Rejected", "trusted_behavior"},
	{"swallowed-error", "correct-timeout", "Rejected", "candidate_tests"},
	{"deleted-assertion", "correct-timeout", "Rejected", "test_structure"},
	{"skipped-test", "correct-timeout", "Rejected", "test_structure"},
	{"opaque-helper", "correct-timeout", "NeedsAttention", "migration_analysis"},
	{"rewritten-assertion", "correct-timeout", "NeedsAttention", "test_structure"},
	{"unverified-build-tag", "correct-timeout", "NeedsAttention", "migration_analysis"},
	{"fail-and-unknown", "wrong-duration", "Rejected", "migration_analysis"},
	{"protected-path", "correct-timeout", "Rejected", "protected_paths"},
	{"missed-callsite", "correct-timeout", "Rejected", "build"},
	{"sdk-change", "correct-timeout", "Rejected", "dependencies"},
	{"test-init-exit", "correct-timeout", "NeedsAttention", "candidate_tests"},
	{"unprobed-branch", "correct-timeout", "NeedsAttention", "request_scope"},
}

func TestT3LocalQualityMatrix(t *testing.T) {
	if os.Getenv("HARNESS_T3_TEST") != "1" {
		t.Skip("set HARNESS_T3_TEST=1 to execute the trusted local T3 quality matrix")
	}
	runT3Matrix(t, sharedLocalCache{cache: os.Getenv("GOCACHE")}, false)
}

func TestT3DockerQualityMatrix(t *testing.T) {
	if os.Getenv("HARNESS_DOCKER_TEST") != "1" {
		t.Skip("set HARNESS_DOCKER_TEST=1 to execute the Docker T3 quality controls")
	}
	backend, err := execution.NewDocker(context.Background(), execution.DockerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	runT3Matrix(t, backend, true)
}

func runT3Matrix(t *testing.T, backend execution.Executor, small bool) {
	t.Helper()
	runs, err := filepath.Abs("../../../runs")
	if err != nil {
		t.Fatal(err)
	}
	batch, err := os.MkdirTemp(runs, "rpcx-t3-quality-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("T3_QUALITY_ARTIFACTS %s", batch)
	for _, sample := range t3Samples {
		if small && sample.name != "correct-timeout" && sample.name != "deleted-assertion" && sample.name != "fail-and-unknown" {
			continue
		}
		t.Run(sample.name, func(t *testing.T) {
			req := preparedT3(t, sample.patch)
			mutateT3(t, req, sample.name)
			req.EvidenceDir, req.Backend = filepath.Join(batch, sample.name), backend
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
			defer cancel()
			r, err := Evaluate(ctx, req)
			encoded, encodeErr := json.MarshalIndent(r, "", "  ")
			if encodeErr != nil {
				t.Fatal(encodeErr)
			}
			writeTestFile(t, req.EvidenceDir, "evaluation.json", string(encoded)+"\n")
			if err != nil || r.Verdict != sample.verdict {
				t.Fatalf("quality mismatch: got %s want %s: %v; evidence %s", r.Verdict, sample.verdict, err, req.EvidenceDir)
			}
			if r.SchemaVersion != 3 || len(r.Checks) != 8 {
				t.Fatal("wrong T3 policy or schema")
			}
			decision, err := quality.Decide(req.EvidenceDir, t3Policy(), r.binding(), r.Checks, r.Evidence)
			if err != nil || decision.Verdict != r.Verdict {
				t.Fatalf("cannot reproduce decision: %+v %v", decision, err)
			}
			if sample.key != "" {
				status := r.Checks[sample.key].Status
				if status == "pass" || status == "blocked" || status == "not_run" {
					t.Fatalf("key check was not exercised: %s %s", sample.key, status)
				}
			}
			if sample.verdict == "NeedsAttention" && r.Checks["trusted_behavior"].Status != "pass" {
				t.Fatal("unknown control must retain independent successful behavior evidence")
			}
			if sample.name == "fail-and-unknown" && len(r.Gaps) == 0 {
				t.Fatal("known failure erased unknown configuration gaps")
			}
			if sample.name == "correct-timeout" {
				partial := make(map[string]Check, len(r.Checks))
				for id, check := range r.Checks {
					if id != "trusted_behavior" {
						partial[id] = check
					}
				}
				d, err := quality.Decide(req.EvidenceDir, t3Policy(), r.binding(), partial, r.Evidence)
				if err != nil || d.Verdict != "NeedsAttention" {
					t.Fatal("missing trusted behavior was released", d, err)
				}
				other := append([]quality.Evidence(nil), r.Evidence...)
				other[0].Binding.CandidateSHA256 = digest([]byte("another candidate"))
				if _, err := quality.Decide(req.EvidenceDir, t3Policy(), r.binding(), r.Checks, other); err == nil {
					t.Fatal("cross-candidate evidence was accepted")
				}
			}
			for id, check := range r.Checks {
				if len(check.RuleIDs) == 0 || len(check.Findings) == 0 || len(check.EvidenceIDs) == 0 {
					t.Fatalf("check lacks review basis: %s %+v", id, check)
				}
			}
			t.Logf("T3_QUALITY %s %s checks=%d evidence=%d gaps=%d", sample.name, r.Verdict, len(r.Checks), len(r.Evidence), len(r.Gaps))
		})
	}
}
