package ghcli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/analysis"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

// TestSavedCandidateParentClassification 在新目录验证已保存候选，不生成候选或读取独立参考。
func TestSavedCandidateParentClassification(t *testing.T) {
	source := os.Getenv("HARNESS_GHCLI_REGRESSION_CANDIDATE")
	if source == "" {
		t.Skip("set HARNESS_GHCLI_REGRESSION_CANDIDATE and HARNESS_GHCLI_REGRESSION_OUTPUT for saved-candidate Docker regression")
	}
	const candidateSHA256 = "39390941b07031512fd3eac31104acfcb281409e545819d94cf158ecb4c74de9"
	candidate, err := gate.ReadFiles(source)
	if err != nil {
		t.Fatal(err)
	}
	if hash := gate.TreeHash(candidate); hash != candidateSHA256 {
		t.Fatalf("unexpected saved candidate: %s", hash)
	}
	t.Cleanup(func() {
		after, err := gate.ReadFiles(source)
		if err != nil || gate.TreeHash(after) != candidateSHA256 {
			t.Fatalf("saved candidate changed: %v", err)
		}
	})
	output := os.Getenv("HARNESS_GHCLI_REGRESSION_OUTPUT")
	if output == "" {
		t.Fatal("HARNESS_GHCLI_REGRESSION_OUTPUT is required")
	}
	output, err = gate.Canonical(output)
	if err != nil {
		t.Fatal(err)
	}
	source, err = gate.Canonical(source)
	if err != nil {
		t.Fatal(err)
	}
	if gate.Overlap(output, source) {
		t.Fatal("regression output must not overlap the saved candidate")
	}
	if err = os.Mkdir(output, 0755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	backend, err := execution.NewDocker(ctx, execution.DockerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	fixtures := baselineFixture(t)
	baseline := filepath.Join(output, "baseline")
	if err = gate.CopyFiles(baseline, fixtures); err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(output, "spec.md")
	if err = os.WriteFile(spec, Spec(), 0644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, status, reason, verdict string }{
		{"saved-candidate", quality.StatusUnknown, "request_parent_unproven", quality.NeedsAttention},
		{"detached-parent", quality.StatusFail, "request_parent_not_preserved", quality.Rejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(output, tc.name)
			layout := filepath.Join(dir, "input")
			if err := gate.CopyFiles(layout, fixtures); err != nil {
				t.Fatal(err)
			}
			files := make(map[string][]byte, len(candidate))
			for name, data := range candidate {
				files[name] = data
			}
			if tc.name == "detached-parent" {
				before := []byte("context.WithTimeout(parent, outboundCallTimeout)")
				if bytes.Count(files[boundaryFile], before) != 1 {
					t.Fatal("expected exactly one timeout parent mutation")
				}
				files[boundaryFile] = bytes.Replace(files[boundaryFile], before, []byte("context.WithTimeout(context.Background(), outboundCallTimeout)"), 1)
			}
			root := filepath.Join(layout, "ghcli")
			if err := gate.CopyFiles(root, files); err != nil {
				t.Fatal(err)
			}
			evidence := filepath.Join(dir, "evidence")
			out, evalErr := Evaluate(ctx, checks.EvaluationRequest{BaselineDir: filepath.Join(baseline, "ghcli"), CandidateDir: root, SpecPath: spec, EvidenceDir: evidence, CacheDir: filepath.Join(dir, "cache"), Backend: backend})
			if err := gate.WriteJSON(filepath.Join(dir, "report.json"), out); err != nil {
				t.Fatal(err)
			}
			if evalErr != nil {
				t.Fatal(evalErr)
			}
			if out.Verdict != tc.verdict || out.Checks["migration_completeness"].Status != tc.status || out.CandidateSHA256 != gate.TreeHash(files) {
				t.Fatalf("unexpected decision or candidate binding: %+v", out)
			}
			data, err := os.ReadFile(filepath.Join(evidence, "typed-analysis.json"))
			if err != nil {
				t.Fatal(err)
			}
			var typed analysis.Report
			if err = json.Unmarshal(data, &typed); err != nil {
				t.Fatal(err)
			}
			if typed.Status != "complete" || len(typed.Calls) != 1 || typed.Calls[0].Status != tc.status || typed.Calls[0].Reason != tc.reason {
				t.Fatalf("unexpected typed parent evidence: %+v", typed)
			}
			if tc.name == "saved-candidate" {
				for _, name := range []string{"build", "baseline_tests", "dependencies", "protected_paths", "test_preservation", "semantic_behavior"} {
					if out.Checks[name].Status != quality.StatusPass {
						t.Errorf("saved candidate check %s: %+v", name, out.Checks[name])
					}
				}
			}
		})
	}
}
