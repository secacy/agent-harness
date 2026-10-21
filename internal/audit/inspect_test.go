package audit

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/task"
)

type fixture struct {
	root   string
	plan   campaign.Plan
	record campaign.Record
	target campaign.TargetRecord
	run    RunRecord
	ledger budget.Snapshot
}

func put(t *testing.T, root, p string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	putBytes(t, root, p, append(data, '\n'))
}
func putBytes(t *testing.T, root, p string, data []byte) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(p))
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0600); err != nil {
		t.Fatal(err)
	}
}
func get[T any](t *testing.T, root, p string) T {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
	if err != nil {
		t.Fatal(err)
	}
	var v T
	if err = json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	return v
}
func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "campaign")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 20, 1, 2, 3, 0, time.UTC)
	hash := strings.Repeat("a", 64)
	profile := budget.Profile{Provider: "controlled", Endpoint: "in-process", Model: "fixed", ContextTokens: 90, MaxOutputTokens: 10, SourceSHA256: hash, VerifiedAt: now}
	limits := budget.Limits{MaxCalls: 5, MaxTokens: 500}
	input := task.Target{ID: "orders", ProfileID: "rpcx-demo-orders-t3-v1", BuildConfig: "default", Repository: task.Snapshot{Directory: "/not-read/repo", SHA256: hash}, Specification: task.Specification{Path: "/not-read/spec.md", SHA256: hash}}
	f := &fixture{root: root, plan: campaign.Plan{ID: "audit-test", Concurrency: 1, ExecutionConcurrency: 1, Budget: limits, Profile: profile, Targets: []campaign.Target{{Input: input, Budget: limits, Limits: agent.Limits{MaxModelCalls: 5, MaxToolCalls: 10}, Timeout: time.Minute}}}}
	f.record = campaign.Record{SchemaVersion: 1, ID: f.plan.ID, Directory: "/original-machine/campaign", Status: campaign.Completed, StartedAt: now, FinishedAt: &now}
	f.run = RunRecord{SchemaVersion: 2, ID: "run-1", TargetID: "orders", Directory: "/original-machine/campaign/targets/orders/runs/run-1", Status: "Completed", Phase: "Publishing", StartedAt: now, UpdatedAt: now, FinishedAt: &now, CandidateSHA256: hash, PatchSHA256: hash, Verdict: "NeedsAttention"}
	report := []byte("{\"opaque_for_core\":true}\n")
	putBytes(t, root, "targets/orders/runs/run-1/artifacts/report.json", report)
	f.target = campaign.TargetRecord{ID: "orders", State: campaign.Finished, Attempt: &campaign.Attempt{RunID: "run-1", Directory: runPath("orders", "run-1"), Status: campaign.Completed, Verdict: "NeedsAttention", Report: campaign.Artifact{Path: runPath("orders", "run-1") + "/artifacts/report.json", SHA256: checksum(report)}}}
	totals := budget.Totals{Calls: 1, KnownTokens: 20}
	f.ledger = budget.Snapshot{SchemaVersion: 1, CampaignID: f.plan.ID, Revision: 3, Profile: profile, Limits: limits, Totals: totals, Targets: map[string]budget.Account{"orders": {Limits: limits, RunID: "run-1", Totals: totals}}, Calls: []budget.Call{{ID: "call-1", TargetID: "orders", RunID: "run-1", InputSHA256: hash, ReservedTokens: 100, State: budget.StateSettled, Usage: &agent.Usage{InputTokens: 15, OutputTokens: 5, TotalTokens: 20}, Outcome: "reply", CreatedAt: now, UpdatedAt: now}}, Rejections: []budget.Rejection{}}
	f.save(t)
	return f
}
func (f *fixture) save(t *testing.T) {
	t.Helper()
	put(t, f.root, "plan.json", f.plan)
	data, err := os.ReadFile(filepath.Join(f.root, "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	f.record.PlanSHA256 = checksum(data)
	put(t, f.root, targetPath("orders"), f.target)
	put(t, f.root, runPath("orders", "run-1")+"/run.json", f.run)
	put(t, f.root, ledgerPath, f.ledger)
	f.summary(t)
	put(t, f.root, "campaign.json", f.record)
}
func (f *fixture) summary(t *testing.T) {
	t.Helper()
	counts := campaign.Counts{Verdicts: map[string]int{}}
	if f.target.State == campaign.Skipped {
		counts.Skipped++
	} else if f.target.Attempt != nil {
		switch f.target.Attempt.Status {
		case campaign.Completed:
			counts.Completed++
			counts.Verdicts[f.target.Attempt.Verdict]++
		case campaign.Failed:
			counts.Failed++
		case campaign.Cancelled:
			counts.Cancelled++
		}
	}
	s := campaign.Summary{CampaignID: f.plan.ID, PlanSHA256: f.record.PlanSHA256, Targets: []campaign.TargetRecord{f.target}, Counts: counts, Budget: f.ledger}
	put(t, f.root, "summary.json", s)
	putBytes(t, f.root, "summary.md", []byte("summary\n"))
	data, _ := os.ReadFile(filepath.Join(f.root, "summary.json"))
	f.record.Summary = campaign.Artifact{Path: "summary.json", SHA256: checksum(data)}
}
func auditFixture(t *testing.T, f *fixture, inspector RunInspector) Report {
	t.Helper()
	output := filepath.Join(t.TempDir(), "audit")
	r, err := Inspect(context.Background(), Request{f.root, output}, inspector)
	if err != nil {
		t.Fatal(err)
	}
	marker := get[struct {
		Files map[string]string `json:"files"`
	}](t, output, "published.json")
	if len(marker.Files) != 3 {
		t.Fatal(marker)
	}
	for p, sha := range marker.Files {
		data, err := os.ReadFile(filepath.Join(output, p))
		if err != nil || checksum(data) != sha {
			t.Fatalf("unbound output %s: %v", p, err)
		}
	}
	return r
}
func has(findings []Finding, code, p string) bool {
	for _, f := range findings {
		if f.Code == code && (p == "" || f.Path == p) {
			return true
		}
	}
	return false
}
func assertViolation(t *testing.T, r Report, code, p string) {
	t.Helper()
	if r.Consistency != "violated" || !has(r.Violations, code, p) {
		t.Fatalf("expected %s at %s: %+v", code, p, r)
	}
}

func TestCompletedCoreAndCoverage(t *testing.T) {
	f := newFixture(t)
	before := sourceTree(t, f.root)
	r := auditFixture(t, f, nil)
	if r.Consistency != "inconclusive" || r.ScanComplete || len(r.Violations) != 0 || !has(r.Gaps, "run_inspector_missing", "") || r.ExecutionState != "completed" || r.InputsUnchanged == nil || !*r.InputsUnchanged {
		t.Fatalf("%+v", r)
	}
	second := auditFixture(t, f, nil)
	if !reflect.DeepEqual(r, second) {
		t.Fatal("audit results changed without source changes")
	}
	if !reflect.DeepEqual(before, sourceTree(t, f.root)) {
		t.Fatal("audit mutated source")
	}
}
func sourceTree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if e.IsDir() {
			result[rel] = "directory"
			return nil
		}
		data, err := os.ReadFile(p)
		if err == nil {
			result[rel] = checksum(data)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func TestCoreWithoutRunsCanPass(t *testing.T) {
	f := newFixture(t)
	f.record.Status = campaign.Running
	f.record.FinishedAt = nil
	f.record.Summary = campaign.Artifact{}
	f.target = campaign.TargetRecord{ID: "orders", State: campaign.Pending}
	f.ledger.Calls = []budget.Call{}
	f.ledger.Totals = budget.Totals{}
	f.ledger.Targets["orders"] = budget.Account{Limits: f.plan.Budget}
	f.save(t)
	for _, p := range []string{"summary.json", "summary.md", "targets/orders/runs/run-1"} {
		if err := os.RemoveAll(filepath.Join(f.root, p)); err != nil {
			t.Fatal(err)
		}
	}
	f.record.Summary = campaign.Artifact{}
	put(t, f.root, "campaign.json", f.record)
	r := auditFixture(t, f, nil)
	if r.Consistency != "pass" || !r.ScanComplete || r.ExecutionState != "incomplete" {
		t.Fatalf("%+v", r)
	}
}

func TestOwnershipAndPublishedClaims(t *testing.T) {
	cases := []struct {
		name, code, p string
		change        func(*testing.T, *fixture)
	}{
		{"plan-hash", "plan_digest_mismatch", "plan.json", func(t *testing.T, f *fixture) {
			f.record.PlanSHA256 = strings.Repeat("0", 64)
			put(t, f.root, "campaign.json", f.record)
		}},
		{"run-owner", "run_identity_mismatch", runPath("orders", "run-1") + "/run.json", func(t *testing.T, f *fixture) {
			f.run.TargetID = "other"
			put(t, f.root, runPath("orders", "run-1")+"/run.json", f.run)
		}},
		{"failed-verdict", "failed_run_has_verdict", runPath("orders", "run-1") + "/run.json", func(t *testing.T, f *fixture) {
			f.run.Status = "Failed"
			f.run.Error = "failure"
			put(t, f.root, runPath("orders", "run-1")+"/run.json", f.run)
		}},
		{"report-hash", "artifact_digest_mismatch", runPath("orders", "run-1") + "/artifacts/report.json", func(t *testing.T, f *fixture) {
			putBytes(t, f.root, runPath("orders", "run-1")+"/artifacts/report.json", []byte("changed"))
		}},
		{"cross-target", "target_attempt_mismatch", targetPath("orders"), func(t *testing.T, f *fixture) { f.target.Attempt.Directory = runPath("other", "run-1"); f.save(t) }},
		{"absolute-artifact", "unsafe_artifact", targetPath("orders"), func(t *testing.T, f *fixture) { f.target.Attempt.Report.Path = "/not-read/external"; f.save(t) }},
		{"escape-artifact", "unsafe_artifact", targetPath("orders"), func(t *testing.T, f *fixture) {
			f.target.Attempt.Report.Path = "targets/orders/runs/run-1/../../../../outside"
			f.save(t)
		}},
		{"summary-count", "summary_mismatch", "summary.json", func(t *testing.T, f *fixture) {
			s := get[campaign.Summary](t, f.root, "summary.json")
			s.Counts.Completed = 7
			put(t, f.root, "summary.json", s)
		}},
		{"missing-summary", "campaign_completion_incomplete", "summary.json", func(t *testing.T, f *fixture) { os.Remove(filepath.Join(f.root, "summary.json")) }},
		{"missing-ledger", "prerequisite_missing", ledgerPath, func(t *testing.T, f *fixture) { os.Remove(filepath.Join(f.root, ledgerPath)) }},
		{"pending-call", "state_prerequisite_mismatch", ledgerPath, func(t *testing.T, f *fixture) {
			f.target = campaign.TargetRecord{ID: "orders", State: campaign.Pending}
			f.save(t)
		}},
		{"skipped-call", "state_prerequisite_mismatch", ledgerPath, func(t *testing.T, f *fixture) {
			f.target = campaign.TargetRecord{ID: "orders", State: campaign.Skipped, Reason: "campaign_cancelled"}
			f.save(t)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.change(t, f)
			r := auditFixture(t, f, nil)
			assertViolation(t, r, tc.code, tc.p)
		})
	}
}

func TestBudgetRecomputation(t *testing.T) {
	cases := []struct {
		name, code string
		change     func(*fixture)
	}{
		{"undercount", "budget_totals_mismatch", func(f *fixture) { f.ledger.Totals.KnownTokens = 0 }},
		{"target-undercount", "budget_totals_mismatch", func(f *fixture) {
			a := f.ledger.Targets["orders"]
			a.Totals = budget.Totals{}
			f.ledger.Targets["orders"] = a
		}},
		{"duplicate", "budget_call_identity", func(f *fixture) { f.ledger.Calls = append(f.ledger.Calls, f.ledger.Calls[0]) }},
		{"wrong-reservation", "budget_reservation_mismatch", func(f *fixture) { f.ledger.Calls[0].ReservedTokens = 1 }},
		{"settled-no-usage", "budget_call_state", func(f *fixture) { f.ledger.Calls[0].Usage = nil }},
		{"overflow-usage", "budget_call_state", func(f *fixture) {
			f.ledger.Calls[0].Usage = &agent.Usage{InputTokens: math.MaxInt64, OutputTokens: 1, TotalTokens: 0}
		}},
		{"cache-overflow", "budget_call_state", func(f *fixture) { n := int64(16); f.ledger.Calls[0].Usage.CacheHitTokens = &n }},
		{"negative-total", "budget_limit_violation", func(f *fixture) { f.ledger.Totals.KnownTokens = -1 }},
		{"overflow-total", "budget_limit_violation", func(f *fixture) { f.ledger.Totals.KnownTokens = math.MaxInt64; f.ledger.Totals.ReservedTokens = 1 }},
		{"overflow-sum", "budget_overflow", func(f *fixture) {
			a := f.ledger.Targets["orders"]
			a.Totals.KnownTokens = math.MaxInt64
			f.ledger.Targets["orders"] = a
			f.ledger.Targets["other"] = budget.Account{Limits: f.plan.Budget, Totals: budget.Totals{KnownTokens: 1}}
		}},
		{"wrong-run", "budget_call_identity", func(f *fixture) { f.ledger.Calls[0].RunID = "run-2" }},
		{"wrong-account", "budget_binding_mismatch", func(f *fixture) { a := f.ledger.Targets["orders"]; a.RunID = "run-2"; f.ledger.Targets["orders"] = a }},
		{"terminal-reserved", "terminal_reservation", func(f *fixture) { f.setCall(budget.StateReserved, nil) }},
		{"invalid-without-halt", "budget_call_state", func(f *fixture) { f.setCall(budget.StateInvalid, &agent.Usage{InputTokens: -1}) }},
		{"terminal-halt", "terminal_budget_fault", func(f *fixture) { f.ledger.HaltReason = "shared fault" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tc.change(f)
			f.save(t)
			r := auditFixture(t, f, nil)
			assertViolation(t, r, tc.code, ledgerPath)
		})
	}
}
func (f *fixture) setCall(state budget.CallState, u *agent.Usage) {
	c := &f.ledger.Calls[0]
	c.State = state
	c.Usage = u
	c.Outcome = "reply"
	total := budget.Totals{Calls: 1}
	switch state {
	case budget.StateReserved:
		total.ReservedTokens = 100
		c.Outcome = ""
	case budget.StateUnknown, budget.StateInvalid:
		total.UnknownTokens = 100
	default:
		total.KnownTokens = u.TotalTokens
	}
	f.ledger.Totals = total
	a := f.ledger.Targets["orders"]
	a.Totals = total
	f.ledger.Targets["orders"] = a
}

func TestLegalUnfinishedAndFailureSnapshots(t *testing.T) {
	for _, state := range []budget.CallState{budget.StateReserved, budget.StateUnknown, budget.StateInvalid} {
		t.Run(string(state), func(t *testing.T) {
			f := newFixture(t)
			f.record.Status = campaign.Running
			f.record.FinishedAt = nil
			f.target = campaign.TargetRecord{ID: "orders", State: campaign.Active}
			f.run.Status = "Running"
			f.run.FinishedAt = nil
			f.run.Verdict = ""
			f.run.Phase = "Generating"
			if state == budget.StateInvalid {
				f.setCall(state, &agent.Usage{InputTokens: -1})
				f.ledger.HaltReason = "invalid provider usage"
			} else {
				f.setCall(state, nil)
			}
			f.save(t)
			os.Remove(filepath.Join(f.root, "summary.json"))
			os.Remove(filepath.Join(f.root, "summary.md"))
			f.record.Summary = campaign.Artifact{}
			put(t, f.root, "campaign.json", f.record)
			r := auditFixture(t, f, nil)
			if len(r.Violations) != 0 || r.ExecutionState != "incomplete" || !has(r.Observations, "index_pending", "") {
				t.Fatalf("%+v", r)
			}
		})
	}
	t.Run("run-before-index", func(t *testing.T) {
		f := newFixture(t)
		f.record.Status = campaign.Running
		f.record.FinishedAt = nil
		f.target = campaign.TargetRecord{ID: "orders", State: campaign.Active}
		f.save(t)
		r := auditFixture(t, f, nil)
		if len(r.Violations) != 0 || !has(r.Observations, "publication_pending", "") {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("rejected-result", func(t *testing.T) {
		f := newFixture(t)
		f.target = campaign.TargetRecord{ID: "orders", State: campaign.Finished, Attempt: &campaign.Attempt{Status: campaign.Failed, FailureCode: "invalid_result"}, Error: "callback rejected"}
		f.save(t)
		r := auditFixture(t, f, nil)
		if len(r.Violations) != 0 || !has(r.Observations, "result_rejected", "") || !has(r.Gaps, "rejected_run_unbound", "") || r.ExecutionState != "terminal_with_failures" {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("rejected-corrupt-residue", func(t *testing.T) {
		f := newFixture(t)
		f.target = campaign.TargetRecord{ID: "orders", State: campaign.Finished, Attempt: &campaign.Attempt{Status: campaign.Failed, FailureCode: "invalid_result"}, Error: "callback rejected"}
		f.run.TargetID = "other"
		f.save(t)
		r := auditFixture(t, f, nil)
		assertViolation(t, r, "run_identity_mismatch", "")
	})
	t.Run("failed-index-write", func(t *testing.T) {
		f := newFixture(t)
		f.record.Status = campaign.Failed
		f.record.Error = "control write failed"
		f.save(t)
		put(t, f.root, targetPath("orders"), campaign.TargetRecord{ID: "orders", State: campaign.Active})
		r := auditFixture(t, f, nil)
		if len(r.Violations) != 0 || !has(r.Gaps, "publication_pending", "") {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("early-control-failure", func(t *testing.T) {
		f := newFixture(t)
		f.record.Status = campaign.Failed
		f.record.Error = "prepare failed"
		f.record.Summary = campaign.Artifact{}
		put(t, f.root, "campaign.json", f.record)
		for _, p := range []string{"targets", "budget", "summary.json", "summary.md"} {
			os.RemoveAll(filepath.Join(f.root, p))
		}
		r := auditFixture(t, f, nil)
		if len(r.Violations) != 0 || !has(r.Observations, "initialization_incomplete", "") {
			t.Fatalf("%+v", r)
		}
	})
}

func TestFormatSelection(t *testing.T) {
	for _, tc := range []struct {
		name, p, text, code string
		violation           bool
	}{
		{"future-parent", "campaign.json", `{"schema_version":9}`, "unsupported_schema", false},
		{"duplicate", "campaign.json", `{"schema_version":1,"schema_version":1}`, "invalid_json", true},
		{"missing-required", ledgerPath, `{"schema_version":1}`, "invalid_json", true},
		{"trailing", ledgerPath, `{} {}`, "invalid_json", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			putBytes(t, f.root, tc.p, []byte(tc.text))
			r := auditFixture(t, f, nil)
			if tc.violation {
				assertViolation(t, r, tc.code, tc.p)
			} else if len(r.Violations) != 0 || !has(r.Gaps, tc.code, tc.p) {
				t.Fatalf("%+v", r)
			}
		})
	}
	t.Run("missing-zero-usage", func(t *testing.T) {
		f := newFixture(t)
		data, _ := json.Marshal(f.ledger)
		data = []byte(strings.Replace(string(data), `"InputTokens":15,`, "", 1))
		putBytes(t, f.root, ledgerPath, data)
		r := auditFixture(t, f, nil)
		assertViolation(t, r, "invalid_json", ledgerPath)
	})
	t.Run("unknown-field", func(t *testing.T) {
		f := newFixture(t)
		data, _ := json.Marshal(f.record)
		data = append([]byte(`{"future":true,`), data[1:]...)
		putBytes(t, f.root, "campaign.json", data)
		r := auditFixture(t, f, nil)
		if len(r.Violations) != 0 || !has(r.Gaps, "unsupported_format", "campaign.json") {
			t.Fatalf("%+v", r)
		}
	})
}

func TestSourceDriftAndCancellation(t *testing.T) {
	t.Run("stable-violation-independent-of-drift", func(t *testing.T) {
		f := newFixture(t)
		f.ledger.Totals.KnownTokens = 0
		f.save(t)
		inspector := func(_ context.Context, v *View, ref RunRef) (RunInspection, error) {
			v.ReadFile("summary.md")
			putBytes(t, f.root, "summary.md", []byte("changed"))
			return RunInspection{}, nil
		}
		r := auditFixture(t, f, inspector)
		assertViolation(t, r, "budget_totals_mismatch", ledgerPath)
		if r.InputsUnchanged == nil || *r.InputsUnchanged || !has(r.Gaps, "source_changed", "summary.md") {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("changed-basis-is-not-violation", func(t *testing.T) {
		f := newFixture(t)
		inspector := func(_ context.Context, v *View, ref RunRef) (RunInspection, error) {
			v.ReadFile("summary.json")
			s := get[campaign.Summary](t, f.root, "summary.json")
			s.Counts.Completed = 7
			put(t, f.root, "summary.json", s)
			return RunInspection{Findings: []Finding{{Severity: Violation, Code: "test_binding", Path: "summary.json", Reason: "observed mismatch", Sources: []string{"summary.json"}}}}, nil
		}
		r := auditFixture(t, f, inspector)
		if len(r.Violations) != 0 || !has(r.Gaps, "source_changed", "summary.json") {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("cancelled-before-recheck", func(t *testing.T) {
		f := newFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r, err := Inspect(ctx, Request{f.root, filepath.Join(t.TempDir(), "audit")}, func(context.Context, *View, RunRef) (RunInspection, error) { cancel(); return RunInspection{}, nil })
		if !errors.Is(err, context.Canceled) || r.InputsUnchanged != nil || r.Consistency != "inconclusive" || !has(r.Gaps, "audit_cancelled", "") {
			t.Fatalf("%+v %v", r, err)
		}
	})
}

func TestReadBoundaries(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		f := newFixture(t)
		os.Remove(filepath.Join(f.root, ledgerPath))
		outside := filepath.Join(t.TempDir(), "secret")
		os.WriteFile(outside, []byte("never read"), 0600)
		if err := os.Symlink(outside, filepath.Join(f.root, ledgerPath)); err != nil {
			t.Fatal(err)
		}
		r := auditFixture(t, f, nil)
		assertViolation(t, r, "unsafe_path", ledgerPath)
	})
	t.Run("oversize", func(t *testing.T) {
		f := newFixture(t)
		file, err := os.OpenFile(filepath.Join(f.root, ledgerPath), os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		err = file.Truncate(maxFileBytes + 1)
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		r := auditFixture(t, f, nil)
		if !has(r.Gaps, "source_unreadable", ledgerPath) || r.InputsUnchanged != nil {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("multiple-runs", func(t *testing.T) {
		f := newFixture(t)
		os.Mkdir(filepath.Join(f.root, runPath("orders", "run-2")), 0700)
		r := auditFixture(t, f, nil)
		if !has(r.Gaps, "multiple_runs", "") {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("empty-run", func(t *testing.T) {
		f := newFixture(t)
		f.record.Status = campaign.Running
		f.record.FinishedAt = nil
		f.target = campaign.TargetRecord{ID: "orders", State: campaign.Active}
		f.ledger.Calls = []budget.Call{}
		f.ledger.Totals = budget.Totals{}
		f.ledger.Targets["orders"] = budget.Account{Limits: f.plan.Budget}
		f.save(t)
		os.Remove(filepath.Join(f.root, runPath("orders", "run-1"), "run.json"))
		os.Remove(filepath.Join(f.root, "summary.json"))
		f.record.Summary = campaign.Artifact{}
		put(t, f.root, "campaign.json", f.record)
		r := auditFixture(t, f, nil)
		if len(r.Violations) != 0 || !has(r.Observations, "unindexed_run", "") {
			t.Fatalf("%+v", r)
		}
	})
}
func TestOutputConfigurationAndPublicationFailure(t *testing.T) {
	f := newFixture(t)
	for _, output := range []string{f.root, filepath.Join(f.root, "audit"), "relative", filepath.Dir(f.root)} {
		if _, err := Inspect(context.Background(), Request{f.root, output}, nil); !errors.Is(err, ErrConfig) {
			t.Fatalf("expected config error: %s: %v", output, err)
		}
	}
	output := filepath.Join(t.TempDir(), "audit")
	_, err := Inspect(context.Background(), Request{f.root, output}, func(_ context.Context, _ *View, _ RunRef) (RunInspection, error) {
		putBytes(t, output, "audit.json", []byte("occupied"))
		return RunInspection{}, nil
	})
	if err == nil {
		t.Fatal("expected publication failure")
	}
	if _, err = os.Stat(filepath.Join(output, "published.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed publication has completion marker")
	}
}
