package budget

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
)

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)

type waiter struct{ target, run, input string }

// Ledger 是单进程单写者；首次和恢复代次均独占创建，不能重开重置额度。
type Ledger struct {
	mu       sync.Mutex
	path     string
	state    Snapshot
	persist  func(Snapshot) error
	changed  chan struct{}
	queue    []*waiter
	fault    error
	ctx      context.Context
	cancel   context.CancelCauseFunc
	deadline time.Time
}

// Validate 只检查材料格式和整数边界，上界的真实性由可信宿主核实。
func (p Profile) Validate() error {
	if strings.TrimSpace(p.Provider) == "" || strings.TrimSpace(p.Endpoint) == "" || strings.TrimSpace(p.Model) == "" ||
		p.ContextTokens <= 0 || p.MaxOutputTokens <= 0 || p.ContextTokens > math.MaxInt64-p.MaxOutputTokens ||
		!validDigest(p.SourceSHA256) || p.VerifiedAt.IsZero() {
		return errors.New("verified model identity, positive token bounds and source digest are required")
	}
	return nil
}

func validDigest(value string) bool {
	data, err := hex.DecodeString(value)
	return err == nil && len(data) == sha256.Size && value == strings.ToLower(value)
}
func validLimits(l Limits) bool { return l.MaxCalls > 0 && l.MaxTokens > 0 }

