package inventory

import (
	"context"
	"testing"

	"example.com/rpcx"
)

func TestReserve(t *testing.T) {
	called := false
	t.Cleanup(rpcx.Register(t.Name(), func(_ context.Context, method string, req rpcx.Request) error {
		called = true
		if method != "Inventory.Reserve" || req.SKU != "book" || req.Quantity != 2 {
			t.Fatalf("unexpected RPC: %s %+v", method, req)
		}
		return nil
	}))
	c, err := New(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Reserve("book", 2); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("RPC not called")
	}
}

func TestRelease(t *testing.T) {
	called := false
	t.Cleanup(rpcx.Register(t.Name(), func(_ context.Context, method string, req rpcx.Request) error {
		called = true
		if method != "Inventory.Release" || req.SKU != "book" || req.Quantity != 2 {
			t.Fatalf("unexpected RPC: %s %+v", method, req)
		}
		return nil
	}))
	c, err := New(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Release("book", 2); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("RPC not called")
	}
}
