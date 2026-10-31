package ablation

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/trace"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
)

func TestP1ReferenceKeepsNoCandidateRowsAndIsExclusive(t *testing.T) {
	p := runnableProtocol(t)
	root := filepath.Join(t.TempDir(), "experiment")
	backend := unusedBackend{p.Execution}
	if _, err := Run(context.Background(), root, p, Services{Backend: backend, NewModel: func(Slot) (budget.ProfiledModel, error) { return nil, errors.New("unavailable") }}); err != nil {
		t.Fatal(err)
	}
	outcomes, err := EvaluateAll(context.Background(), root, backend)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 36 {
		t.Fatalf("lost scheduled rows: %d", len(outcomes))
	}
	for _, o := range outcomes {
		if o.State != "no_fixed_candidate" || o.Automatic != evaluation.Inconclusive || o.ReferenceSHA256 != "" {
			t.Fatalf("invented candidate result: %+v", o)
		}
	}
	raw, err := readLocal(root, "references-execution.json")
	if err != nil {
		t.Fatal(err)
	}
	var receipt ReferenceExecution
	if err = Decode(raw, &receipt); err != nil || receipt.FinishedAt == nil || len(receipt.Outcomes) != 36 {
		t.Fatalf("missing durable terminal phase: %+v %v", receipt, err)
	}
	if _, err = EvaluateAll(context.Background(), root, backend); err == nil {
		t.Fatal("reference phase was overwritten")
	}
	after, err := readLocal(root, "references-execution.json")
	if err != nil || string(after) != string(raw) {
		t.Fatal("rejected rerun changed original receipt")
	}
}

func TestP1ReferenceCancellationDoesNotConsumeAttempt(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := EvaluateAll(ctx, root, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	for _, name := range []string{"references", "references-execution.json"} {
		if _, err := os.Lstat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("cancelled preflight wrote reference output")
		}
	}
}

type failedReferenceModel struct{ profile budget.Profile }

func (m failedReferenceModel) BudgetProfile() (budget.Profile, error) { return m.profile, nil }
func (failedReferenceModel) Complete(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
	return agent.Reply{}, errors.New("controlled model failure after preparation")
}

type referenceBackend struct {
	info            execution.Info
	events          []byte
	reference       bool
	first           string
	observed        map[string]int
	cancel          context.CancelFunc
	resourceUnknown bool
}

func (b *referenceBackend) Info() execution.Info { return b.info }
func (b *referenceBackend) Run(ctx context.Context, req execution.Request) (execution.Result, error) {
	if req.Owner == nil {
		return execution.Result{}, errors.New("missing resource owner")
	}
	if b.reference {
		b.observed[req.Owner.TargetID]++
		if b.first == "" {
			b.first = req.Owner.TargetID
			if b.cancel != nil {
				b.cancel()
				return execution.Result{}, context.Canceled
			}
			if b.resourceUnknown {
				return execution.Result{}, execution.ErrResourcesUnknown
			}
			return execution.Result{}, errors.New("local reference facility failure")
		}
		return execution.Result{Stdout: []byte("{}\n")}, nil
	}
	for _, arg := range req.Args {
		if arg == "test" {
			return execution.Result{Stdout: b.events}, nil
		}
	}
	return execution.Result{}, ctx.Err()
}

func godddReferenceEvents(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../runner/profiles.json")
	if err != nil {
		t.Fatal(err)
	}
	var profiles []struct {
		ID       string   `json:"id"`
		Required []string `json:"required_tests"`
	}
	if err = json.Unmarshal(raw, &profiles); err != nil {
		t.Fatal(err)
	}
	var tests []string
	for _, p := range profiles {
		if p.ID == "rpcx-goddd-t3-v1" {
			tests = p.Required
		}
	}
	if len(tests) == 0 {
		t.Fatal("required baseline test fixture missing")
	}
	var data []byte
	packages := map[string]bool{}
	appendEvent := func(event map[string]string) {
		b, e := json.Marshal(event)
		if e != nil {
			t.Fatal(e)
		}
		data = append(data, append(b, '\n')...)
	}
	for _, id := range tests {
		pkg, name, ok := strings.Cut(id, "/Test")
		if !ok {
			t.Fatal(id)
		}
		name = "Test" + name
		packages[pkg] = true
		for _, action := range []string{"run", "pass"} {
			appendEvent(map[string]string{"Action": action, "Package": pkg, "Test": name})
		}
	}
	for pkg := range packages {
		appendEvent(map[string]string{"Action": "pass", "Package": pkg})
	}
	return data
}

