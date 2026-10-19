package budget

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
)

func testProfile() Profile {
	return Profile{Provider: "controlled", Endpoint: "in-process", Model: "fixed", ContextTokens: 90, MaxOutputTokens: 10, SourceSHA256: strings.Repeat("a", 64), VerifiedAt: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)}
}
func newLedger(t *testing.T, calls, tokens int64) *Ledger {
	t.Helper()
	l, err := Create(Config{Directory: filepath.Join(t.TempDir(), "budget"), CampaignID: "campaign", Profile: testProfile(), Limits: Limits{calls, tokens}, Targets: map[string]Limits{"a": {10, 1000}, "b": {10, 1000}}})
	if err != nil {
		t.Fatal(err)
	}
	return l
}
func bind(t *testing.T, l *Ledger, target string) {
	t.Helper()
	if err := l.bind(target, "run-"+target); err != nil {
		t.Fatal(err)
	}
}
func reserve(t *testing.T, l *Ledger, target string) Call {
	t.Helper()
	c, err := l.reserve(context.Background(), target, "run-"+target, strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func usage(input, output int64) *agent.Usage {
	return &agent.Usage{InputTokens: input, OutputTokens: output, TotalTokens: input + output}
}
func disk(t *testing.T, l *Ledger) Snapshot {
	t.Helper()
	data, err := os.ReadFile(l.path)
	if err != nil {
		t.Fatal(err)
	}
	var s Snapshot
	if err = json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	return s
}
func awaitWaiters(t *testing.T, l *Ledger, n int) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		l.mu.Lock()
		got := len(l.queue)
		l.mu.Unlock()
		if got == n {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("wanted %d waiters, got %d", n, got)
		case <-ticker.C:
		}
	}
}
func receive[T any](t *testing.T, c <-chan T) T {
	t.Helper()
	select {
	case v := <-c:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for result")
		var zero T
		return zero
	}
}

func TestReservationIsDurableAndSettlesOnlyOnce(t *testing.T) {
	l := newLedger(t, 5, 250)
	bind(t, l, "a")
	c := reserve(t, l, "a")
	s := disk(t, l)
	if s.Totals != (Totals{Calls: 1, ReservedTokens: 100}) || s.Calls[0].State != StateReserved {
		t.Fatalf("not reserved: %+v", s)
	}
	if err := l.settle(c.ID, usage(15, 5), "reply"); err != nil {
		t.Fatal(err)
	}
	revision := l.Snapshot().Revision
	if err := l.settle(c.ID, usage(15, 5), "reply"); err != nil {
		t.Fatal(err)
	}
	if l.Snapshot().Revision != revision {
		t.Fatal("duplicate settlement rewrote ledger")
	}
	s = disk(t, l)
	if s.Totals != (Totals{Calls: 1, KnownTokens: 20}) || s.Targets["a"].Totals != s.Totals {
		t.Fatalf("wrong totals: %+v", s)
	}
	if err := l.settle(c.ID, usage(16, 5), "reply"); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("conflicting settlement: %v", err)
	}
	if _, err := l.reserve(context.Background(), "a", "run-a", strings.Repeat("b", 64)); !errors.Is(err, ErrHalted) {
		t.Fatalf("not halted: %v", err)
	}
}

func TestConcurrentClaimsForLastCall(t *testing.T) {
	l := newLedger(t, 1, 1000)
	bind(t, l, "a")
	bind(t, l, "b")
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			target := []string{"a", "b"}[i%2]
			_, err := l.reserve(context.Background(), target, "run-"+target, strings.Repeat("b", 64))
			if err == nil {
				successes.Add(1)
			} else {
				var denied *Denied
				if !errors.As(err, &denied) || denied.Scope != "campaign" || denied.Resource != "calls" {
					t.Errorf("unexpected denial: %v", err)
				}
			}
		}(i)
	}
	wg.Wait()
	if successes.Load() != 1 || len(l.Snapshot().Rejections) != 19 {
		t.Fatalf("admitted=%d snapshot=%+v", successes.Load(), l.Snapshot())
	}
}

