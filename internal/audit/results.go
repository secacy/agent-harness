package audit

import (
	"sort"
	"strings"
)

func (i *inspection) finish() {
	for _, f := range i.findings {
		sort.Strings(f.Sources)
		f.Sources = compact(f.Sources)
		for _, p := range f.Sources {
			source := i.view.sources[p]
			if source == nil || source.Verified == nil {
				if f.Severity == Violation {
					f.Severity = Gap
					f.Reason = "unverified source for " + f.Code + ": " + f.Reason
					f.Code = "source_unverified"
				}
				break
			}
			if !*source.Verified {
				f.Severity = Gap
				f.Reason = "changed source for " + f.Code + ": " + f.Reason
				f.Code = "source_changed"
				break
			}
		}
		parts := strings.Split(f.Path, "/")
		if len(parts) > 1 && parts[0] == "targets" {
			f.TargetID = parts[1]
			if len(parts) > 3 && parts[2] == "runs" {
				f.RunID = parts[3]
			}
		}
		switch f.Severity {
		case Violation:
			i.report.Violations = append(i.report.Violations, f)
		case Gap:
			i.report.Gaps = append(i.report.Gaps, f)
		default:
			i.report.Observations = append(i.report.Observations, f)
		}
	}

	keys := make([]string, 0, len(i.view.sources))
	for p := range i.view.sources {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	for _, p := range keys {
		s := *i.view.sources[p]
		i.report.Sources = append(i.report.Sources, s)
		if s.Verified == nil || !*s.Verified {
			code := "source_unverified"
			if s.Verified != nil {
				code = "source_changed"
			}
			i.report.Gaps = append(i.report.Gaps, Finding{Severity: Gap, Code: code, Path: p, Reason: "source stability could not be established", Sources: []string{p}})
		}
	}
	for _, gap := range i.report.Gaps {
		if gap.Path == ledgerPath {
			i.report.Coverage[1].Complete = false
		}
		if gap.Code == "run_inspector_missing" || strings.HasPrefix(gap.Code, "run_inspection_") {
			continue
		}
		if gap.Path == "campaign.json" || gap.Path == "plan.json" || strings.HasPrefix(gap.Path, "summary.") || strings.HasPrefix(gap.Path, "targets") {
			i.report.Coverage[0].Complete = false
		}
	}
	i.report.ScanComplete = len(i.report.Gaps) == 0
	for _, coverage := range i.report.Coverage {
		if !coverage.Complete {
			i.report.ScanComplete = false
		}
	}
	i.report.Consistency = "pass"
	if !i.report.ScanComplete {
		i.report.Consistency = "inconclusive"
	}
	if len(i.report.Violations) > 0 {
		i.report.Consistency = "violated"
		i.report.ExecutionState = "unknown"
	}
	if i.report.InputsUnchanged == nil || !*i.report.InputsUnchanged {
		i.report.ExecutionState = "unknown"
	}
	if i.report.RecoveryExecutionState != "" && i.report.Consistency != "pass" {
		i.report.RecoveryExecutionState = "unknown"
	}
	if i.report.ResourceState != "" && i.report.Consistency != "pass" {
		i.report.ResourceState = "unknown"
	}
	for _, findings := range [][]Finding{i.report.Violations, i.report.Gaps, i.report.Observations} {
		sort.SliceStable(findings, func(a, b int) bool {
			x, y := findings[a], findings[b]
			return x.Path+"\x00"+x.Code+"\x00"+x.Field+"\x00"+x.Reason < y.Path+"\x00"+y.Code+"\x00"+y.Field+"\x00"+y.Reason
		})
	}
}
