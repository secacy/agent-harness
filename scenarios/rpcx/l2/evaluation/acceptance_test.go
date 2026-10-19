package evaluation

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

// 校准预期只存在于测试宿主，不传入 Evaluate 或注入参考测试。
func TestGodddReferenceAcceptance(t *testing.T) {
	if os.Getenv("HARNESS_L2_REFERENCE_ACCEPTANCE") != "1" {
		t.Skip("set HARNESS_L2_REFERENCE_ACCEPTANCE=1 for reference calibration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
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
		{"correct", Pass, ""}, {"missing-timeout", Fail, "TIME-001"}, {"background-fallback", Fail, "CTX-003"}, {"mapping-helper", Pass, ""}, {"protected-license", Fail, "PATH-001"}, {"deleted-assertion", Pass, ""},
		{"deadline-variant", Pass, ""}, {"renamed-wrong-deadline", Fail, "TIME-002"},
	} {
		if selected[0] != "" && !slices.Contains(selected, c.id) {
			continue
		}
		matched++
		t.Run(c.id, func(t *testing.T) {
			patch := c.id
			if c.id == "deadline-variant" || c.id == "renamed-wrong-deadline" {
				patch = "correct"
			}
			req := candidateRequest(t, patch)
			if c.id == "deadline-variant" || c.id == "renamed-wrong-deadline" {
				filename := filepath.Join(req.CandidateDir, "routing/proxying.go")
				data, err := os.ReadFile(filename)
				if err != nil {
					t.Fatal(err)
				}
				old := "context.WithTimeout(ctx, 30*time.Second)"
				new := "context.WithDeadline(ctx, time.Now().Add(30*time.Second))"
				if c.id == "renamed-wrong-deadline" {
					new = "context.WithTimeout(ctx, 5*time.Second)"
				}
				if !strings.Contains(string(data), old) {
					t.Fatal("calibration source anchor missing")
				}
				if err = os.WriteFile(filename, []byte(strings.Replace(string(data), old, new, 1)), 0644); err != nil {
					t.Fatal(err)
				}
				files, err := gate.ReadFiles(req.CandidateDir)
				if err != nil {
					t.Fatal(err)
				}
				req.CandidateSHA256 = gate.TreeHash(files)
				// 错误变体保留 correct 的 Run 名称，参考只能依据实际内容判定。
			}
			calibrateReference(t, ctx, req, backend, c.id, c.status, c.rule, Evaluate)
		})
	}
	if matched == 0 {
		t.Fatal("no reference calibration case selected")
	}
}

func calibrateReference(t *testing.T, ctx context.Context, req Request, backend execution.Executor, id string, status Status, rule string, evaluate func(context.Context, Request) (Report, error)) {
	t.Helper()
	req.Backend = backend
	req.TrustedLocal = backend.Info().Kind == execution.KindLocal
	if output := os.Getenv("HARNESS_L2_REFERENCE_OUTPUT"); output != "" {
		output, err := filepath.Abs(output)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.MkdirAll(output, 0755); err != nil {
			t.Fatal(err)
		}
		root, err := os.MkdirTemp(output, id+"-")
		if err != nil {
			t.Fatal(err)
		}
		req.EvidenceDir = filepath.Join(root, "reference")
	}
	report, err := evaluate(ctx, req)
	t.Log("reference artifacts:", req.EvidenceDir)
	if err != nil || report.Automatic != status || !report.InputsUnchanged {
		t.Fatalf("automatic=%s want=%s err=%v checks=%+v", report.Automatic, status, err, report.Checks)
	}
	if rule != "" {
		found := false
		for _, check := range report.Checks {
			for _, finding := range check.Findings {
				if finding.Rule == rule {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("missing reference rule %s", rule)
		}
	}
	reference, err := os.ReadFile(filepath.Join(req.EvidenceDir, "reference.json"))
	if err != nil {
		t.Fatal(err)
	}
	review, err := os.ReadFile(filepath.Join(req.EvidenceDir, "review-template.json"))
	if err != nil {
		t.Fatal(err)
	}
	assessment, err := Assess(reference, review)
	if err != nil {
		t.Fatal(err)
	}
	if status == Pass && assessment.Status != Inconclusive {
		t.Fatal("automatic pass became full pass without review")
	}
	if status == Fail && assessment.Status != Fail {
		t.Fatal("pending review erased violation")
	}
	if err := gate.WriteJSON(filepath.Join(req.EvidenceDir, "calibration.json"), map[string]any{"kind": "development_calibration", "case_id": id, "expected_automatic": status, "actual_automatic": report.Automatic, "assessment": assessment, "manual_review": "not performed"}); err != nil {
		t.Fatal(err)
	}
}