func twoFailedFixedCandidates(t *testing.T) (string, Protocol, *referenceBackend) {
	t.Helper()
	p := runnableProtocol(t)
	backend := &referenceBackend{info: p.Execution, events: godddReferenceEvents(t), observed: map[string]int{}}
	root := filepath.Join(t.TempDir(), "experiment")
	generated, err := Run(context.Background(), root, p, Services{Backend: backend, NewModel: func(s Slot) (budget.ProfiledModel, error) {
		if s.TaskID == "goddd" && s.Group == A && s.Repetition <= 2 {
			return failedReferenceModel{p.Model}, nil
		}
		return nil, errors.New("not selected in controlled fixture")
	}})
	if err != nil {
		t.Fatal(err)
	}
	fixed := 0
	for i, entry := range generated.Slots {
		if entry.State != "finished" {
			continue
		}
		raw, e := readLocal(AttemptDirectory(root, entry.Slot.ID), "experiment-attempt.json")
		if e != nil {
			t.Fatal(e)
		}
		var a struct {
			Submitted bool `json:"submitted"`
			Record    struct {
				Candidate string `json:"candidate_sha256"`
			} `json:"record"`
		}
		if e = json.Unmarshal(raw, &a); e != nil {
			t.Fatal(e)
		}
		if a.Submitted || a.Record.Candidate == "" {
			t.Fatalf("fixture must retain an unsubmitted fixed candidate: %+v", a)
		}
		fixed++
		if fixed == 1 {
			generated.Slots[i].State = "starting"
			generated.Slots[i].ReceiptSHA256 = ""
		}
	}
	if fixed != 2 {
		t.Fatalf("expected two fixed failed attempts, got %d", fixed)
	}
	if err = gate.WriteJSON(filepath.Join(root, "execution.json"), generated); err != nil {
		t.Fatal(err)
	}
	backend.reference = true
	return root, p, backend
}

func TestP1ReferenceEvaluatesFailedAndUnindexedFixedCandidates(t *testing.T) {
	root, p, backend := twoFailedFixedCandidates(t)
	before, err := readLocal(root, "execution.json")
	if err != nil {
		t.Fatal(err)
	}
	outcomes, err := EvaluateAll(context.Background(), root, backend)
	if err != nil {
		t.Fatal(err)
	}
	failed, finished := 0, 0
	for _, o := range outcomes {
		if o.State == "failed" {
			failed++
			if o.Error == "" || o.ReferenceSHA256 == "" {
				t.Fatalf("local failure evidence lost: %+v", o)
			}
		}
		if o.State == "finished" {
			finished++
			if o.ReferenceSHA256 == "" || o.Automatic != evaluation.Inconclusive {
				t.Fatalf("unexpected reference: %+v", o)
			}
		}
	}
	if len(outcomes) != 36 || failed != 1 || finished != 1 || len(backend.observed) != 2 {
		t.Fatalf("failed/unsubmitted candidates filtered or local failure stopped schedule: %+v calls=%v", outcomes, backend.observed)
	}
	after, err := readLocal(root, "execution.json")
	if err != nil || string(before) != string(after) {
		t.Fatal("reference repaired the generation index in place")
	}
	summary, err := Collect(context.Background(), root)
	if err != nil {
		t.Fatalf("local reference failure prevented honest collection: %v", err)
	}
	if len(summary.Results) != 36 {
		t.Fatal("partial references reduced scheduled denominator")
	}
	fixed, unknownTokens := 0, int64(0)
	for _, result := range summary.Results {
		if result.CandidateSHA256 == "" {
			continue
		}
		fixed++
		if result.Truth != evaluation.Inconclusive || result.Delivery != evaluation.Inconclusive {
			t.Fatalf("partial or automatic-only reference fabricated truth: %+v", result)
		}
		if result.Metrics.UnknownTokens == nil {
			t.Fatal("partial reference lost model accounting")
		}
		unknownTokens += *result.Metrics.UnknownTokens
	}
	if fixed != 2 || unknownTokens != 2*(p.Model.ContextTokens+p.Model.MaxOutputTokens) {
		t.Fatalf("candidate/accounting loss: fixed=%d unknown=%d", fixed, unknownTokens)
	}
}

