package goddd

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
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
)

func TestCalibrationFreeze(t *testing.T) {
	data, err := os.ReadFile("cases/freeze.json")
	if err != nil || digest(data) != "618b81a7212dc1e1f3c1fe96cf9109a6da4cdec9dfcc8b88348e9ed92ab22317" {
		t.Fatal("published calibration identity changed", err)
	}
	var freeze struct {
		SpecSHA256 string            `json:"spec_sha256"`
		Files      map[string]string `json:"files"`
	}
	if err = json.Unmarshal(data, &freeze); err != nil {
		t.Fatal(err)
	}
	if freeze.SpecSHA256 != digest(Spec()) || len(freeze.Files) != 7 {
		t.Fatal("calibration scope differs")
	}
	for name, hash := range freeze.Files {
		if !filepath.IsLocal(name) {
			t.Fatal("invalid calibration path")
		}
		data, err := os.ReadFile(filepath.Join("cases", name))
		if err != nil || digest(data) != hash {
			t.Fatal("calibration material changed:", name, err)
		}
	}
}

// 显式启用真实命令验收，避免常规单测重复等待 30 秒超时探针。
func TestGodddAcceptance(t *testing.T) {
	if os.Getenv("HARNESS_GODDD_ACCEPTANCE") != "1" {
		t.Skip("set HARNESS_GODDD_ACCEPTANCE=1 to execute calibration candidates")
	}
	TestCalibrationFreeze(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	var backend execution.Executor = execution.Local{}
	if os.Getenv("HARNESS_GODDD_BACKEND") == "docker" {
		d, err := execution.NewDocker(ctx, execution.DockerConfig{})
		if err != nil {
			t.Fatal(err)
		}
		backend = d
	}
	output := os.Getenv("HARNESS_GODDD_OUTPUT")
	if output == "" {
		output = t.TempDir()
	}
	output, err := filepath.Abs(output)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(output, 0755); err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Cases []struct {
			ID, Patch, Verdict, Rule string
			PatchSHA256              string            `json:"patch_sha256"`
			Checks                   map[string]string `json:"checks"`
		}
	}
	data, err := os.ReadFile("cases/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	fixtures, err := LoadFixtures("../l2/data/rpcx-l2-goddd-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	only := strings.Split(os.Getenv("HARNESS_GODDD_CASES"), ",")
	matched := 0
	for _, c := range manifest.Cases {
		if only[0] != "" && !slices.Contains(only, c.ID) {
			continue
		}
		matched++
		t.Run(c.ID, func(t *testing.T) {
			patch, err := os.ReadFile(filepath.Join("cases", c.Patch))
			if err != nil || digest(patch) != c.PatchSHA256 {
				t.Fatal("calibration patch digest mismatch", err)
			}
			root, err := os.MkdirTemp(output, c.ID+"-")
			if err != nil {
				t.Fatal(err)
			}
			t.Log("artifacts:", root)
			if err = copyFiles(filepath.Join(root, "baseline"), fixtures); err != nil {
				t.Fatal(err)
			}
			if err = copyFiles(filepath.Join(root, "candidate"), fixtures); err != nil {
				t.Fatal(err)
			}
			if err = copyFiles(filepath.Join(root, "candidate/goddd"), controlCandidate(t, c.ID)); err != nil {
				t.Fatal(err)
			}
			if err = write(filepath.Join(root, "spec.md"), Spec()); err != nil {
				t.Fatal(err)
			}
			result, runErr := Evaluate(ctx, checks.EvaluationRequest{BaselineDir: filepath.Join(root, "baseline/goddd"), CandidateDir: filepath.Join(root, "candidate/goddd"), SpecPath: filepath.Join(root, "spec.md"), EvidenceDir: filepath.Join(root, "evidence"), Backend: backend})
			if err = writeJSON(filepath.Join(root, "evaluation.json"), result); err != nil {
				t.Fatal(err)
			}
			if runErr != nil {
				t.Fatal(runErr)
			}
			if result.Verdict != c.Verdict {
				t.Fatalf("verdict=%s want=%s checks=%+v", result.Verdict, c.Verdict, result.Checks)
			}
			for id, status := range c.Checks {
				if result.Checks[id].Status != status {
					t.Errorf("%s=%s want %s", id, result.Checks[id].Status, status)
				}
			}
			found := false
			for _, check := range result.Checks {
				if slices.Contains(check.RuleIDs, c.Rule) && (check.Status == "pass" && c.Verdict == "ReadyForReview" || check.Status == "fail" && c.Verdict == "Rejected" || check.Status == "unknown" && c.Verdict == "NeedsAttention") {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing finding for specification rule %s", c.Rule)
			}
		})
	}
	if matched == 0 {
		t.Fatal("no calibration candidates selected")
	}
}
