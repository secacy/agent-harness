package audit

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
)

func (i *inspection) checkRecoveryExecution(c campaign.RecoveryCheckpoint, head campaign.RecoveryHead) {
	dir := campaign.RecoveryExecutionPath
	entries, err := i.view.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	i.report.Coverage = append(i.report.Coverage, Coverage{Area: "recovery_execution", Complete: true})
	i.report.RecoveryExecutionState = "incomplete"
	if err != nil {
		i.add(Gap, "recovery_execution_unreadable", dir, "", err.Error())
		return
	}
	allowed := map[string]bool{"activation.json": true, "started.json": true, "result.json": true, "budget": true, "targets": true}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".checkpoint-") && e.Type().IsRegular() {
			continue
		}
		if !allowed[e.Name()] || (e.Name() == "budget" || e.Name() == "targets") != e.IsDir() {
			i.add(Violation, "recovery_execution_entry", dir, "", "unexpected execution entry: "+e.Name())
		}
	}
	ap := dir + "/activation.json"
	var a campaign.Activation
	if !i.read(ap, &a, 1) {
		if i.missing(ap) && len(entries) == 0 {
			i.add(Observation, "recovery_activation_pending", ap, "", "execution directory exists before allocation")
		} else if i.missing(ap) {
			i.add(Violation, "recovery_activation_missing", ap, "", "execution materials lack allocation", dir)
		}
		return
	}
	ids := []string{}
	for _, r := range a.Receipts {
		ids = append(ids, r.TargetID)
	}
	expected, err := campaign.RecoveryReceipts(*i.plan, c, ids, a.CreatedAt)
	if err != nil || !reflect.DeepEqual(expected, a.Receipts) || a.CampaignID != c.CampaignID || a.PlanSHA256 != c.PlanSHA256 || a.Checkpoint != head.Checkpoint || a.CreatedAt.Before(c.CreatedAt) {
		i.add(Violation, "recovery_activation_invalid", ap, "", "allocation differs from the original plan and checkpoint", "plan.json", "recoveries/head.json", head.Checkpoint.Path)
		return
	}
	activationSHA := checksum(i.view.data[ap])
	sp, rp, bp := dir+"/started.json", dir+"/result.json", dir+"/budget/budget.json"
	var started campaign.RecoveryStarted
	hasStarted := i.read(sp, &started, 1)
	if hasStarted && (started.ActivationSHA256 != activationSHA || started.StartedAt.Before(a.CreatedAt) || started.StartedAt.IsZero()) {
		i.add(Violation, "recovery_started_invalid", sp, "", "start marker differs from allocation", ap)
	}
	var current budget.Snapshot
	hasBudget := i.read(bp, &current, 2)
	if !hasBudget {
		current = c.After.Budget
	}
	if len(a.Receipts) == 0 && hasBudget {
		i.add(Violation, "recovery_budget_unexpected", bp, "", "adoption-only execution must not open a model ledger", ap)
	}
	if hasStarted && len(a.Receipts) > 0 && !hasBudget && i.missing(bp) {
		i.add(Violation, "recovery_budget_missing", bp, "", "started execution lacks its inherited ledger", sp)
	}
	if hasBudget {
		if err := campaign.ValidateRecoveryBudget(c.After.Budget, current, a.Receipts); err != nil {
			i.add(Violation, "recovery_budget_invalid", bp, "", err.Error(), ap, head.Checkpoint.Path)
			i.add(Gap, "recovery_execution_checks_blocked", bp, "", "dependent execution checks require a valid inherited ledger")
			return
		}
		for _, e := range i.recoveryEntries(dir + "/budget") {
			if e.Name() != "budget.json" && !strings.HasPrefix(e.Name(), ".budget-") {
				i.add(Violation, "recovery_execution_entry", dir+"/budget", "", "unexpected ledger entry")
			}
		}
		if !hasStarted && (len(current.Calls) != len(c.After.Budget.Calls) || len(current.Rejections) != len(c.After.Budget.Rejections)) {
			i.add(Violation, "recovery_call_before_start", bp, "", "generation activity precedes the start marker", sp)
		}
	}
	if hasStarted && len(a.Receipts) > 0 {
		for _, r := range a.Receipts {
			if current.Targets[r.TargetID].RunID != r.RunID {
				i.add(Violation, "recovery_binding_missing", bp, "", "started receipt lacks its active binding", sp, ap)
			}
		}
		for _, call := range current.Calls[len(c.After.Budget.Calls):] {
			if call.CreatedAt.Before(started.StartedAt) || !call.CreatedAt.Before(i.plan.Recovery.GenerationDeadline) {
				i.add(Violation, "recovery_call_outside_window", bp, "", "model admission is outside the original generation window", sp, "plan.json")
			}
		}
	}
	runs := map[string]RunRecord{}
	for _, r := range a.Receipts {
		index := slices.IndexFunc(i.plan.Targets, func(t campaign.Target) bool { return t.Input.ID == r.TargetID })
		runsDir := dir + "/targets/" + r.TargetID + "/runs"
		names := i.list(runsDir)
		for _, name := range names {
			if name != r.RunID {
				i.add(Violation, "recovery_unallocated_run", runsDir, "", "run directory lacks a receipt", ap)
			}
		}
		if !hasStarted && len(names) > 0 {
			i.add(Violation, "recovery_run_before_start", runsDir, "", "run was created before the start marker", sp)
		}
		p := runsDir + "/" + r.RunID
		var run RunRecord
		if !i.read(p+"/run.json", &run, 2) {
			continue
		}
		runs[r.TargetID] = run
		if run.ID != r.RunID || run.TargetID != r.TargetID || run.Directory != filepath.Join(i.record.Directory, filepath.FromSlash(p)) {
			i.add(Violation, "recovery_run_identity", p+"/run.json", "", "run differs from its allocated receipt", ap)
		}
		i.lifecycle(p+"/run.json", run.Status, run.FinishedAt != nil, run.Error)
		switch run.Phase {
		case "Preparing", "CheckingBaseline", "Generating", "Validating", "Publishing":
		default:
			i.add(Violation, "recovery_run_phase", p+"/run.json", "", "unsupported Run phase")
		}
		if run.StartedAt.IsZero() || run.StartedAt.Before(started.StartedAt) || run.UpdatedAt.IsZero() {
			i.add(Violation, "recovery_run_time", p+"/run.json", "", "run timestamps differ from activation", sp)
		}
		if run.Status == "Completed" && (run.Phase != "Publishing" || !validVerdict(run.Verdict) || !digestPattern.MatchString(run.CandidateSHA256) || !digestPattern.MatchString(run.PatchSHA256)) || run.Status != "Completed" && run.Verdict != "" {
			i.add(Violation, "recovery_run_terminal", p+"/run.json", "", "run carries an invalid quality claim")
		}
		if i.inspector == nil {
			i.add(Gap, "run_inspector_missing", p+"/run.json", "", "recovery run requires scenario inspection")
			continue
		}
		inspection, err := i.inspector(i.view.ctx, i.view, RunRef{Path: p, Target: i.plan.Targets[index].Input, CampaignID: c.CampaignID, OriginalRoot: i.record.Directory, Record: run, BudgetPath: bp})
		for _, f := range inspection.Findings {
			f.Sources = append(f.Sources, f.Path, p+"/run.json", ap, bp, "plan.json")
			i.findings = append(i.findings, f)
		}
		if err != nil || !inspection.Complete {
			i.add(Gap, "run_inspection_incomplete", p+"/run.json", "", "recovery scenario inspection did not complete")
		}
	}
	// 限定所有 Target 目录，不能把另一份候选隐藏在未分配路径。
	for _, name := range i.list(dir + "/targets") {
		if !slices.Contains(ids, name) {
			i.add(Violation, "recovery_unallocated_target", dir+"/targets", "", "target lacks an allocation receipt", ap)
		}
		for _, e := range i.recoveryEntries(dir + "/targets/" + name) {
			if e.Name() != "runs" || !e.IsDir() {
				i.add(Violation, "recovery_execution_entry", dir+"/targets/"+name, "", "unexpected target entry")
			}
		}
	}
	var result campaign.RecoveryResult
	if !i.read(rp, &result, 1) {
		if i.missing(rp) {
			i.add(Observation, "recovery_execution_interrupted", rp, "", "allocated execution has no final result and must not be repeated", ap)
		}
		return
	}
	if !hasStarted || result.ActivationSHA256 != activationSHA || !result.StartedAt.Equal(started.StartedAt) || result.FinishedAt.Before(result.StartedAt) || !reflect.DeepEqual(result.Budget, current) || len(result.Targets) != len(c.After.Targets) || !terminal(string(result.Status)) || result.Status == campaign.Completed && result.Error != "" || result.Status != campaign.Completed && result.Error == "" {
		i.add(Violation, "recovery_result_invalid", rp, "", "result lacks matching activation, ledger or lifecycle", ap, sp, bp)
		return
	}
	complete, failed := true, false
	for n, t := range result.Targets {
		old := c.After.Targets[n]
		if t.ID != old.ID {
			i.add(Violation, "recovery_result_target", rp, "", "result target order differs from checkpoint", head.Checkpoint.Path)
			continue
		}
		allocated := slices.IndexFunc(a.Receipts, func(r campaign.RunReceipt) bool { return r.TargetID == t.ID })
		if allocated < 0 {
			if !reflect.DeepEqual(t.Attempt, old.Current) || old.Current == nil && (t.State != campaign.Pending || t.Error != "" || t.Reason != "") || old.Current != nil && t.State != campaign.Finished {
				i.add(Violation, "recovery_history_changed", rp, "", "unselected target history was changed", head.Checkpoint.Path)
			}
		} else {
			r := a.Receipts[allocated]
			if t.State != campaign.Finished || t.Attempt == nil {
				if result.Status == campaign.Completed {
					i.add(Violation, "recovery_result_target", rp, "", "completed execution lacks an allocated result", ap)
				}
				complete = false
				continue
			}
			attempt := *t.Attempt
			if attempt.RunID == "" {
				if (attempt.Status != campaign.Failed && attempt.Status != campaign.Cancelled) || attempt.Verdict != "" || attempt.Directory != "" || attempt.Report.Path != "" || attempt.Report.SHA256 != "" || t.Error == "" {
					i.add(Violation, "recovery_result_target", rp, "", "invalid identity-free failure")
				}
				if _, ok := runs[t.ID]; ok {
					i.add(Gap, "recovery_unbound_run", rp, "", "failed callback left an unbound run", ap)
				}
			} else {
				run, ok := runs[t.ID]
				d := dir + "/targets/" + t.ID + "/runs/" + r.RunID
				if !ok || attempt.RunID != r.RunID || attempt.Directory != d || string(attempt.Status) != run.Status || !terminal(run.Status) || attempt.Verdict != run.Verdict || attempt.Status == campaign.Completed && t.Error != "" || attempt.Status != campaign.Completed && t.Error == "" {
					i.add(Violation, "recovery_result_target", rp, "", "result differs from allocated run", d+"/run.json", ap)
				}
				if attempt.Status == campaign.Completed && attempt.Report.Path == "" {
					i.add(Violation, "terminal_claim_incomplete", rp, "", "completed recovery lacks report")
				}
				if attempt.Report.Path != "" {
					i.checkArtifact(attempt.Report, rp, d)
				}
			}
		}
		if t.Attempt == nil {
			complete = false
		} else if t.Attempt.Status != campaign.Completed {
			failed = true
		}
	}
	i.report.RecoveryExecutionState = "incomplete"
	if complete {
		i.report.RecoveryExecutionState = "completed"
		if failed || result.Status != campaign.Completed {
			i.report.RecoveryExecutionState = "terminal_with_failures"
		}
	}
	i.add(Observation, "recovery_execution_recorded", rp, "", "recovery result published; original root status remains historical", ap, sp, bp)
}

func (i *inspection) recoveryEntries(p string) []os.DirEntry {
	entries, err := i.view.ReadDir(path.Clean(p))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		i.add(Gap, "recovery_execution_unreadable", p, "", err.Error())
	}
	return entries
}
