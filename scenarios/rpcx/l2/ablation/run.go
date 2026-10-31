package ablation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/runner"
)

type Services struct {
	Backend  execution.Executor
	NewModel func(Slot) (budget.ProfiledModel, error)
}

type Execution struct {
	ProtocolSHA256 string          `json:"protocol_sha256"`
	StartedAt      time.Time       `json:"started_at"`
	FinishedAt     *time.Time      `json:"finished_at,omitempty"`
	Slots          []SlotExecution `json:"slots"`
	Error          string          `json:"error,omitempty"`
}

type SlotExecution struct {
	Slot          Slot   `json:"slot"`
	State         string `json:"state"`
	ReceiptSHA256 string `json:"receipt_sha256,omitempty"`
	Error         string `json:"error,omitempty"`
}

// Run executes the predeclared schedule once, serially. It does not authorize
// spending: the host must validate the frozen launch and human reviews first.
// A new output directory and persistent shared ledger prevent accidental reruns.
func Run(parent context.Context, output string, p Protocol, services Services) (out Execution, err error) {
	if err = p.Validate(); err != nil {
		return out, err
	}
	if services.Backend == nil || services.NewModel == nil || !reflect.DeepEqual(services.Backend.Info(), p.Execution) {
		return out, errors.New("experiment services differ from frozen execution policy")
	}
	if err = parent.Err(); err != nil {
		return out, err
	}
	output, err = gate.Canonical(output)
	if err != nil {
		return out, err
	}
	for _, t := range p.Tasks {
		if err = runner.ValidateTarget(t.Input, output); err != nil {
			return out, err
		}
		paths := []string{t.Input.Repository.Directory, t.Input.Specification.Path}
		for _, dependency := range t.Input.Dependencies {
			paths = append(paths, dependency.Directory)
		}
		for _, path := range paths {
			canonical, e := gate.Canonical(path)
			if e != nil {
				return out, e
			}
			if canonical != path {
				return out, errors.New("freeze canonical task paths before execution")
			}
		}
	}
	if err = os.Mkdir(output, 0700); err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(parent, p.HostTimeout)
	defer cancel()
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return out, err
	}
	data = append(data, '\n')
	if err = publishJSON(filepath.Join(output, "protocol.json"), p); err != nil {
		return out, err
	}
	out = Execution{ProtocolSHA256: gate.Digest(data), StartedAt: time.Now().UTC()}
	for _, s := range p.Schedule {
		out.Slots = append(out.Slots, SlotExecution{Slot: s, State: "not_started"})
	}
	publish := func() error { return publishJSON(filepath.Join(output, "execution.json"), out) }
	if err = publish(); err != nil {
		return out, err
	}
	registry := execution.ResourceRegistry{SchemaVersion: 1, CampaignID: p.ID, PlanSHA256: out.ProtocolSHA256, Policy: p.Resources}
	if err = execution.CreateResourceRegistry(output, registry); err != nil {
		return out, err
	}
	backend := execution.NewResourceGuard(services.Backend)
	defer func() {
		now := time.Now().UTC()
		out.FinishedAt = &now
		if err != nil {
			out.Error = err.Error()
		}
		err = errors.Join(err, publish())
	}()
	accounts := map[string]budget.Limits{}
	for _, s := range p.Schedule {
		accounts[s.ID] = p.AttemptBudget
	}
	ledger, err := budget.Create(budget.Config{Directory: filepath.Join(output, "budget"), CampaignID: p.ID, Profile: p.Model, Limits: p.TotalBudget, Targets: accounts})
	if err != nil {
		return out, err
	}
	for i, s := range p.Schedule {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		// This transition is saved before model construction or dispatch. A crash
		// leaves an identifiable unfinished attempt, not a reusable empty slot.
		entry := &out.Slots[i]
		entry.State = "starting"
		if err = publish(); err != nil {
			return out, err
		}
		model, e := services.NewModel(s)
		if e == nil && model == nil {
			e = errors.New("model factory returned no model")
		}
		profileChanged := false
		if e == nil {
			profile, profileErr := model.BudgetProfile()
			e = profileErr
			if e == nil && !reflect.DeepEqual(profile, p.Model) {
				e = errors.New("model profile differs from frozen protocol")
				profileChanged = true
			}
		}
		if e != nil {
			entry.State = "failed_before_run"
			entry.Error = e.Error()
			if err = publish(); err != nil {
				return out, err
			}
			if profileChanged {
				return out, e
			}
			continue
		}
		t := taskFor(p, s.TaskID)
		input := t.Input
		input.ID = s.ID
		diagnostic := t.DiagnosticLimit
		if s.Group == A {
			diagnostic = 0
		}
		attempt, runErr := runner.RunExperiment(ctx, runner.ExperimentConfig{
			Group: runner.ExperimentGroup(s.Group), PreparationTimeout: p.PreparationTimeout, GenerationTimeout: p.GenerationTimeout, ValidationTimeout: p.ValidationTimeout,
			Config: runner.Config{Target: &input, RunsDir: filepath.Dir(AttemptDirectory(output, s.ID)), RunID: s.ID, Model: model, ModelName: p.Model.Model,
				Budget: &budget.Target{Ledger: ledger, ID: s.ID}, Limits: p.Limits, CompletionPolicy: agent.FinalResponseV1, DiagnosticLimit: diagnostic, Backend: backend,
				ResourceOwner: &execution.ResourceOwner{Root: output, Registry: registry, TargetID: s.ID},
				Timeout:       p.PreparationTimeout + p.GenerationTimeout + p.ValidationTimeout + time.Minute},
		})
		entry.State = "finished"
		if attempt.Record.Directory == "" {
			entry.State = "failed_before_run"
		}
		if runErr != nil {
			entry.Error = runErr.Error()
		}
		if attempt.Record.Directory != "" {
			raw, readErr := os.ReadFile(filepath.Join(attempt.Record.Directory, "experiment-attempt.json"))
			if readErr != nil {
				entry.State = "starting"
				entry.Error = errors.Join(runErr, readErr).Error()
				return out, errors.Join(runErr, readErr)
			}
			entry.ReceiptSHA256 = gate.Digest(raw)
		}
		if err = publish(); err != nil {
			return out, err
		}
		if ledger.Err() != nil {
			return out, fmt.Errorf("shared experiment ledger halted: %w", ledger.Err())
		}
		// Local execution/model failures retain their row and do not delete later
		// slots. Shared accounting/input/resource integrity failures stop dispatch.
		if errors.Is(runErr, budget.ErrHalted) || errors.Is(runErr, budget.ErrIntegrity) || errors.Is(runErr, budget.ErrPersistence) || errors.Is(runErr, runner.ErrInputChanged) || errors.Is(runErr, execution.ErrResourcesUnknown) {
			return out, fmt.Errorf("shared experiment integrity failure: %w", runErr)
		}
	}
	return out, nil
}

func AttemptDirectory(root, slotID string) string {
	return filepath.Join(root, "targets", slotID, "runs", slotID)
}

func taskFor(p Protocol, id string) Task {
	for _, t := range p.Tasks {
		if t.ID == id {
			return t
		}
	}
	return Task{}
}
