package ablation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/trace"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
	"github.com/secacy/agent-harness/scenarios/rpcx/runner"
)

// Collect authenticates experiment artifacts before deriving measurements. A
// missing independent reference/review is unknown, not an inferred gate label.
func Collect(ctx context.Context, root string) (Summary, error) {
	var p Protocol
	var execution Execution
	protocolRaw, err := readLocal(root, "protocol.json")
	if err != nil {
		return Summary{}, err
	}
	if err = Decode(protocolRaw, &p); err != nil {
		return Summary{}, err
	}
	if err = p.Validate(); err != nil {
		return Summary{}, err
	}
	raw, err := readLocal(root, "execution.json")
	if err != nil {
		return Summary{}, err
	}
	if err = Decode(raw, &execution); err != nil {
		return Summary{}, err
	}
	if execution.ProtocolSHA256 != gate.Digest(protocolRaw) || len(execution.Slots) != len(p.Schedule) {
		return Summary{}, errors.New("execution is not bound to complete frozen schedule")
	}
	var references *ReferenceExecution
	if b, e := readLocal(root, "references-execution.json"); e == nil {
		var phase ReferenceExecution
		if e = Decode(b, &phase); e != nil {
			return Summary{}, e
		}
		if phase.SchemaVersion != 1 || phase.ProtocolSHA256 != gate.Digest(protocolRaw) || phase.ExecutionSHA256 != gate.Digest(raw) || len(phase.Outcomes) != len(p.Schedule) {
			return Summary{}, errors.New("independent reference phase differs from frozen generation")
		}
		for i, o := range phase.Outcomes {
			if o.Slot != p.Schedule[i] {
				return Summary{}, errors.New("reference phase lost a scheduled slot")
			}
		}
		references = &phase
	} else if !errors.Is(e, os.ErrNotExist) {
		return Summary{}, e
	}
	var ledger budget.Snapshot
	raw, err = readLocal(root, "budget/budget.json")
	if err != nil {
		return Summary{}, err
	}
	if err = Decode(raw, &ledger); err != nil {
		return Summary{}, err
	}
	if err = budget.ValidateSnapshot(ledger); err != nil {
		return Summary{}, err
	}
	if ledger.CampaignID != p.ID || !reflect.DeepEqual(ledger.Profile, p.Model) || ledger.Limits != p.TotalBudget || len(ledger.Targets) != len(p.Schedule) {
		return Summary{}, errors.New("ledger differs from experiment protocol")
	}
	var results []Result
	var gaps []string
	for i, entry := range execution.Slots {
		if entry.Slot != p.Schedule[i] {
			return Summary{}, errors.New("execution schedule changed")
		}
		account, ok := ledger.Targets[entry.Slot.ID]
		if !ok || account.Limits != p.AttemptBudget {
			return Summary{}, errors.New("attempt allowance differs from frozen protocol")
		}
		r := Result{Slot: entry.Slot, State: "not_started", Truth: evaluation.Inconclusive, Delivery: evaluation.Inconclusive}
		if entry.State == "starting" {
			b, e := readLocal(AttemptDirectory(root, entry.Slot.ID), "experiment-attempt.json")
			if e == nil {
				// This is read-only evidence recovery, not another generation. The
				// terminal trace and ledger still have to pass all checks below.
				var saved runner.ExperimentAttempt
				if e = Decode(b, &saved); e != nil {
					gaps = append(gaps, entry.Slot.ID+":unreadable_inflight_receipt")
				} else if saved.Record.FinishedAt != nil && saved.Record.Status != runner.StatusRunning {
					if _, e = runner.ReplayExperiment(ctx, AttemptDirectory(root, entry.Slot.ID), filepath.Join(root, "budget/budget.json")); e == nil {
						entry.State = "finished"
						entry.ReceiptSHA256 = gate.Digest(b)
						gaps = append(gaps, entry.Slot.ID+":execution_index_incomplete")
					} else {
						gaps = append(gaps, entry.Slot.ID+":uncertified_terminal_receipt:"+e.Error())
					}
				} else {
					gaps = append(gaps, entry.Slot.ID+":unfinished_attempt")
				}
			} else if !errors.Is(e, os.ErrNotExist) {
				return Summary{}, e
			}
		}
		switch entry.State {
		case "not_started":
			if entry.ReceiptSHA256 != "" || account.RunID != "" || account.Totals.Calls != 0 {
				return Summary{}, errors.New("unstarted slot has execution or ledger activity")
			}
		case "starting", "failed_before_run":
			r.State = "Failed"
			r.Failure = entry.State
			if entry.ReceiptSHA256 != "" {
				return Summary{}, errors.New("unfinished slot claims terminal receipt")
			}
			// A process may have died after dispatch. Keep its ledger usage even
			// without a certifiable Agent transcript; its candidate stays unknown.
			if entry.State == "failed_before_run" && (account.RunID != "" || account.Totals.Calls != 0) {
				return Summary{}, errors.New("factory failure has model activity")
			}
			assignAccount(&r, account)
		case "finished":
			if !digestOK(entry.ReceiptSHA256) {
				return Summary{}, errors.New("finished slot missing receipt identity")
			}
			directory := AttemptDirectory(root, entry.Slot.ID)
			raw, err = readLocal(directory, "experiment-attempt.json")
			if err != nil {
				return Summary{}, err
			}
			if gate.Digest(raw) != entry.ReceiptSHA256 {
				return Summary{}, errors.New("experiment receipt changed")
			}
			var a runner.ExperimentAttempt
			if err = Decode(raw, &a); err != nil {
				return Summary{}, err
			}
			t := taskFor(p, entry.Slot.TaskID)
			wantInput := t.Input
			wantInput.ID = entry.Slot.ID
			diagnostic := t.DiagnosticLimit
			if entry.Slot.Group == A {
				diagnostic = 0
			}
			if a.Group != runner.ExperimentGroup(entry.Slot.Group) || a.Record.ID != entry.Slot.ID || a.Record.TargetID != entry.Slot.ID || a.Input.Target == nil || !reflect.DeepEqual(*a.Input.Target, wantInput) || a.Input.ModelName != p.Model.Model || a.Input.Backend != p.Execution || a.Input.MaxModelCalls != p.Limits.MaxModelCalls || a.Input.MaxToolCalls != p.Limits.MaxToolCalls || a.Input.DiagnosticLimit != diagnostic || a.Timeouts.Preparation != p.PreparationTimeout || a.Timeouts.Generation != p.GenerationTimeout || a.Timeouts.Validation != p.ValidationTimeout {
				return Summary{}, errors.New("attempt differs from frozen task, group or resource policy")
			}
			if a.Input.Timeout != (p.PreparationTimeout + p.GenerationTimeout + p.ValidationTimeout + time.Minute).String() {
				return Summary{}, errors.New("attempt overall timeout differs from frozen policy")
			}
			verified, e := runner.ReplayExperiment(ctx, directory, filepath.Join(root, "budget/budget.json"))
			if e != nil {
				return Summary{}, fmt.Errorf("%s: %w", entry.Slot.ID, e)
			}
			r.RunID = a.Record.ID
			r.State = string(a.Record.Status)
			r.Submitted = a.Submitted
			r.Released = a.Released
			r.Verdict = a.Record.Verdict
			r.CandidateSHA256 = a.Record.CandidateSHA256
			r.Failure = verified.FailureCategory
			if r.State == "Completed" {
				r.Failure = ""
			}
			assignAccount(&r, account)
			mc, tc := int64(verified.ModelCalls), int64(verified.ToolCalls)
			r.Metrics.ModelCalls = &mc
			r.Metrics.ToolCalls = &tc
			log, e := trace.Read(filepath.Join(directory, "trace.jsonl"))
			if e != nil {
				return Summary{}, e
			}
			steps := int64(0)
			for _, event := range log.Events {
				if event.Kind == "step" && event.Phase == "begin" {
					steps++
				}
			}
			r.Metrics.Steps = &steps
			duration := a.Record.FinishedAt.Sub(a.Record.StartedAt).Seconds()
			r.Metrics.DurationSeconds = &duration
			if r.CandidateSHA256 != "" {
				referenceRoot := filepath.Join(root, "references", entry.Slot.ID)
				assess := false
				if references != nil {
					o := references.Outcomes[i]
					if o.ReferenceSHA256 != "" {
						b, e := readLocal(referenceRoot, "reference.json")
						if e != nil || gate.Digest(b) != o.ReferenceSHA256 {
							return Summary{}, errors.Join(e, errors.New("registered reference changed"))
						}
					}
					switch o.State {
					case "finished":
						if !digestOK(o.ReferenceSHA256) {
							return Summary{}, errors.New("finished reference has no material identity")
						}
						assess = true
					case "not_started", "running", "failed", "no_fixed_candidate":
						gaps = append(gaps, entry.Slot.ID+":reference_"+o.State)
					default:
						return Summary{}, errors.New("unknown reference phase state")
					}
				}
				if _, e = os.Lstat(filepath.Join(referenceRoot, "reference.json")); e == nil {
					if references == nil {
						return Summary{}, errors.New("unregistered independent reference")
					}
					if assess {
						_, referenceRaw, e := AuthenticateReference(referenceRoot, t, r.CandidateSHA256, entry.Slot.ID, r.RunID)
						if e != nil {
							return Summary{}, e
						}
						reviewRaw, e := readLocal(referenceRoot, "review.json")
						if errors.Is(e, os.ErrNotExist) {
							reviewRaw, e = readLocal(referenceRoot, "review-template.json")
						}
						if e != nil {
							return Summary{}, e
						}
						assessment, e := AssessTruth(referenceRaw, reviewRaw, p.Kind == "formal")
						if e != nil {
							return Summary{}, e
						}
						r.Truth, r.Delivery = assessment.Candidate, assessment.Delivery
					}
				} else if !errors.Is(e, os.ErrNotExist) {
					return Summary{}, e
				}
			}
		default:
			return Summary{}, errors.New("unknown execution slot state")
		}
		if entry.State != "not_started" {
			amount, e := Amount(p.Price, ledger.Calls, entry.Slot.ID)
			if e != nil {
				return Summary{}, e
			}
			r.Metrics.Amount = amount
		}
		results = append(results, r)
	}
	summary, err := Summarize(p, results)
	summary.ProtocolSHA256 = gate.Digest(protocolRaw)
	summary.Gaps = gaps
	summary.ExecutionComplete = execution.FinishedAt != nil
	for _, e := range execution.Slots {
		if e.State == "starting" || e.State == "not_started" {
			summary.ExecutionComplete = false
		}
	}
	return summary, err
}

