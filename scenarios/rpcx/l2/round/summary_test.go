package round

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/task"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
	"github.com/secacy/agent-harness/scenarios/rpcx/runner"
)

func write(t *testing.T, path string, value any) string {
	t.Helper()
	if err := gate.WriteJSON(path, value); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return gate.Digest(data)
}

func fakeRound(t *testing.T) (Request, Manifest) {
	t.Helper()
	root := t.TempDir()
	camp := filepath.Join(root, "campaign")
	h := strings.Repeat("a", 64)
	m := Manifest{SchemaVersion: 1, ID: "synthetic-round", Kind: Calibration, CampaignID: "synthetic-campaign"}
	p := campaign.Plan{ID: m.CampaignID, Profile: budget.Profile{Provider: "controlled", Endpoint: "in-process", Model: "synthetic-only", ContextTokens: 90, MaxOutputTokens: 10, SourceSHA256: h, VerifiedAt: time.Now().UTC()}, Budget: budget.Limits{MaxCalls: 3, MaxTokens: 300}}
	s := campaign.Summary{CampaignID: p.ID, Counts: campaign.Counts{Verdicts: map[string]int{}}, Budget: budget.Snapshot{SchemaVersion: 1, CampaignID: p.ID, Profile: p.Profile, Limits: p.Budget, Targets: map[string]budget.Account{}}}
	now := time.Now().UTC()
	finish := now.Add(time.Second)
	for i, id := range []string{"one", "two", "three"} {
		input := task.Target{ID: id, ProfileID: "rpcx-goddd-t3-v1", BuildConfig: "default", Repository: task.Snapshot{Directory: "/synthetic-input/" + id, SHA256: h}, Specification: task.Specification{Path: "/synthetic-spec", SHA256: h}}
		limit := budget.Limits{MaxCalls: 1, MaxTokens: 100}
		p.Targets = append(p.Targets, campaign.Target{Input: input, Budget: limit})
		pin := TargetPin{ID: id, ProfileID: input.ProfileID, BuildConfig: input.BuildConfig, BaselineSHA256: h, SpecSHA256: h, ReferenceVersion: "synthetic-reference", ReferenceSHA256: h, ToolSHA256: h}
		m.Targets = append(m.Targets, pin)
		runID := "run-" + id
		dir := filepath.Join(camp, "targets", id, "runs", runID)
		candidate := map[string][]byte{"code.go": []byte("package example\n")}
		candidateHash := gate.TreeHash(candidate)
		if err := gate.CopyFiles(filepath.Join(dir, "evidence/candidate/goddd"), candidate); err != nil {
			t.Fatal(err)
		}
		verdict := "ReadyForReview"
		if i == 1 {
			verdict = "NeedsAttention"
		}
		status := runner.StatusCompleted
		code := ""
		if i == 2 {
			status = runner.StatusFailed
			code = "baseline_failed"
			verdict = ""
		}
		record := runner.Record{SchemaVersion: 2, ID: runID, TargetID: id, Directory: dir, Status: status, StartedAt: now, FinishedAt: &finish, Verdict: verdict, FailureCode: code}
		if status == runner.StatusCompleted {
			record.CandidateSHA256 = candidateHash
		}
		write(t, filepath.Join(dir, "run.json"), record)
		report := runner.Report{RunID: runID, Status: status, FailureCode: code, Input: runner.Input{Target: &input, SpecSHA256: h, BaselineSHA256: h, Budget: &budget.Reference{CampaignID: p.ID, TargetID: id, RunID: runID}}}
		if status == runner.StatusCompleted {
			report.CandidateSHA256 = candidateHash
			report.Validation = &checks.Evaluation{Verdict: verdict, CandidateSHA256: candidateHash, SpecSHA256: h, BaselineSHA256: h}
		}
		path := filepath.Join(dir, "artifacts/report.json")
		hash := write(t, path, report)
		relative, _ := filepath.Rel(camp, dir)
		reportRelative, _ := filepath.Rel(camp, path)
		a := campaign.Attempt{RunID: runID, Directory: filepath.ToSlash(relative), Status: campaign.Status(status), Verdict: verdict, FailureCode: code, Report: campaign.Artifact{Path: filepath.ToSlash(reportRelative), SHA256: hash}}
		target := campaign.TargetRecord{ID: id, State: campaign.Finished, Attempt: &a}
		write(t, filepath.Join(camp, "targets", id, "target.json"), target)
		s.Targets = append(s.Targets, target)
		if status == runner.StatusCompleted {
			s.Counts.Completed++
			s.Counts.Verdicts[verdict]++
		} else {
			s.Counts.Failed++
		}
		s.Budget.Targets[id] = budget.Account{Limits: limit}
	}
	m.PlanSHA256 = write(t, filepath.Join(camp, "plan.json"), p)
	s.PlanSHA256 = m.PlanSHA256
	write(t, filepath.Join(camp, "budget/budget.json"), s.Budget)
	hash := write(t, filepath.Join(camp, "summary.json"), s)
	write(t, filepath.Join(camp, "campaign.json"), campaign.Record{SchemaVersion: 1, ID: p.ID, Status: campaign.Completed, PlanSHA256: m.PlanSHA256, StartedAt: now, FinishedAt: &finish, Summary: campaign.Artifact{Path: "summary.json", SHA256: hash}})
	manifestPath := filepath.Join(root, "manifest.json")
	write(t, manifestPath, m)
	return Request{ManifestPath: manifestPath, CampaignDir: camp, EvidenceDir: filepath.Join(root, "round")}, m
}

