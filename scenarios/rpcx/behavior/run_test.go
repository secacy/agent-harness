package behavior

import (
	"context"
	"encoding/json"
	"errors"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/execution"
)

type fakeBackend struct {
	run func(execution.Request) (execution.Result, error)
}

func (fakeBackend) Info() execution.Info { return execution.Info{Kind: execution.KindLocal} }
func (f fakeBackend) Run(_ context.Context, req execution.Request) (execution.Result, error) {
	return f.run(req)
}

func fixture(t *testing.T, patch string) Request {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"demo-orders", "rpcx-v1", "rpcx-v2"} {
		files, err := readTree(context.Background(), filepath.Join("..", "t3", "fixtures", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := writeTree(filepath.Join(root, name), files); err != nil {
			t.Fatal(err)
		}
	}
	module := filepath.Join(root, "demo-orders")
	path, err := filepath.Abs(filepath.Join("..", "t3", "cases", "patches", patch+".patch"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("git", "apply", path)
	command.Dir = module
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("apply fixture: %v: %s", err, data)
	}
	baseline, _ := filepath.Abs(filepath.Join("..", "t3", "fixtures", "demo-orders"))
	return Request{BaselineDir: baseline, CandidateDir: module, SDKDir: filepath.Join(root, "rpcx-v2"), EvidenceDir: filepath.Join(root, "evidence")}
}

func syntheticEvents(slow bool, action string) execution.Result {
	var out strings.Builder
	packages := map[string]bool{}
	for id := range requiredTests(slow) {
		at := strings.Index(id, "/Test")
		pkg, test := id[:at], id[at+1:]
		packages[pkg] = true
		for _, step := range []string{"run", action} {
			data, _ := json.Marshal(map[string]string{"Package": pkg, "Test": test, "Action": step})
			out.Write(data)
			out.WriteByte('\n')
		}
	}
	for pkg := range packages {
		data, _ := json.Marshal(map[string]string{"Package": pkg, "Action": action})
		out.Write(data)
		out.WriteByte('\n')
	}
	exit := 0
	if action == "fail" {
		exit = 1
	}
	return execution.Result{Stdout: []byte(out.String()), ExitCode: exit}
}

func TestEventCompleteness(t *testing.T) {
	for _, sample := range []struct{ name, want string }{
		{"pass", "pass"}, {"fail", "fail"}, {"skip", "unknown"}, {"empty", "unknown"},
		{"malformed", "unknown"}, {"missing-run", "unknown"}, {"duplicate", "unknown"},
		{"missing-package", "unknown"}, {"exit-without-failure", "unknown"}, {"panic", "unknown"}, {"contradictory-exit", "unknown"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			result := syntheticEvents(false, "pass")
			switch sample.name {
			case "fail", "skip":
				result = syntheticEvents(false, sample.name)
			case "empty":
				result.Stdout = nil
			case "malformed":
				result.Stdout = append(result.Stdout, []byte("not json")...)
			case "missing-run":
				result.Stdout = []byte(strings.ReplaceAll(string(result.Stdout), `"Action":"run"`, `"Action":"output"`))
			case "duplicate":
				result.Stdout = append(result.Stdout, result.Stdout...)
			case "missing-package":
				var out strings.Builder
				for _, line := range strings.Split(string(result.Stdout), "\n") {
					if strings.Contains(line, `"Test":`) {
						out.WriteString(line + "\n")
					}
				}
				result.Stdout = []byte(out.String())
			case "exit-without-failure":
				result.ExitCode = 2
			case "panic":
				result.Stdout = append(result.Stdout, []byte(`{"Action":"output","Output":"panic: terminated"}`+"\n")...)
			case "contradictory-exit":
				result = syntheticEvents(false, "fail")
				result.ExitCode = 0
			}
			tests, gaps := observe(result, false)
			if got := aggregate(tests, gaps); got != sample.want {
				t.Fatalf("got %s want %s: %+v %v", got, sample.want, tests, gaps)
			}
		})
	}
}

func TestTrustedAssemblyAndBinding(t *testing.T) {
	req := fixture(t, "correct-timeout")
	poison := "package main\nimport \"os\"\nfunc init() { os.Exit(0) }\n"
	if err := os.WriteFile(filepath.Join(req.CandidateDir, "cmd/server/poison_test.go"), []byte(poison), 0644); err != nil {
		t.Fatal(err)
	}
	calls := 0
	req.Backend = fakeBackend{func(command execution.Request) (execution.Result, error) {
		calls++
		if _, err := os.Stat(filepath.Join(command.Directory, "cmd/server/poison_test.go")); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("candidate test initialization entered trusted assembly")
		}
		if len(command.ReadOnlyDirs) != 2 || len(command.Args) == 0 {
			t.Fatal("unexpected execution roots")
		}
		return syntheticEvents(calls == 2, "pass"), nil
	}}
	r, err := Run(context.Background(), req)
	if err != nil || r.Status != "pass" || calls != 2 || len(r.Tests) != 40 {
		t.Fatalf("unexpected result: %+v %v calls=%d", r, err, calls)
	}
	if r.Inputs["candidate"].Files["cmd/server/poison_test.go"] == "" || r.Assembly.Files["demo-orders/cmd/server/poison_test.go"] != "" {
		t.Fatal("candidate tests must be bound but excluded from execution")
	}
	for path, hash := range r.Production.Files {
		if r.Assembly.Files["demo-orders/"+path] != hash {
			t.Fatal("production file was changed by assembly")
		}
	}
	for name, binding := range map[string]Binding{"trusted": r.Assembly, "materials": r.Material, "inputs/candidate": r.Inputs["candidate"]} {
		files, err := readTree(context.Background(), filepath.Join(req.EvidenceDir, name))
		if err != nil || !reflect.DeepEqual(bind(files), binding) {
			t.Fatalf("artifact binding mismatch: %s %v", name, err)
		}
	}
}

