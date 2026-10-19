package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2"
)

func candidateRequest(t *testing.T, patch string) Request {
	t.Helper()
	fixtures, err := filepath.Abs("../data/rpcx-l2-goddd-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	files, err := gate.ReadFiles(filepath.Join(fixtures, "goddd"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	candidate := filepath.Join(root, "candidate")
	if err := gate.CopyFiles(candidate, files); err != nil {
		t.Fatal(err)
	}
	patchFile, err := filepath.Abs("../../goddd/cases/patches/" + patch + ".patch")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "apply", "--", patchFile)
	cmd.Dir = candidate
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull}
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("apply: %s: %v", data, err)
	}
	files, err = gate.ReadFiles(candidate)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := filepath.Abs("../../goddd/specs/v0.3.0.md")
	if err != nil {
		t.Fatal(err)
	}
	return Request{TargetID: "l2-goddd-booking", RunID: "calibration-" + patch, FixturesDir: fixtures, CandidateDir: candidate, CandidateSHA256: gate.TreeHash(files), SpecPath: spec, EvidenceDir: filepath.Join(root, "evidence"), Backend: execution.Local{}, TrustedLocal: true}
}

type fakeExecutor struct {
	run func(context.Context, execution.Request) (execution.Result, error)
}

func (f fakeExecutor) Info() execution.Info { return execution.Local{}.Info() }
func (f fakeExecutor) Run(ctx context.Context, req execution.Request) (execution.Result, error) {
	return f.run(ctx, req)
}

func passedEvents(tests, packages []string) []byte {
	var data []byte
	for _, pkg := range packages {
		b, _ := json.Marshal(map[string]string{"Action": "start", "Package": pkg})
		data = append(data, append(b, '\n')...)
	}
	for _, test := range tests {
		index := strings.Index(test, "/Test")
		for _, action := range []string{"run", "pass"} {
			b, _ := json.Marshal(map[string]string{"Action": action, "Package": test[:index], "Test": test[index+1:]})
			data = append(data, append(b, '\n')...)
		}
	}
	for _, pkg := range packages {
		b, _ := json.Marshal(map[string]string{"Action": "pass", "Package": pkg})
		data = append(data, append(b, '\n')...)
	}
	return data
}

func successfulFake(t *testing.T) fakeExecutor {
	t.Helper()
	data, err := l2.GodddMaterials()
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Modules map[string]struct {
			Packages []string `json:"packages"`
			Original []string `json:"original_tests"`
			Added    []string `json:"added_tests"`
		} `json:"modules"`
	}
	if err = json.Unmarshal(data["tests.json"], &inventory); err != nil {
		t.Fatal(err)
	}
	tests := inventory.Modules["goddd"]
	return fakeExecutor{run: func(_ context.Context, req execution.Request) (execution.Result, error) {
		out := execution.Result{ExitCode: 0}
		if slices.Contains(req.Args, "edit") {
			if strings.Contains(req.Directory, "/baseline/") {
				out.Stdout = []byte(`{"Require":[{"Path":"example.com/rpcx","Version":"v1.0.0","Indirect":false}],"Replace":[{"Old":{"Path":"example.com/rpcx"},"New":{"Path":"../rpcx-v1"}},{"Old":{"Path":"example.com/rpcx/v2"},"New":{"Path":"../rpcx-v2"}}]}`)
			} else {
				out.Stdout = []byte(`{"Require":[{"Path":"example.com/rpcx/v2","Version":"v2.0.0","Indirect":false}],"Replace":[{"Old":{"Path":"example.com/rpcx/v2"},"New":{"Path":"../rpcx-v2"}}]}`)
			}
		}
		if slices.Contains(req.Args, "test") {
			switch {
			case slices.Contains(req.Args, "-run=^TestL2ReferenceRoutes$"):
				out.Stdout = passedEvents(referenceTests(), []string{godddPackage + "/server"})
			case slices.Contains(req.Args, "-run=^TestL2ReferenceExpiry$"):
				out.Stdout = passedEvents([]string{godddPackage + "/server/TestL2ReferenceExpiry"}, []string{godddPackage + "/server"})
			default:
				out.Stdout = passedEvents(append(tests.Original, tests.Added...), tests.Packages)
			}
		}
		return out, nil
	}}
}

