package audit

import (
	"errors"
	"os"
	"reflect"

	"github.com/secacy/agent-harness/internal/campaign"
)

func (i *inspection) checkSummary() {
	var s campaign.Summary
	loaded := i.read("summary.json", &s, 0)
	record := i.record
	if record.Summary.Path != "" {
		if record.Summary.Path != "summary.json" {
			i.add(Violation, "summary_mismatch", "campaign.json", "summary.path", "summary path is not the fixed campaign layout")
		} else {
			i.checkArtifact(record.Summary, "campaign.json", ".")
		}
	} else if record.Summary.SHA256 != "" {
		i.add(Violation, "summary_mismatch", "campaign.json", "summary", "digest without summary path")
	}
	if !loaded {
		if record.Status == campaign.Completed {
			i.report.ExecutionState = "unknown"
		}
		if record.Status == campaign.Completed && i.missing("summary.json") {
			i.add(Violation, "campaign_completion_incomplete", "summary.json", "", "completed campaign lacks summary", "campaign.json")
		}
		return
	}
	if record.Status == campaign.Completed && record.Summary.Path == "" {
		i.add(Violation, "campaign_completion_incomplete", "campaign.json", "summary", "completed campaign lacks a summary reference")
	}
	if record.Status == campaign.Running {
		i.add(Observation, "publication_pending", "summary.json", "", "summary exists before campaign terminal publication", "campaign.json")
	}
	if s.CampaignID != record.ID || s.PlanSHA256 != record.PlanSHA256 || len(s.Targets) != len(i.plan.Targets) {
		i.add(Violation, "summary_mismatch", "summary.json", "campaign_id/plan_sha256/targets", "summary identity or target count differs", "campaign.json", "plan.json")
	}
	counts := campaign.Counts{Verdicts: map[string]int{}}
	for n, t := range s.Targets {
		if n >= len(i.plan.Targets) {
			break
		}
		id := i.plan.Targets[n].Input.ID
		if !identifier.MatchString(id) {
			continue
		}
		if t.ID != id {
			i.add(Violation, "summary_mismatch", "summary.json", "targets", "summary target order differs", "plan.json")
			continue
		}
		disk := i.targets[id]
		if disk != nil && !reflect.DeepEqual(*disk, t) {
			if record.Status != campaign.Completed && disk.State == campaign.Active && t.State == campaign.Finished && t.Attempt != nil {
				i.add(Gap, "publication_pending", "summary.json", "targets", "summary may contain an unpublished target result", targetPath(id), "campaign.json")
				i.checkAttempt(id, t, *t.Attempt, "summary.json")
			} else {
				i.add(Violation, "summary_mismatch", "summary.json", "targets", "summary and target record differ", targetPath(id), "campaign.json")
			}
		}
		if t.State == campaign.Finished && t.Attempt != nil && (disk == nil || reflect.DeepEqual(*disk, t)) {
			i.checkAttempt(id, t, *t.Attempt, "summary.json")
		}
		if t.State == campaign.Skipped {
			counts.Skipped++
		} else if t.State == campaign.Finished && t.Attempt != nil {
			switch t.Attempt.Status {
			case campaign.Completed:
				counts.Completed++
				counts.Verdicts[t.Attempt.Verdict]++
			case campaign.Failed:
				counts.Failed++
			case campaign.Cancelled:
				counts.Cancelled++
			}
		} else if record.Status == campaign.Completed {
			i.add(Violation, "campaign_completion_incomplete", "summary.json", "targets", "completed campaign has nonterminal targets", "campaign.json")
		}
	}
	if !reflect.DeepEqual(counts, s.Counts) {
		i.add(Violation, "summary_mismatch", "summary.json", "counts", "summary counts do not match target results")
	}
	if i.ledger != nil && !reflect.DeepEqual(s.Budget, *i.ledger) {
		i.add(Violation, "summary_mismatch", "summary.json", "budget", "summary budget differs from ledger", ledgerPath)
	}
	if record.Status == campaign.Completed {
		_, err := i.view.ReadFile("summary.md")
		if errors.Is(err, os.ErrNotExist) {
			i.add(Violation, "campaign_completion_incomplete", "summary.md", "", "completed campaign lacks summary markdown", "campaign.json")
		} else if err != nil {
			i.add(Gap, "source_unreadable", "summary.md", "", err.Error())
		}
	}
	if record.Status == campaign.Running {
		return
	}
	i.report.ExecutionState = "terminal_with_failures"
	if record.Status == campaign.Completed && counts.Failed == 0 && counts.Cancelled == 0 && counts.Skipped == 0 {
		i.report.ExecutionState = "completed"
	}
}
