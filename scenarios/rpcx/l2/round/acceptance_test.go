package round

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
)

// 用原始 Run 身份重新运行参考，不重命名已有校准记录，不生成新候选。
func TestPublishedCampaignAcceptance(t *testing.T) {
	source := os.Getenv("HARNESS_L2_ROUND_CAMPAIGN")
	if source == "" {
		t.Skip("set HARNESS_L2_ROUND_CAMPAIGN for published controlled campaign calibration")
	}
	if !filepath.IsAbs(source) {
		t.Fatal("campaign path must be absolute")
	}
	root := os.Getenv("HARNESS_L2_ROUND_OUTPUT")
	if !filepath.IsAbs(root) {
		t.Fatal("HARNESS_L2_ROUND_OUTPUT must be absolute")
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	var p campaign.Plan
	readJSON(t, filepath.Join(source, "plan.json"), &p)
	var s campaign.Summary
	readJSON(t, filepath.Join(source, "summary.json"), &s)
	if len(p.Targets) != 3 || p.Profile.Provider != "controlled" || p.Profile.Model != "scripted-test-model" {
		t.Fatal("acceptance requires the three-target controlled model campaign")
	}
	raw, err := os.ReadFile(filepath.Join(source, "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := Manifest{SchemaVersion: 1, ID: "published-three-target-calibration", Kind: Calibration, CampaignID: p.ID, PlanSHA256: gate.Digest(raw)}
	req := Request{ManifestPath: filepath.Join(root, "manifest.json"), CampaignDir: source, EvidenceDir: filepath.Join(root, "round")}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	for i, target := range p.Targets {
		input := target.Input
		a := s.Targets[i].Attempt
		if a == nil || a.Status != campaign.Completed {
			t.Fatal("acceptance requires completed controlled candidates")
		}
		module, scenario := "", ""
		evaluate := evaluation.Evaluate
		switch input.ProfileID {
		case "rpcx-goddd-t3-v1":
			module, scenario = "goddd", "goddd"
		case "rpcx-translation-t3-v1":
			module, scenario = "translation", "translation"
			evaluate = evaluation.EvaluateTranslation
		case "rpcx-workouts-t3-v1":
			module, scenario = "trainings", "workouts"
			evaluate = evaluation.EvaluateWorkouts
		default:
			t.Fatal("unexpected profile")
		}
		fixtures, err := filepath.Abs("../data/rpcx-l2-" + scenario + "-v1/fixtures")
		if err != nil {
			t.Fatal(err)
		}
		run := filepath.Join(source, filepath.FromSlash(a.Directory))
		candidate := filepath.Join(run, "evidence/candidate", module)
		files, err := gate.ReadFiles(candidate)
		if err != nil {
			t.Fatal(err)
		}
		evidence := filepath.Join(root, "reference-"+input.ID)
		report, err := evaluate(ctx, evaluation.Request{TargetID: input.ID, RunID: a.RunID, FixturesDir: fixtures, CandidateDir: candidate, CandidateSHA256: gate.TreeHash(files), SpecPath: input.Specification.Path, EvidenceDir: evidence, Backend: execution.Local{}, TrustedLocal: true})
		if err != nil || report.Automatic != evaluation.Pass {
			t.Fatalf("reference %s: %s %v", input.ID, report.Automatic, err)
		}
		data, err := os.ReadFile(filepath.Join(evidence, "reference.json"))
		if err != nil {
			t.Fatal(err)
		}
		req.References = append(req.References, Reference{TargetID: input.ID, Report: Artifact{Path: filepath.Join(evidence, "reference.json"), SHA256: gate.Digest(data)}})
		m.Targets = append(m.Targets, TargetPin{ID: input.ID, ProfileID: input.ProfileID, BuildConfig: input.BuildConfig, BaselineSHA256: input.Repository.SHA256, SpecSHA256: input.Specification.SHA256, ReferenceVersion: report.ReferenceVersion, ReferenceSHA256: report.ReferenceSHA256, ToolSHA256: report.ToolSHA256})
	}
	write(t, req.ManifestPath, m)
	write(t, filepath.Join(root, "request.json"), req)
	before, err := os.ReadFile(filepath.Join(source, "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	report, err := Summarize(ctx, req)
	if err != nil {
		t.Fatalf("round error: %v", err)
	}
	if !report.Valid || !report.ExecutionComplete || report.Counts.Targets != 3 || report.Counts.Runs != 3 || report.Counts.Released != 3 || report.Counts.Success != 0 || report.Counts.UnknownReleases != 3 || report.Cost == nil || report.Cost.Totals.KnownTokens != 120 {
		t.Fatalf("unexpected controlled round: %+v", report)
	}
	after, err := os.ReadFile(filepath.Join(source, "summary.json"))
	if err != nil || gate.Digest(after) != gate.Digest(before) {
		t.Fatal("original campaign changed", err)
	}
	rawReport, _ := json.MarshalIndent(report, "", "  ")
	t.Logf("calibration round (not model effect metrics): %s\n%s", req.EvidenceDir, rawReport)
}
