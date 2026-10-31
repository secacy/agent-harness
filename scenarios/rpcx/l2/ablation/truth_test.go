package ablation

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/task"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/baseline"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
)

func truthDocuments(t *testing.T, change func(*evaluation.Report, *evaluation.Review)) ([]byte, []byte) {
	t.Helper()
	report := evaluation.Report{SchemaVersion: 1, Version: evaluation.Version, TargetID: "goddd-A-01", RunID: "run-1", CandidateSHA256: strings.Repeat("a", 64), InputsUnchanged: true, Automatic: evaluation.Pass, Checks: map[string]evaluation.Observation{}}
	for _, name := range []string{"dependencies", "protected_paths", "build", "candidate_tests", "behavior", "expiry"} {
		report.Checks[name] = evaluation.Observation{Status: evaluation.Pass}
	}
	review := evaluation.Review{SchemaVersion: 1, Kind: "candidate_review", TargetID: report.TargetID, RunID: report.RunID, CandidateSHA256: report.CandidateSHA256, Reviewer: "actual-reviewer", RecordedAt: "2026-10-31T20:00:00+08:00", Items: map[string]evaluation.ReviewItem{}}
	for _, name := range []string{"test_preservation", "change_coverage", "delivery_evidence"} {
		review.Items[name] = evaluation.ReviewItem{Status: evaluation.Pass, Reason: "bounded fixture evidence", Files: []string{"production.go:1"}, Evidence: []string{"reference.json/checks"}}
	}
	if change != nil {
		change(&report, &review)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	review.ReportSHA256 = gate.Digest(raw)
	reviewRaw, err := json.Marshal(review)
	if err != nil {
		t.Fatal(err)
	}
	return raw, reviewRaw
}

func TestAssessTruthSeparatesCandidateAndDelivery(t *testing.T) {
	cases := []struct {
		name                string
		modify              func(*evaluation.Report, *evaluation.Review)
		candidate, delivery evaluation.Status
	}{
		{"pass", nil, evaluation.Pass, evaluation.Pass},
		{"delivery-fail", func(_ *evaluation.Report, r *evaluation.Review) {
			item := r.Items["delivery_evidence"]
			item.Status = evaluation.Fail
			r.Items["delivery_evidence"] = item
		}, evaluation.Pass, evaluation.Fail},
		{"delivery-unknown", func(_ *evaluation.Report, r *evaluation.Review) {
			item := r.Items["delivery_evidence"]
			item.Status = evaluation.Inconclusive
			r.Items["delivery_evidence"] = item
		}, evaluation.Pass, evaluation.Inconclusive},
		{"test-fail", func(_ *evaluation.Report, r *evaluation.Review) {
			item := r.Items["test_preservation"]
			item.Status = evaluation.Fail
			r.Items["test_preservation"] = item
		}, evaluation.Fail, evaluation.Pass},
		{"coverage-unknown", func(_ *evaluation.Report, r *evaluation.Review) {
			item := r.Items["change_coverage"]
			item.Status = evaluation.Inconclusive
			r.Items["change_coverage"] = item
		}, evaluation.Inconclusive, evaluation.Pass},
		{"automatic-fail", func(p *evaluation.Report, _ *evaluation.Review) {
			p.Checks["behavior"] = evaluation.Observation{Status: evaluation.Fail}
			p.Automatic = evaluation.Fail
		}, evaluation.Fail, evaluation.Pass},
		{"automatic-unknown", func(p *evaluation.Report, _ *evaluation.Review) {
			p.Checks["expiry"] = evaluation.Observation{Status: evaluation.Inconclusive}
			p.Automatic = evaluation.Inconclusive
		}, evaluation.Inconclusive, evaluation.Pass},
		{"drift-overrides-fail", func(p *evaluation.Report, r *evaluation.Review) {
			p.InputsUnchanged = false
			p.Checks["behavior"] = evaluation.Observation{Status: evaluation.Fail}
			p.Automatic = evaluation.Fail
			item := r.Items["test_preservation"]
			item.Status = evaluation.Fail
			r.Items["test_preservation"] = item
		}, evaluation.Inconclusive, evaluation.Inconclusive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, review := truthDocuments(t, tc.modify)
			beforeRaw, beforeReview := string(raw), string(review)
			got, err := AssessTruth(raw, review, true)
			if err != nil || got.Candidate != tc.candidate || got.Delivery != tc.delivery {
				t.Fatalf("got %+v: %v", got, err)
			}
			if got.ReferenceSHA256 != gate.Digest(raw) || got.ReviewSHA256 != gate.Digest(review) || got.TargetID != "goddd-A-01" || got.RunID != "run-1" || got.CandidateSHA256 != strings.Repeat("a", 64) {
				t.Fatalf("binding lost: %+v", got)
			}
			if string(raw) != beforeRaw || string(review) != beforeReview {
				t.Fatal("original documents modified")
			}
		})
	}
}

func TestAssessTruthRetainsStrictReferenceValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(*evaluation.Report, *evaluation.Review)
	}{
		{"run-mismatch", func(_ *evaluation.Report, r *evaluation.Review) { r.RunID = "different" }},
		{"missing-review", func(_ *evaluation.Report, r *evaluation.Review) { delete(r.Items, "change_coverage") }},
		{"missing-check", func(p *evaluation.Report, _ *evaluation.Review) { delete(p.Checks, "behavior") }},
		{"unsupported-status", func(_ *evaluation.Report, r *evaluation.Review) {
			item := r.Items["test_preservation"]
			item.Status = "approved"
			r.Items["test_preservation"] = item
		}},
		{"inconsistent-automatic", func(p *evaluation.Report, _ *evaluation.Review) { p.Automatic = evaluation.Fail }},
		{"missing-translation-configurations", func(p *evaluation.Report, _ *evaluation.Review) { p.Version = evaluation.TranslationVersion }},
		{"missing-human-evidence", func(_ *evaluation.Report, r *evaluation.Review) { r.Reviewer = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, review := truthDocuments(t, tc.modify)
			if _, err := AssessTruth(raw, review, true); err == nil {
				t.Fatal("invalid original document accepted")
			}
		})
	}
	raw, review := truthDocuments(t, func(_ *evaluation.Report, r *evaluation.Review) { r.Kind = "development_calibration" })
	if _, err := AssessTruth(raw, review, true); err == nil {
		t.Fatal("calibration admitted as formal")
	}
	if _, err := AssessTruth(raw, review, false); err != nil {
		t.Fatal(err)
	}
	var r evaluation.Review
	if err := json.Unmarshal(review, &r); err != nil {
		t.Fatal(err)
	}
	r.ReportSHA256 = strings.Repeat("f", 64)
	tampered, _ := json.Marshal(r)
	if _, err := AssessTruth(raw, tampered, false); err == nil {
		t.Fatal("mismatched reference bytes accepted")
	}
}

// This fixture invokes the real published reference preparation, input checks,
// material archiving and stop policy. A controlled backend returns an invalid
// module declaration, so no build, candidate command or provider is executed.
type truthFixtureBackend struct{}

func (truthFixtureBackend) Info() execution.Info { return execution.Local{}.Info() }
func (truthFixtureBackend) Run(_ context.Context, _ execution.Request) (execution.Result, error) {
	return execution.Result{ExitCode: 0, Stdout: []byte("{}\n")}, nil
}

