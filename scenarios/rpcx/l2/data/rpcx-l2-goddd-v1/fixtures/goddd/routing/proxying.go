package routing

import (
	"context"
	"time"

	"example.com/rpcx"
	shipping "github.com/marcusolsson/goddd"
)

type proxyService struct {
	context.Context
	client *rpcx.Client
	shipping.RoutingService
}

func (s proxyService) FetchRoutesForSpecification(rs shipping.RouteSpecification) []shipping.Itinerary {
	var resp FetchRoutesResponse
	err := s.client.Call("fetch_routes", FetchRoutesRequest{
		From: string(rs.Origin),
		To:   string(rs.Destination),
	}, &resp)
	if err != nil {
		return []shipping.Itinerary{}
	}

	var itineraries []shipping.Itinerary
	for _, r := range resp.Paths {
		var legs []shipping.Leg
		for _, e := range r.Edges {
			legs = append(legs, shipping.Leg{
				VoyageNumber:   shipping.VoyageNumber(e.Voyage),
				LoadLocation:   shipping.UNLocode(e.Origin),
				UnloadLocation: shipping.UNLocode(e.Destination),
				LoadTime:       e.Departure,
				UnloadTime:     e.Arrival,
			})
		}

		itineraries = append(itineraries, shipping.Itinerary{Legs: legs})
	}

	return itineraries
}

// ServiceMiddleware 包装业务路由服务。
type ServiceMiddleware func(shipping.RoutingService) shipping.RoutingService

// NewProxyingMiddleware 使用本地 RPC 替代原 HTTP 与熔断传输。
func NewProxyingMiddleware(ctx context.Context, addr string) ServiceMiddleware {
	client, err := rpcx.NewClient(addr)
	if err != nil {
		panic(err)
	}
	return func(next shipping.RoutingService) shipping.RoutingService {
		return proxyService{Context: ctx, client: client, RoutingService: next}
	}
}

// FetchRoutesRequest 保留上游远程路由的起点和终点字段。
type FetchRoutesRequest struct {
	From string
	To   string
}

// FetchRoutesResponse 保留上游路线响应的数据结构。
type FetchRoutesResponse struct {
	Paths []struct {
		Edges []struct {
			Origin      string    `json:"origin"`
			Destination string    `json:"destination"`
			Voyage      string    `json:"voyage"`
			Departure   time.Time `json:"departure"`
			Arrival     time.Time `json:"arrival"`
		} `json:"edges"`
	} `json:"paths"`
}
