package rpcx

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCallerContext(t *testing.T) {
	for _, mode := range []string{"no-deadline", "short-deadline", "long-deadline"} {
		t.Run(mode, func(t *testing.T) {
			type key struct{}
			ctx := context.WithValue(context.Background(), key{}, "request-id")
			switch mode {
			case "short-deadline":
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Second)
				defer cancel()
			case "long-deadline":
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, time.Hour)
				defer cancel()
			}
			called := false
			t.Cleanup(Register(t.Name(), func(got context.Context, _ string, _, _ any) error {
				called = true
				if got != ctx || got.Value(key{}) != "request-id" {
					t.Fatal("caller context was replaced")
				}
				return nil
			}))
			client, err := Dial(t.Name())
			if err != nil {
				t.Fatal(err)
			}
			if err := client.Call(ctx, "routes", nil, nil); err != nil {
				t.Fatal(err)
			}
			if !called || ctx.Err() != nil {
				t.Fatal("backend not called or caller context was cancelled")
			}
		})
	}
}

func TestCancelledParent(t *testing.T) {
	t.Cleanup(Register(t.Name(), func(context.Context, string, any, any) error {
		t.Fatal("cancelled request reached backend")
		return nil
	}))
	client, err := Dial(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, release := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer release()
	for _, ctx := range []context.Context{cancelled, expired} {
		if err := client.Call(ctx, "routes", nil, nil); !errors.Is(err, ctx.Err()) {
			t.Fatalf("got %v, want %v", err, ctx.Err())
		}
	}
}

func TestCancellationInBackend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	t.Cleanup(Register(t.Name(), func(got context.Context, _ string, _, _ any) error {
		cancel()
		return got.Err()
	}))
	client, err := Dial(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Call(ctx, "routes", nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}
