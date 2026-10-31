package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/internal/trace"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
	"github.com/secacy/agent-harness/scenarios/rpcx/ghcli"
	"github.com/secacy/agent-harness/scenarios/rpcx/goddd"
	"github.com/secacy/agent-harness/scenarios/rpcx/translation"
	"github.com/secacy/agent-harness/scenarios/rpcx/workouts"
)

// ExperimentGroup selects an experimental policy, never a production verdict.
type ExperimentGroup string

const (
	ExperimentA ExperimentGroup = "A"
	ExperimentB ExperimentGroup = "B"
	ExperimentC ExperimentGroup = "C"
)

// ExperimentConfig uses the same input, tools and budget binding as Run. Each
// phase gets its own deadline from the parent, so gate work cannot consume the
// model's generation allowance. Config.Timeout also bounds the whole attempt;
// callers should allocate enough time for all phases and finalization.
type ExperimentConfig struct {
	Config             Config
	Group              ExperimentGroup
	PreparationTimeout time.Duration
	GenerationTimeout  time.Duration
	ValidationTimeout  time.Duration
}

type ExperimentDurations struct {
	Preparation  time.Duration `json:"preparation_ns"`
	Generation   time.Duration `json:"generation_ns"`
	Validation   time.Duration `json:"validation_ns"`
	Finalization time.Duration `json:"finalization_ns"`
}

type ExperimentTimeouts struct {
	Preparation time.Duration `json:"preparation_ns"`
	Generation  time.Duration `json:"generation_ns"`
	Validation  time.Duration `json:"validation_ns"`
}

// ExperimentAttempt is an experimental receipt. Completed means its configured
// policy ran and artifacts were delivered; Released is separate from quality.
// A readable partial candidate is retained even when Submitted is false.
type ExperimentAttempt struct {
	SchemaVersion int                   `json:"schema_version"`
	Group         ExperimentGroup       `json:"group"`
	Record        Record                `json:"record"`
	Input         Input                 `json:"input"`
	Timeouts      ExperimentTimeouts    `json:"timeouts"`
	Submitted     bool                  `json:"submitted"`
	Released      bool                  `json:"released"`
	Baseline      *BaselineReport       `json:"baseline_check,omitempty"`
	Agent         *agent.Result         `json:"agent,omitempty"`
	Validation    *checks.Evaluation    `json:"validation,omitempty"`
	Diagnostics   []DiagnosticReference `json:"generation_diagnostics,omitempty"`
	CandidatePath string                `json:"candidate_path,omitempty"`
	PatchPath     string                `json:"patch_path,omitempty"`
	Durations     ExperimentDurations   `json:"durations"`
}

func validateExperiment(c ExperimentConfig) (ExperimentConfig, error) {
	if c.Group != ExperimentA && c.Group != ExperimentB && c.Group != ExperimentC {
		return c, fmt.Errorf("%w: unsupported experiment group", ErrInvalidConfig)
	}
	if c.PreparationTimeout <= 0 || c.GenerationTimeout <= 0 || c.ValidationTimeout <= 0 {
		return c, fmt.Errorf("%w: positive phase timeouts required", ErrInvalidConfig)
	}
	if c.Config.CompletionPolicy == "" {
		c.Config.CompletionPolicy = agent.FinalResponseV1
	}
	if c.Config.CompletionPolicy != agent.FinalResponseV1 {
		return c, fmt.Errorf("%w: experiments require the common final-response policy", ErrInvalidConfig)
	}
	if c.Group == ExperimentA && c.Config.DiagnosticLimit != 0 {
		return c, fmt.Errorf("%w: plain group cannot receive migration diagnostics", ErrInvalidConfig)
	}
	if c.Config.Budget == nil || c.Config.Budget.Ledger == nil {
		return c, fmt.Errorf("%w: experiments require a persistent hard budget", ErrInvalidConfig)
	}
	var err error
	c.Config, err = validateConfig(c.Config)
	return c, err
}

type experimentStageResult struct {
	Stage      string             `json:"stage"`
	Input      Input              `json:"input"`
	Baseline   *BaselineReport    `json:"baseline_check,omitempty"`
	Submitted  bool               `json:"submitted"`
	Validation *checks.Evaluation `json:"validation,omitempty"`
}

