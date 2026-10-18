package rpcx

import (
	"context"
	"errors"
	"testing"
)

func TestCallContext(t *testing.T) {
	type key struct{}
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "request"))
	defer cancel()
	called := false
	t.Cleanup(Register(t.Name(), func(got context.Context, method string, req Request) error {
		called = true
		if got.Value(key{}) != "request" || got.Done() != ctx.Done() || method != "reserve" || req.Quantity != 2 {
			t.Fatal("call did not preserve context or request")
		}
		cancel()
		return got.Err()
	}))
	c, err := Dial(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, "reserve", Request{Quantity: 2}); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if !called {
		t.Fatal("handler not called")
	}
}

func TestCallAlreadyCancelled(t *testing.T) {
	t.Cleanup(Register(t.Name(), func(context.Context, string, Request) error {
		t.Fatal("cancelled call reached backend")
		return nil
	}))
	c, err := Dial(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Call(ctx, "reserve", Request{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestUnknownAddress(t *testing.T) {
	if _, err := Dial("missing"); err == nil {
		t.Fatal("expected dial error")
	}
}
