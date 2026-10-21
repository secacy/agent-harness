package audit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/secacy/agent-harness/internal/campaign"
)

func targetPath(id string) string   { return "targets/" + id + "/target.json" }
func runPath(id, run string) string { return "targets/" + id + "/runs/" + run }
func terminal(status string) bool {
	return status == "Completed" || status == "Failed" || status == "Cancelled"
}
func validVerdict(v string) bool {
	return v == "ReadyForReview" || v == "Rejected" || v == "NeedsAttention"
}

func (i *inspection) lifecycle(p, status string, finished bool, errorText string) {
	if status != "Running" && !terminal(status) {
		i.add(Violation, "invalid_state", p, "status", "unknown lifecycle status")
		return
	}
	if status == "Running" && finished || terminal(status) && !finished {
		i.add(Violation, "invalid_state", p, "finished_at", "finish timestamp does not match status")
	}
	if (status == "Failed" || status == "Cancelled") && errorText == "" {
		i.add(Violation, "invalid_state", p, "error", "failed or cancelled record lacks an error")
	}
	if status == "Completed" && errorText != "" {
		i.add(Violation, "invalid_state", p, "error", "completed record carries an execution error")
	}
}

func (i *inspection) inspectTarget(target campaign.Target) {
	id := target.Input.ID
	p := targetPath(id)
	var t campaign.TargetRecord
	if i.read(p, &t, 0) {
		i.targets[id] = &t
		if t.ID != id {
			i.add(Violation, "identity_mismatch", p, "id", "target ID differs from directory", "plan.json")
		}
		switch t.State {
		case campaign.Pending, campaign.Active, campaign.Skipped:
			if t.Attempt != nil {
				i.add(Violation, "invalid_state", p, "attempt", "non-finished target carries an attempt")
			}
			if t.State == campaign.Skipped && t.Reason == "" {
				i.add(Violation, "invalid_state", p, "reason", "skipped target lacks a reason")
			}
		case campaign.Finished:
			if t.Attempt == nil {
				i.add(Violation, "invalid_state", p, "attempt", "finished target lacks an attempt")
			}
		default:
			i.add(Violation, "invalid_state", p, "state", "unsupported target state")
		}
	}
	runsDir := "targets/" + id + "/runs"
	names := i.list(runsDir)
	if len(names) > 0 {
		i.started = true
	}
	if len(names) > 1 {
		i.add(Gap, "multiple_runs", runsDir, "", "one-run protocol cannot select among multiple runs")
	}
	i.runs[id] = map[string]RunRecord{}
	for _, name := range names {
		dir := runPath(id, name)
		rp := dir + "/run.json"
		var run RunRecord
		if !i.read(rp, &run, 2) {
			if i.missing(rp) {
				i.add(Observation, "unindexed_run", rp, "", "run directory exists before its first record", runsDir)
			}
			continue
		}
		i.runs[id][name] = run
		if run.ID != name || run.TargetID != id || run.Directory != filepath.Join(i.record.Directory, filepath.FromSlash(dir)) {
			i.add(Violation, "run_identity_mismatch", rp, "id/target_id/directory", "run identity differs from its owning directory", "campaign.json")
		}
		i.lifecycle(rp, run.Status, run.FinishedAt != nil, run.Error)
		if run.StartedAt.IsZero() || run.UpdatedAt.IsZero() {
			i.add(Violation, "invalid_state", rp, "started_at/updated_at", "run timestamps are missing")
		}
		switch run.Phase {
		case "Preparing", "CheckingBaseline", "Generating", "Validating", "Publishing":
		default:
			i.add(Violation, "invalid_state", rp, "phase", "unsupported Run v2 phase")
		}
		if run.Status == "Completed" && (!validVerdict(run.Verdict) || !digestPattern.MatchString(run.CandidateSHA256) || !digestPattern.MatchString(run.PatchSHA256)) {
			i.add(Violation, "terminal_claim_incomplete", rp, "verdict/candidate_sha256/patch_sha256", "completed run lacks candidate identity or verdict")
		}
		if run.Status == "Completed" && run.Phase != "Publishing" {
			i.add(Violation, "terminal_claim_incomplete", rp, "phase", "completed run has not entered publication")
		}
		if run.Status == "Running" && run.Verdict != "" {
			i.add(Violation, "invalid_state", rp, "verdict", "running record carries a final verdict")
		}
		if (run.Status == "Failed" || run.Status == "Cancelled") && run.Verdict != "" {
			i.add(Violation, "failed_run_has_verdict", rp, "verdict", "failed run carries a final verdict")
		}
		if i.inspector == nil {
			i.add(Gap, "run_inspector_missing", rp, "", "scenario evidence and phase prerequisites have not been inspected")
			i.report.Coverage = append(i.report.Coverage, Coverage{Area: dir + "/scenario", Complete: false})
		} else {
			result, err := i.inspector(i.view.ctx, i.view, RunRef{Path: dir, Target: target.Input, CampaignID: i.record.ID, OriginalRoot: i.record.Directory, Record: run})
			i.findings = append(i.findings, result.Findings...)
			for n := len(i.findings) - len(result.Findings); n < len(i.findings); n++ {
				finding := &i.findings[n]
				finding.Sources = append(finding.Sources, finding.Path, rp, "plan.json", "campaign.json")
			}
			complete := result.Complete && err == nil
			for _, finding := range result.Findings {
				if finding.Severity == Gap {
					complete = false
				}
			}
			i.report.Coverage = append(i.report.Coverage, Coverage{Area: dir + "/scenario", Complete: complete})
			if !complete {
				i.add(Gap, "run_inspection_incomplete", rp, "", "scenario inspection did not complete")
			}
			if err != nil {
				i.add(Gap, "run_inspection_error", rp, "", err.Error())
			}
		}
	}
	if i.targets[id] != nil {
		if (t.State == campaign.Pending || t.State == campaign.Skipped) && len(names) > 0 {
			i.add(Violation, "state_prerequisite_mismatch", p, "state", "unstarted target has run directories", runsDir)
		}
		if t.State == campaign.Active && len(names) > 0 {
			i.add(Observation, "index_pending", p, "", "run exists before target result publication", runsDir)
		}
		if t.State == campaign.Finished && t.Attempt != nil {
			i.checkAttempt(id, t, *t.Attempt, p)
		}
	}
}