func attachFakeReference(t *testing.T, req *Request, m *Manifest, target string, status evaluation.Status, complete bool) {
	t.Helper()
	i := slices.IndexFunc(m.Targets, func(p TargetPin) bool { return p.ID == target })
	pin := &m.Targets[i]
	data := map[string][]byte{"tool.go": []byte("package synthetic\n")}
	refRoot := filepath.Join(filepath.Dir(req.ManifestPath), "reference-"+target)
	toolHash := gate.TreeHash(data)
	if err := gate.CopyFiles(filepath.Join(refRoot, "materials/tools"), data); err != nil {
		t.Fatal(err)
	}
	freeze := []byte("synthetic reference data\n")
	spec := []byte("synthetic specification\n")
	if err := gate.Write(filepath.Join(refRoot, "materials/reference/freeze.json"), freeze); err != nil {
		t.Fatal(err)
	}
	// 用同一伪摘要固定任务输入；机制对照不冒充真实场景材料认证。
	if err := gate.Write(filepath.Join(refRoot, "materials/spec.md"), spec); err != nil {
		t.Fatal(err)
	}
	pin.ToolSHA256 = toolHash
	pin.ReferenceSHA256 = gate.Digest(freeze)
	report := evaluation.Report{SchemaVersion: 1, Version: evaluation.Version, TargetID: target, RunID: "run-" + target, CandidateSHA256: gate.TreeHash(map[string][]byte{"code.go": []byte("package example\n")}), BaselineSHA256: pin.BaselineSHA256, SpecSHA256: pin.SpecSHA256, ReferenceVersion: pin.ReferenceVersion, ReferenceSHA256: pin.ReferenceSHA256, ToolSHA256: pin.ToolSHA256, InputsUnchanged: true, Automatic: status, Checks: map[string]evaluation.Observation{}, Artifacts: map[string]string{}, Commands: []evaluation.Command{{ID: "synthetic", Stdout: "logs/out", Stderr: "logs/err"}}}
	// 规范摘要不能伪造文件字节，替换整套原记录的同一输入绑定。
	pin.SpecSHA256 = gate.Digest(spec)
	report.SpecSHA256 = pin.SpecSHA256
	for _, name := range []string{"dependencies", "protected_paths", "build", "candidate_tests", "behavior", "expiry"} {
		report.Checks[name] = evaluation.Observation{Status: evaluation.Pass}
	}
	if status != evaluation.Pass {
		report.Checks["behavior"] = evaluation.Observation{Status: status, Reason: "synthetic evidence fixture"}
	}
	for _, name := range []string{"logs/out", "logs/err"} {
		if err := gate.Write(filepath.Join(refRoot, name), []byte("synthetic log\n")); err != nil {
			t.Fatal(err)
		}
		report.Artifacts[name] = gate.Digest([]byte("synthetic log\n"))
	}
	path := filepath.Join(refRoot, "reference.json")
	hash := write(t, path, report)
	review := evaluation.Review{SchemaVersion: 1, Kind: "development_calibration", TargetID: target, RunID: report.RunID, CandidateSHA256: report.CandidateSHA256, ReportSHA256: hash, Items: map[string]evaluation.ReviewItem{}, Reviewer: "synthetic-test-only", RecordedAt: time.Now().UTC().Format(time.RFC3339)}
	for _, name := range []string{"test_preservation", "change_coverage", "delivery_evidence"} {
		item := evaluation.ReviewItem{Status: evaluation.Inconclusive, Reason: "pending"}
		if complete {
			item = evaluation.ReviewItem{Status: evaluation.Pass, Reason: "synthetic test mechanism", Files: []string{"test-only://source"}, Evidence: []string{"test-only://evidence"}}
		}
		review.Items[name] = item
	}
	reviewPath := filepath.Join(refRoot, "review.json")
	reviewHash := write(t, reviewPath, review)
	req.References = append(req.References, Reference{TargetID: target, Report: Artifact{Path: path, SHA256: hash}, Review: &Artifact{Path: reviewPath, SHA256: reviewHash}})
	// 同步伪计划和原交付绑定，模拟宿主在事前固定的规范身份。
	var plan campaign.Plan
	readJSON(t, filepath.Join(req.CampaignDir, "plan.json"), &plan)
	plan.Targets[i].Input.Specification.SHA256 = pin.SpecSHA256
	var r runner.Report
	runReport := filepath.Join(req.CampaignDir, "targets", target, "runs", "run-"+target, "artifacts/report.json")
	readJSON(t, runReport, &r)
	r.Input.SpecSHA256 = pin.SpecSHA256
	r.Input.Target.Specification.SHA256 = pin.SpecSHA256
	r.Validation.SpecSHA256 = pin.SpecSHA256
	runHash := write(t, runReport, r)
	var s campaign.Summary
	readJSON(t, filepath.Join(req.CampaignDir, "summary.json"), &s)
	s.Targets[i].Attempt.Report.SHA256 = runHash
	write(t, filepath.Join(req.CampaignDir, "targets", target, "target.json"), s.Targets[i])
	m.PlanSHA256 = write(t, filepath.Join(req.CampaignDir, "plan.json"), plan)
	s.PlanSHA256 = m.PlanSHA256
	publishSummary(t, *req, s, m.PlanSHA256)
	write(t, req.ManifestPath, *m)
}

