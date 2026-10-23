package evaluation

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

// TestGHCLIResponseReferenceDocker 只校准已见开发输入，不调用模型或改变旧报告。
func TestGHCLIResponseReferenceDocker(t *testing.T) {
	if os.Getenv("HARNESS_GHCLI_RESPONSE_REFERENCE") != "1" {
		t.Skip("set HARNESS_GHCLI_RESPONSE_REFERENCE=1 for Docker response calibration")
	}
	output := os.Getenv("HARNESS_GHCLI_RESPONSE_REFERENCE_OUTPUT")
	if !filepath.IsAbs(output) {
		t.Fatal("fresh absolute response calibration output is required")
	}
	output, err := gate.Canonical(output)
	if err != nil {
		t.Fatal(err)
	}
	fixtures, err := gate.Canonical("../data/rpcx-l2-ghcli-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	sourceRoot, err := gate.Canonical("../../ghcli/regression/testdata/response-v1")
	if err != nil || gate.Overlap(output, fixtures) || gate.Overlap(output, sourceRoot) {
		t.Fatalf("invalid calibration input/output boundary: %v", err)
	}
	spec, err := gate.Canonical("../../ghcli/specs/v0.6.0.md")
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := gate.ReadFiles(filepath.Join(fixtures, "ghcli"))
	if err != nil {
		t.Fatal(err)
	}
	var origin struct {
		BaselineSHA256 string `json:"baseline_sha256"`
		BoundarySHA256 string `json:"boundary_sha256"`
		Origin         struct {
			CandidateSHA256 string `json:"candidate_sha256"`
		}
	}
	raw, err := os.ReadFile(filepath.Join(sourceRoot, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &origin); err != nil || gate.TreeHash(baseline) != origin.BaselineSHA256 {
		t.Fatalf("development baseline identity changed: %v", err)
	}
	saved, err := os.ReadFile(filepath.Join(sourceRoot, "outbound.go.txt"))
	if err != nil || gate.Digest(saved) != origin.BoundarySHA256 {
		t.Fatalf("saved development boundary identity changed: %v", err)
	}
	replace := func(data []byte, before, after string) []byte {
		t.Helper()
		if bytes.Count(data, []byte(before)) != 1 {
			t.Fatal("calibration edit must identify exactly one occurrence")
		}
		return bytes.Replace(data, []byte(before), []byte(after), 1)
	}
	candidate := maps.Clone(baseline)
	candidate[ghcliDir+"/outbound.go"] = saved
	candidate["go.mod"] = replace(candidate["go.mod"], "example.com/rpcx v1.0.0", "example.com/rpcx/v2 v2.0.0")
	candidate["go.mod"] = replace(candidate["go.mod"], "replace example.com/rpcx => ../rpcx-v1", "replace example.com/rpcx/v2 => ../rpcx-v2")
	if gate.TreeHash(candidate) != origin.Origin.CandidateSHA256 {
		t.Fatal("reconstructed module differs from the original model output")
	}
	preserved := replace(saved, "\tif err != nil {\n\t\tresponse.Response = nil\n\t}\n", "")
	discarded := replace(preserved, "return response.Response, err", "return response.Response, nil")
	if err := os.Mkdir(output, 0755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	backend, err := execution.NewDocker(ctx, execution.DockerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	var summaries []map[string]any
	for _, tc := range []struct {
		name   string
		source []byte
		status Status
		failed []string
	}{
		{"saved-v2", saved, Fail, []string{"ResponseReturnRedirectDeclined"}},
		{"preserve-response-v2", preserved, Pass, nil},
		{"discard-error-v2", discarded, Fail, []string{"ResponseReturnTransportError", "ResponseReturnRedirectDeclined"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := maps.Clone(candidate)
			files[ghcliDir+"/outbound.go"] = tc.source
			root := filepath.Join(output, tc.name)
			dir := filepath.Join(root, "candidate")
			if err := gate.CopyFiles(dir, files); err != nil {
				t.Fatal(err)
			}
			report, err := EvaluateGHCLI(ctx, Request{TargetID: "ghcli", RunID: tc.name, FixturesDir: fixtures, CandidateDir: dir, CandidateSHA256: gate.TreeHash(files), SpecPath: spec, EvidenceDir: filepath.Join(root, "reference"), Backend: backend})
			summaries = append(summaries, map[string]any{"case": tc.name, "candidate_sha256": gate.TreeHash(files), "automatic": report.Automatic, "expected": tc.status, "report": filepath.Join(root, "reference/reference.json"), "reference_sha256": report.ReferenceSHA256, "tool_sha256": report.ToolSHA256, "commands": report.Commands, "error": report.Error})
			if err := gate.WriteJSON(filepath.Join(output, "results.json"), summaries); err != nil {
				t.Fatal(err)
			}
			if err != nil || !report.InputsUnchanged || report.Automatic != tc.status || report.Version != GHCLIVersion || report.ReferenceVersion != GHCLIDataVersion {
				t.Fatalf("reference calibration mismatch: automatic=%s inputs=%t error=%v", report.Automatic, report.InputsUnchanged, err)
			}
			behavior := report.Checks["behavior"]
			for _, name := range []string{"ResponseReturnSuccess", "ResponseReturnTransportError", "ResponseReturnRedirectDeclined", "ResponseReturnUseLastResponse"} {
				want := "pass"
				if slices.Contains(tc.failed, name) {
					want = "fail"
				}
				if behavior.Tests[ghcliPackage+"/TestReference"+name] != want {
					t.Fatalf("response assertion missing or unexpected: %s %+v", name, behavior)
				}
			}
			if tc.status == Pass {
				if len(report.Commands) != 11 || report.Checks["expiry"].Status != Pass {
					t.Fatal("positive control lacks complete expiry evidence")
				}
				ghcliAssertStandardArtifacts(t, filepath.Join(root, "reference"), report)
			} else if behavior.Status != Fail || len(behavior.Findings) != 1 || behavior.Findings[0].Rule != "BEHAVIOR-001" || report.Checks["expiry"].Status != Inconclusive {
				t.Fatal("response violation lacks rule attribution or fabricates expiry evidence")
			}
			reference, err := os.ReadFile(filepath.Join(root, "reference/reference.json"))
			if err != nil {
				t.Fatal(err)
			}
			review, err := os.ReadFile(filepath.Join(root, "reference/review-template.json"))
			if err != nil {
				t.Fatal(err)
			}
			wantAssessment := Fail
			if tc.status == Pass {
				wantAssessment = Inconclusive
			}
			if assessment, err := Assess(reference, review); err != nil || assessment.Status != wantAssessment {
				t.Fatalf("reference consumer mismatch: %+v %v", assessment, err)
			}
		})
	}
}
