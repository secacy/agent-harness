package rpcx

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDefaultDeadlineAndRelease(t *testing.T) {
	var observed context.Context
	started := time.Now()
	t.Cleanup(Register(t.Name(), func(ctx context.Context, _ string, _ Request) error {
		observed = ctx
		deadline, ok := ctx.Deadline()
		if !ok || deadline.Before(started.Add(30*time.Second)) || deadline.After(time.Now().Add(30*time.Second)) {
			t.Fatalf("unexpected default deadline: %v, %v", deadline, ok)
		}
		return nil
	}))
	c, err := NewClient(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Call("reserve", Request{}); err != nil {
		t.Fatal(err)
	}
	if observed == nil || !errors.Is(observed.Err(), context.Canceled) {
		t.Fatal("call context was not released")
	}
}
