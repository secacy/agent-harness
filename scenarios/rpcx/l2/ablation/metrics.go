package ablation

import (
	"errors"
	"math"

	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
)

func ratio(n, d int) Ratio {
	r := Ratio{Numerator: n, Denominator: d}
	if d > 0 {
		v := float64(n) / float64(d)
		r.Value = &v
	}
	return r
}

// Summarize requires observations to match the frozen schedule exactly. It
// synthesizes missing rows as unknown, never as successful or zero-cost runs.
func Summarize(p Protocol, observations []Result) (Summary, error) {
	out := Summary{ProtocolID: p.ID, Price: p.Price, Kind: p.Kind, AllSlotsObserved: true, Groups: map[Group]Aggregate{}, Tasks: map[string]map[Group]Aggregate{}}
	if err := p.Validate(); err != nil {
		return out, err
	}
	slots := map[string]Slot{}
	for _, s := range p.Schedule {
		slots[s.ID] = s
	}
	seen := map[string]Result{}
	runs := map[string]bool{}
	for _, r := range observations {
		s, ok := slots[r.Slot.ID]
		if !ok || s != r.Slot {
			return out, errors.New("result does not match scheduled slot")
		}
		if _, ok := seen[s.ID]; ok {
			return out, errors.New("duplicate result")
		}
		if r.RunID != "" && runs[r.RunID] {
			return out, errors.New("shared run cannot count as independent generation")
		}
		if err := validateResult(r); err != nil {
			return out, err
		}
		if r.RunID != "" {
			runs[r.RunID] = true
		}
		seen[s.ID] = r
	}
	for _, s := range p.Schedule {
		r, ok := seen[s.ID]
		if !ok {
			out.AllSlotsObserved = false
			r = Result{Slot: s, State: "not_started", Truth: evaluation.Inconclusive, Delivery: evaluation.Inconclusive}
		}
		if r.State == "not_started" {
			out.AllSlotsObserved = false
		}
		out.Results = append(out.Results, r)
	}
	for _, g := range []Group{A, B, C} {
		var rows []Result
		for _, r := range out.Results {
			if r.Slot.Group == g {
				rows = append(rows, r)
			}
		}
		out.Groups[g] = aggregate(rows)
	}
	for _, t := range p.Tasks {
		out.Tasks[t.ID] = map[Group]Aggregate{}
		for _, g := range []Group{A, B, C} {
			var rows []Result
			for _, r := range out.Results {
				if r.Slot.TaskID == t.ID && r.Slot.Group == g {
					rows = append(rows, r)
				}
			}
			out.Tasks[t.ID][g] = aggregate(rows)
		}
	}
	return out, nil
}

func validateResult(r Result) error {
	valid := func(s evaluation.Status) bool {
		return s == evaluation.Pass || s == evaluation.Fail || s == evaluation.Inconclusive
	}
	if !valid(r.Truth) || !valid(r.Delivery) {
		return errors.New("truth and delivery must explicitly include unknown outcomes")
	}
	if r.State != "not_started" && r.State != "Completed" && r.State != "Failed" && r.State != "Cancelled" {
		return errors.New("invalid execution state")
	}
	if r.State == "not_started" && (r.RunID != "" || r.Submitted || r.Released || r.Truth != evaluation.Inconclusive || r.Delivery != evaluation.Inconclusive) {
		return errors.New("unstarted slot claims execution or truth")
	}
	if r.State == "Completed" && (!r.Submitted || r.RunID == "" || !digestOK(r.CandidateSHA256)) {
		return errors.New("completed attempt lacks submitted candidate")
	}
	if r.Truth != evaluation.Inconclusive && (r.RunID == "" || !digestOK(r.CandidateSHA256)) {
		return errors.New("truth lacks candidate identity")
	}
	wantRelease := r.State == "Completed" && r.Submitted && (r.Slot.Group != C || r.Verdict == "ReadyForReview")
	if r.Released != wantRelease {
		return errors.New("release decision contradicts group policy")
	}
	if r.Slot.Group != C && r.Verdict != "" {
		return errors.New("ungated group cannot claim a production verdict")
	}
	for _, v := range []*int64{r.Metrics.KnownTokens, r.Metrics.ReservedTokens, r.Metrics.UnknownTokens, r.Metrics.ModelCalls, r.Metrics.ToolCalls, r.Metrics.Steps} {
		if v != nil && *v < 0 {
			return errors.New("negative measurement")
		}
	}
	for _, v := range []*float64{r.Metrics.DurationSeconds, r.Metrics.Amount} {
		if v != nil && (*v < 0 || math.IsNaN(*v) || math.IsInf(*v, 0)) {
			return errors.New("invalid measurement")
		}
	}
	return nil
}

func aggregate(rows []Result) Aggregate {
	a := Aggregate{Counts: Counts{Scheduled: len(rows), BlockedReasons: map[string]int{}}, Measurements: map[string]Distribution{}}
	c := &a.Counts
	values := map[string][]float64{}
	names := []string{"known_tokens", "reserved_tokens", "unknown_tokens", "model_calls", "tool_calls", "steps", "duration_seconds", "amount"}
	for _, r := range rows {
		if r.State == "not_started" {
			c.NotStarted++
		}
		if r.Submitted {
			c.Submitted++
		}
		if r.Released {
			c.Released++
		}
		switch r.Truth {
		case evaluation.Pass:
			c.TruthPass++
			if r.Released {
				c.Success++
				if r.Delivery == evaluation.Pass {
					c.FullDeliverySuccess++
				}
			} else {
				c.FalseRejection++
				reason := r.Failure
				if reason == "" {
					reason = r.Verdict
				}
				if reason == "" {
					reason = "execution_failure"
				}
				c.BlockedReasons[reason]++
			}
		case evaluation.Fail:
			c.TruthFail++
			if r.Released {
				c.WrongRelease++
			}
		default:
			c.TruthUnknown++
			if r.Released {
				c.ReleasedUnknown++
			}
		}
		ints := map[string]*int64{"known_tokens": r.Metrics.KnownTokens, "reserved_tokens": r.Metrics.ReservedTokens, "unknown_tokens": r.Metrics.UnknownTokens, "model_calls": r.Metrics.ModelCalls, "tool_calls": r.Metrics.ToolCalls, "steps": r.Metrics.Steps}
		for name, v := range ints {
			if v != nil {
				values[name] = append(values[name], float64(*v))
			}
		}
		for name, v := range map[string]*float64{"duration_seconds": r.Metrics.DurationSeconds, "amount": r.Metrics.Amount} {
			if v != nil {
				values[name] = append(values[name], *v)
			}
		}
	}
	a.AutomaticSuccess = ratio(c.Success, c.Scheduled)
	a.AutomaticSuccessUpper = ratio(c.Success+c.ReleasedUnknown, c.Scheduled)
	a.WrongRelease = ratio(c.WrongRelease, c.Released)
	a.WrongReleaseUpper = ratio(c.WrongRelease+c.ReleasedUnknown, c.Released)
	a.FalseRejection = ratio(c.FalseRejection, c.TruthPass)
	for _, name := range names {
		vs := values[name]
		d := Distribution{Observed: len(vs), Missing: len(rows) - len(vs)}
		if len(vs) > 0 {
			min, max, sum := vs[0], vs[0], 0.0
			for _, v := range vs {
				min = math.Min(min, v)
				max = math.Max(max, v)
				sum += v
			}
			mean := sum / float64(len(vs))
			d.Min, d.Max, d.Mean = &min, &max, &mean
		}
		a.Measurements[name] = d
	}
	return a
}
