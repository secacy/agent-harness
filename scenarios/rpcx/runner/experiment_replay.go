package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/internal/repotools"
	"github.com/secacy/agent-harness/internal/trace"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
)

// ReplayExperiment authenticates an experimental receipt and replays its Agent
// without model calls or candidate execution. It does not claim independent
// truth, which is supplied separately by the frozen evaluation protocol.
func ReplayExperiment(ctx context.Context, directory, ledgerPath string) (out ReplayResult, err error) {
	root, err := filepath.Abs(directory)
	if err != nil {
		return out, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return out, err
	}
	out.Directory, out.Mode, out.DerivedFields = root, "experiment-trace-v1", []string{}
	var a ExperimentAttempt
	if err = replayJSON(root, "experiment-attempt.json", &a); err != nil {
		return out, err
	}
	if a.SchemaVersion != 1 || a.Record.SchemaVersion != 2 || a.Record.ID == "" || a.Record.FinishedAt == nil || (a.Record.Status != StatusCompleted && a.Record.Status != StatusFailed && a.Record.Status != StatusCancelled) || a.Input.Target == nil || a.Input.Target.ID != a.Record.TargetID || a.Input.CompletionPolicy != agent.FinalResponseV1 {
		return out, errors.New("invalid or unfinished experimental receipt")
	}
	if a.Group != ExperimentA && a.Group != ExperimentB && a.Group != ExperimentC || a.Group == ExperimentA && a.Input.DiagnosticLimit != 0 {
		return out, errors.New("invalid experimental strategy")
	}
	if a.Timeouts.Preparation <= 0 || a.Timeouts.Generation <= 0 || a.Timeouts.Validation <= 0 || a.Durations.Preparation < 0 || a.Durations.Generation < 0 || a.Durations.Validation < 0 || a.Durations.Finalization < 0 {
		return out, errors.New("invalid experimental timing")
	}
	out.RunID, out.Status, out.Verdict = a.Record.ID, a.Record.Status, a.Record.Verdict
	if _, err = replayBytes(root, "trace.jsonl"); err != nil {
		return out, err
	}
	log, err := trace.Read(filepath.Join(root, "trace.jsonl"))
	if err != nil {
		return out, err
	}
	if log.TruncatedTail || len(log.Events) < 2 {
		return out, errors.New("unfinished experimental trace")
	}
	first, last := log.Events[0], log.Events[len(log.Events)-1]
	if first.Kind != "experiment_attempt" || first.Phase != "begin" || first.ParentSpanID != "" || last.Kind != "experiment_attempt" || last.Phase != "end" || last.SpanID != first.SpanID {
		return out, errors.New("experimental trace requires one terminal root")
	}
	var initial, terminal ExperimentAttempt
	if err = json.Unmarshal(first.Data, &initial); err != nil {
		return out, err
	}
	if err = json.Unmarshal(last.Data, &terminal); err != nil {
		return out, err
	}
	if !reflect.DeepEqual(a, terminal) || last.Error != a.Record.Error {
		return out, errors.New("experimental trace disagrees with receipt")
	}
	if (a.Record.Status == StatusCompleted) != (last.Error == "") || (a.Record.Status == StatusCancelled) != (last.ErrorCategory == trace.CategoryCanceled) {
		return out, errors.New("experimental error does not reproduce terminal status")
	}
	if initial.Group != a.Group || initial.Timeouts != a.Timeouts || initial.Record.ID != a.Record.ID || initial.Record.TargetID != a.Record.TargetID || initial.Record.StartedAt != a.Record.StartedAt {
		return out, errors.New("experimental identity changed")
	}
	original, final := initial.Input, a.Input
	final.SpecSHA256, final.BaselineSHA256, final.Files, final.Budget = "", "", nil, nil
	if !reflect.DeepEqual(original, final) {
		return out, errors.New("experimental configuration changed")
	}
	live, parents := map[string]bool{}, map[string]string{}
	agents, gates := 0, 0
	var gateEnd *trace.Event
	stages := map[string]experimentStageResult{}
	for i, e := range log.Events {
		if e.RunID != a.Record.ID {
			return out, errors.New("experimental trace identity mismatch")
		}
		if e.Phase == "begin" {
			live[e.SpanID] = true
			parents[e.SpanID] = e.ParentSpanID
		} else {
			delete(live, e.SpanID)
		}
		if e.Kind == "agent" && e.Phase == "begin" {
			agents++
		}
		if e.Kind == "gate_check" && e.Phase == "end" {
			gates++
			gateEnd = &log.Events[i]
		}
		if e.Kind == "experiment_stage" && e.Phase == "end" {
			var stage experimentStageResult
			if err = json.Unmarshal(e.Data, &stage); err != nil {
				return out, err
			}
			if stage.Stage != "preparation" && stage.Stage != "generation" && stage.Stage != "validation" {
				return out, errors.New("unknown experimental stage")
			}
			if _, exists := stages[stage.Stage]; exists {
				return out, errors.New("repeated experimental stage")
			}
			stages[stage.Stage] = stage
		}
		if e.Kind == "run" {
			return out, errors.New("experiment impersonates a production Run")
		}
	}
	if len(live) != 0 || agents > 1 || gates > 1 {
		return out, errors.New("ambiguous or unfinished experimental stages")
	}
	for id, parent := range parents {
		if id == first.SpanID {
			continue
		}
		seen := map[string]bool{}
		for parent != first.SpanID {
			if parent == "" || seen[parent] {
				return out, errors.New("experimental span lies outside its root")
			}
			seen[parent] = true
			parent = parents[parent]
		}
	}
	earlyFailure := a.Record.Status != StatusCompleted && agents == 0 && a.Input.SpecSHA256 == "" && a.Input.BaselineSHA256 == "" && len(a.Input.Files) == 0 && a.Input.Budget == nil && a.Baseline == nil
	preparation, exists := stages["preparation"]
	if !exists && !earlyFailure {
		return out, errors.New("experimental preparation stage missing")
	}
	if exists {
		inputWithoutBudget := a.Input
		inputWithoutBudget.Budget = nil
		preparation.Input.Budget = nil
		if !reflect.DeepEqual(preparation.Input, inputWithoutBudget) || !reflect.DeepEqual(preparation.Baseline, a.Baseline) || preparation.Submitted || preparation.Validation != nil {
			return out, errors.New("experimental preparation evidence differs")
		}
	}
	if a.Baseline != nil {
		var baseline BaselineReport
		if err = replayJSON(root, "preflight/baseline.json", &baseline); err != nil {
			return out, err
		}
		if !reflect.DeepEqual(baseline, *a.Baseline) {
			return out, errors.New("saved experimental baseline differs")
		}
	}
	if generation, exists := stages["generation"]; exists {
		if !reflect.DeepEqual(generation.Input, a.Input) || !reflect.DeepEqual(generation.Baseline, a.Baseline) || generation.Submitted != a.Submitted || generation.Validation != nil {
			return out, errors.New("experimental generation evidence differs")
		}
	} else if agents != 0 || a.Submitted {
		return out, errors.New("experimental generation stage missing")
	}
	if validation, exists := stages["validation"]; exists {
		if a.Group != ExperimentC || !reflect.DeepEqual(validation.Input, a.Input) || !reflect.DeepEqual(validation.Baseline, a.Baseline) || validation.Submitted != a.Submitted || !reflect.DeepEqual(validation.Validation, a.Validation) {
			return out, errors.New("experimental validation stage differs")
		}
	} else if gates != 0 {
		return out, errors.New("experimental validation stage missing")
	}
	var saved Input
	if err = replayJSON(root, "inputs/task.json", &saved); err != nil {
		if !earlyFailure || !errors.Is(err, os.ErrNotExist) {
			return out, err
		}
	} else if !reflect.DeepEqual(saved, a.Input) {
		return out, errors.New("saved experiment input differs")
	}
	if a.Input.SpecSHA256 != "" {
		if err = verifyReplayInputs(ctx, root, a.Input, a.Record, Report{Validation: a.Validation}); err != nil {
			return out, err
		}
	}
	if a.Record.CandidateSHA256 != "" {
		if a.CandidatePath != "candidate/"+a.Input.RepositoryName || a.PatchPath != "artifacts/candidate.patch" || a.Record.PatchSHA256 == "" {
			return out, errors.New("experimental candidate lacks bound snapshot or patch")
		}
		candidate, e := readTree(ctx, filepath.Join(root, filepath.FromSlash(a.CandidatePath)))
		if e != nil || candidate.hash() != a.Record.CandidateSHA256 {
			return out, errors.Join(e, errors.New("fixed experiment candidate changed"))
		}
	} else if a.CandidatePath != "" || a.PatchPath != "" || a.Submitted {
		return out, errors.New("experimental candidate identity missing")
	}
	var recording agent.Recording
	if agents == 1 {
		if a.Agent == nil {
			return out, errors.New("experimental Agent transcript missing")
		}
		var persisted agent.Result
		if err = replayJSON(root, "artifacts/agent.json", &persisted); err != nil {
			return out, err
		}
		recording, err = agent.RecordingFromTrace(log)
		if err != nil {
			return out, err
		}
		if !reflect.DeepEqual(recording.Output.Result, *a.Agent) || !reflect.DeepEqual(persisted, *a.Agent) {
			return out, errors.New("experimental Agent records disagree")
		}
		if recording.Input.ModelName != a.Input.ModelName || recording.Input.Limits != (agent.Limits{MaxModelCalls: a.Input.MaxModelCalls, MaxToolCalls: a.Input.MaxToolCalls}) || recording.Input.CompletionPolicy != a.Input.CompletionPolicy {
			return out, errors.New("experimental Loop configuration changed")
		}
		var p profile
		if err = replayJSON(root, "inputs/profile.json", &p); err != nil {
			return out, err
		}
		spec, e := replayBytes(root, "inputs/spec/spec.md")
		if e != nil {
			return out, e
		}
		roots := []string{"baseline", "spec"}
		for name := range p.Dependencies {
			roots = append(roots, name)
		}
		rootMap := map[string]string{}
		for _, name := range roots {
			rootMap[name] = ""
		}
		roots = sortedKeys(rootMap)
		r := runState{profile: p, input: a.Input, spec: spec}
		if !reflect.DeepEqual(recording.Input.Initial, r.generationMessages(roots, a.Group == ExperimentA)) {
			return out, errors.New("experimental prompt does not match group")
		}
		// The wire root enum is sorted by Executor.Specs.
		rootMap["workspace"] = ""
		specs := repotools.Specifications(sortedKeys(rootMap), true)
		if a.Input.DiagnosticLimit != 0 {
			specs = append(specs, diagnosticSpec())
		}
		if !reflect.DeepEqual(recording.Input.Tools, specs) {
			return out, errors.New("experimental tool policy changed")
		}
		result, e := agent.ReplayRecording(ctx, recording)
		if e != nil {
			return out, e
		}
		if result.Reason != a.Record.AgentExitReason || a.Submitted != (result.Reason == agent.ExitCompleted && recording.Output.Failure == nil) {
			return out, errors.New("experimental submission does not reproduce")
		}
		out.ModelCalls, out.ToolCalls = result.ModelCalls, result.ToolCalls
	} else if a.Agent != nil || a.Submitted {
		return out, errors.New("experimental Agent has no trace")
	}
	if a.Group != ExperimentC && (gates != 0 || a.Validation != nil || a.Record.Verdict != "") {
		return out, errors.New("ungated group acquired a gate decision")
	}
	if gateEnd != nil {
		var v checks.Evaluation
		if err = json.Unmarshal(gateEnd.Data, &v); err != nil {
			return out, err
		}
		if a.Validation == nil || !reflect.DeepEqual(v, *a.Validation) {
			return out, errors.New("experimental gate evidence differs")
		}
	} else if a.Validation != nil {
		return out, errors.New("experimental gate has no trace")
	}
	if a.Record.Status == StatusCompleted {
		if last.Error != "" || !a.Submitted || a.Baseline == nil || a.Baseline.Status != "pass" {
			return out, errors.New("completed experiment lacks successful submission and baseline")
		}
		if a.Group == ExperimentC {
			if gateEnd == nil || gateEnd.Error != "" {
				return out, errors.New("complete C experiment lacks a successful gate")
			}
			verdict, e := replayQuality(root, *a.Validation)
			if e != nil {
				return out, e
			}
			if verdict != a.Record.Verdict {
				return out, errors.New("experimental quality does not reproduce")
			}
			out.EvidenceFiles = len(a.Validation.Evidence)
		}
	} else if a.Record.Error == "" || a.Record.Verdict != "" {
		return out, errors.New("failed experiment has invalid terminal outcome")
	}
	released := a.Record.Status == StatusCompleted && a.Submitted && (a.Group != ExperimentC || a.Record.Verdict == quality.ReadyForReview)
	if released != a.Released {
		return out, errors.New("experimental release policy does not reproduce")
	}
	if a.Input.Budget != nil {
		if ledgerPath == "" {
			ledgerPath = findReplayLedger(root)
		}
		if ledgerPath == "" {
			return out, errors.New("experimental budget ledger missing")
		}
		if err = matchReplayLedger(root, ledgerPath, a.Input, a.Record, recording, log, &out); err != nil {
			return out, err
		}
		out.LedgerMatched = true
	} else if agents != 0 {
		return out, errors.New("experimental Agent lacks hard budget binding")
	} else if ledgerPath != "" {
		// Preparation may fail before budget binding. Such an attempt must not
		// hide any provider admission in the shared ledger.
		path, e := filepath.Abs(ledgerPath)
		if e != nil {
			return out, e
		}
		var ledger budget.Snapshot
		if e = replayJSON(filepath.Dir(path), filepath.Base(path), &ledger); e != nil {
			return out, e
		}
		if e = budget.ValidateSnapshot(ledger); e != nil {
			return out, e
		}
		account, exists := ledger.Targets[a.Record.TargetID]
		if !exists || ledger.Profile.Model != a.Input.ModelName || account.Totals.Calls != 0 || (account.RunID != "" && account.RunID != a.Record.ID) {
			return out, errors.New("unbound experiment has unexpected budget activity")
		}
		for _, call := range ledger.Calls {
			if call.TargetID == a.Record.TargetID || call.RunID == a.Record.ID {
				return out, errors.New("unbound experiment has model calls")
			}
		}
		for _, rejection := range ledger.Rejections {
			if rejection.TargetID == a.Record.TargetID || rejection.RunID == a.Record.ID {
				return out, errors.New("unbound experiment has model admission attempts")
			}
		}
		out.LedgerMatched = true
	}
	out.FailureCategory = ReplayFailureCategory(a.Record, Report{Baseline: a.Baseline, Validation: a.Validation}, recording)
	if a.Group != ExperimentC && a.Record.Status == StatusCompleted {
		out.FailureCategory = "none"
	}
	return out, ctx.Err()
}
