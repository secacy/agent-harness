package round

import (
	"fmt"
	"math"
	"path/filepath"
	"reflect"

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
)

func sum(a, b int64) (int64, error) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, fmt.Errorf("%w: invalid or overflowing cost", ErrInvalid)
	}
	return a + b, nil
}

func add(a, b budget.Totals) (budget.Totals, error) {
	var err error
	if a.Calls, err = sum(a.Calls, b.Calls); err != nil {
		return a, err
	}
	if a.KnownTokens, err = sum(a.KnownTokens, b.KnownTokens); err != nil {
		return a, err
	}
	if a.ReservedTokens, err = sum(a.ReservedTokens, b.ReservedTokens); err != nil {
		return a, err
	}
	a.UnknownTokens, err = sum(a.UnknownTokens, b.UnknownTokens)
	return a, err
}

func attachBudget(reader *reader, out *Report, root string, plan campaign.Plan, published budget.Snapshot) error {
	var ledger budget.Snapshot
	exists, err := reader.json(filepath.Join(root, "budget/budget.json"), "", &ledger)
	if err != nil {
		return err
	}
	if !exists {
		out.Issues = append(out.Issues, Issue{Code: "missing_budget", Detail: "cost is unknown; no zero consumption assumed"})
		return nil
	}
	if !reflect.DeepEqual(ledger, published) || ledger.SchemaVersion != 1 || ledger.CampaignID != plan.ID || ledger.Profile != plan.Profile || ledger.Limits != plan.Budget || len(ledger.Targets) != len(out.Targets) {
		return fmt.Errorf("%w: budget snapshot identity mismatch", ErrInvalid)
	}
	total := budget.Totals{}
	results := map[string]*Result{}
	for i := range out.Targets {
		r := &out.Targets[i]
		results[r.TargetID] = r
		account, ok := ledger.Targets[r.TargetID]
		if !ok || account.Limits != plan.Targets[i].Budget || (account.RunID != "" && account.RunID != r.RunID) {
			return fmt.Errorf("%w: target budget identity mismatch", ErrInvalid)
		}
		if account.Totals.Calls > 0 && account.RunID == "" {
			return fmt.Errorf("%w: budget calls without run identity", ErrInvalid)
		}
		total, err = add(total, account.Totals)
		if err != nil {
			return err
		}
		if err = withinLimits(account.Totals, account.Limits); err != nil {
			return err
		}
		copy := account.Totals
		r.Cost = &copy
		zeroCalls, zeroRejections := 0, 0
		r.AdmittedCalls = &zeroCalls
		r.Rejections = &zeroRejections
	}
	if total != ledger.Totals {
		return fmt.Errorf("%w: target costs do not sum to campaign totals", ErrInvalid)
	}
	if err = withinLimits(ledger.Totals, ledger.Limits); err != nil {
		return err
	}
	cost := Cost{Profile: ledger.Profile, Limits: ledger.Limits, Totals: ledger.Totals, Rejections: len(ledger.Rejections)}
	seen := map[string]bool{}
	var known int64
	for _, call := range ledger.Calls {
		r := results[call.TargetID]
		if r == nil || call.RunID != r.RunID || call.ID == "" || seen[call.ID] {
			return fmt.Errorf("%w: duplicate or cross-run budget call", ErrInvalid)
		}
		seen[call.ID] = true
		*r.AdmittedCalls++
		if call.State == budget.StateSettled {
			u := call.Usage
			if u == nil {
				return fmt.Errorf("%w: settled call lacks usage", ErrInvalid)
			}
			tokens, e := sum(u.InputTokens, u.OutputTokens)
			if e != nil || tokens != u.TotalTokens {
				return fmt.Errorf("%w: inconsistent known usage", ErrInvalid)
			}
			cost.KnownInputTokens, err = sum(cost.KnownInputTokens, u.InputTokens)
			if err != nil {
				return err
			}
			cost.KnownOutputTokens, err = sum(cost.KnownOutputTokens, u.OutputTokens)
			if err != nil {
				return err
			}
			known, err = sum(known, tokens)
			if err != nil {
				return err
			}
		}
	}
	if int64(len(ledger.Calls)) != ledger.Totals.Calls || known != ledger.Totals.KnownTokens {
		return fmt.Errorf("%w: call counts or known usage mismatch", ErrInvalid)
	}
	for _, rejection := range ledger.Rejections {
		r := results[rejection.TargetID]
		if r == nil || rejection.RunID != r.RunID {
			return fmt.Errorf("%w: cross-run budget rejection", ErrInvalid)
		}
		*r.Rejections++
	}
	for _, r := range results {
		if int64(*r.AdmittedCalls) != r.Cost.Calls {
			return fmt.Errorf("%w: target admission count mismatch", ErrInvalid)
		}
	}
	out.Cost = &cost
	return nil
}

func withinLimits(t budget.Totals, l budget.Limits) error {
	used, err := sum(t.KnownTokens, t.ReservedTokens)
	if err != nil {
		return err
	}
	used, err = sum(used, t.UnknownTokens)
	if err != nil {
		return err
	}
	if t.Calls < 0 || t.Calls > l.MaxCalls || used > l.MaxTokens {
		return fmt.Errorf("%w: recorded cost exceeds declared budget", ErrInvalid)
	}
	return nil
}
