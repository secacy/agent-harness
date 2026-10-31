package ablation

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

// WriteReport publishes a derived report in a new directory. Raw experiment
// receipts and reviews are never edited when results are recomputed.
func WriteReport(directory string, s Summary) error {
	if err := os.Mkdir(directory, 0700); err != nil {
		return err
	}
	var table bytes.Buffer
	w := csv.NewWriter(&table)
	if err := w.Write([]string{"slot", "task", "group", "repetition", "run_id", "state", "submitted", "released", "gate_verdict", "candidate_truth", "delivery_accuracy", "candidate_sha256", "failure", "known_tokens", "reserved_tokens", "unknown_tokens", "model_calls", "tool_calls", "steps", "duration_seconds", "amount"}); err != nil {
		return err
	}
	for _, r := range s.Results {
		m := r.Metrics
		if err := w.Write([]string{r.Slot.ID, r.Slot.TaskID, string(r.Slot.Group), strconv.Itoa(r.Slot.Repetition), r.RunID, r.State, strconv.FormatBool(r.Submitted), strconv.FormatBool(r.Released), r.Verdict, string(r.Truth), string(r.Delivery), r.CandidateSHA256, r.Failure, intText(m.KnownTokens), intText(m.ReservedTokens), intText(m.UnknownTokens), intText(m.ModelCalls), intText(m.ToolCalls), intText(m.Steps), floatText(m.DurationSeconds), floatText(m.Amount)}); err != nil {
			return err
		}
	}
	w.Flush()
	return errors.Join(w.Error(), gate.WriteJSON(filepath.Join(directory, "summary.json"), s), gate.Write(filepath.Join(directory, "results.csv"), table.Bytes()), gate.Write(filepath.Join(directory, "report.md"), markdown(s)))
}

func intText(v *int64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatInt(*v, 10)
}
func floatText(v *float64) string {
	if v == nil {
		return ""
	}
	return strconv.FormatFloat(*v, 'f', 6, 64)
}
func ratioText(r Ratio) string {
	if r.Value == nil {
		return "N/A (0 denominator)"
	}
	return fmt.Sprintf("%d/%d (%.1f%%)", r.Numerator, r.Denominator, *r.Value*100)
}
func distributionText(d Distribution) string {
	if d.Mean == nil {
		return fmt.Sprintf("unknown; observed=0 missing=%d", d.Missing)
	}
	return fmt.Sprintf("%.3f [%.3f, %.3f]; n=%d missing=%d", *d.Mean, *d.Min, *d.Max, d.Observed, d.Missing)
}
func cell(s string) string { return strings.NewReplacer("|", "\\|", "\n", " ", "\r", " ").Replace(s) }

func markdown(s Summary) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# P1 %s\n\nKind: %s. All scheduled attempts observed: %t. Generation execution terminal: %t.\n\n", cell(s.ProtocolID), cell(s.Kind), s.AllSlotsObserved, s.ExecutionComplete)
	if s.Kind == "development_calibration" {
		fmt.Fprintln(&b, "Controlled development calibration; these numbers do not measure model migration efficacy.")
	}
	fmt.Fprintln(&b, "\n| Group | Scheduled | Submitted | Released | Truth pass/fail/unknown | Automatic success | Wrong release | False rejection |\n| --- | ---: | ---: | ---: | --- | --- | --- | --- |")
	for _, g := range []Group{A, B, C} {
		a := s.Groups[g]
		c := a.Counts
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d/%d/%d | %s | %s | %s |\n", g, c.Scheduled, c.Submitted, c.Released, c.TruthPass, c.TruthFail, c.TruthUnknown, ratioText(a.AutomaticSuccess), ratioText(a.WrongRelease), ratioText(a.FalseRejection))
	}
	fmt.Fprintln(&b, "\nUnknown releases are not confirmed successes or errors. Their possible success/wrong-release bounds are in summary.json; these are uncertainty bounds, not confidence intervals. False rejection uses known truth-pass candidates; unknown correctness remains separately counted.")
	fmt.Fprintln(&b, "\n| Group | Measure | Mean [min, max]; observed/missing |\n| --- | --- | --- |")
	for _, g := range []Group{A, B, C} {
		for _, name := range []string{"known_tokens", "reserved_tokens", "unknown_tokens", "model_calls", "tool_calls", "steps", "duration_seconds", "amount"} {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", g, name, distributionText(s.Groups[g].Measurements[name]))
		}
	}
	fmt.Fprintln(&b, "\nAmounts use the protocol's frozen currency and prices; unknown amounts remain blank in CSV/null in JSON. Token settlements are not supplier invoices. Local model attempts include admission rejections; ledger calls and conservative occupancy remain in the raw evidence. Duration covers each generation attempt's preparation, generation, gate and publication; independent reference and human review are separate.")
	if s.Price != nil {
		fmt.Fprintf(&b, "\nPrice basis: %s per million tokens, input=%g cached input=%g output=%g; recorded %s; source %s.\n", cell(s.Price.Currency), s.Price.InputPerMillion, s.Price.CachedInputPerMillion, s.Price.OutputPerMillion, cell(s.Price.RecordedAt), cell(s.Price.Source))
	}
	fmt.Fprintln(&b, "\n| Slot | Execution | Submitted | Released | Gate | Candidate truth | Delivery accuracy | Failure |\n| --- | --- | --- | --- | --- | --- | --- | --- |")
	for _, r := range s.Results {
		fmt.Fprintf(&b, "| %s | %s | %t | %t | %s | %s | %s | %s |\n", cell(r.Slot.ID), cell(r.State), r.Submitted, r.Released, cell(r.Verdict), r.Truth, r.Delivery, cell(r.Failure))
	}
	if len(s.Gaps) > 0 {
		fmt.Fprintln(&b, "\nEvidence gaps:")
		for _, gap := range s.Gaps {
			fmt.Fprintf(&b, "\n- %s\n", cell(gap))
		}
	}
	fmt.Fprintln(&b, "\nThis comparison contains four previously seen repository slices with repeated stochastic generations. It does not establish unseen-repository generalization or a stable production error rate. B and C use independent candidates, so differences do not isolate gate effects on identical bytes. No group is required to win. Missing human review, reference evidence or scheduled runs prevents claiming P1 acceptance. See summary.json for per-task/group distributions and results.csv for every predeclared attempt.")
	return []byte(b.String())
}