// RunExperiment creates an exclusive attempt directory and never writes a
// production run.json. B and C must be invoked independently by the protocol
// driver; this API never shares a generated candidate between groups.
func RunExperiment(parent context.Context, c ExperimentConfig) (out ExperimentAttempt, err error) {
	if err = parent.Err(); err != nil {
		return out, err
	}
	c, err = validateExperiment(c)
	if err != nil {
		return out, err
	}
	parent, stop := context.WithTimeout(parent, c.Config.Timeout)
	defer stop()
	p, err := lookupProfile(c.Config.Target.ProfileID)
	if err != nil {
		return out, err
	}
	if err = os.MkdirAll(c.Config.RunsDir, 0755); err != nil {
		return out, err
	}
	var dir string
	if c.Config.RunID == "" {
		dir, err = os.MkdirTemp(c.Config.RunsDir, "rpcx-experiment-")
	} else {
		if !runIdentifier.MatchString(c.Config.RunID) {
			return out, fmt.Errorf("%w: invalid experiment ID", ErrInvalidConfig)
		}
		dir = filepath.Join(c.Config.RunsDir, c.Config.RunID)
		err = os.Mkdir(dir, 0700)
	}
	if err != nil {
		return out, err
	}
	now := time.Now().UTC()
	r := &runState{config: c.Config, profile: p,
		record: Record{SchemaVersion: 2, ID: filepath.Base(dir), TargetID: c.Config.Target.ID, Directory: dir, Status: StatusRunning, Phase: PhasePreparing, StartedAt: now, UpdatedAt: now},
		input: Input{CompletionPolicy: c.Config.CompletionPolicy, DiagnosticLimit: c.Config.DiagnosticLimit, Target: c.Config.Target, ProfileSHA256: p.hash(), RepositoryName: p.RepositoryName,
			ScenarioDir: c.Config.ScenarioDir, ModelName: c.Config.ModelName, Backend: c.Config.Backend.Info(), ExecutionMode: c.Config.Backend.Info().Kind,
			MaxModelCalls: c.Config.Limits.MaxModelCalls, MaxToolCalls: c.Config.Limits.MaxToolCalls, Timeout: c.Config.Timeout.String(), SpecVersion: p.SpecVersion},
	}
	out = ExperimentAttempt{SchemaVersion: 1, Group: c.Group, Record: r.record, Input: r.input, Timeouts: ExperimentTimeouts{c.PreparationTimeout, c.GenerationTimeout, c.ValidationTimeout}}
	recorder, err := trace.New(r.path("trace.jsonl"), r.record.ID)
	if err != nil {
		return out, err
	}
	ctx := trace.Context(parent, recorder)
	ctx, root, err := trace.Start(ctx, "experiment_attempt", out)
	if err != nil {
		_ = recorder.Close()
		return out, err
	}
	defer func() {
		started := time.Now()
		err = errors.Join(err, parent.Err())
		err = finishExperiment(ctx, r, &out, err)
		if parent.Err() != nil {
			err = errors.Join(err, parent.Err())
			setExperimentTerminal(r, &out, err)
		}
		out.Durations.Finalization = time.Since(started)
		publish := func() error { return writeJSON(r.path("experiment-attempt.json"), out) }
		if e := publish(); e != nil {
			err = errors.Join(err, e)
			setExperimentTerminal(r, &out, err)
			err = errors.Join(err, publish())
		}
		e := errors.Join(root.End(out, err), recorder.Close())
		if e != nil {
			err = errors.Join(err, e)
			setExperimentTerminal(r, &out, err)
			err = errors.Join(err, publish())
		}
	}()
	if err = writeJSON(r.path("experiment-attempt.json"), out); err != nil {
		return out, err
	}
	if owner := c.Config.ResourceOwner; owner != nil {
		binding := *owner
		binding.RunID = r.record.ID
		rel, e := filepath.Rel(binding.Root, dir)
		if e != nil {
			return out, e
		}
		binding.RunDirectory = filepath.ToSlash(rel)
		r.config.Backend, err = execution.WithResourceOwner(r.config.Backend, binding)
		if err != nil {
			return out, err
		}
	}
	phase := func(name string, timeout time.Duration, duration *time.Duration, work func(context.Context) error) error {
		start := time.Now()
		defer func() { *duration = time.Since(start) }()
		phaseCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		phaseCtx, span, e := trace.Start(phaseCtx, "experiment_stage", map[string]string{"stage": name})
		if e != nil {
			return e
		}
		e = work(phaseCtx)
		e = errors.Join(e, phaseCtx.Err())
		return errors.Join(e, span.End(experimentStageResult{Stage: name, Input: r.input, Baseline: r.baseline, Submitted: out.Submitted, Validation: r.validation}, e))
	}
	err = phase("preparation", c.PreparationTimeout, &out.Durations.Preparation, func(ctx context.Context) error {
		if e := r.prepare(ctx); e != nil {
			return e
		}
		r.record.Phase = PhaseCheckingBaseline
		return r.checkBaseline(ctx)
	})
	if err != nil {
		return out, err
	}
	r.record.Phase = PhaseGenerating
	err = phase("generation", c.GenerationTimeout, &out.Durations.Generation, func(ctx context.Context) error {
		e := r.generateWithMessages(ctx, c.Group == ExperimentA)
		if e == nil {
			out.Submitted = true
		}
		return e
	})
	if err != nil {
		return out, err
	}
	// Freeze the actual submitted bytes before any independent evaluation.
	if err = freezeExperimentCandidate(ctx, r, &out); err != nil {
		return out, err
	}
	if c.Group == ExperimentC {
		r.record.Phase = PhaseValidating
		err = phase("validation", c.ValidationTimeout, &out.Durations.Validation, func(ctx context.Context) error {
			if e := r.checkInputs(ctx); e != nil {
				return e
			}
			return evaluateExperiment(ctx, r)
		})
		if err != nil {
			return out, err
		}
	}
	r.record.Phase = PhasePublishing
	return out, nil
}

