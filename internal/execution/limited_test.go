package execution

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type executorFunc func(context.Context, Request) (Result, error)

func (f executorFunc) Info() Info                                           { return Info{Kind: KindLocal} }
func (f executorFunc) Run(ctx context.Context, req Request) (Result, error) { return f(ctx, req) }

func TestLimitedBoundsCommandsAndWaitsForCleanup(t *testing.T) {
	for _, limit := range []int{1, 2, 3} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			entered, release := make(chan struct{}, 9), make(chan struct{})
			var active, peak atomic.Int32
			backend := executorFunc(func(ctx context.Context, req Request) (Result, error) {
				n := active.Add(1)
				for old := peak.Load(); old < n && !peak.CompareAndSwap(old, n); old = peak.Load() {
				}
				entered <- struct{}{}
				// 代替底层尚在执行回收的阶段，返回前仍占槽。
				select {
				case <-release:
				case <-ctx.Done():
				}
				active.Add(-1)
				return Result{ExitCode: 7}, nil
			})
			limited, err := NewLimited(backend, limit)
			if err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			for range 9 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					result, err := limited.Run(ctx, Request{})
					if err != nil || result.ExitCode != 7 {
						t.Errorf("result=%+v err=%v", result, err)
					}
				}()
			}
			for range limit {
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("slots were not filled")
				}
			}
			if active.Load() != int32(limit) {
				t.Fatal(active.Load())
			}
			waiting, stop := context.WithCancel(ctx)
			stop()
			if _, err := limited.Run(waiting, Request{}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			close(release)
			wg.Wait()
			if peak.Load() != int32(limit) || active.Load() != 0 || limited.Info() != backend.Info() {
				t.Fatalf("peak=%d active=%d", peak.Load(), active.Load())
			}
		})
	}
}

func TestLimitedWaitingCancellationDoesNotStartBackend(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	limited, _ := NewLimited(executorFunc(func(context.Context, Request) (Result, error) {
		calls.Add(1)
		close(entered)
		<-release
		return Result{}, nil
	}), 1)
	finished := make(chan struct{})
	go func() { limited.Run(ctx, Request{}); close(finished) }()
	<-entered
	waiting, stop := context.WithCancel(ctx)
	go stop()
	if _, err := limited.Run(waiting, Request{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	<-finished
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
	if _, err := NewLimited(nil, 1); err == nil {
		t.Fatal("nil backend accepted")
	}
	if _, err := NewLimited(Local{}, 0); err == nil {
		t.Fatal("zero concurrency accepted")
	}
}