func TestIncompleteAndInterruptedExecution(t *testing.T) {
	for _, sample := range []string{"empty", "fail", "backend-error", "output-limit", "assembly-mutation", "source-mutation", "expiry-error"} {
		t.Run(sample, func(t *testing.T) {
			req := fixture(t, "correct-timeout")
			calls := 0
			req.Backend = fakeBackend{func(command execution.Request) (execution.Result, error) {
				calls++
				result := syntheticEvents(calls == 2, "pass")
				switch sample {
				case "empty":
					result.Stdout = nil
				case "fail":
					result = syntheticEvents(false, "fail")
				case "backend-error":
					return result, context.Canceled
				case "output-limit":
					result.OutputLimit = true
				case "assembly-mutation":
					if err := os.WriteFile(filepath.Join(command.Directory, "go.mod"), []byte("changed"), 0644); err != nil {
						t.Fatal(err)
					}
				case "source-mutation":
					if err := os.WriteFile(filepath.Join(req.CandidateDir, "go.mod"), []byte("changed"), 0644); err != nil {
						t.Fatal(err)
					}
				case "expiry-error":
					if calls == 2 {
						return result, context.DeadlineExceeded
					}
				}
				return result, nil
			}}
			r, err := Run(context.Background(), req)
			if sample == "empty" || sample == "fail" {
				want := "unknown"
				if sample == "fail" {
					want = "fail"
				}
				if err != nil || r.Status != want || calls != 1 || len(r.Tests) != 40 {
					t.Fatalf("incomplete fast checks ran expiry or passed: %+v %v calls=%d", r, err, calls)
				}
				return
			}
			if err == nil || !reflect.DeepEqual(r, Report{}) {
				t.Fatalf("execution error produced usable report: %+v %v", r, err)
			}
			if _, err := os.Stat(filepath.Join(req.EvidenceDir, "report.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("stale report survived error")
			}
			if _, err := os.Stat(filepath.Join(req.EvidenceDir, "fast.jsonl")); err != nil {
				t.Fatal("partial raw log was lost")
			}
		})
	}
}

func TestRejectsInvalidInputs(t *testing.T) {
	for _, sample := range []string{"no-backend", "overlap", "cache-alias", "dirty-output", "sdk-change", "baseline-change", "symlink"} {
		t.Run(sample, func(t *testing.T) {
			req := fixture(t, "correct-timeout")
			req.Backend = fakeBackend{func(execution.Request) (execution.Result, error) {
				t.Fatal("invalid input was executed")
				return execution.Result{}, nil
			}}
			switch sample {
			case "no-backend":
				req.Backend = nil
			case "overlap":
				req.EvidenceDir = filepath.Join(req.CandidateDir, "evidence")
			case "cache-alias":
				alias := filepath.Join(filepath.Dir(req.EvidenceDir), "alias")
				if err := os.Symlink(req.CandidateDir, alias); err != nil {
					t.Fatal(err)
				}
				req.EvidenceDir = filepath.Join(alias, "evidence")
			case "dirty-output":
				if err := os.MkdirAll(req.EvidenceDir, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(req.EvidenceDir, "old"), nil, 0644); err != nil {
					t.Fatal(err)
				}
			case "sdk-change":
				if err := os.WriteFile(filepath.Join(req.SDKDir, "client.go"), []byte("package rpcx"), 0644); err != nil {
					t.Fatal(err)
				}
			case "baseline-change":
				base, err := readTree(context.Background(), req.BaselineDir)
				if err != nil {
					t.Fatal(err)
				}
				req.BaselineDir = filepath.Join(filepath.Dir(req.CandidateDir), "baseline")
				base["internal/inventory/client_test.go"] = []byte("package inventory")
				if err := writeTree(req.BaselineDir, base); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink("go.mod", filepath.Join(req.CandidateDir, "alias")); err != nil {
					t.Fatal(err)
				}
			}
			if r, err := Run(context.Background(), req); err == nil || !reflect.DeepEqual(r, Report{}) {
				t.Fatalf("invalid input accepted: %+v %v", r, err)
			}
		})
	}
}

func TestProbeSourceIsFormatted(t *testing.T) {
	data, _ := materials.ReadFile("probe_test.go.txt")
	formatted, err := format.Source(data)
	if err != nil || string(formatted) != string(data) {
		t.Fatal("trusted probe must be valid, formatted Go source", err)
	}
}

func TestLocalFrozenBehavior(t *testing.T) {
	if os.Getenv("HARNESS_BEHAVIOR_TEST") != "1" {
		t.Skip("set HARNESS_BEHAVIOR_TEST=1 to execute trusted fixture behavior including real 30s expiry")
	}
	checkFrozenBehavior(t, execution.Local{})
}

func TestDockerFrozenBehavior(t *testing.T) {
	if os.Getenv("HARNESS_DOCKER_TEST") != "1" {
		t.Skip("set HARNESS_DOCKER_TEST=1 to run real Docker behavior integration")
	}
	backend, err := execution.NewDocker(context.Background(), execution.DockerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	checkFrozenBehavior(t, backend)
}

func TestDockerBehaviorCounterexamples(t *testing.T) {
	if os.Getenv("HARNESS_DOCKER_TEST") != "1" {
		t.Skip("set HARNESS_DOCKER_TEST=1 to run real Docker behavior counterexamples")
	}
	backend, err := execution.NewDocker(context.Background(), execution.DockerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct{ name, want string }{
		{"lost-parent", "fail"}, {"unreleased-context", "fail"}, {"swallowed-error", "fail"}, {"production-exit", "unknown"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			req := fixture(t, "correct-timeout")
			req.Backend = backend
			path := filepath.Join(req.CandidateDir, "internal/inventory/client.go")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			before := string(data)
			switch sample.name {
			case "lost-parent":
				data = []byte(strings.ReplaceAll(before, "context.WithTimeout(ctx,", "context.WithTimeout(context.Background(),"))
			case "unreleased-context":
				data = []byte(strings.ReplaceAll(before, "defer cancel()", "_ = cancel"))
			case "swallowed-error":
				data = []byte(strings.Replace(before, `return fmt.Errorf("reserve inventory: %w", err)`, "return nil", 1))
			case "production-exit":
				path = filepath.Join(req.CandidateDir, "cmd/server/exit.go")
				data = []byte("package main\nimport \"os\"\nfunc init() { os.Exit(0) }\n")
			}
			if string(data) == before {
				t.Fatal("counterexample mutation did not match")
			}
			if err := os.WriteFile(path, data, 0644); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			r, err := Run(ctx, req)
			if err != nil || r.Status != sample.want {
				t.Fatalf("counterexample was misclassified: %+v %v", r, err)
			}
			for _, test := range r.Tests {
				if strings.Contains(test.ID, "/TestHarnessExpiry/") && test.Status != "not_run" {
					t.Fatal("incomplete fast checks did not stop expiry phase")
				}
			}
			data, _ = json.Marshal(r)
			t.Logf("TRUSTED_BEHAVIOR %s", data)
			events, err := os.ReadFile(filepath.Join(req.EvidenceDir, "fast.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("BEHAVIOR_EVENTS fast %s", events)
			meta, err := os.ReadFile(filepath.Join(req.EvidenceDir, "fast.execution.json"))
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("BEHAVIOR_EXECUTION fast %s", meta)
		})
	}
}

func TestDockerDeadlineWithoutTimer(t *testing.T) {
	if os.Getenv("HARNESS_DOCKER_TEST") != "1" {
		t.Skip("set HARNESS_DOCKER_TEST=1 to run the real deadline-without-timer control")
	}
	backend, err := execution.NewDocker(context.Background(), execution.DockerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	req := fixture(t, "correct-timeout")
	req.Backend = backend
	path := filepath.Join(req.CandidateDir, "internal/inventory/client.go")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	before := string(data)
	data = []byte(strings.ReplaceAll(before, "context.WithTimeout(ctx, 30*time.Second)", "harnessDeadlineOnly(ctx)"))
	if string(data) == before {
		t.Fatal("deadline-only mutation did not match")
	}
	// 此反例保留父取消、元数据和表面期限，但故意不为默认期限安装计时器。
	data = append(data, []byte(`
type harnessDeadlineContext struct {
 context.Context
 deadline time.Time
}
func (c harnessDeadlineContext) Deadline() (time.Time, bool) { return c.deadline, true }
func harnessDeadlineOnly(parent context.Context) (context.Context, context.CancelFunc) {
 child, cancel := context.WithCancel(parent)
 deadline := time.Now().Add(30*time.Second)
 if earlier, ok := parent.Deadline(); ok && earlier.Before(deadline) { deadline = earlier }
 return harnessDeadlineContext{child, deadline}, cancel
}
`)...)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	// 保留此组慢检查的完整副本，便于人工复核具体证据。
	runs, err := filepath.Abs(filepath.Join("..", "..", "..", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	req.EvidenceDir, err = os.MkdirTemp(runs, "rpcx-behavior-deadline-only-")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	r, err := Run(ctx, req)
	if err != nil || r.Status != "fail" {
		t.Fatalf("deadline-only implementation was not rejected: %+v %v", r, err)
	}
	for _, test := range r.Tests {
		want := "pass"
		if strings.Contains(test.ID, "/TestHarnessExpiry/") {
			want = "fail"
		}
		if test.Status != want {
			t.Fatalf("control no longer isolates timer expiry: %+v", test)
		}
	}
	auditArtifacts(t, req, r)
	data, _ = json.Marshal(r)
	t.Logf("TRUSTED_BEHAVIOR %s", data)
	t.Logf("BEHAVIOR_ARTIFACTS %s", req.EvidenceDir)
}

func auditArtifacts(t *testing.T, req Request, r Report) {
	t.Helper()
	for name, binding := range map[string]Binding{"trusted": r.Assembly, "materials": r.Material,
		"inputs/baseline": r.Inputs["baseline"], "inputs/candidate": r.Inputs["candidate"], "inputs/rpcx-v2": r.Inputs["rpcx-v2"]} {
		files, err := readTree(context.Background(), filepath.Join(req.EvidenceDir, name))
		if err != nil || !reflect.DeepEqual(bind(files), binding) {
			t.Fatalf("artifact binding mismatch: %s %v", name, err)
		}
	}
	for path, hash := range r.Production.Files {
		if r.Assembly.Files["demo-orders/"+path] != hash {
			t.Fatal("trusted production differs from candidate")
		}
	}
}

func checkFrozenBehavior(t *testing.T, backend execution.Executor) {
	for _, patch := range []string{"correct-timeout", "missing-timeout", "release-missing-timeout", "wrong-duration"} {
		t.Run(patch, func(t *testing.T) {
			req := fixture(t, patch)
			req.Backend = backend
			// 候选测试入口即使主动退出，也不得进入可信执行副本。
			if err := os.WriteFile(filepath.Join(req.CandidateDir, "cmd/server/poison_test.go"), []byte("package main\nimport \"os\"\nfunc init() { os.Exit(0) }\n"), 0644); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			r, err := Run(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			want := "fail"
			if patch == "correct-timeout" {
				want = "pass"
			}
			if r.Status != want || len(r.Tests) != 40 {
				t.Fatalf("unexpected behavior: %+v", r)
			}
			auditArtifacts(t, req, r)
			originalPassed, failed, notRun := 0, 0, 0
			for _, test := range r.Tests {
				if !strings.Contains(test.ID, "/TestHarness") && test.Status == "pass" {
					originalPassed++
				}
				if test.Status == "fail" {
					failed++
				}
				if test.Status == "not_run" {
					notRun++
				}
			}
			if originalPassed != 10 || (want == "fail" && (failed == 0 || notRun != 2)) {
				t.Fatalf("original tests or negative control no longer demonstrate intended distinction: %+v", r.Tests)
			}
			for _, phase := range []string{"fast", "expiry"} {
				data, err := os.ReadFile(filepath.Join(req.EvidenceDir, phase+".jsonl"))
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("BEHAVIOR_EVENTS %s %s", phase, data)
				meta, err := os.ReadFile(filepath.Join(req.EvidenceDir, phase+".execution.json"))
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("BEHAVIOR_EXECUTION %s %s", phase, meta)
			}
			data, _ := json.Marshal(r)
			t.Logf("TRUSTED_BEHAVIOR %s", data)
		})
	}
}
