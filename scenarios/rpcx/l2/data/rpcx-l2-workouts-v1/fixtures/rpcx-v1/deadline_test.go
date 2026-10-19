package rpcx

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDefaultDeadlineAndRelease(t *testing.T) {
	var observed []context.Context
	want := errors.New("backend failure")
	started := time.Now()
	t.Cleanup(Register(t.Name(), func(ctx context.Context, _ string, _, _ any) error {
		deadline, ok := ctx.Deadline()
		if !ok || deadline.Before(started.Add(30*time.Second)) || deadline.After(time.Now().Add(30*time.Second)) {
			t.Fatalf("unexpected default deadline: %v, %v", deadline, ok)
		}
		if ctx.Err() != nil {
			t.Fatal("call reused a cancelled context")
		}
		observed = append(observed, ctx)
		if len(observed) == 2 {
			return want
		}
		return nil
	}))
	client, err := NewClient(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Call("routes", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := client.Call("routes", nil, nil); !errors.Is(err, want) {
		t.Fatalf("got %v", err)
	}
	for _, ctx := range observed {
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatal("call context was not released")
		}
	}
}
