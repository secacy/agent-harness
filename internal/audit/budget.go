package audit

import (
	"fmt"
	"math"
	"sort"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
)

func limitsValid(l budget.Limits) bool { return l.MaxCalls > 0 && l.MaxTokens > 0 }
func addInt(a, b int64) (int64, bool) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, false
	}
	return a + b, true
}
func addTotals(a, b budget.Totals) (budget.Totals, bool) {
	out := budget.Totals{}
	pairs := [][3]*int64{{&out.Calls, &a.Calls, &b.Calls}, {&out.KnownTokens, &a.KnownTokens, &b.KnownTokens}, {&out.ReservedTokens, &a.ReservedTokens, &b.ReservedTokens}, {&out.UnknownTokens, &a.UnknownTokens, &b.UnknownTokens}}
	for _, p := range pairs {
		v, ok := addInt(*p[1], *p[2])
		if !ok {
			return budget.Totals{}, false
		}
		*p[0] = v
	}
	return out, true
}
func usageValid(u *agent.Usage, p budget.Profile) bool {
	if u == nil {
		return false
	}
	sum, ok := addInt(u.InputTokens, u.OutputTokens)
	if !ok || sum != u.TotalTokens || u.InputTokens > p.ContextTokens || u.OutputTokens > p.MaxOutputTokens {
		return false
	}
	if u.CacheHitTokens != nil && (*u.CacheHitTokens < 0 || *u.CacheHitTokens > u.InputTokens) {
		return false
	}
	if u.CacheMissTokens != nil && (*u.CacheMissTokens < 0 || *u.CacheMissTokens > u.InputTokens) {
		return false
	}
	return u.CacheHitTokens == nil || u.CacheMissTokens == nil || *u.CacheHitTokens == u.InputTokens-*u.CacheMissTokens
}

