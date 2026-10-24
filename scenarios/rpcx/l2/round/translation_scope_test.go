package round

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
	"github.com/secacy/agent-harness/scenarios/rpcx/runner"
)

func TestTranslationScopeEnrollmentPreservesReference(t *testing.T) {
	e, plan := enrollmentFixture(t)
	originalReference := e.Targets[1].ReferenceVersion
	e.Targets[1].ProfileID = runner.ProfileTranslationScope
	plan.Targets[1].Input.ProfileID = runner.ProfileTranslationScope
	e.SchemaVersion = 2
	e.Protocol = testProtocol(t, e.Targets, plan)
	raw, _ := json.Marshal(plan)
	m, err := e.Bind(raw)
	if err != nil || m.Targets[1].ReferenceVersion != originalReference {
		t.Fatal("scope profile changed independent reference admission", m, err)
	}
}

// 只改合成记录的场景身份，验证新 Profile 的模块映射与跨场景参考拒绝。
func TestTranslationScopeRoundMapping(t *testing.T) {
	req, m := fakeRound(t)
	var plan campaign.Plan
	readJSON(t, filepath.Join(req.CampaignDir, "plan.json"), &plan)
	var summary campaign.Summary
	readJSON(t, filepath.Join(req.CampaignDir, "summary.json"), &summary)
	for i := range m.Targets {
		m.Targets[i].ProfileID = runner.ProfileTranslationScope
		plan.Targets[i].Input.ProfileID = runner.ProfileTranslationScope
		attempt := summary.Targets[i].Attempt
		dir := filepath.Join(req.CampaignDir, filepath.FromSlash(attempt.Directory))
		if err := os.Rename(filepath.Join(dir, "evidence/candidate/goddd"), filepath.Join(dir, "evidence/candidate/translation")); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(req.CampaignDir, filepath.FromSlash(attempt.Report.Path))
		var report runner.Report
		readJSON(t, path, &report)
		report.Input.Target.ProfileID = runner.ProfileTranslationScope
		attempt.Report.SHA256 = write(t, path, report)
		write(t, filepath.Join(req.CampaignDir, "targets", m.Targets[i].ID, "target.json"), summary.Targets[i])
	}
	m.PlanSHA256 = write(t, filepath.Join(req.CampaignDir, "plan.json"), plan)
	summary.PlanSHA256 = m.PlanSHA256
	publishSummary(t, req, summary, m.PlanSHA256)
	write(t, req.ManifestPath, m)
	out, err := Summarize(context.Background(), req)
	if err != nil || !out.Valid || out.Counts.Candidates != 2 || out.Counts.UnknownReleases != 1 {
		t.Fatal("scope profile module mapping failed", out.Counts, err)
	}
	attachFakeReference(t, &req, &m, "one", evaluation.Pass, false)
	// 默认的 Goddd 合成参考不能因新门禁版本被当成 Translation 参考。
	out, err = Summarize(context.Background(), req)
	if err == nil || out.Valid || out.Metrics != nil {
		t.Fatal("cross-scenario reference accepted")
	}
}
