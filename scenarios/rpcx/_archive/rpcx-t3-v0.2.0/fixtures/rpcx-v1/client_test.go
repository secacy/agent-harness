package rpcx

import (
	"context"
	"errors"
	"testing"
)

func TestCall(t *testing.T) {
	want := errors.New("backend failure")
	called := false
	t.Cleanup(Register(t.Name(), func(_ context.Context, method string, req Request) error {
		called = true
		if method != "reserve" || req.SKU != "book" || req.Quantity != 2 {
			t.Fatal("request changed")
		}
		return want
	}))
	c, err := NewClient(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Call("reserve", Request{SKU: "book", Quantity: 2}); !errors.Is(err, want) {
		t.Fatalf("got %v", err)
	}
	if !called {
		t.Fatal("handler not called")
	}
}

func TestUnknownAddress(t *testing.T) {
	if _, err := NewClient("missing"); err == nil {
		t.Fatal("expected client error")
	}
}
