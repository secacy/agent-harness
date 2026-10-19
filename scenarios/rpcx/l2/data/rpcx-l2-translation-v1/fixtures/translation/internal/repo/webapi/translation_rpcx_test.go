package webapi

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"example.com/rpcx"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/usecase/translation"
)

func TestSelectedAdapter(t *testing.T) {
	if adapterVariant != "default" && adapterVariant != "rpcx_alternate" {
		t.Fatal("unknown adapter variant")
	}
	t.Log("RPC_ADAPTER", adapterVariant)
}

func TestTranslationRoundTrip(t *testing.T) {
	input := entity.Translation{Source: "en", Destination: "de", Original: "hello"}
	var observed context.Context
	t.Cleanup(rpcx.Register(t.Name(), func(ctx context.Context, method string, request, response any) error {
		observed = ctx
		if method != "translate" || request != (translateRequest{Text: "hello", Source: "en", Destination: "de"}) {
			t.Fatal("request mapping changed")
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) < 29*time.Second || time.Until(deadline) > 30*time.Second {
			t.Fatal("v1 default deadline missing")
		}
		value, ok := response.(*translateResponse)
		if !ok {
			t.Fatal("unexpected response type")
		}
		value.Text = "hallo"
		return nil
	}))
	api, err := New(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	result, err := api.Translate(context.Background(), input)
	want := input
	want.Translation = "hallo"
	if err != nil || result != want || observed == nil || !errors.Is(observed.Err(), context.Canceled) {
		t.Fatal("response mapping or resource release changed", result, err)
	}
}

func TestTranslationErrors(t *testing.T) {
	if _, err := New(t.Name() + "-missing"); err == nil {
		t.Fatal("missing backend accepted")
	}
	remote := errors.New("translation unavailable")
	calls := 0
	t.Cleanup(rpcx.Register(t.Name(), func(context.Context, string, any, any) error { calls++; return remote }))
	api, err := New(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"remote", "cancelled", "expired"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			expected := remote
			if mode == "cancelled" {
				cancel()
				expected = context.Canceled
			}
			if mode == "expired" {
				var release context.CancelFunc
				ctx, release = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer release()
				expected = context.DeadlineExceeded
			}
			before := calls
			got, err := api.Translate(ctx, entity.Translation{Original: "hello"})
			if got != (entity.Translation{}) || !errors.Is(err, expected) {
				t.Fatal("error chain changed", got, err)
			}
			want := before
			if mode == "remote" {
				want++
			}
			if calls != want {
				t.Fatal("cancel precheck did not prevent RPC")
			}
		})
	}
}

type recordingRepo struct {
	events *[]string
	stored entity.Translation
	err    error
}

func (r *recordingRepo) Store(_ context.Context, _ string, t entity.Translation) error {
	*r.events = append(*r.events, "store")
	r.stored = t
	return r.err
}
func (*recordingRepo) GetHistory(context.Context, string) ([]entity.Translation, error) {
	return nil, nil
}

func TestUseCaseThroughRPC(t *testing.T) {
	for _, mode := range []string{"success", "remote-error", "store-error", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			var events []string
			failure := errors.New("injected failure")
			t.Cleanup(rpcx.Register(t.Name(), func(_ context.Context, _ string, _ any, response any) error {
				events = append(events, "rpc")
				if mode == "remote-error" {
					return failure
				}
				response.(*translateResponse).Text = "hallo"
				return nil
			}))
			api, err := New(t.Name())
			if err != nil {
				t.Fatal(err)
			}
			repo := &recordingRepo{events: &events}
			if mode == "store-error" {
				repo.err = failure
			}
			uc := translation.New(repo, api)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			result, err := uc.Translate(ctx, "user-1", entity.Translation{Source: "en", Destination: "de", Original: "hello"})
			want := []string{"rpc", "store"}
			switch mode {
			case "success":
				if err != nil || result.Translation != "hallo" || repo.stored != result {
					t.Fatal("successful result not stored")
				}
			case "cancelled":
				want = nil
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation chain changed", err)
				}
			case "remote-error":
				want = []string{"rpc"}
				if !errors.Is(err, failure) {
					t.Fatal("remote failure lost", err)
				}
			case "store-error":
				if !errors.Is(err, failure) {
					t.Fatal("store failure lost", err)
				}
			}
			if mode != "success" && result != (entity.Translation{}) {
				t.Fatal("error returned successful result")
			}
			if !reflect.DeepEqual(events, want) {
				t.Fatal("side effect order changed", events, want)
			}
		})
	}
}
