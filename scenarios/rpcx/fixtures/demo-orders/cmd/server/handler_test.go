package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"example.com/demo-orders/internal/inventory"
	"example.com/demo-orders/internal/orders"
	"example.com/rpcx"
)

func newHandler(t *testing.T) *Handler {
	t.Helper()
	t.Cleanup(rpcx.Register(t.Name(), func(context.Context, string, rpcx.Request) error { return nil }))
	inv, err := inventory.New(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	return &Handler{service: orders.New(inv)}
}

func TestPlace(t *testing.T) {
	h := newHandler(t)
	w := httptest.NewRecorder()
	h.Place(w, httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(`{"ID":"o1","SKU":"book","Quantity":2}`)))
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if _, ok := h.service.Get("o1"); !ok {
		t.Fatal("order missing")
	}
}

func TestCancel(t *testing.T) {
	h := newHandler(t)
	if err := h.service.Place(context.Background(), "o1", "book", 2); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.Cancel(w, httptest.NewRequest(http.MethodDelete, "/orders?id=o1", nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if _, ok := h.service.Get("o1"); ok {
		t.Fatal("order remains")
	}
}

func TestBadRequest(t *testing.T) {
	h := newHandler(t)
	for _, body := range []string{"{", `{"ID":"o1","Quantity":0}`} {
		w := httptest.NewRecorder()
		h.Place(w, httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(body)))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status %d", w.Code)
		}
	}
}

func TestNotFound(t *testing.T) {
	h := newHandler(t)
	w := httptest.NewRecorder()
	h.Cancel(w, httptest.NewRequest(http.MethodDelete, "/orders?id=missing", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status %d", w.Code)
	}
}
