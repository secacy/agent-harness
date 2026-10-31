package ablation

import (
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/task"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
)

func testProtocol() Protocol {
	h := strings.Repeat("a", 64)
	p := Protocol{SchemaVersion: 1, ID: "p1-test", Kind: "formal", Model: budget.Profile{Provider: "controlled", Endpoint: "in-process", Model: "fixed", ContextTokens: 90, MaxOutputTokens: 10, SourceSHA256: h, VerifiedAt: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)}, Execution: execution.Info{Kind: "docker", Image: "sha256:" + h, CPUs: 2, MemoryBytes: 64 << 20, Pids: 128}, Limits: agent.Limits{MaxModelCalls: 4, MaxToolCalls: 8}, AttemptBudget: budget.Limits{MaxCalls: 4, MaxTokens: 1000}, TotalBudget: budget.Limits{MaxCalls: 144, MaxTokens: 36000}, PreparationTimeout: time.Minute, GenerationTimeout: time.Minute, ValidationTimeout: time.Minute, ReferenceTimeout: time.Minute, HostTimeout: time.Hour, Repetitions: 3, Analysis: "independent-generations", FailurePolicy: "retain-all-no-retry-stop-on-shared-integrity-failure"}
	for _, id := range []string{"goddd", "translation", "workouts", "ghcli"} {
		v := "v1"
		limit := 0
		if id == "translation" {
			v = "v2"
			limit = 2
		}
		if id == "workouts" {
			limit = 2
		}
		p.Tasks = append(p.Tasks, Task{ID: id, Input: task.Target{ID: id, ProfileID: "rpcx-" + id + "-t3-" + v, BuildConfig: "default", Repository: task.Snapshot{Directory: filepath.Join("/input", id), SHA256: h}, Specification: task.Specification{Path: "/input/spec.md", SHA256: h}}, DiagnosticLimit: limit, ReferenceVersion: "reference-" + id, ReferenceSHA256: h, ReferenceToolSHA256: h})
	}
	p.Schedule = Schedule(p.Tasks, p.Repetitions)
	p.Resources = execution.ResourcePolicy{Protocol: execution.ResourceProtocol, Context: "controlled", EngineID: "controlled-engine"}
	return p
}

func row(s Slot, truth evaluation.Status, verdict string) Result {
	r := Result{Slot: s, RunID: s.ID, State: "Completed", Submitted: true, CandidateSHA256: strings.Repeat("a", 64), Truth: truth, Delivery: evaluation.Inconclusive}
	if s.Group == C {
		r.Verdict = verdict
	}
	r.Released = s.Group != C || verdict == "ReadyForReview"
	return r
}

func TestP1RetainsAllScheduledAttemptsAndUnknownCosts(t *testing.T) {
	p := testProtocol()
	var rows []Result
	for _, s := range p.Schedule {
		if s.TaskID != "goddd" {
			continue
		}
		verdict := "ReadyForReview"
		truth := evaluation.Pass
		if s.Repetition == 2 {
			truth = evaluation.Fail
		}
		if s.Repetition == 3 {
			truth = evaluation.Inconclusive
		}
		if s.Group == C && s.Repetition == 1 {
			verdict = "NeedsAttention"
		}
		rows = append(rows, row(s, truth, verdict))
	}
	s, err := Summarize(p, rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Results) != 36 || s.AllSlotsObserved {
		t.Fatalf("lost schedule: %+v", s)
	}
	a, c := s.Groups[A], s.Groups[C]
	if a.Counts.NotStarted != 9 || a.AutomaticSuccess.Numerator != 1 || a.AutomaticSuccess.Denominator != 12 || a.WrongRelease.Numerator != 1 || a.WrongRelease.Denominator != 3 || a.WrongReleaseUpper.Numerator != 2 {
		t.Fatalf("A counts: %+v", a)
	}
	if c.Counts.FalseRejection != 1 || c.FalseRejection.Denominator != 1 || c.Counts.BlockedReasons["NeedsAttention"] != 1 || c.AutomaticSuccess.Numerator != 0 || c.WrongRelease.Denominator != 2 {
		t.Fatalf("C counts: %+v", c)
	}
	if s.Tasks["translation"][C].WrongRelease.Value != nil {
		t.Fatal("zero denominator must be N/A")
	}
	if a.Measurements["amount"].Mean != nil || a.Measurements["known_tokens"].Observed != 0 || a.Measurements["amount"].Missing != 12 {
		t.Fatal("unknown resources became zero")
	}
	if s.Tasks["goddd"][A].Counts.Scheduled != 3 {
		t.Fatal("per task denominator wrong")
	}
}