func assignAccount(r *Result, a budget.Account) {
	known, reserved, unknown := a.Totals.KnownTokens, a.Totals.ReservedTokens, a.Totals.UnknownTokens
	r.Metrics.KnownTokens, r.Metrics.ReservedTokens, r.Metrics.UnknownTokens = &known, &reserved, &unknown
}

// Amount uses mutually exclusive cached/uncached input and output usage once.
// Missing cache details or unresolved calls make the amount unknown.
func Amount(price *Price, calls []budget.Call, targetID string) (*float64, error) {
	if price == nil {
		return nil, nil
	}
	if err := price.Validate(); err != nil {
		return nil, err
	}
	amount := 0.0
	for _, c := range calls {
		if c.TargetID != targetID {
			continue
		}
		if c.State != budget.StateSettled || c.Usage == nil {
			return nil, nil
		}
		u := c.Usage
		if u.InputTokens < 0 || u.OutputTokens < 0 {
			return nil, errors.New("invalid settled usage")
		}
		cached := int64(0)
		if price.CachedInputPerMillion != price.InputPerMillion && u.InputTokens > 0 {
			if u.CacheHitTokens == nil || u.CacheMissTokens == nil {
				return nil, nil
			}
			if *u.CacheHitTokens < 0 || *u.CacheMissTokens < 0 || *u.CacheHitTokens > u.InputTokens || *u.CacheMissTokens != u.InputTokens-*u.CacheHitTokens {
				return nil, errors.New("invalid disjoint cache accounting")
			}
			cached = *u.CacheHitTokens
		}
		amount += (float64(u.InputTokens-cached)*price.InputPerMillion + float64(cached)*price.CachedInputPerMillion + float64(u.OutputTokens)*price.OutputPerMillion) / 1e6
	}
	return &amount, nil
}

func readLocal(root, relative string) ([]byte, error) {
	if !filepath.IsLocal(relative) {
		return nil, errors.New("non-local experiment artifact path")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(root, relative)
	// The caller's root may use an OS alias such as macOS /var. Within that
	// canonical root, evidence may not redirect into another tree via symlinks.
	for current := path; ; current = filepath.Dir(current) {
		info, e := os.Lstat(current)
		if e != nil {
			return nil, e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("symlink experiment artifact")
		}
		if current == root {
			break
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 32<<20 {
		return nil, errors.New("invalid or oversized experiment artifact")
	}
	return os.ReadFile(path)
}
