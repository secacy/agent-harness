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

func translationCandidate(t *testing.T, patch string) Request {
	t.Helper()
	return scenarioCandidate(t, "translation", "v0.4.0", patch)
}

func fakeSelection(config buildConfiguration) []byte {
	a, _ := json.Marshal(map[string]any{"ImportPath": translationPackage + "/internal/repo/webapi", "GoFiles": []string{config.adapter, "tracing.go", "translation_types.go"}, "IgnoredGoFiles": []string{config.ignored}})
	b, _ := json.Marshal(map[string]any{"ImportPath": translationPackage + "/internal/usecase", "GoFiles": []string{"contracts.go"}, "XTestGoFiles": []string{"translation_test.go", "mocks_repo_test.go"}})
	return append(append(a, '\n'), append(b, '\n')...)
}

func translationFake(t *testing.T) fakeExecutor {
	t.Helper()
	host, err := l2.TranslationMaterials()
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Modules map[string]testInventory `json:"modules"`
	}
	if err = json.Unmarshal(host["tests.json"], &inventory); err != nil {
		t.Fatal(err)
	}
	tests := inventory.Modules["translation"]
	shared := successfulFake(t)
	return fakeExecutor{run: func(ctx context.Context, req execution.Request) (execution.Result, error) {
		out, err := shared.Run(ctx, req)
		if slices.Contains(req.Args, "list") {
			config := translationScenario().configurations[0]
			if slices.Contains(req.Args, "-tags=rpcx_alternate") {
				config = translationScenario().configurations[1]
			}
			out.Stdout = fakeSelection(config)
		}
		if slices.Contains(req.Args, "test") {
			switch {
			case slices.Contains(req.Args, "-run=^TestL2ReferenceTranslation$"):
				out.Stdout = passedEvents(translationReferenceTests(), []string{translationPackage + "/internal/repo/webapi"})
			case slices.Contains(req.Args, "-run=^TestL2ReferenceExpiry$"):
				out.Stdout = passedEvents(translationScenario().expiryTests, []string{translationPackage + "/internal/repo/webapi"})
			default:
				out.Stdout = passedEvents(append(tests.Original, tests.Added...), tests.Packages)
			}
		}
		return out, err
	}}
}

