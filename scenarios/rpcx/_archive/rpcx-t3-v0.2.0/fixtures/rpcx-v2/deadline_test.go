package rpcx

import (
	"context"
	"testing"
)

func TestNoImplicitDeadline(t *testing.T) {
	called := false
	t.Cleanup(Register(t.Name(), func(ctx context.Context, _ string, _ Request) error {
		called = true
		if _, ok := ctx.Deadline(); ok {
			t.Fatal("v2 unexpectedly supplied a deadline")
		}
		return nil
	}))
	c, err := Dial(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Call(context.Background(), "reserve", Request{}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("backend was not called")
	}
}
