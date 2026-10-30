package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/internal/repotools"
	"github.com/secacy/agent-harness/internal/trace"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
)

// ReplayResult describes verified replay, never a new production Run. Legacy
// finish reasons are explicitly derived; absent historical timing stays absent.
type ReplayResult struct {
	RunID              string   `json:"run_id"`
	Directory          string   `json:"directory"`
	Mode               string   `json:"mode"`
	Status             Status   `json:"status"`
	Verdict            string   `json:"verdict"`
	ModelCalls         int      `json:"model_calls"` // Local Loop attempts; not proof of provider admission.
	AdmittedCalls      int      `json:"admitted_calls"`
	DeniedAttempts     int      `json:"denied_attempts"`
	UnadmittedAttempts int      `json:"unadmitted_attempts"`
	ReservedTokens     int64    `json:"reserved_tokens"`
	UnknownTokens      int64    `json:"unknown_tokens"`
	Cost               *float64 `json:"cost"` // No frozen price source: null, never zero.
	ToolCalls          int      `json:"tool_calls"`
	KnownTokens        int64    `json:"known_tokens"`
	UnknownCalls       int      `json:"unknown_calls"`
	LedgerMatched      bool     `json:"ledger_matched"`
	DerivedFields      []string `json:"derived_fields"`
	FailureCategory    string   `json:"failure_category"`
	EvidenceFiles      int      `json:"evidence_files"`
}