func (i *inspection) checkBudget() {
	i.report.Coverage[1].Complete = true
	s := i.ledger
	violation := func(code, field, reason string, deps ...string) {
		i.add(Violation, code, ledgerPath, field, reason, deps...)
	}
	if !identifier.MatchString(s.CampaignID) || s.Revision < 1 || s.Profile.Validate() != nil || !limitsValid(s.Limits) || len(s.Targets) == 0 || s.Calls == nil || s.Rejections == nil {
		violation("budget_invalid", "campaign_id/revision/profile/limits", "invalid ledger identity, revision or limits")
	}
	if s.SchemaVersion == 1 && (len(s.RecoveryIDs) != 0 || s.RecoveryRevision != 0) {
		violation("budget_invalid", "recovery_ids/recovery_revision", "legacy ledger cannot declare recovery history")
	}
	if i.record != nil && s.CampaignID != i.record.ID {
		violation("identity_mismatch", "campaign_id", "ledger belongs to another campaign", "campaign.json")
	}
	if i.plan != nil {
		profile, planned := s.Profile, i.plan.Profile
		profile.VerifiedAt, planned.VerifiedAt = profile.VerifiedAt.UTC(), planned.VerifiedAt.UTC()
		if profile != planned || s.Limits != i.plan.Budget || len(s.Targets) != len(i.plan.Targets) {
			violation("budget_plan_mismatch", "profile/limits/targets", "ledger differs from the fixed plan", "plan.json")
		}
		for _, target := range i.plan.Targets {
			a, ok := s.Targets[target.Input.ID]
			if !ok || a.Limits != target.Budget {
				violation("budget_plan_mismatch", "targets", "target limits differ from the plan", "plan.json")
			}
		}
	}
	q, qOK := addInt(s.Profile.ContextTokens, s.Profile.MaxOutputTokens)
	totals := budget.Totals{}
	perTarget := map[string]budget.Totals{}
	totalOK := true
	seen := map[string]bool{}
	for n, c := range s.Calls {
		field := fmt.Sprintf("calls[%d]", n)
		a, registered := s.Targets[c.TargetID]
		if !identifier.MatchString(c.ID) || seen[c.ID] || !registered || !identifier.MatchString(c.RunID) || a.RunID != c.RunID || !digestPattern.MatchString(c.InputSHA256) {
			violation("budget_call_identity", field, "duplicate or invalid call identity")
		}
		seen[c.ID] = true
		if s.SchemaVersion == 1 && c.RecoveryID != "" {
			violation("budget_call_state", field, "legacy call cannot declare recovery provenance")
		}
		if c.CreatedAt.IsZero() || c.UpdatedAt.IsZero() {
			violation("budget_invalid", field, "call timestamps are missing")
		}
		if !qOK || q <= 0 || c.ReservedTokens != q {
			violation("budget_reservation_mismatch", field+".reserved_tokens", "reservation is not the verified C+O bound")
		}
		amount := budget.Totals{Calls: 1}
		switch c.State {
		case budget.StateReserved:
			amount.ReservedTokens = c.ReservedTokens
			if c.Usage != nil || c.Outcome != "" {
				violation("budget_call_state", field, "reserved call has settlement fields")
			}
			i.add(Observation, "unresolved_reservation", ledgerPath, field, "reservation remains occupied; no release is permitted")
			deps := []string{}
			completed := i.record != nil && i.record.Status == campaign.Completed
			if completed {
				deps = append(deps, "campaign.json")
			}
			run, ok := i.runs[c.TargetID][c.RunID]
			if ok && run.Status == "Completed" {
				completed = true
				deps = append(deps, runPath(c.TargetID, c.RunID)+"/run.json")
			}
			if completed {
				violation("terminal_reservation", field, "completed lifecycle still has an unsettled call", deps...)
			}
		case budget.StateSettled:
			if !usageValid(c.Usage, s.Profile) {
				violation("budget_call_state", field, "settled call lacks valid usage")
				totalOK = false
			} else {
				amount.KnownTokens = c.Usage.TotalTokens
			}
		case budget.StateUnknown:
			amount.UnknownTokens = c.ReservedTokens
			if c.Usage != nil {
				violation("budget_call_state", field, "unknown call has usage")
			}
			i.add(Observation, "unknown_usage", ledgerPath, field, "unknown cost retains the entire reservation")
		case budget.StateInvalid:
			amount.UnknownTokens = c.ReservedTokens
			if c.Usage == nil || usageValid(c.Usage, s.Profile) || s.HaltReason == "" {
				violation("budget_call_state", field, "invalid call must retain invalid usage and a halt reason")
			}
			i.add(Observation, "provider_usage_invalid", ledgerPath, field, "provider usage is invalid; retained reservation may not bound actual cost")
		default:
			violation("budget_call_state", field, "unsupported call state")
			totalOK = false
		}
		if c.State != budget.StateReserved && c.Outcome != "reply" && c.Outcome != "model_error" {
			violation("budget_call_state", field+".outcome", "invalid settlement outcome")
		}
		var ok bool
		totals, ok = addTotals(totals, amount)
		if !ok {
			totalOK = false
			violation("budget_overflow", field, "campaign recomputation overflows or contains negative values")
		}
		targetTotal, ok := addTotals(perTarget[c.TargetID], amount)
		if !ok {
			totalOK = false
			violation("budget_overflow", field, "target recomputation overflows or contains negative values")
		}
		perTarget[c.TargetID] = targetTotal
	}
	if totalOK && totals != s.Totals {
		violation("budget_totals_mismatch", "totals", "stored totals differ from individual calls")
	}
	sum := budget.Totals{}
	sumOK := true
	ids := make([]string, 0, len(s.Targets))
	for id := range s.Targets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a := s.Targets[id]
		field := "targets." + id
		if s.SchemaVersion == 1 && len(a.RunIDs) != 0 {
			violation("budget_invalid", field, "legacy account cannot declare multiple run bindings")
		}
		if !identifier.MatchString(id) || !limitsValid(a.Limits) || a.RunID != "" && !identifier.MatchString(a.RunID) {
			violation("budget_invalid", field, "invalid account identity or limits")
		}
		if totalOK && a.Totals != perTarget[id] {
			violation("budget_totals_mismatch", field+".totals", "stored account totals differ from individual calls")
		}
		i.checkLimits(field, a.Totals, a.Limits)
		var ok bool
		sum, ok = addTotals(sum, a.Totals)
		if !ok {
			sumOK = false
			violation("budget_overflow", field, "account totals cannot be summed")
		}
		if a.RunID != "" && identifier.MatchString(id) && identifier.MatchString(a.RunID) && i.plan != nil {
			p := runPath(id, a.RunID) + "/run.json"
			if _, ok := i.runs[id][a.RunID]; !ok {
				if _, observed := i.view.sources[p]; !observed {
					i.view.ReadFile(p)
				}
				if i.missing(p) {
					violation("budget_binding_mismatch", field+".run_id", "bound run record is missing", p)
				} else {
					i.add(Gap, "budget_run_unverified", ledgerPath, field+".run_id", "bound run could not be inspected", p)
				}
			}
			t := i.targets[id]
			if t != nil && (t.State == campaign.Pending || t.State == campaign.Skipped) {
				violation("state_prerequisite_mismatch", field+".run_id", "unstarted target has budget activity", targetPath(id))
			}
		}
	}
	if sumOK && sum != s.Totals {
		violation("budget_totals_mismatch", "totals", "sum of account totals differs from campaign totals")
	}
	i.checkLimits("totals", s.Totals, s.Limits)
	for n, r := range s.Rejections {
		a, ok := s.Targets[r.TargetID]
		if !ok || a.RunID != r.RunID || !identifier.MatchString(r.RunID) || !digestPattern.MatchString(r.InputSHA256) || (r.Scope != "campaign" && r.Scope != "target") || (r.Resource != "calls" && r.Resource != "tokens") || r.At.IsZero() {
			violation("budget_rejection_invalid", fmt.Sprintf("rejections[%d]", n), "invalid rejected-request identity or reason")
		}
	}
	if s.HaltReason != "" {
		i.add(Observation, "budget_halted", ledgerPath, "halt_reason", "ledger records a shared admission fault")
		if i.record != nil && i.record.Status == campaign.Completed {
			violation("terminal_budget_fault", "halt_reason", "completed campaign has a persisted shared budget fault", "campaign.json")
		}
	}
}

func (i *inspection) checkLimits(field string, t budget.Totals, l budget.Limits) {
	total, ok := addInt(t.KnownTokens, t.ReservedTokens)
	if ok {
		total, ok = addInt(total, t.UnknownTokens)
	}
	if !ok || t.Calls < 0 || t.Calls > l.MaxCalls || total > l.MaxTokens || !limitsValid(l) {
		i.add(Violation, "budget_limit_violation", ledgerPath, field, "stored occupancy is negative, overflowing or exceeds limits")
	}
}
