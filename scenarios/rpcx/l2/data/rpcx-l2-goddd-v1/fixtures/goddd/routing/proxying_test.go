package routing

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"example.com/rpcx"
	shipping "github.com/marcusolsson/goddd"
)

func TestRouteMapping(t *testing.T) {
	departure := time.Date(2026, 1, 1, 8, 0, 0, 0, time.UTC)
	arrival := departure.Add(24 * time.Hour)
	calls := 0
	t.Cleanup(rpcx.Register(t.Name(), func(_ context.Context, method string, request, response any) error {
		calls++
		if method != "fetch_routes" || request != (FetchRoutesRequest{From: "SESTO", To: "AUMEL"}) {
			t.Fatalf("unexpected request: %s %#v", method, request)
		}
		return json.Unmarshal([]byte(`{"paths":[{"edges":[
			{"origin":"SESTO","destination":"CNHKG","voyage":"001A","departure":"2026-01-01T08:00:00Z","arrival":"2026-01-02T08:00:00Z"},
			{"origin":"CNHKG","destination":"AUMEL","voyage":"002A","departure":"2026-01-02T08:00:00Z","arrival":"2026-01-03T08:00:00Z"}
		]},{"edges":[{"origin":"SESTO","destination":"AUMEL","voyage":"003A","departure":"2026-01-01T08:00:00Z","arrival":"2026-01-02T08:00:00Z"}]}]}`), response)
	}))
	service := NewProxyingMiddleware(context.Background(), t.Name())(nil)
	got := service.FetchRoutesForSpecification(shipping.RouteSpecification{Origin: shipping.SESTO, Destination: shipping.AUMEL})
	want := []shipping.Itinerary{
		{Legs: []shipping.Leg{
			{VoyageNumber: "001A", LoadLocation: "SESTO", UnloadLocation: "CNHKG", LoadTime: departure, UnloadTime: arrival},
			{VoyageNumber: "002A", LoadLocation: "CNHKG", UnloadLocation: "AUMEL", LoadTime: arrival, UnloadTime: arrival.Add(24 * time.Hour)},
		}},
		{Legs: []shipping.Leg{
			{VoyageNumber: "003A", LoadLocation: "SESTO", UnloadLocation: "AUMEL", LoadTime: departure, UnloadTime: arrival},
		}},
	}
	if calls != 1 || !reflect.DeepEqual(got, want) {
		t.Fatalf("calls=%d routes=%#v, want %#v", calls, got, want)
	}
}

func TestRemoteFailureReturnsEmptyRoutes(t *testing.T) {
	t.Cleanup(rpcx.Register(t.Name(), func(context.Context, string, any, any) error {
		return errors.New("routing unavailable")
	}))
	service := NewProxyingMiddleware(context.Background(), t.Name())(nil)
	got := service.FetchRoutesForSpecification(shipping.RouteSpecification{})
	if got == nil || len(got) != 0 {
		t.Fatalf("got %#v, want non-nil empty routes", got)
	}
}

func TestEmptyRemoteResponse(t *testing.T) {
	t.Cleanup(rpcx.Register(t.Name(), func(context.Context, string, any, any) error { return nil }))
	service := NewProxyingMiddleware(context.Background(), t.Name())(nil)
	if got := service.FetchRoutesForSpecification(shipping.RouteSpecification{}); got != nil {
		t.Fatalf("got %#v, want nil routes for empty response", got)
	}
}
