package evaluation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2"
)

func workoutsCandidate(t *testing.T, patch string) Request {
	t.Helper()
	return scenarioCandidate(t, "workouts", "v0.5.0", patch)
}

func workoutsFake(t *testing.T) fakeExecutor {
	t.Helper()
	host, err := l2.WorkoutsMaterials()
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Modules map[string]testInventory `json:"modules"`
	}
	if err = json.Unmarshal(host["tests.json"], &inventory); err != nil {
		t.Fatal(err)
	}
	shared := successfulFake(t)
	return fakeExecutor{run: func(ctx context.Context, req execution.Request) (execution.Result, error) {
		out, err := shared.Run(ctx, req)
		if slices.Contains(req.Args, "test") {
			module := "trainings"
			if filepath.Base(req.Directory) == "common" {
				module = "common"
			}
			tests := inventory.Modules[module]
			out.Stdout = passedEvents(append(tests.Original, tests.Added...), tests.Packages)
			for _, step := range workoutsSteps() {
				if slices.Contains(req.Args, "-run="+step.pattern) {
					out.Stdout = passedEvents(step.required, []string{workoutsPackage + "/adapters"})
				}
			}
		}
		return out, err
	}}
}

func TestWorkoutsReferenceBoundaries(t *testing.T) {
	if _, err := workoutsData(); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"pass", "common-build-error", "common-no-events", "common-tests-fail", "trainer-expiry-no-events", "trainer-expiry-error", "reference-compile-error", "common-drift", "wrong-spec", "wrong-sdk"} {
		t.Run(mode, func(t *testing.T) {
			req := workoutsCandidate(t, "correct")
			fake := workoutsFake(t)
			calls := 0
			req.Backend = fakeExecutor{run: func(ctx context.Context, command execution.Request) (execution.Result, error) {
				calls++
				out, err := fake.Run(ctx, command)
				common := filepath.Base(command.Directory) == "common"
				switch mode {
				case "common-build-error":
					if common && slices.Contains(command.Args, "build") {
						out.ExitCode = 1
					}
				case "common-no-events":
					if common && slices.Contains(command.Args, "test") {
						out.Stdout = nil
					}
				case "common-tests-fail":
					if common && slices.Contains(command.Args, "test") {
						out.ExitCode = 1
						out.Stdout = []byte(`{"Action":"run","Package":"common","Test":"TestFailed"}` + "\n" + `{"Action":"fail","Package":"common","Test":"TestFailed"}` + "\n")
					}
				case "trainer-expiry-no-events":
					if slices.Contains(command.Args, "-run=^TestL2ReferenceExpiry$/^trainer$") {
						out.Stdout = nil
					}
				case "trainer-expiry-error":
					if slices.Contains(command.Args, "-run=^TestL2ReferenceExpiry$/^trainer$") {
						return execution.Result{ExitCode: -1}, context.DeadlineExceeded
					}
				case "reference-compile-error":
					if slices.Contains(command.Args, "-run=^TestL2ReferenceCancellation$") {
						out.Stdout = nil
						out.ExitCode = 1
					}
				case "common-drift":
					if calls == 1 {
						if err := os.WriteFile(filepath.Join(req.EvidenceDir, "production/common/LICENSE"), []byte("changed"), 0644); err != nil {
							t.Fatal(err)
						}
					}
				}
				return out, err
			}}
			if mode == "wrong-spec" {
				req.SpecPath = filepath.Join(t.TempDir(), "spec")
				if err := os.WriteFile(req.SpecPath, []byte("wrong"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "wrong-sdk" {
				files, err := gate.ReadFiles(req.FixturesDir)
				if err != nil {
					t.Fatal(err)
				}
				files["rpcx-v2/client.go"] = []byte("wrong sdk")
				req.FixturesDir = filepath.Join(t.TempDir(), "fixtures")
				if err = gate.CopyFiles(req.FixturesDir, files); err != nil {
					t.Fatal(err)
				}
			}
			report, err := EvaluateWorkouts(context.Background(), req)
			want := Inconclusive
			if mode == "pass" {
				want = Pass
			}
			if report.Automatic != want {
				t.Fatalf("automatic=%s want=%s err=%v checks=%+v", report.Automatic, want, err, report.Checks)
			}
			if (mode == "wrong-spec" || mode == "wrong-sdk") && calls != 0 {
				t.Fatal("untrusted inputs reached executor")
			}
			if mode == "common-drift" && report.InputsUnchanged {
				t.Fatal("common drift accepted")
			}
			if (mode == "common-build-error" || mode == "common-no-events" || mode == "common-tests-fail" || mode == "trainer-expiry-error" || mode == "common-drift") && err == nil {
				t.Fatal("execution or integrity error missing")
			}
			raw, readErr := os.ReadFile(filepath.Join(req.EvidenceDir, "reference.json"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			review, readErr := os.ReadFile(filepath.Join(req.EvidenceDir, "review-template.json"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			a, assessErr := Assess(raw, review)
			if assessErr != nil || a.Status != Inconclusive {
				t.Fatalf("assessment=%+v err=%v", a, assessErr)
			}
			if mode == "pass" {
				for _, corruption := range []string{"drop-common", "drop-common-tests", "false-module-aggregate", "drop-trainer-expiry"} {
					t.Run(corruption, func(t *testing.T) {
						var altered Report
						_ = json.Unmarshal(raw, &altered)
						switch corruption {
						case "drop-common":
							altered.LocalModules = nil
						case "drop-common-tests":
							delete(altered.LocalModules["common"].Checks, "tests")
						case "false-module-aggregate":
							altered.Checks["local_modules"] = Observation{Status: Inconclusive}
						case "drop-trainer-expiry":
							delete(altered.Checks["expiry"].Tests, workoutsPackage+"/adapters/TestL2ReferenceExpiry/trainer")
						}
						alteredRaw, _ := json.Marshal(altered)
						var bound Review
						_ = json.Unmarshal(review, &bound)
						bound.ReportSHA256 = gate.Digest(alteredRaw)
						boundRaw, _ := json.Marshal(bound)
						if _, err := Assess(alteredRaw, boundRaw); err == nil {
							t.Fatal("incomplete Workouts evidence accepted")
						}
					})
				}
			}
		})
	}
}

func TestWorkoutsReferenceAcceptance(t *testing.T) {
	if os.Getenv("HARNESS_L2_REFERENCE_ACCEPTANCE") != "1" {
		t.Skip("set HARNESS_L2_REFERENCE_ACCEPTANCE=1 for reference calibration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	var backend execution.Executor = execution.Local{}
	if os.Getenv("HARNESS_L2_REFERENCE_BACKEND") == "docker" {
		d, err := execution.NewDocker(ctx, execution.DockerConfig{})
		if err != nil {
			t.Fatal(err)
		}
		backend = d
	}
	selected := strings.Split(os.Getenv("HARNESS_L2_REFERENCE_CASES"), ",")
	matched := 0
	for _, c := range []struct {
		id     string
		status Status
		rule   string
	}{
		{"correct", Pass, ""}, {"trainer-missing-timeout", Fail, "TIME-001"}, {"background-fallback", Fail, "CTX-001"}, {"outer-context", Fail, "CTX-001"},
		{"zero-balance-call", Fail, "BEHAVIOR-001"}, {"swallowed-error", Fail, "BEHAVIOR-001"}, {"mapping-helper", Pass, ""}, {"deleted-assertion", Pass, ""}, {"protected-license", Fail, "PATH-001"},
		{"deadline-variant", Pass, ""}, {"shared-callback-deadline", Fail, "TIME-001"}, {"renamed-wrong-deadline", Fail, "TIME-001"},
	} {
		if selected[0] != "" && !slices.Contains(selected, c.id) {
			continue
		}
		matched++
		t.Run(c.id, func(t *testing.T) {
			patch := c.id
			if slices.Contains([]string{"deadline-variant", "shared-callback-deadline", "renamed-wrong-deadline"}, c.id) {
				patch = "correct"
			}
			req := workoutsCandidate(t, patch)
			if patch != c.id {
				for _, name := range []string{"adapters/users_grpc.go", "adapters/trainer_grpc.go"} {
					p := filepath.Join(req.CandidateDir, name)
					raw, err := os.ReadFile(p)
					if err != nil {
						t.Fatal(err)
					}
					s := string(raw)
					old := "context.WithTimeout(ctx, 30*time.Second)"
					replacement := "context.WithDeadline(ctx, time.Now().Add(30*time.Second))"
					if c.id == "renamed-wrong-deadline" {
						replacement = "context.WithTimeout(ctx, 5*time.Second)"
					}
					if !strings.Contains(s, old) {
						t.Fatal("calibration anchor missing")
					}
					if c.id == "shared-callback-deadline" {
						s = strings.Replace(s, "\tctx, cancel := "+old+"\n\tdefer cancel()\n", "", 1)
						if strings.HasSuffix(name, "users_grpc.go") {
							s = strings.Replace(s, "\t\"time\"\n", "", 1)
						}
					} else {
						s = strings.Replace(s, old, replacement, 1)
					}
					if err = os.WriteFile(p, []byte(s), 0644); err != nil {
						t.Fatal(err)
					}
				}
				if c.id == "shared-callback-deadline" {
					p := filepath.Join(req.CandidateDir, "app/command/cancel_training.go")
					raw, err := os.ReadFile(p)
					if err != nil {
						t.Fatal(err)
					}
					s := strings.Replace(string(raw), "\"context\"", "\"context\"\n\"time\"", 1)
					s = strings.Replace(s, "func(ctx context.Context, tr *training.Training) (*training.Training, error) {", "func(ctx context.Context, tr *training.Training) (*training.Training, error) {\nctx, cancel := context.WithTimeout(ctx,30*time.Second)\ndefer cancel()", 1)
					if err = os.WriteFile(p, []byte(s), 0644); err != nil {
						t.Fatal(err)
					}
				}
				files, err := gate.ReadFiles(req.CandidateDir)
				if err != nil {
					t.Fatal(err)
				}
				req.CandidateSHA256 = gate.TreeHash(files)
			}
			calibrateReference(t, ctx, req, backend, c.id, c.status, c.rule, EvaluateWorkouts)
		})
	}
	if matched == 0 {
		t.Fatal("no reference calibration case selected")
	}
}
