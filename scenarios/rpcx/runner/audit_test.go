package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/audit"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/internal/task"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
)

type auditFixture struct {
	root, dir, relative string
	record              campaign.Record
	target              campaign.TargetRecord
	report              Report
}

func auditPut(t *testing.T, root, p string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	auditBytes(t, root, p, append(raw, '\n'))
}
func auditBytes(t *testing.T, root, p string, raw []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(p))
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, raw, 0600); err != nil {
		t.Fatal(err)
	}
}
func (f *auditFixture) publish(t *testing.T) {
	t.Helper()
	auditPut(t, f.dir, "artifacts/report.json", f.report)
	data, _ := os.ReadFile(filepath.Join(f.dir, "artifacts/report.json"))
	f.target.Attempt.Report.SHA256 = digest(data)
	auditPut(t, f.root, "targets/orders/target.json", f.target)
	var ledger budget.Snapshot
	data, err := os.ReadFile(filepath.Join(f.root, "budget/budget.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &ledger); err != nil {
		t.Fatal(err)
	}
	summary := campaign.Summary{CampaignID: "audit", PlanSHA256: f.record.PlanSHA256, Targets: []campaign.TargetRecord{f.target}, Budget: ledger, Counts: campaign.Counts{Completed: 1, Verdicts: map[string]int{quality.NeedsAttention: 1}}}
	auditPut(t, f.root, "summary.json", summary)
	auditBytes(t, f.root, "summary.md", []byte("synthetic summary"))
	data, _ = os.ReadFile(filepath.Join(f.root, "summary.json"))
	f.record.Summary = campaign.Artifact{Path: "summary.json", SHA256: digest(data)}
	auditPut(t, f.root, "campaign.json", f.record)
}

// 仅构造格式和摘要自洽的材料，不执行门禁，也不声称这些候选通过业务验证。
func newAuditFixture(t *testing.T, profileID string) *auditFixture {
	t.Helper()
	p, err := lookupProfile(profileID)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	relative := "targets/orders/runs/run-1"
	dir := filepath.Join(root, relative)
	fixtureRoot := "../fixtures"
	sourceName := p.RepositoryName
	if profileID == ProfileWorkouts {
		sourceName = "workouts"
	}
	if profileID == ProfileT3 {
		fixtureRoot = "../t3/fixtures"
	} else if profileID != ProfileT1T2 {
		fixtureRoot = "../l2/data/rpcx-l2-" + sourceName + "-v1/fixtures"
	}
	files := mustReadTree(t, fixtureRoot)
	if err = files.write(context.Background(), filepath.Join(dir, "baseline")); err != nil {
		t.Fatal(err)
	}
	if err = files.write(context.Background(), filepath.Join(dir, "workspace")); err != nil {
		t.Fatal(err)
	}
	inputTarget := task.Target{ID: "orders", ProfileID: profileID, BuildConfig: p.BuildConfig, Repository: task.Snapshot{Directory: "/original/source", SHA256: p.RepositorySHA256}, Specification: task.Specification{Path: "/original/spec.md", SHA256: p.SpecSHA256}, Dependencies: map[string]task.Snapshot{}}
	for name, sha := range p.Dependencies {
		inputTarget.Dependencies[name] = task.Snapshot{Directory: "/original/" + name, SHA256: sha}
	}
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	modelProfile := budget.Profile{Provider: "controlled", Endpoint: "in-process", Model: "fixed", ContextTokens: 90, MaxOutputTokens: 10, SourceSHA256: strings.Repeat("a", 64), VerifiedAt: now}
	limits := budget.Limits{MaxCalls: 1, MaxTokens: 100}
	plan := campaign.Plan{ID: "audit", Concurrency: 1, ExecutionConcurrency: 1, Budget: limits, Profile: modelProfile, Targets: []campaign.Target{{Input: inputTarget, Budget: limits, Limits: agent.Limits{MaxModelCalls: 1, MaxToolCalls: 1}, Timeout: time.Minute}}}
	auditPut(t, root, "plan.json", plan)
	planRaw, _ := os.ReadFile(filepath.Join(root, "plan.json"))
	modelRaw, _ := json.Marshal(modelProfile)
	input := Input{Target: &inputTarget, ProfileSHA256: p.hash(), RepositoryName: p.RepositoryName, ModelName: "fixed", SpecVersion: p.SpecVersion, SpecSHA256: p.SpecSHA256, BaselineSHA256: p.RepositorySHA256, Files: files.manifest(), Budget: &budget.Reference{CampaignID: "audit", TargetID: "orders", RunID: "run-1", LedgerPath: "/original/campaign/budget/budget.json", ProfileSHA256: digest(modelRaw)}}
	auditPut(t, dir, "inputs/task.json", input)
	auditPut(t, dir, "inputs/profile.json", p)
	specPath := "../specs/" + p.SpecVersion + ".md"
	if profileID == ProfileT3 {
		specPath = "../t3/specs/" + p.SpecVersion + ".md"
	} else if profileID != ProfileT1T2 {
		specPath = "../" + sourceName + "/specs/" + p.SpecVersion + ".md"
	}
	spec, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatal(err)
	}
	auditBytes(t, dir, "inputs/spec/spec.md", spec)
	baseline := BaselineReport{SchemaVersion: 1, Status: "pass", ProfileSHA256: p.hash(), BaselineSHA256: p.RepositorySHA256, LayoutSHA256: files.hash(), SpecSHA256: p.SpecSHA256, BuildConfig: p.BuildConfig, Tests: map[string]string{}}
	addCommand := func(name string) {
		stdout := "preflight/" + name + ".stdout.log"
		stderr := "preflight/" + name + ".stderr.log"
		auditBytes(t, dir, stdout, nil)
		auditBytes(t, dir, stderr, nil)
		baseline.Commands = append(baseline.Commands, BaselineCommand{Name: name, Args: []string{"synthetic"}, Stdout: stdout, Stderr: stderr, StdoutSHA256: digest(nil), StderrSHA256: digest(nil)})
	}
	for _, config := range p.configurations() {
		prefix := ""
		states := map[string]string{}
		for _, id := range p.RequiredTests {
			states[id] = "pass"
		}
		if len(p.BuildConfigurations) > 0 {
			prefix = config.ID + "-"
			if baseline.ConfigurationTests == nil {
				baseline.ConfigurationTests = map[string]map[string]string{}
			}
			baseline.ConfigurationTests[config.ID] = states
		} else {
			baseline.Tests = states
		}
		addCommand(prefix + "build")
		addCommand(prefix + "test")
	}
	for module, tests := range p.PrerequisiteTests {
		if baseline.ModuleTests == nil {
			baseline.ModuleTests = map[string]map[string]string{}
		}
		states := map[string]string{}
		for _, id := range tests {
			states[id] = "pass"
		}
		baseline.ModuleTests[module] = states
		addCommand(module + "-build")
		addCommand(module + "-test")
	}
	auditPut(t, dir, "preflight/baseline.json", baseline)
	candidateSHA := files.module(p.RepositoryName).hash()
	validation := checks.Evaluation{SchemaVersion: 3, VerifierVersion: p.Verifier, SpecVersion: p.SpecVersion, SpecSHA256: p.SpecSHA256, BaselineSHA256: p.RepositorySHA256, CandidateSHA256: candidateSHA, Verdict: quality.NeedsAttention, Checks: map[string]checks.Check{"constructed": {Status: quality.StatusUnknown, RuleIDs: []string{"TEST-001"}}}, Commands: []checks.Command{}}
	if profileID == ProfileT1T2 {
		validation.SchemaVersion = 2
	}
	policy := quality.Policy{SpecSHA256: p.SpecSHA256, Requirements: []quality.Requirement{{ID: "constructed", RuleIDs: []string{"TEST-001"}}}}
	policyPath := "policy.json"
	if profileID == ProfileT1T2 || profileID == ProfileT3 {
		policyPath = "quality-policy.json"
	}
	auditPut(t, dir, "evidence/"+policyPath, policy)
	snapshots := map[string]map[string]string{}
	for _, prefix := range []string{"baseline", "workspace"} {
		for _, name := range append([]string{p.RepositoryName}, sortedKeys(p.Dependencies)...) {
			snapshots[filepath.Join("/original/campaign", relative, prefix, name)] = files.module(name).manifest()
		}
	}
	auditPut(t, dir, "evidence/input-snapshots.json", snapshots)
	files.module(p.RepositoryName).write(context.Background(), filepath.Join(dir, "evidence/baseline", p.RepositoryName))
	files.module(p.RepositoryName).write(context.Background(), filepath.Join(dir, "evidence/candidate", p.RepositoryName))
	switch profileID {
	case ProfileT1T2:
		auditBytes(t, dir, "evidence/context-probe.go.txt", []byte("synthetic probe"))
		raw, _ := json.Marshal(policy)
		validation.MaterialsSHA256 = digest(append(raw, []byte("synthetic probe")...))
	case ProfileT3:
		auditBytes(t, dir, "evidence/verifier-materials/old.go.txt", []byte("old implementation"))
		raw, _ := json.Marshal(map[string]string{"old.go": digest([]byte("old implementation"))})
		validation.MaterialsSHA256 = digest(raw)
	default:
		material := tree{"old.go": []byte("old implementation")}
		material.write(context.Background(), filepath.Join(dir, "evidence/tool"))
		validation.MaterialsSHA256 = material.hash()
		for _, ext := range []string{".json", ".tar.gz"} {
			data, err := os.ReadFile(filepath.Join(filepath.Dir(fixtureRoot), "dependencies/modules"+ext))
			if err != nil {
				t.Fatal(err)
			}
			auditBytes(t, dir, "host-materials/rpcx-"+p.RepositoryName+"-modules"+ext, data)
		}
	}
	for n, ep := range []string{policyPath, "input-snapshots.json"} {
		data, _ := os.ReadFile(filepath.Join(dir, "evidence", ep))
		validation.Evidence = append(validation.Evidence, quality.Evidence{ID: []string{"policy", "input-snapshots"}[n], Kind: "constructed", Path: ep, SHA256: digest(data), Binding: quality.Binding{SpecSHA256: p.SpecSHA256, BaselineSHA256: p.RepositorySHA256, CandidateSHA256: candidateSHA}})
	}
	auditBytes(t, dir, "artifacts/candidate.patch", []byte("synthetic patch"))
	auditPut(t, dir, "artifacts/agent.json", agent.Result{Reason: agent.ExitCompleted})
	auditBytes(t, dir, "artifacts/review.md", []byte("review"))
	record := audit.RunRecord{SchemaVersion: 2, ID: "run-1", TargetID: "orders", Directory: "/original/campaign/" + relative, Status: "Completed", Phase: "Publishing", AgentExitReason: string(agent.ExitCompleted), StartedAt: now, UpdatedAt: now, FinishedAt: &now, CandidateSHA256: candidateSHA, PatchSHA256: digest([]byte("synthetic patch")), Verdict: quality.NeedsAttention}
	auditPut(t, dir, "run.json", record)
	ledger := budget.Snapshot{SchemaVersion: 1, CampaignID: "audit", Revision: 2, Profile: modelProfile, Limits: limits, Targets: map[string]budget.Account{"orders": {Limits: limits, RunID: "run-1"}}, Calls: []budget.Call{}, Rejections: []budget.Rejection{}}
	auditPut(t, root, "budget/budget.json", ledger)
	f := &auditFixture{root: root, dir: dir, relative: relative, record: campaign.Record{SchemaVersion: 1, ID: "audit", Directory: "/original/campaign", Status: campaign.Completed, StartedAt: now, FinishedAt: &now, PlanSHA256: digest(planRaw)}, target: campaign.TargetRecord{ID: "orders", State: campaign.Finished, Attempt: &campaign.Attempt{RunID: "run-1", Directory: relative, Status: campaign.Completed, Verdict: quality.NeedsAttention, Report: campaign.Artifact{Path: relative + "/artifacts/report.json"}}}, report: Report{RunID: "run-1", Status: StatusCompleted, Review: "review.md", AgentRecord: "agent.json", Input: input, Baseline: &baseline, Validation: &validation, CandidateSHA256: record.CandidateSHA256, PatchSHA256: record.PatchSHA256}}
	f.publish(t)
	return f
}
func inspectAuditFixture(t *testing.T, f *auditFixture) audit.Report {
	t.Helper()
	r, err := audit.Inspect(context.Background(), audit.Request{CampaignDir: f.root, OutputDir: filepath.Join(t.TempDir(), "audit")}, InspectRun)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func auditHas(fs []audit.Finding, code string) bool {
	for _, f := range fs {
		if f.Code == code {
			return true
		}
	}
	return false
}
func TestAuditFiveProfiles(t *testing.T) {
	for _, id := range []string{ProfileT1T2, ProfileT3, ProfileGoddd, ProfileTranslation, ProfileWorkouts} {
		t.Run(id, func(t *testing.T) {
			f := newAuditFixture(t, id)
			r := inspectAuditFixture(t, f)
			if r.Consistency != "pass" || !r.ScanComplete {
				t.Fatalf("violations=%+v gaps=%+v", r.Violations, r.Gaps)
			}
		})
	}
}
func TestAuditRejectsBrokenBindings(t *testing.T) {
	cases := []struct {
		name, code string
		change     func(*testing.T, *auditFixture)
	}{
		{"candidate", "tree_digest_mismatch", func(t *testing.T, f *auditFixture) {
			auditBytes(t, f.dir, "workspace/demo-orders/new.go", []byte("changed"))
		}},
		{"extra-baseline", "input_manifest_mismatch", func(t *testing.T, f *auditFixture) { auditBytes(t, f.dir, "baseline/extra.txt", []byte("changed")) }},
		{"spec", "material_digest_mismatch", func(t *testing.T, f *auditFixture) { auditBytes(t, f.dir, "inputs/spec/spec.md", []byte("changed")) }},
		{"profile", "profile_digest_mismatch", func(t *testing.T, f *auditFixture) { auditBytes(t, f.dir, "inputs/profile.json", []byte("{}")) }},
		{"patch", "material_digest_mismatch", func(t *testing.T, f *auditFixture) {
			auditBytes(t, f.dir, "artifacts/candidate.patch", []byte("changed"))
		}},
		{"missing-review", "material_not_published", func(t *testing.T, f *auditFixture) { os.Remove(filepath.Join(f.dir, "artifacts/review.md")) }},
		{"evidence-escape", "unsafe_artifact", func(t *testing.T, f *auditFixture) {
			f.report.Validation.Evidence[0].Path = "../../outside"
			f.publish(t)
		}},
		{"evidence-binding", "evidence_binding_mismatch", func(t *testing.T, f *auditFixture) {
			f.report.Validation.Evidence[0].Binding.CandidateSHA256 = strings.Repeat("0", 64)
			f.publish(t)
		}},
		{"missing-reference", "evidence_reference_missing", func(t *testing.T, f *auditFixture) {
			c := f.report.Validation.Checks["constructed"]
			c.EvidenceIDs = []string{"absent"}
			f.report.Validation.Checks["constructed"] = c
			f.publish(t)
		}},
		{"missing-check", "check_inventory_mismatch", func(t *testing.T, f *auditFixture) { delete(f.report.Validation.Checks, "constructed"); f.publish(t) }},
		{"baseline-not-passed", "baseline_prerequisite_failed", func(t *testing.T, f *auditFixture) {
			f.report.Baseline.Status = "fail"
			auditPut(t, f.dir, "preflight/baseline.json", f.report.Baseline)
			f.publish(t)
		}},
		{"budget-ref", "budget_reference_mismatch", func(t *testing.T, f *auditFixture) {
			f.report.Input.Budget.TargetID = "other"
			auditPut(t, f.dir, "inputs/task.json", f.report.Input)
			f.publish(t)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newAuditFixture(t, ProfileT3)
			tc.change(t, f)
			r := inspectAuditFixture(t, f)
			if r.Consistency != "violated" || !auditHas(r.Violations, tc.code) {
				t.Fatalf("expected %s; violations=%+v gaps=%+v", tc.code, r.Violations, r.Gaps)
			}
		})
	}
}
func TestAuditExistingCampaign(t *testing.T) {
	root := os.Getenv("HARNESS_RPCX_AUDIT_CAMPAIGN")
	if root == "" {
		t.Skip("set HARNESS_RPCX_AUDIT_CAMPAIGN to inspect an existing archive")
	}
	output := os.Getenv("HARNESS_RPCX_AUDIT_OUTPUT")
	if output == "" {
		output = filepath.Join(t.TempDir(), "audit")
	}
	r, err := audit.Inspect(context.Background(), audit.Request{CampaignDir: root, OutputDir: output}, InspectRun)
	if err != nil || r.Consistency != "pass" {
		t.Fatalf("%v violations=%+v gaps=%+v", err, r.Violations, r.Gaps)
	}
}

func TestAuditInterruptedAndUnsupportedRecords(t *testing.T) {
	t.Run("report-before-run", func(t *testing.T) {
		f := newAuditFixture(t, ProfileT3)
		raw, err := os.ReadFile(filepath.Join(f.dir, "run.json"))
		if err != nil {
			t.Fatal(err)
		}
		var record audit.RunRecord
		if err = json.Unmarshal(raw, &record); err != nil {
			t.Fatal(err)
		}
		record.Status = "Running"
		record.FinishedAt = nil
		record.Verdict = ""
		auditPut(t, f.dir, "run.json", record)
		f.record.Status = campaign.Running
		f.record.FinishedAt = nil
		f.record.Summary = campaign.Artifact{}
		auditPut(t, f.root, "campaign.json", f.record)
		auditPut(t, f.root, "targets/orders/target.json", campaign.TargetRecord{ID: "orders", State: campaign.Active})
		os.Remove(filepath.Join(f.root, "summary.json"))
		os.Remove(filepath.Join(f.root, "summary.md"))
		r := inspectAuditFixture(t, f)
		if r.Consistency != "pass" || r.ExecutionState != "incomplete" || !auditHas(r.Observations, "publication_pending") {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("stale-success-report", func(t *testing.T) {
		f := newAuditFixture(t, ProfileT3)
		var record audit.RunRecord
		raw, _ := os.ReadFile(filepath.Join(f.dir, "run.json"))
		json.Unmarshal(raw, &record)
		record.Status = "Failed"
		record.Error = "publication failed"
		record.Verdict = ""
		auditPut(t, f.dir, "run.json", record)
		f.target.Attempt.Status = campaign.Failed
		f.target.Attempt.Verdict = ""
		f.target.Error = "publication failed"
		f.publish(t)
		// 保留旧 Completed 报告，同时修正上层终态的计数。
		var summary campaign.Summary
		raw, _ = os.ReadFile(filepath.Join(f.root, "summary.json"))
		json.Unmarshal(raw, &summary)
		summary.Counts = campaign.Counts{Failed: 1, Verdicts: map[string]int{}}
		auditPut(t, f.root, "summary.json", summary)
		raw, _ = os.ReadFile(filepath.Join(f.root, "summary.json"))
		f.record.Summary.SHA256 = digest(raw)
		auditPut(t, f.root, "campaign.json", f.record)
		r := inspectAuditFixture(t, f)
		if len(r.Violations) != 0 || !auditHas(r.Gaps, "stale_report") {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("future-verifier", func(t *testing.T) {
		f := newAuditFixture(t, ProfileT3)
		f.report.Validation.SchemaVersion = 999
		f.publish(t)
		r := inspectAuditFixture(t, f)
		if len(r.Violations) != 0 || !auditHas(r.Gaps, "unsupported_verifier") {
			t.Fatalf("%+v", r)
		}
	})
}

func TestAuditReportDriftInvalidatesDigestClaims(t *testing.T) {
	f := newAuditFixture(t, ProfileT3)
	f.report.Validation.Evidence[0].SHA256 = strings.Repeat("0", 64)
	f.publish(t)
	wrapper := func(ctx context.Context, v *audit.View, ref audit.RunRef) (audit.RunInspection, error) {
		result, err := InspectRun(ctx, v, ref)
		raw, readErr := os.ReadFile(filepath.Join(f.dir, "artifacts/report.json"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		auditBytes(t, f.dir, "artifacts/report.json", append(raw, ' '))
		return result, err
	}
	r, err := audit.Inspect(context.Background(), audit.Request{CampaignDir: f.root, OutputDir: filepath.Join(t.TempDir(), "audit")}, wrapper)
	if err != nil || len(r.Violations) != 0 || !auditHas(r.Gaps, "source_changed") {
		t.Fatalf("%v %+v", err, r)
	}
}
