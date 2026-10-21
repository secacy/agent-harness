package round

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
	"github.com/secacy/agent-harness/scenarios/rpcx/runner"
)

// 仅改合成记录的通用身份，不读取新应用、门禁或参考控制。
func ghcliRoundFixture(t *testing.T) (Request, Manifest) {
	t.Helper()
	req, m := fakeRound(t)
	var plan campaign.Plan
	readJSON(t, filepath.Join(req.CampaignDir, "plan.json"), &plan)
	var summary campaign.Summary
	readJSON(t, filepath.Join(req.CampaignDir, "summary.json"), &summary)
	for i := range m.Targets {
		m.Targets[i].ProfileID = "rpcx-ghcli-t3-v1"
		m.Targets[i].ReferenceVersion = evaluation.GHCLIDataVersion
		plan.Targets[i].Input.ProfileID = m.Targets[i].ProfileID
		attempt := summary.Targets[i].Attempt
		dir := filepath.Join(req.CampaignDir, filepath.FromSlash(attempt.Directory))
		if err := os.Rename(filepath.Join(dir, "evidence/candidate/goddd"), filepath.Join(dir, "evidence/candidate/ghcli")); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(req.CampaignDir, filepath.FromSlash(attempt.Report.Path))
		var report runner.Report
		readJSON(t, path, &report)
		report.Input.Target.ProfileID = m.Targets[i].ProfileID
		attempt.Report.SHA256 = write(t, path, report)
		write(t, filepath.Join(req.CampaignDir, "targets", m.Targets[i].ID, "target.json"), summary.Targets[i])
	}
	m.PlanSHA256 = write(t, filepath.Join(req.CampaignDir, "plan.json"), plan)
	summary.PlanSHA256 = m.PlanSHA256
	publishSummary(t, req, summary, m.PlanSHA256)
	write(t, req.ManifestPath, m)
	return req, m
}

func TestGHCLIRoundCalibrationUsesModuleMapping(t *testing.T) {
	req, _ := ghcliRoundFixture(t)
	out, err := Summarize(context.Background(), req)
	if err != nil || !out.Valid || !out.InputsUnchanged || out.Counts.Candidates != 2 || out.Counts.UnknownReleases != 1 {
		t.Fatalf("synthetic GHCLI calibration failed: counts=%+v err=%v", out.Counts, err)
	}
}

func TestGHCLIRoundRejectsCrossTargetAndRunReference(t *testing.T) {
	for _, mode := range []string{"target", "run", "scenario"} {
		t.Run(mode, func(t *testing.T) {
			req, m := ghcliRoundFixture(t)
			attachFakeReference(t, &req, &m, "one", evaluation.Pass, false)
			ref := &req.References[0]
			var report evaluation.Report
			readJSON(t, ref.Report.Path, &report)
			report.Version = evaluation.GHCLIVersion
			switch mode {
			case "target":
				report.TargetID = "another-target"
			case "run":
				report.RunID = "another-run"
			case "scenario":
				report.Version = evaluation.Version
			}
			ref.Report.SHA256 = write(t, ref.Report.Path, report)
			out, err := Summarize(context.Background(), req)
			if err == nil || out.Valid || out.Metrics != nil {
				t.Fatalf("mismatched reference accepted: valid=%t err=%v", out.Valid, err)
			}
		})
	}
}

func TestGHCLIRoundProtocolEligibility(t *testing.T) {
	for _, mode := range []string{"schema1-calibration", "schema1-development", "schema1-formal", "schema1-heldout", "schema2-calibration", "schema2-development", "schema2-formal", "schema2-heldout"} {
		t.Run(mode, func(t *testing.T) {
			req, m := ghcliRoundFixture(t)
			var plan campaign.Plan
			readJSON(t, filepath.Join(req.CampaignDir, "plan.json"), &plan)
			switch {
			case strings.HasSuffix(mode, "development"):
				m.Kind = Development
			case strings.HasSuffix(mode, "formal"):
				m.Kind = Formal
			case strings.HasSuffix(mode, "heldout"):
				m.Kind = "heldout"
			}
			if strings.HasPrefix(mode, "schema2") {
				m.SchemaVersion = 2
				m.Protocol = testProtocol(t, m.Targets, plan)
				if strings.HasSuffix(mode, "heldout") {
					m.Kind = Calibration
					m.Protocol.Split = "heldout"
				}
			}
			enrollment := Enrollment{SchemaVersion: m.SchemaVersion, ID: m.ID, Kind: m.Kind, Targets: m.Targets, Protocol: m.Protocol}
			// 合成记录不认证参考字节；实际发布身份另由四任务纳入测试覆盖。
			err := enrollment.validate(plan.Targets, false)
			manifestErr := validateManifest(m)
			allowed := mode == "schema1-calibration" || mode == "schema2-calibration" || mode == "schema2-development" || mode == "schema2-formal"
			if allowed {
				if err != nil || manifestErr != nil {
					t.Fatalf("allowed GHCLI round rejected: %v %v", err, manifestErr)
				}
			} else if err == nil || manifestErr == nil {
				t.Fatalf("unadmitted GHCLI round accepted: %v %v", err, manifestErr)
			}
		})
	}
}
