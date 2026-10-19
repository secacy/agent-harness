package execution

import (
	"context"
	"errors"
)

// Limited 在实际启动命令前取槽，底层完成退出与回收后才释放。
type Limited struct {
	backend Executor
	slots   chan struct{}
}

func NewLimited(backend Executor, concurrency int) (*Limited, error) {
	if backend == nil || concurrency < 1 {
		return nil, errors.New("backend and positive execution concurrency are required")
	}
	return &Limited{backend: backend, slots: make(chan struct{}, concurrency)}, nil
}

func (e *Limited) Info() Info { return e.backend.Info() }

func (e *Limited) Run(ctx context.Context, request Request) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	select {
	case e.slots <- struct{}{}:
		defer func() { <-e.slots }()
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return e.backend.Run(ctx, request)
}
