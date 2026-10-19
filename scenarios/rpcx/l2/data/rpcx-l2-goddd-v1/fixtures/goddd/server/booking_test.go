package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"example.com/rpcx"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/metrics/discard"
	shipping "github.com/marcusolsson/goddd"
	"github.com/marcusolsson/goddd/booking"
	"github.com/marcusolsson/goddd/inmem"
	"github.com/marcusolsson/goddd/routing"
)

func TestBookingRoutesThroughRPC(t *testing.T) {
	for _, mode := range []string{"success", "unknown-cargo", "remote-failure"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			t.Cleanup(rpcx.Register(t.Name(), func(ctx context.Context, method string, request, response any) error {
				calls++
				if method != "fetch_routes" || request != (routing.FetchRoutesRequest{From: "SESTO", To: "AUMEL"}) {
					t.Fatalf("unexpected request: %s %#v", method, request)
				}
				if mode == "remote-failure" {
					return errors.New("routing unavailable")
				}
				return json.Unmarshal([]byte(`{"paths":[{"edges":[{"origin":"SESTO","destination":"AUMEL","voyage":"001A"}]}]}`), response)
			}))
			rs := routing.NewProxyingMiddleware(context.Background(), t.Name())(nil)
			service := booking.NewService(inmem.NewCargoRepository(), inmem.NewLocationRepository(), inmem.NewHandlingEventRepository(), rs)
			service = booking.NewLoggingService(log.NewNopLogger(), service)
			service = booking.NewInstrumentingService(discard.NewCounter(), discard.NewHistogram(), service)
			id, err := service.BookNewCargo(shipping.SESTO, shipping.AUMEL, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			if mode == "unknown-cargo" {
				id = "missing"
			}
			handler := New(service, log.NewNopLogger())
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/booking/v1/cargos/"+string(id)+"/request_routes", nil)
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body)
			}
			var response struct {
				Routes []shipping.Itinerary `json:"routes"`
			}
			if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
				t.Fatal(err)
			}
			wantCalls := 1
			if mode == "unknown-cargo" {
				wantCalls = 0
			}
			if calls != wantCalls {
				t.Fatalf("calls=%d, want %d", calls, wantCalls)
			}
			if mode == "success" {
				if len(response.Routes) != 1 || len(response.Routes[0].Legs) != 1 {
					t.Fatalf("routes=%#v", response.Routes)
				}
				leg := response.Routes[0].Legs[0]
				if leg.VoyageNumber != "001A" || leg.LoadLocation != shipping.SESTO || leg.UnloadLocation != shipping.AUMEL {
					t.Fatalf("route changed: %#v", leg)
				}
			} else if len(response.Routes) != 0 {
				t.Fatalf("got routes on %s: %#v", mode, response.Routes)
			}
		})
	}
}