func TestReferenceMechanisms(t *testing.T) {
	for _, mode := range []string{"pass", "digest-mismatch", "command-error", "empty-events", "oracle-build-error", "baseline-build-error", "candidate-build-error", "input-drift", "material-drift", "log-drift"} {
		t.Run(mode, func(t *testing.T) {
			req := candidateRequest(t, "correct")
			fake := successfulFake(t)
			calls := 0
			req.Backend = fakeExecutor{run: func(ctx context.Context, command execution.Request) (execution.Result, error) {
				calls++
				if mode == "command-error" {
					return execution.Result{ExitCode: -1}, context.DeadlineExceeded
				}
				if mode == "input-drift" && calls == 1 {
					if err := os.WriteFile(filepath.Join(req.CandidateDir, "LICENSE"), []byte("changed"), 0644); err != nil {
						t.Error(err)
					}
				}
				if mode == "material-drift" && calls == 1 {
					if err := os.WriteFile(filepath.Join(req.EvidenceDir, "materials/spec.md"), []byte("changed"), 0644); err != nil {
						t.Error(err)
					}
				}
				if mode == "log-drift" && calls == 2 {
					if err := os.WriteFile(filepath.Join(req.EvidenceDir, "logs/go-version.stdout.log"), []byte("changed"), 0644); err != nil {
						t.Error(err)
					}
				}
				if mode == "candidate-build-error" && slices.Contains(command.Args, "build") && strings.Contains(command.Directory, "/candidate/") {
					return execution.Result{ExitCode: 1}, nil
				}
				if mode == "baseline-build-error" && slices.Contains(command.Args, "build") && strings.Contains(command.Directory, "/baseline/") {
					return execution.Result{ExitCode: 1}, nil
				}
				if mode == "oracle-build-error" && slices.Contains(command.Args, "-run=^TestL2ReferenceRoutes$") {
					return execution.Result{ExitCode: 1, Stdout: []byte("{\"Action\":\"fail\",\"Package\":\"github.com/marcusolsson/goddd/server\"}\n")}, nil
				}
				out, err := fake.Run(ctx, command)
				if mode == "empty-events" && slices.Contains(command.Args, "test") && !strings.Contains(command.Directory, "/production/") {
					out.Stdout = nil
				}
				return out, err
			}}
			if mode == "digest-mismatch" {
				req.CandidateSHA256 = strings.Repeat("0", 64)
			}
			report, err := Evaluate(context.Background(), req)
			want := Inconclusive
			if mode == "pass" {
				want = Pass
			}
			if mode == "candidate-build-error" {
				want = Fail
			}
			if report.Automatic != want {
				t.Fatalf("automatic=%s want=%s report=%+v err=%v", report.Automatic, want, report, err)
			}
			if (mode == "command-error" || mode == "digest-mismatch" || mode == "input-drift" || mode == "material-drift" || mode == "log-drift" || mode == "baseline-build-error") && err == nil {
				t.Fatal("missing execution/identity error")
			}
			if mode == "digest-mismatch" && calls != 0 {
				t.Fatal("invalid input reached executor")
			}
			if mode == "input-drift" && report.InputsUnchanged {
				t.Fatal("input drift accepted")
			}
			data, readErr := os.ReadFile(filepath.Join(req.EvidenceDir, "reference.json"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			review, readErr := os.ReadFile(filepath.Join(req.EvidenceDir, "review-template.json"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			assessment, assessErr := Assess(data, review)
			if assessErr != nil {
				t.Fatal(assessErr)
			}
			if assessment.Status == Pass {
				t.Fatal("pending manual review became complete pass")
			}
			if mode == "candidate-build-error" && assessment.Status != Fail {
				t.Fatal("pending review erased confirmed failure")
			}
		})
	}
}

func TestReferenceDataAndInputBoundaries(t *testing.T) {
	oldFreeze, err := material.ReadFile("data/goddd-v1/freeze.json")
	if err != nil || gate.Digest(oldFreeze) != "25471d9a65936cf18af69416f141d9f975235e04ad629e4db5563d75e3f93abb" {
		t.Fatal("earlier calibration reference data changed", err)
	}
	if _, err := referenceData(); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"spec", "sdk", "overlap", "trust", "existing-output", "pre-cancelled"} {
		t.Run(mode, func(t *testing.T) {
			req := candidateRequest(t, "correct")
			calls := 0
			req.Backend = fakeExecutor{run: func(context.Context, execution.Request) (execution.Result, error) {
				calls++
				return execution.Result{}, errors.New("unexpected call")
			}}
			ctx := context.Background()
			switch mode {
			case "spec":
				req.SpecPath = filepath.Join(t.TempDir(), "spec.md")
				if err := os.WriteFile(req.SpecPath, []byte("wrong spec"), 0644); err != nil {
					t.Fatal(err)
				}
			case "sdk":
				files, err := gate.ReadFiles(req.FixturesDir)
				if err != nil {
					t.Fatal(err)
				}
				files["rpcx-v2/client.go"] = []byte("package rpcx")
				req.FixturesDir = filepath.Join(t.TempDir(), "fixtures")
				if err = gate.CopyFiles(req.FixturesDir, files); err != nil {
					t.Fatal(err)
				}
			case "overlap":
				req.EvidenceDir = filepath.Join(req.CandidateDir, "evidence")
			case "trust":
				req.TrustedLocal = false
			case "existing-output":
				if err := os.Mkdir(req.EvidenceDir, 0755); err != nil {
					t.Fatal(err)
				}
			case "pre-cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, err := Evaluate(ctx, req); err == nil || calls != 0 {
				t.Fatalf("invalid input accepted err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestReviewBindingAndUnknown(t *testing.T) {
	req := candidateRequest(t, "mapping-helper")
	req.Backend = successfulFake(t)
	_, err := Evaluate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	reference, err := os.ReadFile(filepath.Join(req.EvidenceDir, "reference.json"))
	if err != nil {
		t.Fatal(err)
	}
	template, err := os.ReadFile(filepath.Join(req.EvidenceDir, "review-template.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"pending", "complete", "deleted-assertion", "wrong-run", "wrong-candidate", "wrong-report", "missing-author", "missing-item", "missing-evidence", "invalid-input", "unknown-field"} {
		t.Run(mode, func(t *testing.T) {
			var review Review
			if err := json.Unmarshal(template, &review); err != nil {
				t.Fatal(err)
			}
			review.Kind = "development_calibration"
			review.Reviewer = "synthetic-test-only"
			review.RecordedAt = time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
			for key := range review.Items {
				review.Items[key] = ReviewItem{Status: Pass, Reason: "synthetic review mechanism fixture", Files: []string{"candidate/file.go:1"}, Evidence: []string{"test-only://fixture"}}
			}
			raw := append([]byte{}, reference...)
			switch mode {
			case "pending":
				_ = json.Unmarshal(template, &review)
			case "deleted-assertion":
				review.Items["test_preservation"] = ReviewItem{Status: Fail, Reason: "synthetic deleted assertion", Files: []string{"candidate/booking/service_test.go:1"}, Evidence: []string{"test-only://diff"}}
			case "wrong-run":
				review.RunID = "other-run"
			case "wrong-candidate":
				review.CandidateSHA256 = strings.Repeat("0", 64)
			case "wrong-report":
				review.ReportSHA256 = strings.Repeat("0", 64)
			case "missing-author":
				review.Reviewer = ""
			case "missing-item":
				delete(review.Items, "change_coverage")
			case "missing-evidence":
				item := review.Items["change_coverage"]
				item.Evidence = nil
				review.Items["change_coverage"] = item
			case "invalid-input":
				var report Report
				_ = json.Unmarshal(raw, &report)
				report.InputsUnchanged = false
				raw, _ = json.Marshal(report)
				review.ReportSHA256 = gate.Digest(raw)
			}
			data, _ := json.Marshal(review)
			if mode == "unknown-field" {
				data = append([]byte(`{"unexpected":true,`), data[1:]...)
			}
			assessment, err := Assess(raw, data)
			switch mode {
			case "pending", "invalid-input":
				if err != nil || assessment.Status != Inconclusive {
					t.Fatalf("%+v %v", assessment, err)
				}
			case "complete":
				if err != nil || assessment.Status != Pass || assessment.Kind != "development_calibration" {
					t.Fatalf("%+v %v", assessment, err)
				}
			case "deleted-assertion":
				if err != nil || assessment.Status != Fail {
					t.Fatalf("%+v %v", assessment, err)
				}
			default:
				if err == nil {
					t.Fatal("invalid review accepted")
				}
			}
		})
	}
}