func TestTemporaryTokenShortageWaitsAndReclaimsDifference(t *testing.T) {
	l := newLedger(t, 10, 100)
	bind(t, l, "a")
	bind(t, l, "b")
	first := reserve(t, l, "a")
	result := make(chan error, 1)
	go func() {
		_, err := l.reserve(context.Background(), "b", "run-b", strings.Repeat("b", 64))
		result <- err
	}()
	awaitWaiters(t, l, 1)
	// 结算为零后才有完整的下一次预留额度。
	if err := l.settle(first.ID, usage(0, 0), "reply"); err != nil {
		t.Fatal(err)
	}
	if err := receive(t, result); err != nil {
		t.Fatal(err)
	}
	if l.Snapshot().Totals != (Totals{Calls: 2, ReservedTokens: 100}) {
		t.Fatal(l.Snapshot().Totals)
	}
}

func TestUnknownUsageDoesNotReleaseAndWakesDeniedWaiter(t *testing.T) {
	l := newLedger(t, 10, 100)
	bind(t, l, "a")
	bind(t, l, "b")
	first := reserve(t, l, "a")
	result := make(chan error, 1)
	go func() {
		_, err := l.reserve(context.Background(), "b", "run-b", strings.Repeat("b", 64))
		result <- err
	}()
	awaitWaiters(t, l, 1)
	if err := l.settle(first.ID, nil, "model_error"); err != nil {
		t.Fatal(err)
	}
	var denied *Denied
	if err := receive(t, result); !errors.As(err, &denied) || denied.Resource != "tokens" {
		t.Fatalf("expected permanent token denial: %v", err)
	}
	if l.Snapshot().Totals != (Totals{Calls: 1, UnknownTokens: 100}) {
		t.Fatal(l.Snapshot().Totals)
	}
}

func TestFIFOAndCancellation(t *testing.T) {
	l := newLedger(t, 10, 100)
	bind(t, l, "a")
	bind(t, l, "b")
	first := reserve(t, l, "a")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelled := make(chan error, 1)
	go func() { _, err := l.reserve(ctx, "a", "run-a", strings.Repeat("b", 64)); cancelled <- err }()
	awaitWaiters(t, l, 1)
	next := make(chan Call, 1)
	go func() {
		c, err := l.reserve(context.Background(), "b", "run-b", strings.Repeat("b", 64))
		if err != nil {
			t.Error(err)
		}
		next <- c
	}()
	awaitWaiters(t, l, 2)
	cancel()
	if err := receive(t, cancelled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	awaitWaiters(t, l, 1)
	if err := l.settle(first.ID, usage(0, 0), "reply"); err != nil {
		t.Fatal(err)
	}
	if c := receive(t, next); c.TargetID != "b" {
		t.Fatal(c)
	}
	if len(l.Snapshot().Rejections) != 0 {
		t.Fatal("cancelled wait was recorded as budget rejection")
	}
}

func TestTargetLimitsDoNotBlockAnotherTarget(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits Limits
		want   string
	}{{"calls", Limits{1, 1000}, "calls"}, {"tokens", Limits{10, 100}, "tokens"}} {
		t.Run(tc.name, func(t *testing.T) {
			l := newLedger(t, 10, 1000)
			l.state.Targets["a"] = Account{Limits: tc.limits}
			bind(t, l, "a")
			bind(t, l, "b")
			c := reserve(t, l, "a")
			if err := l.settle(c.ID, usage(10, 5), "reply"); err != nil {
				t.Fatal(err)
			}
			_, err := l.reserve(context.Background(), "a", "run-a", strings.Repeat("b", 64))
			var denied *Denied
			if !errors.As(err, &denied) || denied.Scope != "target" || denied.Resource != tc.want {
				t.Fatal(err)
			}
			reserve(t, l, "b")
		})
	}
}

func TestInvalidUsageHaltsAndPreservesObservedValues(t *testing.T) {
	for name, u := range map[string]*agent.Usage{"negative": {InputTokens: -1, TotalTokens: -1}, "sum": {InputTokens: 1, OutputTokens: 2, TotalTokens: 1}, "input-bound": usage(91, 0), "output-bound": usage(0, 11), "overflow": {InputTokens: math.MaxInt64, OutputTokens: 1, TotalTokens: math.MaxInt64}, "cache": {InputTokens: 1, TotalTokens: 1, CacheHitTokens: ptr(2)}} {
		t.Run(name, func(t *testing.T) {
			l := newLedger(t, 5, 1000)
			bind(t, l, "a")
			c := reserve(t, l, "a")
			if err := l.settle(c.ID, u, "reply"); !errors.Is(err, ErrIntegrity) {
				t.Fatal(err)
			}
			s := disk(t, l)
			if s.Calls[0].State != StateInvalid || s.Calls[0].Usage.InputTokens != u.InputTokens || s.Totals.UnknownTokens != 100 || s.HaltReason == "" {
				t.Fatalf("invalid observation lost: %+v", s)
			}
			select {
			case <-l.Done():
			default:
				t.Fatal("halt signal not closed")
			}
		})
	}
}
func ptr(n int64) *int64 { return &n }

