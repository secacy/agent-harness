package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/task"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/ablation"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
	"github.com/secacy/agent-harness/scenarios/rpcx/runner"
)

type p1CalibrationCase struct {
	ID                string            `json:"id"`
	PatchSHA256       string            `json:"patch_sha256"`
	CandidateSHA256   string            `json:"candidate_sha256"`
	ExpectedVerdict   string            `json:"expected_c_verdict"`
	ExpectedAutomatic evaluation.Status `json:"expected_reference_automatic"`
	ScriptSHA256      string            `json:"script_sha256"`
}
type p1CalibrationSlot struct {
	ID              string                 `json:"id"`
	CaseID          string                 `json:"case_id"`
	Group           runner.ExperimentGroup `json:"group"`
	ExpectedRelease bool                   `json:"expected_release"`
	ExpectedVerdict string                 `json:"expected_verdict"`
}
type p1CalibrationPlan struct {
	SchemaVersion  int                      `json:"schema_version"`
	Kind           string                   `json:"kind"`
	KnownAnswer    bool                     `json:"known_answer"`
	FormalAttempts int                      `json:"formal_attempts"`
	UsageMeaning   string                   `json:"usage_meaning"`
	MoneyMeaning   string                   `json:"money_meaning"`
	HumanReview    string                   `json:"human_review"`
	Target         task.Target              `json:"target"`
	Backend        execution.Info           `json:"backend"`
	Resources      execution.ResourcePolicy `json:"resources"`
	Model          budget.Profile           `json:"model"`
	Limits         agent.Limits             `json:"agent_limits"`
	AttemptBudget  budget.Limits            `json:"attempt_budget"`
	Preparation    time.Duration            `json:"preparation_timeout_ns"`
	Generation     time.Duration            `json:"generation_timeout_ns"`
	Validation     time.Duration            `json:"validation_timeout_ns"`
	Reference      time.Duration            `json:"reference_timeout_ns"`
	Host           time.Duration            `json:"host_timeout_ns"`
	Cases          []p1CalibrationCase      `json:"cases"`
	Slots          []p1CalibrationSlot      `json:"slots"`
	Sources        map[string]string        `json:"source_sha256"`
}
type p1CalibrationOutcome struct {
	Slot               p1CalibrationSlot `json:"slot"`
	State              string            `json:"state"`
	Error              string            `json:"error,omitempty"`
	RunDirectory       string            `json:"run_directory,omitempty"`
	ReceiptSHA256      string            `json:"receipt_sha256,omitempty"`
	CandidateSHA256    string            `json:"candidate_sha256,omitempty"`
	Submitted          bool              `json:"submitted"`
	Released           bool              `json:"released"`
	Verdict            string            `json:"verdict,omitempty"`
	ReplayVerified     bool              `json:"replay_verified"`
	ReferenceState     string            `json:"reference_state"`
	ReferenceSHA256    string            `json:"reference_sha256,omitempty"`
	ReferenceAutomatic evaluation.Status `json:"reference_automatic"`
	CandidateTruth     evaluation.Status `json:"candidate_truth"`
	Delivery           evaluation.Status `json:"delivery"`
}

