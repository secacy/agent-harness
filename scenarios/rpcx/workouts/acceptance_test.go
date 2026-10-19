package workouts

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
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2"
)

func TestCalibrationFreeze(t *testing.T) {
	data, err := os.ReadFile("cases/freeze.json")
	if err != nil || gate.Digest(data) != "e61e9ed77aaa37a12b5127ed6b98b61fe4ac43429c1e887d02cf1065a4b04539" {
		t.Fatal("published calibration identity changed", err)
	}
	var freeze struct {
		SpecSHA256     string            `json:"spec_sha256"`
		BaselineSHA256 string            `json:"baseline_freeze_sha256"`
		Files          map[string]string `json:"files"`
	}
	if err = json.Unmarshal(data, &freeze); err != nil {
		t.Fatal(err)
	}
	if freeze.SpecSHA256 != gate.Digest(Spec()) || freeze.SpecSHA256 != SpecSHA256() || freeze.BaselineSHA256 != l2.WorkoutsFreezeSHA256 || len(freeze.Files) != 10 {
		t.Fatal("calibration scope differs")
	}
	for name, hash := range freeze.Files {
		if !filepath.IsLocal(name) {
			t.Fatal("invalid calibration path")
		}
		data, err := os.ReadFile(filepath.Join("cases", name))
		if err != nil || gate.Digest(data) != hash {
			t.Fatal("calibration material changed:", name, err)
		}
	}
}

// 显式启用真实命令验收，避免常规单测重复等待 30 秒超时探针。
func TestWorkoutsAcceptance(t *testing.T) {
	if os.Getenv("HARNESS_WORKOUTS_ACCEPTANCE") != "1" {
		t.Skip("set HARNESS_WORKOUTS_ACCEPTANCE=1 to execute calibration candidates")
	}
	TestCalibrationFreeze(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	var backend execution.Executor = execution.Local{}
	if os.Getenv("HARNESS_WORKOUTS_BACKEND") == "docker" {
		d, err := execution.NewDocker(ctx, execution.DockerConfig{})
		if err != nil {
			t.Fatal(err)
		}
		backend = d
	}
	output := os.Getenv("HARNESS_WORKOUTS_OUTPUT")
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
			PatchSHA256              string                       `json:"patch_sha256"`
			Checks                   map[string]string            `json:"checks"`
			ConfigurationChecks      map[string]map[string]string `json:"configuration_checks"`
		}
	}
	data, err := os.ReadFile("cases/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	fixtures, err := LoadFixtures("../l2/data/rpcx-l2-workouts-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	only := strings.Split(os.Getenv("HARNESS_WORKOUTS_CASES"), ",")
	matched := 0
	for _, c := range manifest.Cases {
		if only[0] != "" && !slices.Contains(only, c.ID) {
			continue
		}
		matched++
		t.Run(c.ID, func(t *testing.T) {
			patch, err := os.ReadFile(filepath.Join("cases", c.Patch))
			if err != nil || gate.Digest(patch) != c.PatchSHA256 {
				t.Fatal("calibration patch digest mismatch", err)
			}
			root, err := os.MkdirTemp(output, c.ID+"-")
			if err != nil {
				t.Fatal(err)
			}
			t.Log("artifacts:", root)
			if err = gate.CopyFiles(filepath.Join(root, "baseline"), fixtures); err != nil {
				t.Fatal(err)
			}
			if err = gate.CopyFiles(filepath.Join(root, "candidate"), fixtures); err != nil {
				t.Fatal(err)
			}
			if err = gate.CopyFiles(filepath.Join(root, "candidate/trainings"), controlCandidate(t, c.ID)); err != nil {
				t.Fatal(err)
			}
			if err = gate.Write(filepath.Join(root, "spec.md"), Spec()); err != nil {
				t.Fatal(err)
			}
			result, runErr := Evaluate(ctx, checks.EvaluationRequest{BaselineDir: filepath.Join(root, "baseline/trainings"), CandidateDir: filepath.Join(root, "candidate/trainings"), SpecPath: filepath.Join(root, "spec.md"), EvidenceDir: filepath.Join(root, "evidence"), Backend: backend})
			if err = gate.WriteJSON(filepath.Join(root, "evaluation.json"), result); err != nil {
				t.Fatal(err)
			}
			if runErr != nil {
				t.Fatal(runErr)
			}
			if len(c.Checks) != len(result.Checks) {
				t.Fatalf("incomplete check expectations: %d versus %d", len(c.Checks), len(result.Checks))
			}
			if result.Verdict != c.Verdict {
				t.Fatalf("verdict=%s want=%s; see %s", result.Verdict, c.Verdict, root)
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
			if slices.Contains([]string{"trainer-missing-timeout", "background-fallback", "outer-context", "zero-balance-call", "swallowed-error"}, c.ID) {
				attributed := false
				for _, finding := range result.Checks["semantic_behavior"].Findings {
					if finding.Code == "behavior_violation" && slices.Contains(finding.RuleIDs, c.Rule) && len(finding.EvidenceIDs) > 0 {
						attributed = true
					}
				}
				if !attributed {
					t.Fatal("expected violation has no independently observed rule attribution")
				}
			}
		})
	}
	if matched == 0 {
		t.Fatal("no calibration candidates selected")
	}
}
