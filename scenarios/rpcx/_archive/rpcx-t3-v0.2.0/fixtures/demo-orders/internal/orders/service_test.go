package orders

import (
	"context"
	"errors"
	"testing"

	"example.com/demo-orders/internal/inventory"
	"example.com/rpcx"
)

func newService(t *testing.T, handler rpcx.Handler) *Service {
	t.Helper()
	t.Cleanup(rpcx.Register(t.Name(), handler))
	inv, err := inventory.New(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	return New(inv)
}

func TestPlace(t *testing.T) {
	s := newService(t, func(context.Context, string, rpcx.Request) error { return nil })
	if err := s.Place(context.Background(), "o1", "book", 2); err != nil {
		t.Fatal(err)
	}
	order, ok := s.Get("o1")
	if !ok || order.SKU != "book" || order.Quantity != 2 {
		t.Fatalf("unexpected order: %+v, %v", order, ok)
	}
	if err := s.Place(context.Background(), "o1", "book", 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate: got %v, want conflict", err)
	}
}

func TestPlaceFailure(t *testing.T) {
	want := errors.New("out of stock")
	s := newService(t, func(context.Context, string, rpcx.Request) error { return want })
	if err := s.Place(context.Background(), "o1", "book", 2); !errors.Is(err, want) {
		t.Fatalf("got %v", err)
	}
	if _, ok := s.Get("o1"); ok {
		t.Fatal("failed order was stored")
	}
}

func TestCancel(t *testing.T) {
	released := false
	s := newService(t, func(_ context.Context, method string, req rpcx.Request) error {
		if method == "Inventory.Release" {
			released = true
			if req.SKU != "book" || req.Quantity != 2 {
				t.Fatalf("unexpected release: %+v", req)
			}
		}
		return nil
	})
	if err := s.Place(context.Background(), "o1", "book", 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel("o1"); err != nil {
		t.Fatal(err)
	}
	if !released {
		t.Fatal("inventory not released")
	}
	if _, ok := s.Get("o1"); ok {
		t.Fatal("cancelled order remains")
	}
	if err := s.Cancel("o1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestCancelFailure(t *testing.T) {
	want := errors.New("inventory unavailable")
	s := newService(t, func(_ context.Context, method string, _ rpcx.Request) error {
		if method == "Inventory.Release" {
			return want
		}
		return nil
	})
	if err := s.Place(context.Background(), "o1", "book", 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel("o1"); !errors.Is(err, want) {
		t.Fatalf("got %v", err)
	}
	if _, ok := s.Get("o1"); !ok {
		t.Fatal("failed cancellation removed order")
	}
}