func (i *inspection) checkAttempt(id string, t campaign.TargetRecord, a campaign.Attempt, p string) {
	if a.RunID == "" {
		if a.Directory != "" || a.Report.Path != "" || a.Report.SHA256 != "" || a.Verdict != "" || (a.Status != campaign.Failed && a.Status != campaign.Cancelled) || t.Error == "" {
			i.add(Violation, "target_attempt_mismatch", p, "attempt", "identity-free failure has inconsistent fields")
			return
		}
		if a.FailureCode == "invalid_result" {
			i.add(Observation, "result_rejected", p, "attempt", "scheduler rejected the callback result")
			if len(i.runs[id]) > 0 {
				i.add(Gap, "rejected_run_unbound", p, "attempt", "remaining runs cannot be bound to the rejected callback from this snapshot")
			}
		} else if len(i.runs[id]) > 0 {
			i.add(Gap, "unindexed_run", p, "attempt", "identity-free failure has remaining run material")
		}
		return
	}
	if !identifier.MatchString(a.RunID) || a.Directory != runPath(id, a.RunID) {
		i.add(Violation, "target_attempt_mismatch", p, "attempt.directory", "attempt escapes its owning target")
		return
	}
	rp := a.Directory + "/run.json"
	run, ok := i.runs[id][a.RunID]
	if !ok {
		if _, observed := i.view.sources[rp]; !observed {
			_, err := i.view.ReadFile(rp)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				i.add(Gap, "source_unreadable", rp, "", err.Error(), p)
			}
		}
		if i.missing(rp) {
			i.add(Violation, "target_attempt_mismatch", p, "attempt.run_id", "referenced run is missing", rp)
		} else {
			i.add(Gap, "run_unverified", rp, "", "referenced run could not be inspected", p)
		}
		return
	}
	if !terminal(string(a.Status)) || string(a.Status) != run.Status || a.Verdict != run.Verdict || (a.Status == campaign.Completed && t.Error != "") || ((a.Status == campaign.Failed || a.Status == campaign.Cancelled) && t.Error == "") {
		i.add(Violation, "target_attempt_mismatch", p, "attempt", "attempt status/verdict/error does not match its run", rp)
	}
	if a.Status == campaign.Completed && a.Report.Path == "" {
		i.add(Violation, "terminal_claim_incomplete", p, "attempt.report", "completed attempt lacks report")
	}
	if a.Report.Path != "" {
		i.checkArtifact(a.Report, p, a.Directory)
	} else if a.Report.SHA256 != "" {
		i.add(Violation, "target_attempt_mismatch", p, "attempt.report", "digest without report path")
	}
	if a.Status == campaign.Completed && i.ledger != nil && i.ledger.Targets[id].RunID != a.RunID {
		i.add(Violation, "budget_binding_mismatch", p, "attempt.run_id", "completed attempt lacks its budget binding", ledgerPath)
	}
}

func (i *inspection) checkArtifact(a campaign.Artifact, owner, dir string) {
	if !safeRelative(a.Path) || a.Path == "." || !(strings.HasPrefix(a.Path, dir+"/") || dir == ".") || !digestPattern.MatchString(a.SHA256) {
		i.add(Violation, "unsafe_artifact", owner, "", "invalid artifact path or digest")
		return
	}
	data, err := i.view.ReadFile(a.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			i.add(Violation, "terminal_claim_incomplete", a.Path, "", "referenced artifact is missing", owner)
		} else {
			severity, code := Gap, "source_unreadable"
			if errors.Is(err, ErrUnsafePath) {
				severity, code = Violation, "unsafe_path"
			}
			i.add(severity, code, a.Path, "", err.Error(), owner)
		}
	} else if checksum(data) != a.SHA256 {
		i.add(Violation, "artifact_digest_mismatch", a.Path, "", "artifact bytes differ from the published reference", owner)
	}
}
