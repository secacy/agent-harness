package budget

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Activate 独占创建恢复代次账本，继承检查点全部费用；调用方须持原 Campaign 写锁。
// 本函数不重开已有目录，也不读取凭证或授予付费调用权限。
func Activate(directory string, seed Snapshot, deadline time.Time) (*Ledger, error) {
	if err := ValidateSnapshot(seed); err != nil {
		return nil, err
	}
	if directory == "" || seed.SchemaVersion != 2 || seed.HaltReason != "" || seed.Totals.ReservedTokens != 0 || !deadline.After(time.Now()) {
		return nil, errors.New("activation requires a recovered non-halted snapshot and future deadline")
	}
	for _, account := range seed.Targets {
		if account.RunID != "" {
			return nil, errors.New("checkpoint still has an active run binding")
		}
	}
	path, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	if err = os.Mkdir(path, 0700); err != nil {
		return nil, err
	}
	parent, err := os.Open(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	if err = errors.Join(parent.Sync(), parent.Close()); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	l := &Ledger{path: filepath.Join(path, "budget.json"), state: cloneSnapshot(seed), changed: make(chan struct{}), ctx: ctx, cancel: cancel, deadline: deadline}
	l.persist = func(s Snapshot) error { return writeSnapshot(l.path, s) }
	if err = l.persist(l.state); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPersistence, err)
	}
	return l, nil
}

// BindRun 仅在恢复收据已持久化后使用；旧身份不能再次绑定。
func (l *Ledger) BindRun(target, run string) error { return l.bind(target, run) }

// CanAdmit 检查下一次调用是否可能准入，不预留、不写账或保证未来余额。
func CanAdmit(s Snapshot, target string) error {
	a, ok := s.Targets[target]
	if !ok {
		return errors.New("unregistered target")
	}
	q := s.Profile.ContextTokens + s.Profile.MaxOutputTokens
	for _, check := range []struct {
		totals Totals
		limits Limits
		scope  string
	}{{s.Totals, s.Limits, "campaign"}, {a.Totals, a.Limits, "target"}} {
		if ready, denied := available(check.totals, check.limits, q); !ready {
			if denied == nil {
				denied = &Denied{Resource: "tokens"}
			}
			denied.Scope = check.scope
			return denied
		}
	}
	return nil
}