// TestP1DockerCalibration executes only when a fresh output path is explicitly
// supplied. The known-answer model emits recorded tool calls; it never contacts
// a provider, writes a workspace directly, or claims an actual human review.
func TestP1DockerCalibration(t *testing.T) {
	output := os.Getenv("HARNESS_P1_CALIBRATION_OUTPUT")
	if output == "" {
		t.Skip("set HARNESS_P1_CALIBRATION_OUTPUT to a fresh directory for known-answer Docker calibration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	project, err := gate.Canonical("../../..")
	if err != nil {
		t.Fatal(err)
	}
	output, err = gate.Canonical(output)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := filepath.Join(project, "scenarios/rpcx/l2/data/rpcx-l2-goddd-v1/fixtures")
	spec := filepath.Join(project, "scenarios/rpcx/goddd/specs/v0.3.0.md")
	target, err := runner.TargetFromGoddd("goddd", fixtures, spec)
	if err != nil {
		t.Fatal(err)
	}
	if err = runner.ValidateTarget(target, output); err != nil {
		t.Fatal(err)
	}
	if gate.Overlap(output, filepath.Join(project, "scenarios")) || gate.Overlap(output, filepath.Join(project, "internal")) {
		t.Fatal("calibration output overlaps sources")
	}
	if err = os.Mkdir(output, 0700); err != nil {
		t.Fatal(err)
	}
	t.Log("development calibration output:", output)
	if err = gate.WriteJSON(filepath.Join(output, "calibration-marker.json"), map[string]any{"kind": "development_calibration", "known_answer": true, "formal_attempts": 0, "human_review": "not performed", "model": "in-process known-candidate tool script; no provider request", "usage": "synthetic zero usage, not supplier data", "money": "not applicable"}); err != nil {
		t.Fatal(err)
	}
	baseline, err := gate.ReadFiles(target.Repository.Directory)
	if err != nil {
		t.Fatal(err)
	}
	caseRoot := filepath.Join(project, "scenarios/rpcx/goddd/cases")
	manifestRaw, err := os.ReadFile(filepath.Join(caseRoot, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	freezeRaw, err := os.ReadFile(filepath.Join(caseRoot, "freeze.json"))
	if err != nil {
		t.Fatal(err)
	}
	var freeze struct {
		Files map[string]string `json:"files"`
	}
	if err = json.Unmarshal(freezeRaw, &freeze); err != nil || freeze.Files["manifest.json"] != gate.Digest(manifestRaw) {
		t.Fatal("Goddd calibration manifest identity changed", err)
	}
	var manifest struct {
		Cases []struct {
			ID, Patch, Verdict string
			PatchSHA256        string `json:"patch_sha256"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	expected := []p1CalibrationCase{
		{ID: "correct", ExpectedVerdict: "ReadyForReview", ExpectedAutomatic: evaluation.Pass},
		{ID: "missing-timeout", ExpectedVerdict: "Rejected", ExpectedAutomatic: evaluation.Fail},
		{ID: "mapping-helper", ExpectedVerdict: "NeedsAttention", ExpectedAutomatic: evaluation.Pass},
	}
	scripts := map[string][]agent.Reply{}
	for i := range expected {
		c := &expected[i]
		var patch string
		for _, m := range manifest.Cases {
			if m.ID == c.ID {
				patch = m.Patch
				c.PatchSHA256 = m.PatchSHA256
				if m.Verdict != c.ExpectedVerdict {
					t.Fatal("predeclared case verdict differs from frozen manifest")
				}
			}
		}
		if patch != "patches/"+c.ID+".patch" || freeze.Files[patch] != c.PatchSHA256 {
			t.Fatal("calibration case identity missing", c.ID)
		}
		patchRaw, e := os.ReadFile(filepath.Join(caseRoot, patch))
		if e != nil || gate.Digest(patchRaw) != c.PatchSHA256 {
			t.Fatal("calibration patch changed", c.ID, e)
		}
		candidate := filepath.Join(output, "calibration-materials/candidates", c.ID, "goddd")
		if err = gate.CopyFiles(candidate, baseline); err != nil {
			t.Fatal(err)
		}
		if err = gate.Write(filepath.Join(output, "calibration-materials", patch), patchRaw); err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(ctx, "git", "apply", "--", filepath.Join(caseRoot, patch))
		command.Dir = candidate
		command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if data, e := command.CombinedOutput(); e != nil {
			t.Fatalf("apply fixed calibration patch: %v %s", e, data)
		}
		after, e := gate.ReadFiles(candidate)
		if e != nil {
			t.Fatal(e)
		}
		c.CandidateSHA256 = gate.TreeHash(after)
		scripts[c.ID] = p1CandidateScript(t, baseline, after)
		raw, e := json.Marshal(scripts[c.ID])
		if e != nil {
			t.Fatal(e)
		}
		c.ScriptSHA256 = gate.Digest(raw)
	}
	if err = gate.Write(filepath.Join(output, "calibration-materials/manifest.json"), manifestRaw); err != nil {
		t.Fatal(err)
	}
	if err = gate.Write(filepath.Join(output, "calibration-materials/freeze.json"), freezeRaw); err != nil {
		t.Fatal(err)
	}
	backend, err := execution.NewDocker(ctx, execution.DockerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	resources, err := backend.ResourcePolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := p1CalibrationSources(project)
	if err != nil {
		t.Fatal(err)
	}
	capabilityRaw, err := json.Marshal(map[string]any{"kind": "controlled", "scripts": expected, "source": sources["scenarios/rpcx/runner/p1_calibration_test.go"], "usage": "synthetic zero"})
	if err != nil {
		t.Fatal(err)
	}
	profile := budget.Profile{Provider: "controlled", Endpoint: "in-process", Model: "p1-known-candidate-script-v1", ContextTokens: 131072, MaxOutputTokens: 65536, SourceSHA256: gate.Digest(capabilityRaw), VerifiedAt: time.Now().UTC()}
	q := profile.ContextTokens + profile.MaxOutputTokens
	allowance := budget.Limits{MaxCalls: 3, MaxTokens: 3 * q}
	plan := p1CalibrationPlan{SchemaVersion: 1, Kind: "development_calibration", KnownAnswer: true, FormalAttempts: 0, UsageMeaning: "synthetic zero usage; not supplier data", MoneyMeaning: "not applicable; no provider requests", HumanReview: "not performed", Target: target, Backend: backend.Info(), Resources: resources, Model: profile, Limits: agent.Limits{MaxModelCalls: 3, MaxToolCalls: 32}, AttemptBudget: allowance, Preparation: 2 * time.Minute, Generation: 4 * time.Minute, Validation: 4 * time.Minute, Reference: 5 * time.Minute, Host: 45 * time.Minute, Cases: expected, Sources: sources}
	for _, c := range expected {
		for _, group := range []runner.ExperimentGroup{runner.ExperimentA, runner.ExperimentB, runner.ExperimentC} {
			slot := p1CalibrationSlot{ID: "goddd-" + c.ID + "-" + string(group), CaseID: c.ID, Group: group, ExpectedRelease: group != runner.ExperimentC || c.ExpectedVerdict == "ReadyForReview"}
			if group == runner.ExperimentC {
				slot.ExpectedVerdict = c.ExpectedVerdict
			}
			plan.Slots = append(plan.Slots, slot)
		}
	}
	// All cases, expected outcomes, source bytes and resource/usage policies are
	// fixed here, before the first candidate command or RunExperiment dispatch.
	if err = gate.WriteJSON(filepath.Join(output, "calibration-plan.json"), plan); err != nil {
		t.Fatal(err)
	}
	planRaw, err := os.ReadFile(filepath.Join(output, "calibration-plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	registry := execution.ResourceRegistry{SchemaVersion: 1, CampaignID: "p1-docker-calibration", PlanSHA256: gate.Digest(planRaw), Policy: resources}
	if err = execution.CreateResourceRegistry(output, registry); err != nil {
		t.Fatal(err)
	}
	accounts := map[string]budget.Limits{}
	for _, s := range plan.Slots {
		accounts[s.ID] = allowance
	}
	ledger, err := budget.Create(budget.Config{Directory: filepath.Join(output, "budget"), CampaignID: registry.CampaignID, Profile: profile, Limits: budget.Limits{MaxCalls: 27, MaxTokens: 27 * q}, Targets: accounts})
	if err != nil {
		t.Fatal(err)
	}
	guard := execution.NewResourceGuard(backend)
	var outcomes []p1CalibrationOutcome
	for _, s := range plan.Slots {
		outcomes = append(outcomes, p1CalibrationOutcome{Slot: s, State: "not_started", ReferenceState: "not_started", ReferenceAutomatic: evaluation.Inconclusive, CandidateTruth: evaluation.Inconclusive, Delivery: evaluation.Inconclusive})
	}
	publish := func() {
		t.Helper()
		if e := gate.WriteJSON(filepath.Join(output, "calibration-results.json"), map[string]any{"kind": "development_calibration", "plan_sha256": registry.PlanSHA256, "formal_attempts": 0, "human_review": "not performed", "outcomes": outcomes}); e != nil {
			t.Fatal(e)
		}
	}
	publish()
	defer publish()
	for i, s := range plan.Slots {
		o := &outcomes[i]
		o.State = "running"
		publish()
		input := target
		input.ID = s.ID
		owner := execution.ResourceOwner{Root: output, Registry: registry, TargetID: s.ID, RunID: s.ID, RunDirectory: "targets/" + s.ID + "/runs/" + s.ID}
		attempt, runErr := runner.RunExperiment(ctx, runner.ExperimentConfig{Group: s.Group, PreparationTimeout: plan.Preparation, GenerationTimeout: plan.Generation, ValidationTimeout: plan.Validation, Config: runner.Config{Target: &input, RunsDir: filepath.Join(output, "targets", s.ID, "runs"), RunID: s.ID, Model: &p1ScriptModel{profile: profile, replies: scripts[s.CaseID], coached: s.Group != runner.ExperimentA}, ModelName: profile.Model, Budget: &budget.Target{Ledger: ledger, ID: s.ID}, Limits: plan.Limits, CompletionPolicy: agent.FinalResponseV1, Timeout: plan.Preparation + plan.Generation + plan.Validation + time.Minute, Backend: guard, ResourceOwner: &owner}})
		o.State = string(attempt.Record.Status)
		o.RunDirectory = attempt.Record.Directory
		o.CandidateSHA256 = attempt.Record.CandidateSHA256
		o.Submitted = attempt.Submitted
		o.Released = attempt.Released
		o.Verdict = attempt.Record.Verdict
		if runErr != nil {
			o.Error = runErr.Error()
		}
		if attempt.Record.Directory != "" {
			if b, e := os.ReadFile(filepath.Join(attempt.Record.Directory, "experiment-attempt.json")); e == nil {
				o.ReceiptSHA256 = gate.Digest(b)
			}
		}
		publish()
		if errors.Is(runErr, execution.ErrResourcesUnknown) || errors.Is(runErr, runner.ErrInputChanged) || ledger.Err() != nil {
			t.Fatalf("shared calibration integrity halted: %v ledger=%v", runErr, ledger.Err())
		}
		t.Run(s.ID, func(t *testing.T) {
			if runErr != nil {
				t.Errorf("RunExperiment: %v", runErr)
			}
			if attempt.Record.Status != runner.StatusCompleted || !attempt.Submitted || attempt.Released != s.ExpectedRelease || attempt.Record.Verdict != s.ExpectedVerdict {
				t.Errorf("unexpected actual policy outcome: status=%s submitted=%v released=%v verdict=%s; expected released=%v verdict=%s", attempt.Record.Status, attempt.Submitted, attempt.Released, attempt.Record.Verdict, s.ExpectedRelease, s.ExpectedVerdict)
			}
			var want string
			for _, c := range plan.Cases {
				if c.ID == s.CaseID {
					want = c.CandidateSHA256
				}
			}
			if o.CandidateSHA256 != want {
				t.Errorf("actual tool-edited candidate differs from prebuilt case: %s != %s", o.CandidateSHA256, want)
			}
			if attempt.Record.Directory != "" {
				if _, e := runner.ReplayExperiment(ctx, attempt.Record.Directory, filepath.Join(output, "budget/budget.json")); e != nil {
					t.Errorf("offline replay: %v", e)
				} else {
					o.ReplayVerified = true
				}
			}
		})
		publish()
	}
	// References use only the three C candidates, after all nine generation/gate
	// attempts have ended. This avoids rerunning identical known cases nine times.
	if err = os.Mkdir(filepath.Join(output, "references"), 0700); err != nil {
		t.Fatal(err)
	}
	identity, err := evaluation.PublishedIdentity("rpcx-l2-reference-goddd-data-v2")
	if err != nil {
		t.Fatal(err)
	}
	expectedTask := ablation.Task{ID: "goddd", Input: target, ReferenceVersion: identity.Version, ReferenceSHA256: identity.ReferenceSHA256, ReferenceToolSHA256: identity.ToolSHA256}
	for i := range outcomes {
		o := &outcomes[i]
		if o.Slot.Group != runner.ExperimentC {
			o.ReferenceState = "not_selected_identical_known_case"
			continue
		}
		if o.CandidateSHA256 == "" {
			o.ReferenceState = "no_fixed_candidate"
			publish()
			continue
		}
		owner := execution.ResourceOwner{Root: output, Registry: registry, TargetID: o.Slot.ID, RunID: o.Slot.ID, RunDirectory: "targets/" + o.Slot.ID + "/runs/" + o.Slot.ID}
		owned, e := execution.WithResourceOwner(guard, owner)
		if e != nil {
			t.Fatal(e)
		}
		evidence := filepath.Join(output, "references", o.Slot.ID)
		o.ReferenceState = "running"
		publish()
		referenceCtx, stop := context.WithTimeout(ctx, plan.Reference)
		report, e := evaluation.Evaluate(referenceCtx, evaluation.Request{TargetID: o.Slot.ID, RunID: o.Slot.ID, FixturesDir: filepath.Join(o.RunDirectory, "baseline"), CandidateDir: filepath.Join(o.RunDirectory, "candidate/goddd"), CandidateSHA256: o.CandidateSHA256, SpecPath: filepath.Join(o.RunDirectory, "inputs/spec/spec.md"), EvidenceDir: evidence, Backend: owned})
		stop()
		o.ReferenceState = "finished"
		o.ReferenceAutomatic = report.Automatic
		if e != nil {
			o.ReferenceState = "failed"
			o.Error = errors.Join(errors.New(o.Error), e).Error()
		}
		if b, readErr := os.ReadFile(filepath.Join(evidence, "reference.json")); readErr == nil {
			o.ReferenceSHA256 = gate.Digest(b)
		}
		publish()
		if errors.Is(e, execution.ErrResourcesUnknown) || !report.InputsUnchanged {
			t.Fatalf("reference integrity halted: %v inputs_unchanged=%v", e, report.InputsUnchanged)
		}
		t.Run(o.Slot.ID+"-reference", func(t *testing.T) {
			if e != nil {
				t.Errorf("reference execution: %v", e)
			}
			var want evaluation.Status
			for _, c := range plan.Cases {
				if c.ID == o.Slot.CaseID {
					want = c.ExpectedAutomatic
				}
			}
			if report.Automatic != want {
				t.Errorf("automatic reference=%s want=%s", report.Automatic, want)
			}
			_, raw, authErr := ablation.AuthenticateReference(evidence, expectedTask, o.CandidateSHA256, o.Slot.ID, o.Slot.ID)
			if authErr != nil {
				t.Errorf("authenticate independent reference: %v", authErr)
				return
			}
			review, readErr := os.ReadFile(filepath.Join(evidence, "review-template.json"))
			if readErr != nil {
				t.Error(readErr)
				return
			}
			assessment, assessErr := ablation.AssessTruth(raw, review, false)
			if assessErr != nil {
				t.Error(assessErr)
				return
			}
			o.CandidateTruth = assessment.Candidate
			o.Delivery = assessment.Delivery
			expectedTruth := evaluation.Inconclusive
			if want == evaluation.Fail {
				expectedTruth = evaluation.Fail
			}
			if o.CandidateTruth != expectedTruth || o.Delivery != evaluation.Inconclusive {
				t.Errorf("unreviewed known case fabricated human truth: %+v", assessment)
			}
		})
		publish()
	}
	entries, err := execution.ReadResources(output, registry)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Dispatch != nil && entry.Removed == nil {
			t.Fatalf("resource cleanup unconfirmed: %s", entry.Intent.CommandID)
		}
	}
	current, err := p1CalibrationSources(project)
	if err != nil || !maps.Equal(current, sources) {
		t.Fatal("calibration source material changed during execution", err)
	}
	if ledger.Err() != nil {
		t.Fatal(ledger.Err())
	}
}

type p1ScriptModel struct {
	profile budget.Profile
	replies []agent.Reply
	next    int
	coached bool
}

func (m *p1ScriptModel) BudgetProfile() (budget.Profile, error) { return m.profile, nil }
func (m *p1ScriptModel) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolSpec) (agent.Reply, error) {
	if err := ctx.Err(); err != nil {
		return agent.Reply{}, err
	}
	if m.next >= len(m.replies) {
		return agent.Reply{}, errors.New("known-answer calibration script exhausted")
	}
	if m.next == 0 && (len(messages) == 0 || strings.Contains(messages[0].Content, "Review each RPC boundary") != m.coached) {
		return agent.Reply{}, errors.New("calibration received wrong group generation prompt")
	}
	if m.next == 1 {
		for _, message := range messages {
			if message.Role == agent.RoleTool && message.IsError {
				return agent.Reply{}, errors.New("known candidate tool edit failed: " + message.Content)
			}
		}
	}
	reply := m.replies[m.next]
	m.next++
	if m.next == 3 {
		// Report observed checks without claiming migration correctness or a gate.
		var checks []string
		for _, message := range messages {
			if message.Role == agent.RoleTool && strings.HasPrefix(message.ToolCallID, "check-") {
				checks = append(checks, fmt.Sprintf("%s error=%v: %s", message.ToolCallID, message.IsError, message.Content))
			}
		}
		reply.Content = "Development calibration: submitted a supplied known candidate through repository tools. No model migration claim or human review. Actual check feedback:\n" + strings.Join(checks, "\n") + "\nIndependent validation remains separate."
	}
	return reply, nil
}

func p1CandidateScript(t *testing.T, before, after map[string][]byte) []agent.Reply {
	t.Helper()
	if len(before) != len(after) {
		t.Fatal("fixed calibration cases must not add or remove files")
	}
	var edits []agent.ToolCall
	for _, name := range slices.Sorted(maps.Keys(before)) {
		next, ok := after[name]
		if !ok {
			t.Fatal("fixed case removed a file", name)
		}
		if bytes.Equal(before[name], next) {
			continue
		}
		if len(before[name]) == 0 {
			t.Fatal("cannot replace an empty baseline file", name)
		}
		raw, err := json.Marshal(map[string]string{"path": name, "old": string(before[name]), "new": string(next)})
		if err != nil {
			t.Fatal(err)
		}
		edits = append(edits, agent.ToolCall{ID: fmt.Sprintf("edit-%02d", len(edits)+1), Name: "replace_text", Arguments: string(raw)})
	}
	if len(edits) == 0 || len(edits) > 30 {
		t.Fatal("unexpected fixed case edit count", len(edits))
	}
	zero := func() *agent.Usage { return &agent.Usage{} }
	return []agent.Reply{{Finish: agent.FinishToolCalls, ToolCalls: edits, Usage: zero()}, {Finish: agent.FinishToolCalls, ToolCalls: []agent.ToolCall{{ID: "check-build", Name: "go_check", Arguments: `{"kind":"build"}`}, {ID: "check-test", Name: "go_check", Arguments: `{"kind":"test"}`}}, Usage: zero()}, {Finish: agent.FinishStop, Usage: zero()}}
}

func p1CalibrationSources(project string) (map[string]string, error) {
	command := exec.Command("git", "-C", project, "ls-files", "-z", "--", "internal", "scenarios/rpcx", "go.mod", "go.sum")
	raw, err := command.Output()
	if err != nil {
		return nil, err
	}
	names := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	names = append(names, "scenarios/rpcx/runner/p1_calibration_test.go")
	hashes := map[string]string{}
	for _, name := range names {
		if !filepath.IsLocal(name) {
			return nil, errors.New("invalid source inventory path")
		}
		data, e := os.ReadFile(filepath.Join(project, filepath.FromSlash(name)))
		if e != nil {
			return nil, e
		}
		hashes[name] = gate.Digest(data)
	}
	return hashes, nil
}