func freezeExperimentCandidate(ctx context.Context, r *runState, out *ExperimentAttempt) error {
	candidate, e := readTree(ctx, r.path("workspace/"+r.profile.RepositoryName))
	if e != nil {
		return e
	}
	if r.record.CandidateSHA256 != "" && r.record.CandidateSHA256 != candidate.hash() {
		return errors.New("candidate changed after experimental submission")
	}
	r.record.CandidateSHA256 = candidate.hash()
	if out.CandidatePath == "" {
		out.CandidatePath = "candidate/" + r.profile.RepositoryName
		if e = candidate.write(ctx, r.path(out.CandidatePath)); e != nil {
			out.CandidatePath = ""
			return e
		}
	} else {
		saved, e := readTree(ctx, r.path(out.CandidatePath))
		if e != nil || saved.hash() != candidate.hash() {
			return errors.Join(e, errors.New("fixed experiment candidate changed"))
		}
	}
	return nil
}

func finishExperiment(parent context.Context, r *runState, out *ExperimentAttempt, runErr error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
	defer cancel()
	runErr = errors.Join(runErr, os.MkdirAll(r.path("artifacts"), 0700))
	if r.agent != nil {
		runErr = errors.Join(runErr, writeJSON(r.path("artifacts/agent.json"), r.agent))
	}
	if r.prepared {
		runErr = errors.Join(runErr, r.checkInputs(ctx))
		freezeErr := freezeExperimentCandidate(ctx, r, out)
		runErr = errors.Join(runErr, freezeErr)
		if freezeErr == nil {
			candidate, e := readTree(ctx, r.path(out.CandidatePath))
			if e == nil {
				var patch []byte
				patch, e = exportPatch(ctx, r.record.Directory, r.fixtures.module(r.profile.RepositoryName), candidate)
				if e == nil {
					e = writeAtomic(r.path("artifacts/candidate.patch"), patch)
				}
				if e == nil {
					r.record.PatchSHA256 = digest(patch)
					out.PatchPath = "artifacts/candidate.patch"
				}
			}
			runErr = errors.Join(runErr, e)
		}
	}
	out.Input, out.Baseline, out.Agent, out.Validation, out.Diagnostics = r.input, r.baseline, r.agent, r.validation, r.diagnostics
	setExperimentTerminal(r, out, runErr)
	return runErr
}

func setExperimentTerminal(r *runState, out *ExperimentAttempt, err error) {
	now := time.Now().UTC()
	r.record.UpdatedAt, r.record.FinishedAt = now, &now
	r.record.Status, r.record.Error, r.record.Verdict = StatusCompleted, "", ""
	if err != nil {
		r.record.Status, r.record.Error = StatusFailed, err.Error()
		if errors.Is(err, context.Canceled) {
			r.record.Status = StatusCancelled
		}
		if errors.Is(err, ErrInputChanged) {
			r.record.FailureCode = "input_invalid"
		}
		if errors.Is(err, ErrBaseline) {
			r.record.FailureCode = "baseline_failed"
			if r.baseline != nil && r.baseline.Status == "error" {
				r.record.FailureCode = "baseline_error"
			}
		}
	} else if r.validation != nil {
		r.record.Verdict = r.validation.Verdict
	}
	out.Record = r.record
	out.Released = err == nil && out.Submitted && (out.Group != ExperimentC || r.record.Verdict == quality.ReadyForReview)
}

func evaluateExperiment(ctx context.Context, r *runState) error {
	evaluate := checks.Evaluate
	switch r.profile.Verifier {
	case goddd.VerifierVersion:
		evaluate = goddd.Evaluate
	case translation.VerifierVersion:
		evaluate = translation.Evaluate
	case translation.ScopeVerifierVersion:
		evaluate = translation.EvaluateScope
	case workouts.VerifierVersion:
		evaluate = workouts.Evaluate
	case ghcli.VerifierVersion:
		evaluate = ghcli.Evaluate
	}
	gateCtx, span, e := trace.Start(ctx, "gate_check", map[string]string{"verifier": r.profile.Verifier, "candidate_sha256": r.record.CandidateSHA256})
	if e != nil {
		return e
	}
	v, e := evaluate(gateCtx, checks.EvaluationRequest{BaselineDir: r.path("baseline/" + r.profile.RepositoryName), CandidateDir: r.path("workspace/" + r.profile.RepositoryName), SpecPath: r.path("inputs/spec/spec.md"), EvidenceDir: r.path("evidence"), CacheDir: r.path("cache/validation"), Backend: r.config.Backend})
	r.validation = &v
	e = errors.Join(e, span.End(v, e))
	if e != nil {
		return e
	}
	if v.VerifierVersion != r.profile.Verifier || v.SpecSHA256 != r.input.SpecSHA256 || v.BaselineSHA256 != r.input.BaselineSHA256 || v.CandidateSHA256 != r.record.CandidateSHA256 {
		return errors.New("experiment validation digests do not match prepared inputs")
	}
	return nil
}