func readJSON(t *testing.T, path string, out any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, out); err != nil {
		t.Fatal(err)
	}
}
func publishSummary(t *testing.T, req Request, s campaign.Summary, planSHA string) {
	t.Helper()
	hash := write(t, filepath.Join(req.CampaignDir, "summary.json"), s)
	var record campaign.Record
	readJSON(t, filepath.Join(req.CampaignDir, "campaign.json"), &record)
	record.PlanSHA256 = planSHA
	record.Summary.SHA256 = hash
	write(t, filepath.Join(req.CampaignDir, "campaign.json"), record)
}

func TestRoundMetrics(t *testing.T) {
	for _, mode := range []string{"complete", "pending", "missing-reference", "migration-fail", "delivery-fail", "both-fail", "no-release", "all-skipped"} {
		t.Run(mode, func(t *testing.T) {
			req, m := fakeRound(t)
			if mode != "missing-reference" && mode != "all-skipped" {
				status := evaluation.Pass
				if mode == "migration-fail" || mode == "both-fail" {
					status = evaluation.Fail
				}
				attachFakeReference(t, &req, &m, "one", status, mode != "pending")
				attachFakeReference(t, &req, &m, "two", evaluation.Pass, true)
			}
			if mode == "delivery-fail" || mode == "both-fail" {
				ref := &req.References[0]
				var review evaluation.Review
				readJSON(t, ref.Review.Path, &review)
				item := review.Items["delivery_evidence"]
				item.Status = evaluation.Fail
				review.Items["delivery_evidence"] = item
				ref.Review.SHA256 = write(t, ref.Review.Path, review)
			}
			if mode == "no-release" {
				var s campaign.Summary
				readJSON(t, filepath.Join(req.CampaignDir, "summary.json"), &s)
				a := s.Targets[0].Attempt
				a.Verdict = "Rejected"
				s.Counts.Verdicts["Rejected"] = 1
				delete(s.Counts.Verdicts, "ReadyForReview")
				dir := filepath.Join(req.CampaignDir, filepath.FromSlash(a.Directory))
				var r runner.Record
				readJSON(t, filepath.Join(dir, "run.json"), &r)
				r.Verdict = "Rejected"
				write(t, filepath.Join(dir, "run.json"), r)
				var p runner.Report
				path := filepath.Join(req.CampaignDir, filepath.FromSlash(a.Report.Path))
				readJSON(t, path, &p)
				p.Validation.Verdict = "Rejected"
				a.Report.SHA256 = write(t, path, p)
				write(t, filepath.Join(req.CampaignDir, "targets/one/target.json"), s.Targets[0])
				publishSummary(t, req, s, m.PlanSHA256)
			}
			if mode == "all-skipped" {
				var s campaign.Summary
				readJSON(t, filepath.Join(req.CampaignDir, "summary.json"), &s)
				s.Counts = campaign.Counts{Skipped: 3, Verdicts: map[string]int{}}
				for i := range s.Targets {
					s.Targets[i] = campaign.TargetRecord{ID: s.Targets[i].ID, State: campaign.Skipped, Reason: "campaign_cancelled"}
					write(t, filepath.Join(req.CampaignDir, "targets", s.Targets[i].ID, "target.json"), s.Targets[i])
				}
				publishSummary(t, req, s, m.PlanSHA256)
			}
			report, err := Summarize(context.Background(), req)
			if err != nil || !report.Valid || !report.InputsUnchanged || report.Counts.Targets != 3 {
				t.Fatalf("%+v err=%v", report, err)
			}
			if report.Counts.Runs != 3 && mode != "all-skipped" {
				t.Fatal("failed target disappeared from runs")
			}
			if mode == "complete" && (report.Counts.Success != 1 || report.Counts.NeedsAttention != 1 || report.Metrics.Success.Upper != 1 || report.Metrics.Success.Denominator != 3) {
				t.Fatalf("%+v", report.Counts)
			}
			if (mode == "pending" || mode == "missing-reference") && (report.Counts.UnknownReleases != 1 || report.Counts.Success != 0 || report.Metrics.WrongRelease.Upper != 1) {
				t.Fatal("unknown release became pass or fail")
			}
			if (mode == "migration-fail" || mode == "delivery-fail" || mode == "both-fail") && report.Counts.WrongReleases != 1 {
				t.Fatal("wrong release count missing or duplicated")
			}
			if mode == "both-fail" && len(report.Counts.WrongReleaseCategories) != 2 {
				t.Fatal("both failure categories not retained")
			}
			if mode == "no-release" && report.Metrics.WrongRelease != nil {
				t.Fatal("zero release denominator fabricated")
			}
			if mode == "all-skipped" && (report.Counts.Runs != 0 || report.Metrics.AttentionCandidates != nil || report.ExecutionComplete) {
				t.Fatal("skipped tasks became attempts")
			}
		})
	}
}

