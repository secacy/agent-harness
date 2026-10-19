package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/execution"
)

type baselineBackend struct {
	run func(context.Context, execution.Request) (execution.Result, error)
}

func (b baselineBackend) Info() execution.Info { return execution.Local{}.Info() }
func (b baselineBackend) Run(ctx context.Context, req execution.Request) (execution.Result, error) {
	return b.run(ctx, req)
}

func explicitConfig(t *testing.T) Config {
	t.Helper()
	config := configForTest(t, modelFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
		t.Fatal("failed preparation reached model")
		return agent.Reply{}, nil
	}))
	target, err := TargetFromScenario("orders-target", config.ScenarioDir, "v0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	config.Target = &target
	config.ScenarioDir = ""
	return config
}
func passedBaselineEvents(p profile) []byte {
	data := []byte{}
	packages := map[string]bool{}
	for _, name := range p.RequiredTests {
		at := strings.LastIndex(name, "/")
		pkg, test := name[:at], name[at+1:]
		event, _ := json.Marshal(map[string]string{"Action": "pass", "Package": pkg, "Test": test})
		data = append(append(data, event...), '\n')
		packages[pkg] = true
	}
	for _, pkg := range sortedKeys(packages) {
		event, _ := json.Marshal(map[string]string{"Action": "pass", "Package": pkg})
		data = append(append(data, event...), '\n')
	}
	return data
}

func TestExplicitTargetInputFailureHasRunAndNoCandidate(t *testing.T) {
	for _, mode := range []string{"missing", "changed", "symlink", "specification"} {
		t.Run(mode, func(t *testing.T) {
			config := explicitConfig(t)
			target := *config.Target
			target.Repository.Directory = filepath.Join(t.TempDir(), "source")
			if mode != "missing" {
				files := mustReadTree(t, config.Target.Repository.Directory)
				if mode == "changed" {
					files["extra.txt"] = []byte("drift")
				}
				if err := files.write(context.Background(), target.Repository.Directory); err != nil {
					t.Fatal(err)
				}
				if mode == "symlink" {
					if err := os.Symlink(os.DevNull, filepath.Join(target.Repository.Directory, "link")); err != nil {
						t.Fatal(err)
					}
				}
			}
			if mode == "specification" {
				target.Specification.Path = filepath.Join(t.TempDir(), "missing.md")
			}
			config.Target = &target
			record, err := Run(context.Background(), config)
			if !errors.Is(err, ErrInputChanged) || record.Directory == "" || record.Status != StatusFailed || record.TargetID != target.ID || record.FailureCode != "input_invalid" || record.Verdict != "" {
				t.Fatalf("record=%+v err=%v", record, err)
			}
			report := readJSON[Report](t, filepath.Join(record.Directory, "artifacts/report.json"))
			if report.Validation != nil || report.Baseline != nil || report.AgentRecord != "" || report.Input.Target.ID != target.ID || report.Input.BaselineSHA256 != "" {
				t.Fatalf("unauthenticated inputs produced facts: %+v", report)
			}
			assertReviewLinks(t, record.Directory, mustReadTree(t, filepath.Join(record.Directory, "artifacts"))["review.md"])
		})
	}
}

func TestUnsupportedTargetRejectedBeforeRun(t *testing.T) {
	for _, mode := range []string{"profile", "build-config", "snapshot", "dependency", "mixed-entry", "overlap", "budget-id"} {
		t.Run(mode, func(t *testing.T) {
			config := explicitConfig(t)
			switch mode {
			case "profile":
				config.Target.ProfileID = "not-supported"
			case "build-config":
				config.Target.BuildConfig = "extra"
			case "snapshot":
				config.Target.Repository.SHA256 = strings.Repeat("f", 64)
			case "dependency":
				config.Target.Dependencies["untrusted"] = config.Target.Repository
			case "mixed-entry":
				config.ScenarioDir = "scenarios/rpcx"
			case "overlap":
				config.RunsDir = filepath.Join(config.Target.Repository.Directory, "runs")
			case "budget-id":
				config.Budget = &budget.Target{ID: "different"}
			}
			record, err := Run(context.Background(), config)
			if !errors.Is(err, ErrInvalidConfig) || record.Directory != "" {
				t.Fatalf("record=%+v err=%v", record, err)
			}
		})
	}
}

