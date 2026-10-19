package rpcx

import (
	"context"
	"errors"
	"testing"
)

func TestRequestResponseAndError(t *testing.T) {
	type query struct{ Text string }
	type result struct{ Text string }
	want := errors.New("backend failure")
	t.Cleanup(Register(t.Name(), func(_ context.Context, method string, request, response any) error {
		if method != "translate" || request.(query).Text != "hello" {
			t.Fatal("request changed")
		}
		response.(*result).Text = "你好"
		return want
	}))
	client, err := Dial(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	var response result
	if err := client.Call(context.Background(), "translate", query{Text: "hello"}, &response); !errors.Is(err, want) {
		t.Fatalf("got %v, want backend error", err)
	}
	if response.Text != "你好" {
		t.Fatal("response was not filled")
	}
}

func TestUnknownAddress(t *testing.T) {
	if _, err := Dial("missing"); err == nil {
		t.Fatal("expected unknown address error")
	}
}

func TestRegistration(t *testing.T) {
	handler := func(context.Context, string, any, any) error { return nil }
	cleanup := Register(t.Name(), handler)
	t.Cleanup(cleanup)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("duplicate address accepted")
			}
		}()
		Register(t.Name(), handler)
	}()
	cleanup()
	t.Cleanup(Register(t.Name(), handler))
	cleanup()
	if _, err := Dial(t.Name()); err != nil {
		t.Fatal("repeated cleanup removed a new registration")
	}
}

func TestNilHandler(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("nil handler accepted")
		}
	}()
	Register(t.Name(), nil)
}