func TestRoundMissingAndConflictingRecords(t *testing.T) {
	for _, mode := range []string{"missing-run-report", "missing-run", "missing-reference", "missing-review", "missing-log", "missing-budget", "wrong-run", "wrong-candidate", "wrong-spec", "duplicate-reference", "duplicate-run", "wrong-report-hash", "cross-target-path", "missing-target-in-plan", "missing-common-pin", "calibration-review-in-real-round", "bad-cost-total", "reused-output"} {
		t.Run(mode, func(t *testing.T) {
			req, m := fakeRound(t)
			attachFakeReference(t, &req, &m, "one", evaluation.Pass, true)
			path := filepath.Join(req.CampaignDir, "targets/one/runs/run-one")
			var s campaign.Summary
			readJSON(t, filepath.Join(req.CampaignDir, "summary.json"), &s)
			conflict := true
			switch mode {
			case "missing-run-report":
				if err := os.Remove(filepath.Join(path, "artifacts/report.json")); err != nil {
					t.Fatal(err)
				}
				conflict = false
			case "missing-run":
				if err := os.Remove(filepath.Join(path, "run.json")); err != nil {
					t.Fatal(err)
				}
				conflict = false
			case "missing-reference":
				if err := os.Remove(req.References[0].Report.Path); err != nil {
					t.Fatal(err)
				}
				conflict = false
			case "missing-review":
				if err := os.Remove(req.References[0].Review.Path); err != nil {
					t.Fatal(err)
				}
				conflict = false
			case "missing-log":
				if err := os.Remove(filepath.Join(filepath.Dir(req.References[0].Report.Path), "logs/out")); err != nil {
					t.Fatal(err)
				}
				conflict = false
			case "missing-budget":
				if err := os.Remove(filepath.Join(req.CampaignDir, "budget/budget.json")); err != nil {
					t.Fatal(err)
				}
				conflict = false
			case "wrong-run", "wrong-candidate", "wrong-spec":
				var ref evaluation.Report
				readJSON(t, req.References[0].Report.Path, &ref)
				if mode == "wrong-run" {
					ref.RunID = "another-run"
				}
				if mode == "wrong-candidate" {
					ref.CandidateSHA256 = strings.Repeat("b", 64)
				}
				if mode == "wrong-spec" {
					ref.SpecSHA256 = strings.Repeat("b", 64)
				}
				req.References[0].Report.SHA256 = write(t, req.References[0].Report.Path, ref)
			case "duplicate-reference":
				req.References = append(req.References, req.References[0])
			case "duplicate-run":
				s.Targets[1].Attempt.RunID = s.Targets[0].Attempt.RunID
				write(t, filepath.Join(req.CampaignDir, "targets/two/target.json"), s.Targets[1])
				publishSummary(t, req, s, m.PlanSHA256)
			case "wrong-report-hash":
				s.Targets[0].Attempt.Report.SHA256 = strings.Repeat("b", 64)
				write(t, filepath.Join(req.CampaignDir, "targets/one/target.json"), s.Targets[0])
				publishSummary(t, req, s, m.PlanSHA256)
			case "cross-target-path":
				s.Targets[0].Attempt.Report.Path = s.Targets[1].Attempt.Report.Path
				write(t, filepath.Join(req.CampaignDir, "targets/one/target.json"), s.Targets[0])
				publishSummary(t, req, s, m.PlanSHA256)
			case "missing-target-in-plan":
				m.Targets = m.Targets[:2]
				write(t, req.ManifestPath, m)
			case "missing-common-pin":
				m.Targets[0].ToolSHA256 = ""
				write(t, req.ManifestPath, m)
			case "calibration-review-in-real-round":
				// 避开 controlled kind 检查，单独检验 Review 的类别边界。
				var plan campaign.Plan
				readJSON(t, filepath.Join(req.CampaignDir, "plan.json"), &plan)
				plan.Profile.Provider = "synthetic-provider-for-mechanism-only"
				s.Budget.Profile = plan.Profile
				m.PlanSHA256 = write(t, filepath.Join(req.CampaignDir, "plan.json"), plan)
				s.PlanSHA256 = m.PlanSHA256
				write(t, filepath.Join(req.CampaignDir, "budget/budget.json"), s.Budget)
				publishSummary(t, req, s, m.PlanSHA256)
				m.Kind = Development
				write(t, req.ManifestPath, m)
			case "bad-cost-total":
				s.Budget.Totals.KnownTokens = 100
				write(t, filepath.Join(req.CampaignDir, "budget/budget.json"), s.Budget)
				publishSummary(t, req, s, m.PlanSHA256)
			case "reused-output":
				if err := os.Mkdir(req.EvidenceDir, 0755); err != nil {
					t.Fatal(err)
				}
			}
			report, err := Summarize(context.Background(), req)
			if conflict {
				if err == nil || report.Metrics != nil {
					t.Fatalf("conflicting record accepted: valid=%t metrics=%+v err=%v", report.Valid, report.Metrics, err)
				}
			} else {
				if err != nil || !report.Valid || report.Counts.Targets != 3 || report.Counts.Released != 1 {
					t.Fatalf("missing records changed membership: counts=%+v err=%v", report.Counts, err)
				}
				if mode == "missing-budget" {
					if report.Cost != nil {
						t.Fatal("missing cost treated as zero")
					}
				} else if report.Counts.UnknownReleases != 1 {
					t.Fatal("missing reference/delivery evidence became conclusive")
				}
				if mode == "missing-run-report" && report.Counts.UnknownSuccess != 0 {
					t.Fatal("incomplete delivery counted as potential success")
				}
			}
		})
	}
}