func TestP1ReferenceParentCancellationRetainsUnstartedRows(t *testing.T) {
	root, _, backend := twoFailedFixedCandidates(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend.cancel = cancel
	outcomes, err := EvaluateAll(ctx, root, backend)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("lost parent cancellation: %v", err)
	}
	if len(outcomes) != 36 || len(backend.observed) != 1 {
		t.Fatalf("continued dispatch after parent cancellation: %v %+v", backend.observed, outcomes)
	}
	notStarted := 0
	for _, o := range outcomes {
		if o.State == "not_started" {
			notStarted++
		}
	}
	if notStarted == 0 {
		t.Fatal("cancelled phase dropped unstarted slots")
	}
	raw, e := readLocal(root, "references-execution.json")
	if e != nil {
		t.Fatal(e)
	}
	var phase ReferenceExecution
	if e = Decode(raw, &phase); e != nil || phase.FinishedAt == nil || phase.Error == "" {
		t.Fatal("cancelled phase was not recorded")
	}
}

func TestP1ReferenceUnknownResourcesStopFurtherCandidates(t *testing.T) {
	root, _, backend := twoFailedFixedCandidates(t)
	backend.resourceUnknown = true
	outcomes, err := EvaluateAll(context.Background(), root, backend)
	if !errors.Is(err, execution.ErrResourcesUnknown) {
		t.Fatalf("unknown cleanup did not stop reference phase: %v", err)
	}
	if len(outcomes) != 36 || len(backend.observed) != 1 {
		t.Fatalf("new candidate admitted after unknown cleanup: %v", backend.observed)
	}
	remaining := 0
	for _, o := range outcomes {
		if o.State == "not_started" {
			remaining++
		}
	}
	if remaining == 0 {
		t.Fatal("shared failure lost unstarted rows")
	}
}