// Replay reruns the production Agent Loop against recorded external responses,
// authenticates its accounting and recomputes quality aggregation from archived
// evidence. It never executes repository tools, a model, or candidate code, and
// never follows saved absolute source or ledger paths back to the old machine.
func Replay(ctx context.Context, directory, ledgerPath string) (out ReplayResult, err error) {
	root, err := filepath.Abs(directory)
	if err != nil {
		return out, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return out, err
	}
	out.Directory = root
	out.DerivedFields = []string{}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	var record Record
	var report Report
	var input Input
	if err = replayJSON(root, "run.json", &record); err != nil {
		return out, err
	}
	out.RunID = record.ID
	if record.SchemaVersion != 2 || record.ID == "" || record.FinishedAt == nil || record.Status == StatusRunning {
		return out, errors.New("replay requires a supported terminal Run")
	}
	if err = replayJSON(root, "artifacts/report.json", &report); err != nil {
		return out, err
	}
	if err = replayJSON(root, "inputs/task.json", &input); err != nil {
		return out, err
	}
	if report.RunID != record.ID || report.Status != record.Status || report.Error != record.Error || report.FailureCode != record.FailureCode || !reflect.DeepEqual(report.Input, input) || report.CandidateSHA256 != record.CandidateSHA256 || report.PatchSHA256 != record.PatchSHA256 {
		return out, errors.New("run, report and saved input disagree")
	}
	var persisted agent.Result
	hasAgent := report.AgentRecord != ""
	if hasAgent {
		if report.AgentRecord != "agent.json" {
			return out, errors.New("unsupported agent record path")
		}
		if err = replayJSON(root, "artifacts/agent.json", &persisted); err != nil {
			return out, err
		}
	}
	var recording agent.Recording
	var log trace.Log
	var runEnd, gateEnd *trace.Event
	path := filepath.Join(root, "trace.jsonl")
	if _, statErr := os.Lstat(path); statErr == nil {
		out.Mode = "trace-v1"
		if _, err = replayBytes(root, "trace.jsonl"); err != nil {
			return out, err
		}
		log, err = trace.Read(path)
		if err != nil {
			return out, err
		}
		if log.TruncatedTail {
			return out, errors.New("cannot certify replay from a truncated trace")
		}
		runBegins, runEnds, gateEnds, agentBegins := 0, 0, 0, 0
		var runBegin, preparedBegin, baselineEnd *trace.Event
		rootSpan := ""
		parents := map[string]string{}
		live := map[string]bool{}
		hasBudgetSpans := false
		for i, e := range log.Events {
			if e.Phase == "begin" {
				parents[e.SpanID] = e.ParentSpanID
				live[e.SpanID] = true
			} else {
				delete(live, e.SpanID)
			}
			if e.Kind == "budget_reserve" || e.Kind == "budget_settle" {
				hasBudgetSpans = true
			}
			if e.Kind == "prepared" && e.Phase == "begin" {
				if preparedBegin != nil {
					return out, errors.New("multiple prepared snapshots")
				}
				preparedBegin = &log.Events[i]
			}
			if e.Kind == "baseline_check" && e.Phase == "end" {
				if baselineEnd != nil {
					return out, errors.New("multiple baseline results")
				}
				baselineEnd = &log.Events[i]
			}
			if e.RunID != record.ID {
				return out, errors.New("trace Run identity mismatch")
			}
			if e.Kind == "agent" && e.Phase == "begin" {
				agentBegins++
			}
			if e.Kind == "run" {
				if e.ParentSpanID != "" {
					return out, errors.New("Run span must be a root")
				}
				if e.Phase == "begin" {
					runBegins++
					runBegin = &log.Events[i]
					rootSpan = e.SpanID
				} else {
					runEnds++
					runEnd = &log.Events[i]
				}
			}
			if e.Kind == "gate_check" && e.Phase == "end" {
				gateEnds++
				gateEnd = &log.Events[i]
			}
		}
		if runBegins != 1 || runEnds != 1 || runEnd == nil || gateEnds > 1 || agentBegins > 1 {
			return out, errors.New("trace requires one complete Run and unambiguous stages")
		}
		if len(live) != 0 || runEnd.Sequence != int64(len(log.Events)) {
			return out, errors.New("Run trace has unfinished spans or records after its terminal event")
		}
		for id, parent := range parents {
			if id == rootSpan {
				continue
			}
			if parent == "" {
				return out, errors.New("trace has a span outside the Run")
			}
			seen := map[string]bool{}
			for parent != rootSpan {
				if parent == "" || seen[parent] {
					return out, errors.New("trace span does not descend from the Run")
				}
				seen[parent] = true
				var ok bool
				parent, ok = parents[parent]
				if !ok {
					return out, errors.New("trace span has an unknown ancestor")
				}
			}
		}
		if hasBudgetSpans && input.Budget == nil {
			return out, errors.New("budget trace has no published budget reference")
		}
		var began Input
		if err = json.Unmarshal(runBegin.Data, &began); err != nil {
			return out, err
		}
		finalConfig := input
		began.Budget = nil
		began.SpecSHA256 = ""
		began.BaselineSHA256 = ""
		began.Files = nil
		finalConfig.Budget = nil
		finalConfig.SpecSHA256 = ""
		finalConfig.BaselineSHA256 = ""
		finalConfig.Files = nil
		if !reflect.DeepEqual(began, finalConfig) {
			return out, errors.New("Run trace began with different configuration")
		}
		if preparedBegin != nil {
			var prepared Input
			if err = json.Unmarshal(preparedBegin.Data, &prepared); err != nil {
				return out, err
			}
			prepared.Budget = nil
			preparedExpected := input
			preparedExpected.Budget = nil
			if !reflect.DeepEqual(prepared, preparedExpected) {
				return out, errors.New("prepared input differs from published input")
			}
		} else if record.Status == StatusCompleted || hasAgent {
			return out, errors.New("execution lacks an authenticated prepared snapshot")
		}
		if baselineEnd != nil {
			var baseline *BaselineReport
			if err = json.Unmarshal(baselineEnd.Data, &baseline); err != nil {
				return out, err
			}
			if !reflect.DeepEqual(baseline, report.Baseline) {
				return out, errors.New("baseline trace differs from published report")
			}
		} else if report.Baseline != nil {
			return out, errors.New("baseline report has no trace execution")
		}
		var terminal Record
		if err = json.Unmarshal(runEnd.Data, &terminal); err != nil {
			return out, err
		}
		if !reflect.DeepEqual(terminal, record) || runEnd.Error != record.Error {
			return out, errors.New("trace terminal record disagrees with published Run")
		}
		if agentBegins == 1 {
			if !hasAgent {
				return out, errors.New("trace has an Agent but no persisted result")
			}
			recording, err = agent.RecordingFromTrace(log)
			if err != nil {
				return out, err
			}
			if !reflect.DeepEqual(recording.Output.Result, persisted) {
				return out, errors.New("trace and persisted Agent result differ")
			}
		} else if hasAgent {
			return out, errors.New("Agent result has no trace execution")
		}
		if gateEnd != nil {
			var validation checks.Evaluation
			if err = json.Unmarshal(gateEnd.Data, &validation); err != nil {
				return out, err
			}
			if record.Status != StatusCompleted {
				validation.Verdict = ""
			}
			if report.Validation == nil || !reflect.DeepEqual(validation, *report.Validation) {
				return out, errors.New("gate trace differs from published validation")
			}
		} else if report.Validation != nil {
			return out, errors.New("published validation has no gate trace")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return out, statErr
	} else {
		out.Mode = "legacy-completed"
		if record.Status != StatusCompleted || !hasAgent {
			return out, errors.New("legacy replay requires a completed Run and complete Agent history")
		}
		tools, toolErr := legacyReplayTools(persisted, input)
		if toolErr != nil {
			return out, toolErr
		}
		recording, err = agent.LegacyRecording(persisted, tools, agent.Limits{MaxModelCalls: input.MaxModelCalls, MaxToolCalls: input.MaxToolCalls}, input.CompletionPolicy)
		if err != nil {
			return out, err
		}
		recording.Input.ModelName = input.ModelName
		for i := range recording.Operations {
			if recording.Operations[i].Model != nil {
				recording.Operations[i].Model.Input.ModelName = input.ModelName
			}
		}
		out.DerivedFields = []string{"reply.finish: derived from authenticated completed Loop protocol", "tool timing: unavailable"}
	}
	if hasAgent {
		if recording.Input.ModelName != input.ModelName || recording.Input.Limits != (agent.Limits{MaxModelCalls: input.MaxModelCalls, MaxToolCalls: input.MaxToolCalls}) || recording.Input.CompletionPolicy != input.CompletionPolicy {
			return out, errors.New("recorded Loop configuration differs from Run input")
		}
		result, replayErr := agent.ReplayRecording(ctx, recording)
		if replayErr != nil {
			return out, replayErr
		}
		if result.Reason != record.AgentExitReason {
			return out, errors.New("replayed Agent exit differs from Run")
		}
		out.ModelCalls, out.ToolCalls = result.ModelCalls, result.ToolCalls
		for _, u := range result.Usage {
			if u == nil || !validReplayUsage(u) {
				out.UnknownCalls++
			} else {
				out.KnownTokens += u.TotalTokens
			}
		}
	}
	if err = verifyReplayInputs(ctx, root, input, record, report); err != nil {
		return out, err
	}
	expectedStatus := StatusCompleted
	if out.Mode == "trace-v1" && runEnd.ErrorCategory != "" {
		expectedStatus = StatusFailed
		if runEnd.ErrorCategory == trace.CategoryCanceled {
			expectedStatus = StatusCancelled
		}
	}
	if expectedStatus != record.Status {
		return out, errors.New("execution outcome does not reproduce Run status")
	}
	if expectedStatus == StatusCompleted {
		if !hasAgent || persisted.Reason != agent.ExitCompleted || report.Baseline == nil || report.Baseline.Status != "pass" || report.Validation == nil || (gateEnd != nil && gateEnd.Error != "") {
			return out, errors.New("completed Run lacks successful generation, baseline or gate")
		}
		verdict, e := replayQuality(root, *report.Validation)
		if e != nil {
			return out, e
		}
		if verdict != record.Verdict || verdict != report.Validation.Verdict {
			return out, errors.New("recomputed quality differs from original verdict")
		}
		out.Verdict = verdict
		out.EvidenceFiles = len(report.Validation.Evidence)
	} else if record.Verdict != "" || (report.Validation != nil && report.Validation.Verdict != "") {
		return out, errors.New("failed execution must not publish a verdict")
	}
	out.Status = expectedStatus
	if input.Budget != nil {
		if ledgerPath == "" {
			ledgerPath = findReplayLedger(root)
		}
		if ledgerPath == "" {
			return out, errors.New("Run references a budget but no local ledger was provided or found")
		}
		if err = matchReplayLedger(root, ledgerPath, input, record, recording, log, &out); err != nil {
			return out, err
		}
		out.LedgerMatched = true
	} else if ledgerPath != "" {
		return out, errors.New("explicit ledger cannot authenticate a Run with no budget reference")
	}
	out.FailureCategory = ReplayFailureCategory(record, report, recording)
	return out, ctx.Err()
}

func legacyReplayTools(result agent.Result, input Input) ([]agent.ToolSpec, error) {
	if len(result.Messages) == 0 {
		return nil, errors.New("legacy conversation is missing")
	}
	const prefix = "Available roots: workspace (writable), "
	const suffix = " (read-only)."
	_, rest, ok := strings.Cut(result.Messages[0].Content, prefix)
	if !ok {
		return nil, errors.New("legacy tool roots were not recorded")
	}
	names, _, ok := strings.Cut(rest, suffix)
	if !ok {
		return nil, errors.New("legacy tool roots are incomplete")
	}
	roots := []string{"workspace"}
	seen := map[string]bool{"workspace": true}
	for _, name := range strings.Split(names, ", ") {
		if name == "" || seen[name] || strings.ContainsAny(name, "/\\\n\r") {
			return nil, errors.New("invalid legacy root")
		}
		seen[name] = true
		roots = append(roots, name)
	}
	sort.Strings(roots)
	tools := repotools.Specifications(roots, true)
	if input.DiagnosticLimit != 0 {
		tools = append(tools, diagnosticSpec())
	}
	return tools, nil
}

func replayBytes(root, relative string) ([]byte, error) {
	if !filepath.IsLocal(relative) {
		return nil, errors.New("replay path must stay inside its source")
	}
	path := root
	for _, part := range strings.Split(filepath.Clean(relative), string(filepath.Separator)) {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("replay material traverses a symlink")
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 128*1024*1024 {
		return nil, errors.New("replay material is not a bounded regular file")
	}
	return io.ReadAll(io.LimitReader(f, 128*1024*1024+1))
}
func replayJSON(root, relative string, value any) error {
	data, err := replayBytes(root, relative)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(value); err != nil {
		return fmt.Errorf("read %s: %w", relative, err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("read %s: trailing JSON", relative)
	}
	return nil
}
func verifyReplayInputs(ctx context.Context, root string, input Input, record Record, report Report) error {
	if input.RepositoryName == "" || !filepath.IsLocal(input.RepositoryName) || filepath.Base(input.RepositoryName) != input.RepositoryName {
		return errors.New("invalid repository identity")
	}
	profileData, err := replayBytes(root, "inputs/profile.json")
	if err != nil {
		return err
	}
	if digest(profileData) != input.ProfileSHA256 {
		// Profiles are saved indented but their identity is the canonical struct.
		var p profile
		if err = json.Unmarshal(profileData, &p); err != nil {
			return err
		}
		if p.hash() != input.ProfileSHA256 {
			return errors.New("profile identity changed")
		}
	}
	spec, err := replayBytes(root, "inputs/spec/spec.md")
	if err != nil {
		return err
	}
	if digest(spec) != input.SpecSHA256 {
		return errors.New("specification identity changed")
	}
	baseline, err := readTree(ctx, filepath.Join(root, "baseline"))
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(baseline.manifest(), input.Files) || baseline.module(input.RepositoryName).hash() != input.BaselineSHA256 {
		return errors.New("baseline identity changed")
	}
	candidate, err := readTree(ctx, filepath.Join(root, "workspace", input.RepositoryName))
	if err != nil {
		return err
	}
	if record.CandidateSHA256 != "" && candidate.hash() != record.CandidateSHA256 {
		return errors.New("candidate identity changed")
	}
	if record.PatchSHA256 != "" {
		patch, e := replayBytes(root, "artifacts/candidate.patch")
		if e != nil {
			return e
		}
		if digest(patch) != record.PatchSHA256 {
			return errors.New("candidate patch changed")
		}
	}
	if report.Validation != nil {
		v := report.Validation
		if v.SpecSHA256 != "" && (v.SpecSHA256 != input.SpecSHA256 || v.BaselineSHA256 != input.BaselineSHA256 || v.CandidateSHA256 != record.CandidateSHA256) {
			return errors.New("validation is bound to different inputs")
		}
	}
	return nil
}
func replayQuality(root string, v checks.Evaluation) (string, error) {
	var policies []quality.Evidence
	for _, e := range v.Evidence {
		if e.Kind == "host_policy" {
			policies = append(policies, e)
		}
	}
	if len(policies) != 1 {
		return "", errors.New("quality policy must have one bound host_policy evidence entry")
	}
	var policy quality.Policy
	if err := replayJSON(filepath.Join(root, "evidence"), policies[0].Path, &policy); err != nil {
		return "", err
	}
	decision, err := quality.Decide(filepath.Join(root, "evidence"), policy, quality.Binding{SpecSHA256: v.SpecSHA256, BaselineSHA256: v.BaselineSHA256, CandidateSHA256: v.CandidateSHA256}, v.Checks, v.Evidence)
	if err != nil {
		return "", err
	}
	if !reflect.DeepEqual(decision.Gaps, v.Gaps) {
		return "", errors.New("recomputed quality gaps differ from recorded evidence")
	}
	return decision.Verdict, nil
}
func findReplayLedger(root string) string {
	for dir := root; ; dir = filepath.Dir(dir) {
		path := filepath.Join(dir, "budget", "budget.json")
		if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
			return path
		}
		if parent := filepath.Dir(dir); parent == dir {
			break
		}
	}
	return ""
}
func replayRequestDigest(input agent.ModelInput) string {
	data, _ := json.Marshal(struct {
		Messages []agent.Message
		Tools    []agent.ToolSpec
	}{Messages: input.Messages, Tools: input.Tools})
	return digest(data)
}
func matchReplayLedger(root, path string, input Input, record Record, recording agent.Recording, log trace.Log, out *ReplayResult) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	var ledger budget.Snapshot
	if err = replayJSON(filepath.Dir(abs), filepath.Base(abs), &ledger); err != nil {
		return err
	}
	if err = budget.ValidateSnapshot(ledger); err != nil {
		return fmt.Errorf("invalid replay ledger: %w", err)
	}
	reference := input.Budget
	profileData, _ := json.Marshal(ledger.Profile)
	if reference.CampaignID != ledger.CampaignID || reference.RunID != record.ID || reference.TargetID != record.TargetID || reference.ProfileSHA256 != digest(profileData) || ledger.Profile.Model != input.ModelName {
		return errors.New("budget reference identity differs from ledger")
	}
	var calls []budget.Call
	byID := map[string]budget.Call{}
	for _, call := range ledger.Calls {
		if call.RunID == record.ID {
			if call.TargetID != record.TargetID {
				return errors.New("ledger call belongs to another Target")
			}
			calls = append(calls, call)
			byID[call.ID] = call
		}
	}
	used := map[string]bool{}
	index := 0
	deniedCount := 0
	for _, op := range recording.Operations {
		if op.Model == nil {
			continue
		}
		model := op.Model
		request := replayRequestDigest(model.Input)
		denied := false
		if model.Output.Failure != nil {
			for _, kind := range model.Output.Failure.Kinds {
				if kind == "budget" {
					denied = true
				}
			}
		}
		if denied {
			found := false
			for _, r := range ledger.Rejections {
				if r.RunID == record.ID && r.TargetID == record.TargetID && r.InputSHA256 == request {
					found = true
				}
			}
			if !found {
				return errors.New("budget-denied model attempt lacks ledger rejection")
			}
			deniedCount++
			continue
		}
		if index >= len(calls) {
			return errors.New("model response has no admitted ledger call")
		}
		call := calls[index]
		index++
		if call.InputSHA256 != request || !reflect.DeepEqual(call.Usage, model.Output.Reply.Usage) || used[call.ID] {
			return errors.New("model request or usage differs from unique ledger call")
		}
		if call.State == budget.StateReserved {
			return errors.New("terminal model response still has unsettled reservation")
		}
		if record.Status == StatusCompleted && (call.Outcome != "reply" || call.State == budget.StateInvalid) {
			return errors.New("completed Run has a failed or invalid model settlement")
		}

		used[call.ID] = true
	}
	if index != len(calls) {
		return errors.New("ledger has calls absent from replay")
	}
	rejections := 0
	for _, r := range ledger.Rejections {
		if r.RunID == record.ID {
			if r.TargetID != record.TargetID {
				return errors.New("ledger rejection belongs to another Target")
			}
			rejections++
		}
	}
	if rejections != deniedCount {
		return errors.New("ledger rejections differ from replay attempts")
	}

	if len(log.Events) != 0 {
		reserved, settled := map[string]bool{}, map[string]bool{}
		for _, e := range log.Events {
			if e.Phase != "end" || (e.Kind != "budget_reserve" && e.Kind != "budget_settle") {
				continue
			}
			var payload struct {
				Call     budget.Call
				Revision int64
			}
			if err = json.Unmarshal(e.Data, &payload); err != nil {
				return err
			}
			if payload.Revision <= 0 || payload.Revision > ledger.Revision {
				return errors.New("budget trace revision is outside its ledger history")
			}
			if payload.Call.ID == "" {
				if e.Error == "" {
					return errors.New("successful budget span has no call")
				}
				continue
			}
			expected, ok := byID[payload.Call.ID]
			if !ok {
				return errors.New("budget trace references an unrelated call")
			}
			if e.Kind == "budget_reserve" {
				if reserved[expected.ID] || payload.Call.State != budget.StateReserved || payload.Call.RunID != expected.RunID || payload.Call.TargetID != expected.TargetID || payload.Call.InputSHA256 != expected.InputSHA256 || payload.Call.ReservedTokens != expected.ReservedTokens {
					return errors.New("budget reservation trace mismatch")
				}
				reserved[expected.ID] = true
			} else {
				if settled[expected.ID] || !reflect.DeepEqual(payload.Call, expected) {
					return errors.New("budget settlement trace mismatch")
				}
				settled[expected.ID] = true
			}
		}
		for _, call := range calls {
			if !reserved[call.ID] || !settled[call.ID] {
				return errors.New("ledger call lacks complete reservation/settlement trace")
			}
		}
	}
	out.KnownTokens = 0
	out.UnknownCalls = 0
	out.AdmittedCalls = len(calls)
	out.DeniedAttempts = deniedCount
	out.UnadmittedAttempts = out.ModelCalls - len(calls)
	for _, call := range calls {
		switch call.State {
		case budget.StateSettled:
			out.KnownTokens += call.Usage.TotalTokens
		case budget.StateReserved:
			out.ReservedTokens += call.ReservedTokens
		case budget.StateUnknown, budget.StateInvalid:
			out.UnknownTokens += call.ReservedTokens
			out.UnknownCalls++
		}
	}
	return nil
}

func validReplayUsage(u *agent.Usage) bool {
	if u == nil || u.InputTokens < 0 || u.OutputTokens < 0 || u.TotalTokens < 0 || u.InputTokens > math.MaxInt64-u.OutputTokens || u.InputTokens+u.OutputTokens != u.TotalTokens {
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

// ReplayFailureCategory reports the primary execution or quality failure. Tool
// errors that the Agent repaired remain trace events, not failed Run counts.
func ReplayFailureCategory(record Record, report Report, _ agent.Recording) string {
	category, _ := classifyRun(record, report)
	if category == "success" {
		return "none"
	}
	return category
}