func TestBaselineFailuresNeverCallModelOrReserveBudget(t *testing.T) {
	for _, mode := range []string{"build", "test", "empty", "skip", "package-fail", "malformed", "infrastructure", "evidence-write", "cancel", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			config := explicitConfig(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			profile := budget.Profile{Provider: "controlled", Endpoint: "in-process", Model: config.ModelName, ContextTokens: 90, MaxOutputTokens: 10, SourceSHA256: strings.Repeat("a", 64), VerifiedAt: time.Now().UTC()}
			ledger, err := budget.Create(budget.Config{Directory: filepath.Join(t.TempDir(), "budget"), CampaignID: "preflight", Profile: profile, Limits: budget.Limits{MaxCalls: 1, MaxTokens: 100}, Targets: map[string]budget.Limits{config.Target.ID: {MaxCalls: 1, MaxTokens: 100}}})
			if err != nil {
				t.Fatal(err)
			}
			config.Budget = &budget.Target{Ledger: ledger, ID: config.Target.ID}
			p, _ := lookupProfile(config.Target.ProfileID)
			good := passedBaselineEvents(p)
			config.Backend = baselineBackend{run: func(ctx context.Context, req execution.Request) (execution.Result, error) {
				saved := readJSON[Record](t, filepath.Join(filepath.Dir(filepath.Dir(req.Directory)), "run.json"))
				if saved.Phase != PhaseCheckingBaseline {
					t.Fatal("baseline phase not saved")
				}
				if mode == "infrastructure" {
					return execution.Result{ExitCode: -1}, errors.New("injected command start failure")
				}
				if mode == "cancel" {
					cancel()
					return execution.Result{ExitCode: -1}, ctx.Err()
				}
				if mode == "timeout" {
					<-ctx.Done()
					return execution.Result{ExitCode: -1}, ctx.Err()
				}
				if req.Args[1] == "build" {
					if mode == "evidence-write" {
						path := filepath.Join(filepath.Dir(filepath.Dir(req.Directory)), "preflight/baseline.json")
						if err := os.Remove(path); err != nil {
							t.Fatal(err)
						}
						if err := os.Mkdir(path, 0700); err != nil {
							t.Fatal(err)
						}
					}

					if mode == "build" {
						return execution.Result{ExitCode: 1, Stderr: []byte("injected build failure")}, nil
					}
					return execution.Result{ExitCode: 0}, nil
				}
				result := execution.Result{ExitCode: 0, Stdout: good}
				switch mode {
				case "test":
					result.ExitCode = 1
				case "empty":
					result.Stdout = nil
				case "skip":
					result.Stdout = []byte(strings.ReplaceAll(string(good), `"pass"`, `"skip"`))
				case "package-fail":
					result.Stdout = append(good, []byte(`{"Action":"fail","Package":"example.com/demo-orders/cmd/server"}`)...)
				case "malformed":
					result.Stdout = []byte("not JSON")
				}
				return result, nil
			}}
			if mode == "timeout" {
				config.Timeout = 150 * time.Millisecond
			}
			record, err := Run(ctx, config)
			wantCode := "baseline_failed"
			if mode == "infrastructure" || mode == "evidence-write" || mode == "cancel" || mode == "timeout" {
				wantCode = "baseline_error"
			}
			if !errors.Is(err, ErrBaseline) || record.Directory == "" || record.Verdict != "" || record.AgentExitReason != "" || record.Phase != PhaseCheckingBaseline || record.FailureCode != wantCode {
				t.Fatalf("record=%+v err=%v", record, err)
			}
			report := readJSON[Report](t, filepath.Join(record.Directory, "artifacts/report.json"))
			if report.Baseline == nil || report.Baseline.Status == "pass" || report.Validation != nil || report.AgentRecord != "" {
				t.Fatalf("failed baseline continued: %+v", report)
			}
			for _, command := range report.Baseline.Commands {
				for _, log := range []struct{ path, hash string }{{command.Stdout, command.StdoutSHA256}, {command.Stderr, command.StderrSHA256}} {
					data, readErr := os.ReadFile(filepath.Join(record.Directory, log.path))
					if readErr != nil || digest(data) != log.hash {
						t.Fatalf("failed baseline log lost: %s %v", log.path, readErr)
					}
				}
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if mode == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			if s := ledger.Snapshot(); s.Totals.Calls != 0 || len(s.Calls) != 0 || len(s.Rejections) != 0 {
				t.Fatal("baseline failure consumed model budget")
			}
			assertReviewLinks(t, record.Directory, mustReadTree(t, filepath.Join(record.Directory, "artifacts"))["review.md"])
		})
	}
}

func TestExplicitTargetChecksRealBaselineBeforeModel(t *testing.T) {
	testRealBaselineBeforeModel(t, nil)
}