func TestP1ReferenceSkipsUncertifiedStartingAttempts(t *testing.T) {
	for _, mode := range []string{"initial_running", "partial_running_receipt", "broken_terminal_trace"} {
		t.Run(mode, func(t *testing.T) {
			root, p, backend := twoFailedFixedCandidates(t)
			executionRaw, err := readLocal(root, "execution.json")
			if err != nil {
				t.Fatal(err)
			}
			var generated Execution
			if err = Decode(executionRaw, &generated); err != nil {
				t.Fatal(err)
			}
			var slot Slot
			for _, entry := range generated.Slots {
				if entry.State == "starting" {
					slot = entry.Slot
					break
				}
			}
			if slot.ID == "" {
				t.Fatal("fixture has no starting slot")
			}
			directory := AttemptDirectory(root, slot.ID)
			switch mode {
			case "initial_running":
				log, e := trace.Read(filepath.Join(directory, "trace.jsonl"))
				if e != nil {
					t.Fatal(e)
				}
				if len(log.Events) == 0 {
					t.Fatal("fixture has no initial trace receipt")
				}
				// Use the runtime's actual initial payload, which has already been
				// published before its terminal receipt and trace are available.
				if err = gate.Write(filepath.Join(directory, "experiment-attempt.json"), log.Events[0].Data); err != nil {
					t.Fatal(err)
				}
			case "partial_running_receipt":
				if err = gate.Write(filepath.Join(directory, "experiment-attempt.json"), []byte(`{"schema_version":1,"record":{"status":"Running"`)); err != nil {
					t.Fatal(err)
				}
			case "broken_terminal_trace":
				raw, e := os.ReadFile(filepath.Join(directory, "trace.jsonl"))
				if e != nil {
					t.Fatal(e)
				}
				if err = gate.Write(filepath.Join(directory, "trace.jsonl"), append(raw, []byte("invalid terminal trace\n")...)); err != nil {
					t.Fatal(err)
				}
			}
			outcomes, err := EvaluateAll(context.Background(), root, backend)
			if err != nil {
				t.Fatalf("uncertified starting attempt stopped independent reference work: %v", err)
			}
			if len(outcomes) != 36 || len(backend.observed) != 1 || backend.observed[slot.ID] != 0 {
				t.Fatalf("uncertified candidate executed or later candidate skipped: calls=%v", backend.observed)
			}
			var excluded ReferenceOutcome
			for _, o := range outcomes {
				if o.Slot == slot {
					excluded = o
				}
			}
			want := "evidence_incomplete:"
			if mode == "broken_terminal_trace" {
				want = "evidence_invalid:"
			}
			if excluded.State != "no_fixed_candidate" || excluded.Automatic != evaluation.Inconclusive || excluded.ReferenceSHA256 != "" || !strings.HasPrefix(excluded.Error, want) {
				t.Fatalf("bad skipped receipt: %+v", excluded)
			}
			if _, e := os.Lstat(filepath.Join(root, "references", slot.ID)); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("uncertified candidate reference directory was created")
			}
			summary, err := Collect(context.Background(), root)
			if err != nil {
				t.Fatalf("uncertified starting evidence prevented collection: %v", err)
			}
			if len(summary.Results) != 36 {
				t.Fatal("unfinished starting slot left denominator")
			}
			unknown := int64(0)
			for _, r := range summary.Results {
				if r.Metrics.UnknownTokens != nil {
					unknown += *r.Metrics.UnknownTokens
				}
				if r.Slot == slot && r.Truth != evaluation.Inconclusive {
					t.Fatal("uncertified candidate acquired truth")
				}
			}
			if unknown != 2*(p.Model.ContextTokens+p.Model.MaxOutputTokens) {
				t.Fatalf("uncertified receipt lost persistent accounting: %d", unknown)
			}
			after, e := readLocal(root, "execution.json")
			if e != nil || string(after) != string(executionRaw) {
				t.Fatal("reference edited incomplete generation index")
			}
		})
	}
}

func TestP1ReferenceFinishedTraceCorruptionRemainsHardFailure(t *testing.T) {
	root, _, backend := twoFailedFixedCandidates(t)
	raw, err := readLocal(root, "execution.json")
	if err != nil {
		t.Fatal(err)
	}
	var generated Execution
	if err = Decode(raw, &generated); err != nil {
		t.Fatal(err)
	}
	var id string
	for _, entry := range generated.Slots {
		if entry.State == "finished" {
			id = entry.Slot.ID
			break
		}
	}
	if id == "" {
		t.Fatal("fixture has no finished slot")
	}
	path := filepath.Join(AttemptDirectory(root, id), "trace.jsonl")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = gate.Write(path, append(content, []byte("invalid terminal trace\n")...)); err != nil {
		t.Fatal(err)
	}
	if _, err = EvaluateAll(context.Background(), root, backend); err == nil {
		t.Fatal("corrupted committed terminal trace was silently ignored")
	}
	if len(backend.observed) != 0 {
		t.Fatal("reference command dispatched before generation integrity failure")
	}
	if _, e := os.Lstat(filepath.Join(root, "references-execution.json")); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("invalid generation consumed reference phase")
	}
}
