package ablation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
	"github.com/secacy/agent-harness/scenarios/rpcx/runner"
)

// ReferenceOutcome retains one scheduled slot, including attempts that never
// yielded a fixed candidate. Reference execution is not a human truth review.
type ReferenceOutcome struct {
	Slot            Slot              `json:"slot"`
	State           string            `json:"state"`
	Error           string            `json:"error,omitempty"`
	ReferenceSHA256 string            `json:"reference_sha256,omitempty"`
	Automatic       evaluation.Status `json:"automatic"`
}

type ReferenceExecution struct {
	SchemaVersion   int                `json:"schema_version"`
	ProtocolSHA256  string             `json:"protocol_sha256"`
	ExecutionSHA256 string             `json:"execution_sha256"`
	StartedAt       time.Time          `json:"started_at"`
	FinishedAt      *time.Time         `json:"finished_at,omitempty"`
	Outcomes        []ReferenceOutcome `json:"outcomes"`
	Error           string             `json:"error,omitempty"`
}

// EvaluateAll authenticates generation evidence before evaluating every fixed
// candidate, including rejected, failed and unsubmitted attempts. It never
// retries or overwrites an existing reference phase. Local reference failures
// retain their row and allow other candidates to run; shared integrity/resource
// failures and parent cancellation stop admission, preserving not_started rows.
func EvaluateAll(ctx context.Context, root string, backend execution.Executor) (outcomes []ReferenceOutcome, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	root, err = gate.Canonical(root)
	if err != nil {
		return nil, err
	}
	references := filepath.Join(root, "references")
	receipt := filepath.Join(root, "references-execution.json")
	for _, path := range []string{references, receipt} {
		if _, e := os.Lstat(path); e == nil {
			return nil, errors.New("reference execution already exists; use a separately versioned experiment")
		} else if !errors.Is(e, os.ErrNotExist) {
			return nil, e
		}
	}
	var p Protocol
	protocolRaw, err := readLocal(root, "protocol.json")
	if err != nil {
		return nil, err
	}
	if err = Decode(protocolRaw, &p); err != nil {
		return nil, err
	}
	if err = p.Validate(); err != nil {
		return nil, err
	}
	if backend == nil || !reflect.DeepEqual(backend.Info(), p.Execution) {
		return nil, errors.New("reference backend differs from frozen experiment")
	}
	executionRaw, err := readLocal(root, "execution.json")
	if err != nil {
		return nil, err
	}
	var generation Execution
	if err = Decode(executionRaw, &generation); err != nil {
		return nil, err
	}
	// Collect binds the complete schedule, original receipts, terminal traces and
	// shared ledger. It handles a terminal receipt whose index remains starting.
	// This occurs before creating reference output, so corrupt generation cannot
	// dispatch a candidate command or consume a reference attempt.
	if _, err = Collect(ctx, root); err != nil {
		return nil, fmt.Errorf("authenticate generation before reference: %w", err)
	}
	ledgerRaw, err := readLocal(root, "budget/budget.json")
	if err != nil {
		return nil, err
	}
	registry := execution.ResourceRegistry{SchemaVersion: 1, CampaignID: p.ID, PlanSHA256: gate.Digest(protocolRaw), Policy: p.Resources}
	if err = referenceResourcesClear(root, registry); err != nil {
		return nil, err
	}
	inputs := map[string]string{"protocol.json": gate.Digest(protocolRaw), "execution.json": gate.Digest(executionRaw), "budget/budget.json": gate.Digest(ledgerRaw)}
	phase := ReferenceExecution{SchemaVersion: 1, ProtocolSHA256: gate.Digest(protocolRaw), ExecutionSHA256: gate.Digest(executionRaw), StartedAt: time.Now().UTC()}
	for _, s := range p.Schedule {
		phase.Outcomes = append(phase.Outcomes, ReferenceOutcome{Slot: s, State: "not_started", Automatic: evaluation.Inconclusive})
	}
	// Reserve the phase once. A crash before references is created still leaves
	// this exclusive receipt and prevents an unrecorded retry on the same root.
	file, err := os.OpenFile(receipt, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	if err = file.Close(); err != nil {
		return nil, err
	}
	defer func() {
		now := time.Now().UTC()
		phase.FinishedAt = &now
		if err != nil {
			phase.Error = err.Error()
		}
		err = errors.Join(err, publishReferenceExecution(root, phase))
		outcomes = phase.Outcomes
	}()
	if err = publishReferenceExecution(root, phase); err != nil {
		return nil, err
	}
	if err = os.Mkdir(references, 0700); err != nil {
		return nil, err
	}
	guard := execution.NewResourceGuard(backend)
	for i, entry := range generation.Slots {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		current := &phase.Outcomes[i]
		directory := AttemptDirectory(root, entry.Slot.ID)
		if entry.State != "finished" && entry.State != "starting" {
			current.State = "no_fixed_candidate"
			current.Error = "generation " + entry.State
			if err = publishReferenceExecution(root, phase); err != nil {
				return nil, err
			}
			continue
		}
		raw, e := readLocal(directory, "experiment-attempt.json")
		if errors.Is(e, os.ErrNotExist) && entry.State == "starting" {
			current.State = "no_fixed_candidate"
			current.Error = "evidence_incomplete: generation index and terminal receipt incomplete"
			if err = publishReferenceExecution(root, phase); err != nil {
				return nil, err
			}
			continue
		}
		if e != nil {
			return nil, e
		}
		var attempt runner.ExperimentAttempt
		if decodeErr := Decode(raw, &attempt); decodeErr != nil {
			if entry.State == "starting" {
				current.State = "no_fixed_candidate"
				current.Error = "evidence_incomplete: unreadable in-flight receipt: " + decodeErr.Error()
				if err = publishReferenceExecution(root, phase); err != nil {
					return nil, err
				}
				continue
			}
			return nil, decodeErr
		}
		// Runtime publishes an initial Running receipt before dispatch. Merely
		// finding that file does not certify a terminal attempt or its candidate.
		if entry.State == "starting" && (attempt.Record.FinishedAt == nil || attempt.Record.Status == runner.StatusRunning) {
			current.State = "no_fixed_candidate"
			current.Error = "evidence_incomplete: starting attempt has no terminal receipt"
			if err = publishReferenceExecution(root, phase); err != nil {
				return nil, err
			}
			continue
		}
		// Recheck immediately before candidate execution, including starting slots
		// discovered from their complete receipt. Submitted is deliberately unused.
		if _, replayErr := runner.ReplayExperiment(ctx, directory, filepath.Join(root, "budget/budget.json")); replayErr != nil {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			if entry.State == "starting" {
				current.State = "no_fixed_candidate"
				current.Error = "evidence_invalid: uncertified starting terminal receipt: " + replayErr.Error()
				if err = publishReferenceExecution(root, phase); err != nil {
					return nil, err
				}
				continue
			}
			return nil, fmt.Errorf("%s generation evidence: %w", entry.Slot.ID, replayErr)
		}
		if attempt.Record.CandidateSHA256 == "" {
			current.State = "no_fixed_candidate"
			if err = publishReferenceExecution(root, phase); err != nil {
				return nil, err
			}
			continue
		}
		if err = referenceInputsUnchanged(root, inputs); err != nil {
			return nil, err
		}
		if err = referenceResourcesClear(root, registry); err != nil {
			return nil, err
		}
		owner := execution.ResourceOwner{Root: root, Registry: registry, TargetID: entry.Slot.ID, RunID: attempt.Record.ID, RunDirectory: filepath.ToSlash(filepath.Join("targets", entry.Slot.ID, "runs", attempt.Record.ID))}
		owned, e := execution.WithResourceOwner(guard, owner)
		if e != nil {
			return nil, e
		}
		current.State = "running"
		if err = publishReferenceExecution(root, phase); err != nil {
			return nil, err
		}
		req := evaluation.Request{TargetID: entry.Slot.ID, RunID: attempt.Record.ID, FixturesDir: filepath.Join(directory, "baseline"), CandidateDir: filepath.Join(directory, filepath.FromSlash(attempt.CandidatePath)), CandidateSHA256: attempt.Record.CandidateSHA256, SpecPath: filepath.Join(directory, "inputs/spec/spec.md"), EvidenceDir: filepath.Join(references, entry.Slot.ID), Backend: owned}
		referenceCtx, cancel := context.WithTimeout(ctx, p.ReferenceTimeout)
		report, localErr := evaluateReference(referenceCtx, entry.Slot.TaskID, req)
		cancel()
		current.State = "finished"
		current.Automatic = report.Automatic
		if current.Automatic == "" {
			current.Automatic = evaluation.Inconclusive
		}
		if localErr != nil {
			current.State = "failed"
			current.Error = localErr.Error()
		}
		if saved, e := readLocal(req.EvidenceDir, "reference.json"); e == nil {
			current.ReferenceSHA256 = gate.Digest(saved)
		} else if !errors.Is(e, os.ErrNotExist) {
			localErr = errors.Join(localErr, e)
			current.State = "failed"
			current.Error = localErr.Error()
		}
		if err = publishReferenceExecution(root, phase); err != nil {
			return nil, err
		}
		if err = referenceInputsUnchanged(root, inputs); err != nil {
			return nil, err
		}
		if e = referenceResourcesClear(root, registry); e != nil {
			return nil, e
		}
		if errors.Is(localErr, execution.ErrResourcesUnknown) {
			return nil, localErr
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if !report.InputsUnchanged && !(errors.Is(localErr, context.DeadlineExceeded) && report.ProductionSHA256 == "") {
			return nil, fmt.Errorf("%s reference input integrity was not confirmed: %w", entry.Slot.ID, errors.Join(localErr, runner.ErrInputChanged))
		}
		if _, err = runner.ReplayExperiment(ctx, directory, filepath.Join(root, "budget/budget.json")); err != nil {
			return nil, fmt.Errorf("%s generation evidence changed during reference: %w", entry.Slot.ID, err)
		}
	}
	return phase.Outcomes, nil
}

func evaluateReference(ctx context.Context, taskID string, req evaluation.Request) (evaluation.Report, error) {
	switch taskID {
	case "goddd":
		return evaluation.Evaluate(ctx, req)
	case "translation":
		return evaluation.EvaluateTranslation(ctx, req)
	case "workouts":
		return evaluation.EvaluateWorkouts(ctx, req)
	case "ghcli":
		return evaluation.EvaluateGHCLI(ctx, req)
	default:
		return evaluation.Report{}, errors.New("unsupported reference task")
	}
}

func referenceInputsUnchanged(root string, inputs map[string]string) error {
	for name, hash := range inputs {
		data, err := readLocal(root, name)
		if err != nil {
			return err
		}
		if gate.Digest(data) != hash {
			return fmt.Errorf("shared experiment input changed: %s", name)
		}
	}
	return nil
}

func referenceResourcesClear(root string, registry execution.ResourceRegistry) error {
	entries, err := execution.ReadResources(root, registry)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Dispatch != nil && entry.Removed == nil {
			return fmt.Errorf("%w: command %s has no confirmed cleanup", execution.ErrResourcesUnknown, entry.Intent.CommandID)
		}
	}
	return nil
}

func publishReferenceExecution(root string, phase ReferenceExecution) error {
	return publishJSON(filepath.Join(root, "references-execution.json"), phase)
}