func preparedTruthFixture(t *testing.T) (string, Task, evaluation.Report) {
	t.Helper()
	fixtures, err := filepath.Abs("../data/rpcx-l2-goddd-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	files, err := gate.ReadFiles(filepath.Join(fixtures, "goddd"))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	candidate := filepath.Join(directory, "candidate")
	if err = gate.CopyFiles(candidate, files); err != nil {
		t.Fatal(err)
	}
	spec, err := filepath.Abs("../../goddd/specs/v0.3.0.md")
	if err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(directory, "evidence")
	report, err := evaluation.Evaluate(context.Background(), evaluation.Request{TargetID: "goddd-A-01", RunID: "run-1", FixturesDir: fixtures, CandidateDir: candidate, CandidateSHA256: gate.TreeHash(files), SpecPath: spec, EvidenceDir: evidence, Backend: truthFixtureBackend{}, TrustedLocal: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Automatic != evaluation.Inconclusive || !report.InputsUnchanged {
		t.Fatalf("expected complete dependency uncertainty: %+v", report)
	}
	identity, err := evaluation.PublishedIdentity("rpcx-l2-reference-goddd-data-v2")
	if err != nil {
		t.Fatal(err)
	}
	expected := Task{ID: "goddd", ReferenceVersion: identity.Version, ReferenceSHA256: identity.ReferenceSHA256, ReferenceToolSHA256: identity.ToolSHA256, Input: task.Target{ID: "goddd", ProfileID: "rpcx-goddd-t3-v1", Repository: task.Snapshot{SHA256: gate.TreeHash(files)}, Specification: task.Specification{SHA256: identity.SpecSHA256}, Dependencies: map[string]task.Snapshot{}}}
	for _, name := range []string{"rpcx-v1", "rpcx-v2"} {
		f, err := gate.ReadFiles(filepath.Join(fixtures, name))
		if err != nil {
			t.Fatal(err)
		}
		expected.Input.Dependencies[name] = task.Snapshot{SHA256: gate.TreeHash(f)}
	}
	return evidence, expected, report
}

func TestAuthenticateReferenceBindsActualEvidence(t *testing.T) {
	root, expected, report := preparedTruthFixture(t)
	got, raw, err := AuthenticateReference(root, expected, report.CandidateSHA256, report.TargetID, report.RunID)
	if err != nil || got.CandidateSHA256 != report.CandidateSHA256 || len(raw) == 0 {
		t.Fatalf("authentic fixture rejected: %v", err)
	}
	mutate := func(name string, apply func() func()) {
		t.Run(name, func(t *testing.T) {
			restore := apply()
			defer restore()
			if _, _, err := AuthenticateReference(root, expected, report.CandidateSHA256, report.TargetID, report.RunID); err == nil {
				t.Fatal("altered evidence accepted")
			}
		})
	}
	changeFile := func(name string, content []byte) func() {
		path := filepath.Join(root, name)
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
		return func() {
			if err := os.WriteFile(path, before, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	mutate("changed-log", func() func() { return changeFile(report.Commands[0].Stdout, []byte("changed")) })
	mutate("reference-member", func() func() { return changeFile("materials/reference/routes_test.go.txt", []byte("changed")) })
	mutate("candidate-bytes", func() func() { return changeFile("candidate/goddd/go.mod", []byte("module wrong")) })
	mutate("probe-copy", func() func() {
		return changeFile("production/goddd/server/l2_reference_test.go", []byte("package server"))
	})
	mutate("deleted-log-index", func() func() {
		copy := report
		copy.Artifacts = map[string]string{}
		for k, v := range report.Artifacts {
			copy.Artifacts[k] = v
		}
		delete(copy.Artifacts, copy.Commands[0].Stdout)
		b, _ := json.Marshal(copy)
		return changeFile("reference.json", b)
	})
	mutate("deleted-command-and-index", func() func() {
		copy := report
		copy.Artifacts = map[string]string{}
		for k, v := range report.Artifacts {
			copy.Artifacts[k] = v
		}
		command := copy.Commands[0]
		copy.Commands = copy.Commands[1:]
		delete(copy.Artifacts, command.Stdout)
		delete(copy.Artifacts, command.Stderr)
		b, _ := json.Marshal(copy)
		return changeFile("reference.json", b)
	})
	mutate("symlink-log", func() func() {
		path := filepath.Join(root, report.Commands[0].Stdout)
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(t.TempDir(), "log")
		if err = os.WriteFile(target, b, 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err = os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		return func() {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, b, 0600); err != nil {
				t.Fatal(err)
			}
		}
	})
	for _, name := range []string{"../outside", "/absolute", "logs/../reference.json", "logs//out"} {
		if _, err := referenceFile(root, name); err == nil {
			t.Fatalf("non-local/canonical path accepted: %s", name)
		}
	}
	for _, change := range []func(*Task){func(t *Task) { t.ReferenceVersion = "old" }, func(t *Task) { t.ReferenceSHA256 = strings.Repeat("a", 64) }, func(t *Task) { t.ReferenceToolSHA256 = strings.Repeat("a", 64) }, func(t *Task) { t.Input.Specification.SHA256 = strings.Repeat("a", 64) }, func(t *Task) { t.Input.Repository.SHA256 = strings.Repeat("a", 64) }} {
		wrong := expected
		change(&wrong)
		if _, _, err := AuthenticateReference(root, wrong, report.CandidateSHA256, report.TargetID, report.RunID); err == nil {
			t.Fatal("frozen identity mismatch accepted")
		}
	}
}

func TestReferenceCommandCoverageCannotLosePassingChecks(t *testing.T) {
	cases := []struct {
		task   string
		report evaluation.Report
		seen   map[string]bool
	}{
		{"goddd", evaluation.Report{Checks: map[string]evaluation.Observation{"build": {Status: evaluation.Pass}}}, map[string]bool{"go-version": true, "baseline-mod": true, "candidate-mod": true}},
		{"translation", evaluation.Report{Configurations: map[string]evaluation.ConfigurationReport{"rpcx_alternate": {Checks: map[string]evaluation.Observation{"selection": {Status: evaluation.Pass}}}}}, map[string]bool{"go-version": true, "baseline-mod": true, "candidate-mod": true}},
	}
	for _, tc := range cases {
		if err := authenticateCommandCoverage(tc.task, tc.report, tc.seen); err == nil {
			t.Fatal("passing check without command admitted")
		}
	}
}

func TestReferenceHostRequiresEveryPublishedDependencyArtifact(t *testing.T) {
	host, err := l2.GHCLIMaterials()
	if err != nil {
		t.Fatal(err)
	}
	hash, err := l2.HostMaterialsSHA256Version(host, baseline.GHCLIVersion)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	report := evaluation.Report{HostMaterialsSHA256: hash, Artifacts: map[string]string{}}
	for name, data := range host {
		leaf, ok := strings.CutPrefix(name, "dependencies/")
		if !ok {
			continue
		}
		artifact := "modules.bundle/" + leaf
		if leaf == "modules.json" {
			artifact = "modules.json"
		}
		if err := gate.Write(filepath.Join(root, artifact), data); err != nil {
			t.Fatal(err)
		}
		report.Artifacts[artifact] = gate.Digest(data)
	}
	if err = authenticateReferenceHost(root, report); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"modules.json", "modules.bundle/bundle.json", "modules.bundle/modules-000.tar.gz.part"} {
		t.Run(name, func(t *testing.T) {
			hash, ok := report.Artifacts[name]
			if !ok {
				t.Fatal("fixture missing required published artifact")
			}
			delete(report.Artifacts, name)
			defer func() { report.Artifacts[name] = hash }()
			if err := authenticateReferenceHost(root, report); err == nil {
				t.Fatal("deleting index reference hid missing evidence")
			}
		})
	}
	report.Artifacts["modules.bundle/unpublished.part"] = strings.Repeat("a", 64)
	if err := authenticateReferenceHost(root, report); err == nil {
		t.Fatal("unpublished host artifact accepted")
	}
}