func TestRoundInputIntegrity(t *testing.T) {
	req, _ := fakeRound(t)
	root := filepath.Dir(req.ManifestPath)
	r := &reader{ctx: context.Background(), output: root, sources: map[string]Source{}, trees: map[string]string{}}
	path := filepath.Join(root, "file")
	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.read(path, ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := r.unchanged(); err == nil {
		t.Fatal("changed source accepted")
	}
	if _, err := child(root, "../outside"); err == nil {
		t.Fatal("path traversal accepted")
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("other"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := child(root, "escape"); err == nil {
		t.Fatal("symlink escape accepted")
	}
}

func TestRoundBudgetUnknownAndOnceOnly(t *testing.T) {
	req, m := fakeRound(t)
	var s campaign.Summary
	readJSON(t, filepath.Join(req.CampaignDir, "summary.json"), &s)
	now := time.Now().UTC()
	for i, id := range []string{"one", "two"} {
		call := budget.Call{ID: "call-" + id, TargetID: id, RunID: "run-" + id, ReservedTokens: 100, State: budget.StateUnknown, CreatedAt: now, UpdatedAt: now}
		totals := budget.Totals{Calls: 1, UnknownTokens: 100}
		if i == 0 {
			call.State = budget.StateSettled
			call.Usage = &agent.Usage{InputTokens: 11, OutputTokens: 9, TotalTokens: 20}
			totals = budget.Totals{Calls: 1, KnownTokens: 20}
		}
		a := s.Budget.Targets[id]
		a.RunID = call.RunID
		a.Totals = totals
		s.Budget.Targets[id] = a
		s.Budget.Calls = append(s.Budget.Calls, call)
	}
	s.Budget.Totals = budget.Totals{Calls: 2, KnownTokens: 20, UnknownTokens: 100}
	s.Budget.Rejections = []budget.Rejection{{TargetID: "one", RunID: "run-one", Scope: "campaign", Resource: "tokens", At: now}}
	write(t, filepath.Join(req.CampaignDir, "budget/budget.json"), s.Budget)
	publishSummary(t, req, s, m.PlanSHA256)
	report, err := Summarize(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if report.Cost.Totals.KnownTokens != 20 || report.Cost.Totals.UnknownTokens != 100 || report.Cost.KnownInputTokens != 11 || report.Cost.Rejections != 1 || *report.Targets[0].Rejections != 1 {
		t.Fatalf("cost duplicated or unknown lost: %+v", report.Cost)
	}
	if report.Targets[0].Cost.KnownTokens != 20 || report.Targets[1].Cost.UnknownTokens != 100 {
		t.Fatal("target costs lost")
	}
}

func TestArchiveAndRepeatedReadIntegrity(t *testing.T) {
	for _, mode := range []string{"copy-changed", "second-read-changed", "missing-appeared"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			r := &reader{ctx: context.Background(), output: root, sources: map[string]Source{}, trees: map[string]string{}}
			path := filepath.Join(root, "source")
			if mode != "missing-appeared" {
				if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := r.read(path, ""); err != nil {
				t.Fatal(err)
			}
			if mode == "copy-changed" {
				canonical, err := gate.Canonical(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, r.sources[canonical].Copy), []byte("modified"), 0644); err != nil {
					t.Fatal(err)
				}
				if err := r.unchanged(); err == nil {
					t.Fatal("archived copy drift accepted")
				}
			} else {
				if err := os.WriteFile(path, []byte("modified"), 0644); err != nil {
					t.Fatal(err)
				}
				if _, _, err := r.read(path, ""); err == nil {
					t.Fatal("source change hidden by another read")
				}
			}
		})
	}
}