func TestP1MeasuresObservedMeanAndRangeWithoutDroppingUnknown(t *testing.T) {
	p := testProtocol()
	var rows []Result
	for _, s := range p.Schedule {
		if s.TaskID != "goddd" || s.Group != A || s.Repetition == 3 {
			continue
		}
		r := row(s, evaluation.Pass, "")
		v := int64(s.Repetition * 100)
		r.Metrics.KnownTokens = &v
		rows = append(rows, r)
	}
	out, err := Summarize(p, rows)
	if err != nil {
		t.Fatal(err)
	}
	d := out.Tasks["goddd"][A].Measurements["known_tokens"]
	if d.Observed != 2 || d.Missing != 1 || *d.Mean != 150 || *d.Min != 100 || *d.Max != 200 {
		t.Fatalf("distribution: %+v", d)
	}
}

func TestP1RejectsSharedGenerationAndContradictoryRelease(t *testing.T) {
	p := testProtocol()
	first, second := row(p.Schedule[0], evaluation.Pass, "ReadyForReview"), row(p.Schedule[1], evaluation.Pass, "ReadyForReview")
	second.RunID = first.RunID
	if _, err := Summarize(p, []Result{first, second}); err == nil {
		t.Fatal("shared run counted independently")
	}
	r := row(Slot{ID: "x", Group: C}, evaluation.Pass, "Rejected")
	r.Released = true
	if err := validateResult(r); err == nil {
		t.Fatal("rejected candidate released")
	}
	r = row(p.Schedule[0], evaluation.Pass, "")
	r.Metrics.Amount = new(float64)
	*r.Metrics.Amount = math.NaN()
	if err := validateResult(r); err == nil {
		t.Fatal("NaN accepted")
	}
	second = first
	if _, err := Summarize(p, []Result{first, second}); err == nil {
		t.Fatal("duplicate attempt accepted")
	}
}

func TestP1ProtocolRejectsMissingCellsAndInsufficientBudget(t *testing.T) {
	for name, mutate := range map[string]func(*Protocol){
		"duplicate cell": func(p *Protocol) {
			p.Schedule[1].TaskID = p.Schedule[0].TaskID
			p.Schedule[1].Group = p.Schedule[0].Group
		},
		"missing row":          func(p *Protocol) { p.Schedule = p.Schedule[:35] },
		"two repeats":          func(p *Protocol) { p.Repetitions = 2 },
		"insufficient reserve": func(p *Protocol) { p.AttemptBudget.MaxTokens = 99 },
		"shared allowance":     func(p *Protocol) { p.TotalBudget.MaxTokens-- },
		"unpinned image":       func(p *Protocol) { p.Execution.Image = "golang:latest" },
		"old translation":      func(p *Protocol) { p.Tasks[1].Input.ProfileID = "rpcx-translation-t3-v1" },
	} {
		t.Run(name, func(t *testing.T) {
			p := testProtocol()
			mutate(&p)
			if err := p.Validate(); err == nil {
				t.Fatal("invalid protocol accepted")
			}
		})
	}
}

func TestP1ScheduleRotatesOrderAndBalancesCells(t *testing.T) {
	p := testProtocol()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	for r := 0; r < 3; r++ {
		if p.Schedule[r*12].Group != []Group{A, B, C}[r] {
			t.Fatal("order did not rotate")
		}
	}
	price := Price{Currency: "USD", Source: "https://example.com/pricing", RecordedAt: "2026-10-31T00:00:00Z", InputPerMillion: 1, CachedInputPerMillion: .1, OutputPerMillion: 3}
	amount, err := price.ConservativeAmount(90000000)
	if err != nil || amount != 270 {
		t.Fatalf("monetary upper estimate: %v %v", amount, err)
	}
}