func TestPersistenceFailureClosesAdmission(t *testing.T) {
	for _, stage := range []string{"reserve", "uncertain-rename", "settle"} {
		t.Run(stage, func(t *testing.T) {
			l := newLedger(t, 5, 1000)
			bind(t, l, "a")
			var c Call
			if stage == "settle" {
				c = reserve(t, l, "a")
			}
			saved := l.persist
			l.persist = func(s Snapshot) error {
				if stage == "uncertain-rename" {
					if err := saved(s); err != nil {
						return err
					}
				}
				return errors.New("injected sync failure")
			}
			var err error
			if stage == "settle" {
				err = l.settle(c.ID, usage(1, 1), "reply")
			} else {
				_, err = l.reserve(context.Background(), "a", "run-a", strings.Repeat("b", 64))
			}
			if !errors.Is(err, ErrPersistence) {
				t.Fatal(err)
			}
			if stage == "settle" && l.Snapshot().Totals.ReservedTokens != 100 {
				t.Fatal("lost durable reservation")
			}
			if _, err = l.reserve(context.Background(), "a", "run-a", strings.Repeat("b", 64)); !errors.Is(err, ErrHalted) {
				t.Fatal(err)
			}
			if stage == "uncertain-rename" && len(disk(t, l).Calls) != 1 {
				t.Fatal("test did not persist uncertain reservation")
			}
		})
	}
}

func TestCreateAndBindRejectInvalidOrExistingIdentity(t *testing.T) {
	l := newLedger(t, 5, 1000)
	config := Config{Directory: filepath.Dir(l.path), CampaignID: "campaign", Limits: Limits{5, 1000}, Targets: map[string]Limits{"a": {5, 1000}}, Profile: testProfile()}
	if _, err := Create(config); err == nil {
		t.Fatal("reopened existing ledger")
	}
	bind(t, l, "a")
	if err := l.bind("a", "another-run"); err == nil {
		t.Fatal("rebound target")
	}
	if err := l.bind("missing", "run"); err == nil {
		t.Fatal("accepted unknown target")
	}
	if err := l.bind("b", "../run"); err == nil {
		t.Fatal("accepted invalid run ID")
	}
	profile := testProfile()
	profile.ContextTokens = math.MaxInt64
	if err := profile.Validate(); err == nil {
		t.Fatal("accepted overflowing reservation")
	}
	s := l.Snapshot()
	s.Targets["a"] = Account{}
	s.Profile.Model = "changed"
	if l.Snapshot().Profile.Model == "changed" || l.Snapshot().Targets["a"].RunID == "" {
		t.Fatal("snapshot aliases ledger")
	}
}

type controlledModel struct {
	profile  Profile
	complete func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error)
}

func (m controlledModel) BudgetProfile() (Profile, error) { return m.profile, nil }
func (m controlledModel) Complete(ctx context.Context, msg []agent.Message, tools []agent.ToolSpec) (agent.Reply, error) {
	return m.complete(ctx, msg, tools)
}

func TestModelPersistsBeforeCallingAndUnknownErrorsAreCharged(t *testing.T) {
	for _, tc := range []struct {
		name  string
		u     *agent.Usage
		err   error
		state CallState
	}{{"known", usage(10, 5), nil, StateSettled}, {"protocol-error", usage(10, 5), errors.New("invalid reply"), StateSettled}, {"no-usage", nil, nil, StateUnknown}, {"timeout", nil, context.DeadlineExceeded, StateUnknown}} {
		t.Run(tc.name, func(t *testing.T) {
			l := newLedger(t, 5, 1000)
			provider := controlledModel{testProfile(), func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
				if s := disk(t, l); len(s.Calls) != 1 || s.Calls[0].State != StateReserved {
					t.Fatal("model ran before durable reservation")
				}
				return agent.Reply{Usage: tc.u}, tc.err
			}}
			model, err := NewModel(l, "a", "run-a", provider)
			if err != nil {
				t.Fatal(err)
			}
			_, err = model.Complete(context.Background(), []agent.Message{{Role: agent.RoleUser, Content: "hello"}}, nil)
			if !errors.Is(err, tc.err) {
				t.Fatalf("error=%v want=%v", err, tc.err)
			}
			s := disk(t, l)
			if s.Calls[0].State != tc.state || s.Totals.Calls != 1 || s.Targets["a"].RunID != "run-a" {
				t.Fatalf("wrong call: %+v", s)
			}
		})
	}
}

