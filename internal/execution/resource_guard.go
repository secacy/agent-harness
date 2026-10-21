package execution

import (
	"context"
	"errors"
	"sync"
)

// ResourceGuard 在任一回收结果未知后关闭本代新命令准入，避免释放槽位后继续堆积容器。
// 已经在途的命令仍各自退出和回收；它不替代停止后的实时检查。
type ResourceGuard struct {
	Executor
	mu    sync.Mutex
	fault error
}

func NewResourceGuard(backend Executor) *ResourceGuard { return &ResourceGuard{Executor: backend} }
func (e *ResourceGuard) Run(ctx context.Context, r Request) (Result, error) {
	e.mu.Lock()
	fault := e.fault
	e.mu.Unlock()
	if fault != nil {
		return Result{ExitCode: -1}, errors.Join(ErrResourcesUnknown, fault)
	}
	if r.Owner == nil {
		return Result{ExitCode: -1}, errors.New("tracked execution requires a bound Run owner")
	}
	result, err := e.Executor.Run(ctx, r)
	if errors.Is(err, ErrResourcesUnknown) {
		e.mu.Lock()
		e.fault = errors.Join(e.fault, err)
		e.mu.Unlock()
	}
	return result, err
}
