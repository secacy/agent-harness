package orders

import (
	"context"
	"errors"
	"sync"

	"example.com/demo-orders/internal/inventory"
)

var ErrNotFound = errors.New("order not found")
var ErrInvalid = errors.New("invalid order")
var ErrConflict = errors.New("order already exists")

type Order struct {
	ID       string
	SKU      string
	Quantity int
}

type Service struct {
	mu     sync.Mutex
	inv    *inventory.Client
	orders map[string]Order
}

func New(inv *inventory.Client) *Service {
	return &Service{inv: inv, orders: make(map[string]Order)}
}

func (s *Service) Place(ctx context.Context, id, sku string, n int) error {
	if id == "" || sku == "" || n <= 0 {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.orders[id]; ok {
		return ErrConflict
	}
	if err := s.inv.Reserve(sku, n); err != nil {
		return err
	}
	s.orders[id] = Order{ID: id, SKU: sku, Quantity: n}
	return nil
}

func (s *Service) Cancel(orderID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	order, ok := s.orders[orderID]
	if !ok {
		return ErrNotFound
	}
	if err := s.inv.Release(order.SKU, order.Quantity); err != nil {
		return err
	}
	delete(s.orders, orderID)
	return nil
}

func (s *Service) Get(id string) (Order, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	order, ok := s.orders[id]
	return order, ok
}