func TestModelDeniedOrFailedReservationNeverCallsProvider(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "budget", true: "storage"}[failure], func(t *testing.T) {
			l := newLedger(t, 5, 50)
			if failure {
				l = newLedger(t, 5, 1000)
			}
			var calls atomic.Int32
			model, err := NewModel(l, "a", "run-a", controlledModel{testProfile(), func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
				calls.Add(1)
				return agent.Reply{}, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			if failure {
				l.persist = func(Snapshot) error { return errors.New("disk full") }
			}
			_, err = model.Complete(context.Background(), nil, nil)
			if err == nil || calls.Load() != 0 {
				t.Fatalf("err=%v calls=%d", err, calls.Load())
			}
			if !failure && !errors.Is(err, agent.ErrModelBudget) {
				t.Fatal(err)
			}
		})
	}
}

func TestIntegrityFaultCancelsAnotherInFlightModel(t *testing.T) {
	l := newLedger(t, 5, 1000)
	entered := make(chan struct{})
	finished := make(chan error, 1)
	model, err := NewModel(l, "a", "run-a", controlledModel{testProfile(), func(ctx context.Context, _ []agent.Message, _ []agent.ToolSpec) (agent.Reply, error) {
		close(entered)
		<-ctx.Done()
		return agent.Reply{}, ctx.Err()
	}})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, err := model.Complete(context.Background(), nil, nil); finished <- err }()
	receive(t, entered)
	bind(t, l, "b")
	call := reserve(t, l, "b")
	if err := l.settle(call.ID, usage(0, 11), "reply"); !errors.Is(err, ErrIntegrity) {
		t.Fatal(err)
	}
	if err := receive(t, finished); !errors.Is(err, ErrHalted) || !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if l.Snapshot().Totals.UnknownTokens != 200 {
		t.Fatal("cancelled call lost unknown cost")
	}
}

func TestModelProfileMustMatchLedger(t *testing.T) {
	l := newLedger(t, 5, 1000)
	p := testProfile()
	p.Model = "different"
	if _, err := NewModel(l, "a", "run-a", controlledModel{profile: p}); err == nil {
		t.Fatal("accepted different profile")
	}
}

func TestFIFOGrantsFirstReadyWaiter(t *testing.T) {
	l := newLedger(t, 10, 100)
	bind(t, l, "a")
	bind(t, l, "b")
	first := reserve(t, l, "a")
	a := make(chan Call, 1)
	b := make(chan Call, 1)
	go func() {
		c, err := l.reserve(context.Background(), "a", "run-a", strings.Repeat("b", 64))
		if err != nil {
			t.Error(err)
		}
		a <- c
	}()
	awaitWaiters(t, l, 1)
	go func() {
		c, err := l.reserve(context.Background(), "b", "run-b", strings.Repeat("b", 64))
		if err != nil {
			t.Error(err)
		}
		b <- c
	}()
	awaitWaiters(t, l, 2)
	if err := l.settle(first.ID, usage(0, 0), "reply"); err != nil {
		t.Fatal(err)
	}
	granted := receive(t, a)
	select {
	case <-b:
		t.Fatal("later waiter overtook first waiter")
	default:
	}
	if err := l.settle(granted.ID, usage(0, 0), "reply"); err != nil {
		t.Fatal(err)
	}
	receive(t, b)
}

func TestCallerCancellationStillSettlesUnknownUsage(t *testing.T) {
	l := newLedger(t, 5, 1000)
	entered := make(chan struct{})
	model, err := NewModel(l, "a", "run-a", controlledModel{testProfile(), func(ctx context.Context, _ []agent.Message, _ []agent.ToolSpec) (agent.Reply, error) {
		close(entered)
		<-ctx.Done()
		return agent.Reply{}, ctx.Err()
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := model.Complete(ctx, nil, nil); result <- err }()
	receive(t, entered)
	cancel()
	if err := receive(t, result); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if s := disk(t, l); s.Totals.UnknownTokens != 100 || s.Totals.ReservedTokens != 0 {
		t.Fatal(s)
	}
}