func TestExplicitTargetDockerBaselineBeforeModel(t *testing.T) {
	if os.Getenv("HARNESS_DOCKER_TEST") != "1" {
		t.Skip("set HARNESS_DOCKER_TEST=1 for real Docker integration")
	}
	backend, err := execution.NewDocker(context.Background(), execution.DockerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	testRealBaselineBeforeModel(t, backend)
}

func testRealBaselineBeforeModel(t *testing.T, backend execution.Executor) {
	config := explicitConfig(t)
	if backend != nil {
		config.Backend = backend
		config.TrustedLocal = false
	}
	original := config.Target.Repository.Directory
	// 物理路径与模块名解耦，但内容和 Go module 身份不改变。
	moved := filepath.Join(t.TempDir(), "repository-snapshot")
	if err := mustReadTree(t, original).write(context.Background(), moved); err != nil {
		t.Fatal(err)
	}
	moved, err := canonicalPath(moved)
	if err != nil {
		t.Fatal(err)
	}
	config.Target.Repository.Directory = moved
	config.Model = modelFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
		dirs, err := os.ReadDir(config.RunsDir)
		if err != nil || len(dirs) != 1 {
			t.Fatal(err)
		}
		dir := filepath.Join(config.RunsDir, dirs[0].Name())
		baseline := readJSON[BaselineReport](t, filepath.Join(dir, "preflight/baseline.json"))
		if baseline.Status != "pass" || len(baseline.Commands) != 2 || len(baseline.Tests) != 10 || baseline.BaselineSHA256 != config.Target.Repository.SHA256 {
			t.Fatalf("model before baseline: %+v", baseline)
		}
		material, err := os.ReadFile(filepath.Join(dir, "inputs/profile.json"))
		if err != nil || digest(material) != baseline.ProfileSHA256 {
			t.Fatalf("profile material binding: %v", err)
		}
		return agent.Reply{}, errors.New("stop after authenticated baseline")
	})
	record, err := Run(context.Background(), config)
	if err == nil || record.AgentExitReason != agent.ExitFailed {
		t.Fatalf("record=%+v err=%v", record, err)
	}
	report := readJSON[Report](t, filepath.Join(record.Directory, "artifacts/report.json"))
	if report.Baseline.Status != "pass" || report.Input.Target.Repository.Directory != moved || report.Input.Target.ID != record.TargetID {
		t.Fatal(report.Input)
	}
	for _, command := range report.Baseline.Commands {
		if backend != nil {
			if command.Backend.Kind != execution.KindDocker || command.ContainerID == "" {
				t.Fatal("baseline bypassed Docker")
			}
			t.Logf("baseline container: %s", command.ContainerID)
		}
		for _, log := range []struct{ path, hash string }{{command.Stdout, command.StdoutSHA256}, {command.Stderr, command.StderrSHA256}} {
			data, err := os.ReadFile(filepath.Join(record.Directory, log.path))
			if err != nil || digest(data) != log.hash {
				t.Fatalf("log binding: %+v %v", log, err)
			}
		}
	}
}

func TestSupportedProfilesMatchFrozenInputs(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"v0.1.0", "v0.2.0"} {
		t.Run(version, func(t *testing.T) {
			scenario := root
			if version == "v0.2.0" {
				scenario = filepath.Join(root, "t3")
			}
			target, err := TargetFromScenario("profile-test", scenario, version)
			if err != nil {
				t.Fatal(err)
			}
			p, err := lookupProfile(target.ProfileID)
			if err != nil {
				t.Fatal(err)
			}
			// 已发布 Profile 的材料身份不原地改变，调整时需发布新 ID。
			expected := map[string]string{ProfileT1T2: "523b35c268aadf4784a411811f96ac1417a7dd86e7e16897dc742d57509e5e1a", ProfileT3: "144867b1171d6a3b980336a0425265459cc464cb9d0ff9d5c4edd7a3c4864f3a"}
			if p.hash() != expected[p.ID] {
				t.Fatal("published profile material changed")
			}
			if _, _, err = readTarget(context.Background(), target, p); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBaselineEventsRequireTestAndPackagePass(t *testing.T) {
	p, _ := lookupProfile(ProfileT1T2)
	data := passedBaselineEvents(p)
	if _, err := baselineEvents(data, p.RequiredTests); err != nil {
		t.Fatal(err)
	}
	withoutPackages := []byte{}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, `"Test"`) {
			withoutPackages = append(withoutPackages, []byte(line+"\n")...)
		}
	}
	if _, err := baselineEvents(withoutPackages, p.RequiredTests); err == nil {
		t.Fatal("missing package completion accepted")
	}
}

func TestBaselineRejectsChangedPreparedInputs(t *testing.T) {
	for _, name := range []string{"workspace/demo-orders/go.mod", "baseline/demo-orders/go.mod", "inputs/profile.json", "inputs/spec/spec.md", "workspace/rpcx-v1/go.mod"} {
		t.Run(name, func(t *testing.T) {
			config := explicitConfig(t)
			p, _ := lookupProfile(config.Target.ProfileID)
			events := passedBaselineEvents(p)
			changed := false
			config.Backend = baselineBackend{run: func(ctx context.Context, req execution.Request) (execution.Result, error) {
				if !changed {
					root := filepath.Dir(filepath.Dir(req.Directory))
					path := filepath.Join(root, filepath.FromSlash(name))
					file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = file.WriteString("\nchanged\n"); err != nil {
						t.Fatal(err)
					}
					if err = file.Close(); err != nil {
						t.Fatal(err)
					}
					changed = true
				}
				return execution.Result{ExitCode: 0, Stdout: events}, nil
			}}
			record, err := Run(context.Background(), config)
			if !errors.Is(err, ErrInputChanged) || record.Status != StatusFailed || record.FailureCode != "input_invalid" || record.Verdict != "" || record.AgentExitReason != "" {
				t.Fatalf("record=%+v err=%v", record, err)
			}
		})
	}
}