// Create 独占创建账本目录；初始化失败留下的目录也不能当作新批次复用。
func Create(config Config) (*Ledger, error) {
	if err := config.Profile.Validate(); err != nil {
		return nil, err
	}
	if config.Directory == "" || !identifier.MatchString(config.CampaignID) || !validLimits(config.Limits) || len(config.Targets) == 0 {
		return nil, errors.New("directory, campaign ID, positive limits and targets are required")
	}
	targets := make(map[string]Account, len(config.Targets))
	for id, limits := range config.Targets {
		if !identifier.MatchString(id) || !validLimits(limits) {
			return nil, errors.New("target IDs and limits must be valid")
		}
		targets[id] = Account{Limits: limits}
	}
	config.Profile.VerifiedAt = config.Profile.VerifiedAt.UTC()
	path, err := filepath.Abs(config.Directory)
	if err != nil {
		return nil, err
	}
	if err = os.Mkdir(path, 0700); err != nil {
		return nil, fmt.Errorf("create exclusive budget directory: %w", err)
	}
	// 目录本身也需要先持久化，不能只同步内部快照。
	parent, err := os.Open(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	if err = errors.Join(parent.Sync(), parent.Close()); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	l := &Ledger{path: filepath.Join(path, "budget.json"), changed: make(chan struct{}), ctx: ctx, cancel: cancel,
		state: Snapshot{SchemaVersion: 1, CampaignID: config.CampaignID, Profile: config.Profile, Limits: config.Limits, Targets: targets, Calls: []Call{}, Rejections: []Rejection{}}}
	l.persist = func(s Snapshot) error { return writeSnapshot(l.path, s) }
	if err = l.commit(l.state); err != nil {
		return nil, err
	}
	return l, nil
}

func cloneSnapshot(s Snapshot) Snapshot {
	s.Targets = copyTargets(s.Targets)
	s.RecoveryIDs = append([]string(nil), s.RecoveryIDs...)
	s.Calls = append([]Call{}, s.Calls...)
	for i := range s.Calls {
		s.Calls[i].Usage = copyUsage(s.Calls[i].Usage)
	}
	s.Rejections = append([]Rejection{}, s.Rejections...)
	return s
}
func copyTargets(in map[string]Account) map[string]Account {
	out := make(map[string]Account, len(in))
	for k, v := range in {
		v.RunIDs = append([]string(nil), v.RunIDs...)
		out[k] = v
	}
	return out
}
func copyUsage(u *agent.Usage) *agent.Usage {
	if u == nil {
		return nil
	}
	v := *u
	if u.CacheHitTokens != nil {
		n := *u.CacheHitTokens
		v.CacheHitTokens = &n
	}
	if u.CacheMissTokens != nil {
		n := *u.CacheMissTokens
		v.CacheMissTokens = &n
	}
	return &v
}

// Snapshot 返回独立副本，不能借修改报告影响准入。
func (l *Ledger) Snapshot() Snapshot    { l.mu.Lock(); defer l.mu.Unlock(); return cloneSnapshot(l.state) }
func (l *Ledger) Done() <-chan struct{} { return l.ctx.Done() }
func (l *Ledger) Err() error            { l.mu.Lock(); defer l.mu.Unlock(); return l.fault }
func (l *Ledger) notify()               { close(l.changed); l.changed = make(chan struct{}) }
func (l *Ledger) halt(err error) {
	if l.fault == nil {
		l.fault = err
		l.cancel(err)
	}
	l.notify()
}

// commit 调用方持锁；先持久化候选状态，再发布内存状态。
func (l *Ledger) commit(next Snapshot) error {
	if l.state.Revision == math.MaxInt64 {
		err := fmt.Errorf("%w: ledger revision overflows", ErrIntegrity)
		l.halt(err)
		return err
	}
	next.Revision = l.state.Revision + 1
	if l.fault != nil {
		next.HaltReason = l.fault.Error()
	}
	if err := l.persist(next); err != nil {
		err = fmt.Errorf("%w: %w", ErrPersistence, err)
		l.halt(err)
		return err
	}
	l.state = next
	l.notify()
	return nil
}

func (l *Ledger) bind(target, run string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fault != nil {
		return errors.Join(ErrHalted, l.fault)
	}
	account, ok := l.state.Targets[target]
	if !ok || !identifier.MatchString(run) {
		return errors.New("registered target and valid run ID are required")
	}
	if account.RunID != "" {
		if account.RunID == run {
			return nil
		}
		return errors.New("target is already bound to another run")
	}
	next := cloneSnapshot(l.state)
	if next.SchemaVersion == 2 {
		if slices.Contains(account.RunIDs, run) {
			return errors.New("historical run cannot receive a new model binding")
		}
		account.RunIDs = append(account.RunIDs, run)
	}
	account.RunID = run
	next.Targets[target] = account
	return l.commit(next)
}

func available(t Totals, limits Limits, q int64) (bool, *Denied) {
	if t.Calls >= limits.MaxCalls {
		return false, &Denied{Resource: "calls"}
	}
	// 逐次相减，避免余额计算和相加溢出。
	free := limits.MaxTokens - t.KnownTokens - t.UnknownTokens
	if q > free {
		return false, &Denied{Resource: "tokens"}
	}
	return q <= free-t.ReservedTokens, nil
}
func (l *Ledger) admissible(w *waiter) (bool, *Denied) {
	q := l.state.Profile.ContextTokens + l.state.Profile.MaxOutputTokens
	a := l.state.Targets[w.target]
	targetOK, denied := available(a.Totals, a.Limits, q)
	if denied != nil {
		denied.Scope = "target"
		return false, denied
	}
	campaignOK, denied := available(l.state.Totals, l.state.Limits, q)
	if denied != nil {
		denied.Scope = "campaign"
		return false, denied
	}
	return targetOK && campaignOK, nil
}
func (l *Ledger) firstReady() *waiter {
	for _, w := range l.queue {
		if ok, _ := l.admissible(w); ok {
			return w
		}
	}
	return nil
}
func (l *Ledger) remove(w *waiter) {
	for i, v := range l.queue {
		if v == w {
			l.queue = append(l.queue[:i], l.queue[i+1:]...)
			break
		}
	}
	l.notify()
}

func (l *Ledger) reserve(ctx context.Context, target, run, input string) (Call, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	account, ok := l.state.Targets[target]
	if !ok || account.RunID != run || !validDigest(input) {
		return Call{}, errors.New("reservation identity is invalid")
	}
	w := &waiter{target, run, input}
	l.queue = append(l.queue, w)
	defer l.remove(w)
	for {
		if err := ctx.Err(); err != nil {
			return Call{}, err
		}
		if !l.deadline.IsZero() && !time.Now().Before(l.deadline) {
			return Call{}, context.DeadlineExceeded
		}
		if l.fault != nil {
			return Call{}, errors.Join(ErrHalted, l.fault)
		}
		ready, denied := l.admissible(w)
		if denied != nil {
			next := cloneSnapshot(l.state)
			next.Rejections = append(next.Rejections, Rejection{target, run, input, denied.Scope, denied.Resource, time.Now().UTC()})
			if err := l.commit(next); err != nil {
				return Call{}, err
			}
			return Call{}, denied
		}
		if ready && l.firstReady() == w {
			now := time.Now().UTC()
			q := l.state.Profile.ContextTokens + l.state.Profile.MaxOutputTokens
			call := Call{ID: fmt.Sprintf("call-%d", len(l.state.Calls)+1), TargetID: target, RunID: run, InputSHA256: input, ReservedTokens: q, State: StateReserved, CreatedAt: now, UpdatedAt: now}
			next := cloneSnapshot(l.state)
			next.Calls = append(next.Calls, call)
			next.Totals.Calls++
			next.Totals.ReservedTokens += q
			a := next.Targets[target]
			a.Totals.Calls++
			a.Totals.ReservedTokens += q
			next.Targets[target] = a
			if err := l.commit(next); err != nil {
				return Call{}, err
			}
			return call, nil
		}
		changed := l.changed
		l.mu.Unlock()
		select {
		case <-ctx.Done():
		case <-changed:
		}
		l.mu.Lock()
	}
}

func validUsage(u *agent.Usage, p Profile) bool {
	if u == nil {
		return true
	}
	if u.InputTokens < 0 || u.OutputTokens < 0 || u.TotalTokens < 0 || u.InputTokens > p.ContextTokens || u.OutputTokens > p.MaxOutputTokens || u.InputTokens > math.MaxInt64-u.OutputTokens || u.InputTokens+u.OutputTokens != u.TotalTokens {
		return false
	}
	if u.CacheHitTokens != nil && (*u.CacheHitTokens < 0 || *u.CacheHitTokens > u.InputTokens) {
		return false
	}
	if u.CacheMissTokens != nil && (*u.CacheMissTokens < 0 || *u.CacheMissTokens > u.InputTokens) {
		return false
	}
	if u.CacheHitTokens != nil && u.CacheMissTokens != nil && *u.CacheHitTokens != u.InputTokens-*u.CacheMissTokens {
		return false
	}
	return true
}

func (l *Ledger) settle(id string, usage *agent.Usage, outcome string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	index := -1
	for i, c := range l.state.Calls {
		if c.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return errors.New("unknown budget call ID")
	}
	before := l.state.Calls[index]
	if before.State != StateReserved {
		if reflect.DeepEqual(before.Usage, usage) && before.Outcome == outcome {
			return nil
		}
		err := fmt.Errorf("%w: conflicting settlement for %s", ErrIntegrity, id)
		l.halt(err)
		return errors.Join(err, l.commit(cloneSnapshot(l.state)))
	}
	next := cloneSnapshot(l.state)
	call := &next.Calls[index]
	call.Usage = copyUsage(usage)
	call.Outcome = outcome
	call.UpdatedAt = time.Now().UTC()
	call.State = StateSettled
	var integrity error
	if usage == nil {
		call.State = StateUnknown
	} else if !validUsage(usage, next.Profile) {
		call.State = StateInvalid
		integrity = fmt.Errorf("%w: invalid usage for %s", ErrIntegrity, id)
		next.HaltReason = integrity.Error()
	}
	account := next.Targets[call.TargetID]
	for _, total := range []*Totals{&next.Totals, &account.Totals} {
		total.ReservedTokens -= call.ReservedTokens
		if call.State == StateSettled {
			total.KnownTokens += usage.TotalTokens
		} else {
			total.UnknownTokens += call.ReservedTokens
		}
	}
	next.Targets[call.TargetID] = account
	if err := l.commit(next); err != nil {
		return errors.Join(integrity, err)
	}
	if integrity != nil {
		l.halt(integrity)
	}
	return integrity
}

func digestJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