func TestTranslationConfigurationEvidence(t *testing.T) {
	oldFreeze, err := material.ReadFile("data/translation-v1/freeze.json")
	if err != nil || gate.Digest(oldFreeze) != "afeb051cd0d7d71eb3a0795a803147b0d8dd1de35c6454a229960be5bbb3c471" {
		t.Fatal("earlier Translation calibration reference changed", err)
	}
	if _, err := translationData(); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"both-pass", "alternate-no-events", "alternate-build-fails", "oracle-build-fails", "wrong-selection", "baseline-selection-error", "alternate-cancelled"} {
		t.Run(mode, func(t *testing.T) {
			req := translationCandidate(t, "correct")
			fake := translationFake(t)
			req.Backend = fakeExecutor{run: func(ctx context.Context, command execution.Request) (execution.Result, error) {
				out, err := fake.Run(ctx, command)
				alternate := slices.Contains(command.Args, "-tags=rpcx_alternate")
				if !alternate {
					return out, err
				}
				switch mode {
				case "alternate-no-events":
					if slices.Contains(command.Args, "test") && !strings.Contains(command.Directory, "/baseline/") {
						out.Stdout = nil
					}
				case "alternate-build-fails":
					if slices.Contains(command.Args, "build") && strings.Contains(command.Directory, "/candidate/") {
						out.ExitCode = 1
					}
				case "oracle-build-fails":
					if strings.Contains(command.Directory, "/production/") {
						out.Stdout = nil
						out.ExitCode = 1
					}
				case "wrong-selection":
					if slices.Contains(command.Args, "list") && strings.Contains(command.Directory, "/candidate/") {
						out.Stdout = fakeSelection(translationScenario().configurations[0])
					}
				case "baseline-selection-error":
					if slices.Contains(command.Args, "list") && strings.Contains(command.Directory, "/baseline/") {
						out.Stdout = nil
					}
				case "alternate-cancelled":
					return execution.Result{ExitCode: -1}, context.Canceled
				}
				return out, err
			}}
			report, err := EvaluateTranslation(context.Background(), req)
			want := Inconclusive
			if mode == "both-pass" {
				want = Pass
			}
			if mode == "alternate-build-fails" || mode == "wrong-selection" {
				want = Fail
			}
			if report.Automatic != want || len(report.Configurations) != 2 {
				t.Fatalf("status=%s want=%s err=%v configs=%+v", report.Automatic, want, err, report.Configurations)
			}
			if report.Configurations["default"].Checks["expiry"].Status != Pass {
				t.Fatal("default evidence was lost")
			}
			raw, readErr := os.ReadFile(filepath.Join(req.EvidenceDir, "reference.json"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			review, readErr := os.ReadFile(filepath.Join(req.EvidenceDir, "review-template.json"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			assessment, assessErr := Assess(raw, review)
			if assessErr != nil || assessment.Status == Pass {
				t.Fatalf("pending review became complete: %+v %v", assessment, assessErr)
			}
			if want == Fail && assessment.Status != Fail {
				t.Fatal("confirmed configuration violation lost")
			}
			if mode == "both-pass" {
				for _, corruption := range []string{"delete-config", "wrong-tags", "drop-section", "missing-file-evidence", "false-aggregate", "unknown-config"} {
					t.Run(corruption, func(t *testing.T) {
						var changed Report
						_ = json.Unmarshal(raw, &changed)
						entry := changed.Configurations["rpcx_alternate"]
						switch corruption {
						case "delete-config":
							delete(changed.Configurations, "rpcx_alternate")
						case "wrong-tags":
							entry.Tags = ""
							changed.Configurations["rpcx_alternate"] = entry
						case "drop-section":
							delete(entry.Checks, "expiry")
						case "missing-file-evidence":
							entry.SelectedFiles = nil
							changed.Configurations["rpcx_alternate"] = entry
						case "false-aggregate":
							entry.Checks["expiry"] = Observation{Status: Inconclusive, Reason: "missing"}
						case "unknown-config":
							changed.Configurations["other"] = entry
						}
						changedRaw, _ := json.Marshal(changed)
						var updated Review
						_ = json.Unmarshal(review, &updated)
						updated.ReportSHA256 = gate.Digest(changedRaw)
						updatedRaw, _ := json.Marshal(updated)
						if _, err := Assess(changedRaw, updatedRaw); err == nil {
							t.Fatal("invalid configuration report accepted")
						}
					})
				}
			}
		})
	}
}

func TestTranslationSelectedFiles(t *testing.T) {
	config := translationScenario().configurations[1]
	for _, mode := range []string{"pass", "invalid-json", "missing-package", "missing-mock", "wrong-adapter", "command-error"} {
		t.Run(mode, func(t *testing.T) {
			data := fakeSelection(config)
			exit := 0
			switch mode {
			case "invalid-json":
				data = []byte("not json")
			case "missing-package":
				data = nil
			case "missing-mock":
				data = []byte(strings.ReplaceAll(string(data), "mocks_repo_test.go", "other_test.go"))
			case "wrong-adapter":
				data = fakeSelection(translationScenario().configurations[0])
			case "command-error":
				exit = 1
			}
			got, _ := translationSelection(data, exit, config)
			want := Inconclusive
			if mode == "pass" {
				want = Pass
			}
			if mode == "missing-mock" || mode == "wrong-adapter" {
				want = Fail
			}
			if got.Status != want {
				t.Fatalf("%+v", got)
			}
		})
	}
}

// 校准样本和预期由宿主选择，不进入参考输入。
func TestTranslationReferenceAcceptance(t *testing.T) {
	if os.Getenv("HARNESS_L2_REFERENCE_ACCEPTANCE") != "1" {
		t.Skip("set HARNESS_L2_REFERENCE_ACCEPTANCE=1 for reference calibration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
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
		{"correct", Pass, ""}, {"alternate-missing-timeout", Fail, "TIME-001"}, {"default-only-api", Fail, "BUILD-001"},
		{"mapping-helper", Pass, ""}, {"background-fallback", Fail, "CTX-001"}, {"protected-mock", Fail, "PATH-001"}, {"deleted-assertion", Pass, ""},
		{"deadline-variant", Pass, ""}, {"renamed-wrong-deadline", Fail, "TIME-001"},
		{"tagged-fields", Pass, ""},
	} {
		if selected[0] != "" && !slices.Contains(selected, c.id) {
			continue
		}
		matched++
		t.Run(c.id, func(t *testing.T) {
			patch := c.id
			if c.id == "deadline-variant" || c.id == "renamed-wrong-deadline" || c.id == "tagged-fields" {
				patch = "correct"
			}
			req := translationCandidate(t, patch)
			if c.id == "deadline-variant" || c.id == "renamed-wrong-deadline" {
				for _, name := range []string{"translation_google.go", "translation_rpcx_alternate.go"} {
					filename := filepath.Join(req.CandidateDir, "internal/repo/webapi", name)
					data, err := os.ReadFile(filename)
					if err != nil {
						t.Fatal(err)
					}
					old := "context.WithTimeout(ctx, 30*time.Second)"
					replacement := "context.WithDeadline(ctx, time.Now().Add(30*time.Second))"
					if c.id == "renamed-wrong-deadline" {
						replacement = "context.WithTimeout(ctx, 5*time.Second)"
					}
					if !strings.Contains(string(data), old) {
						t.Fatal("calibration source anchor missing")
					}
					if err = os.WriteFile(filename, []byte(strings.Replace(string(data), old, replacement, 1)), 0644); err != nil {
						t.Fatal(err)
					}
				}
			}
			if c.id == "tagged-fields" {
				source := "package webapi\n\ntype translateRequest struct {\nText string `json:\"source_text\"`\nSource string `json:\"from\"`\nDestination string `json:\"to\"`\n}\ntype translateResponse struct { Text string `json:\"result_text\"` }\n"
				if err := os.WriteFile(filepath.Join(req.CandidateDir, "internal/repo/webapi/translation_types.go"), []byte(source), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if patch != c.id {
				files, err := gate.ReadFiles(req.CandidateDir)
				if err != nil {
					t.Fatal(err)
				}
				req.CandidateSHA256 = gate.TreeHash(files)
			}
			calibrateReference(t, ctx, req, backend, c.id, c.status, c.rule, EvaluateTranslation)
		})
	}
	if matched == 0 {
		t.Fatal("no reference calibration case selected")
	}
}
