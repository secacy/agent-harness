// Package rpcx is an in-memory SDK fixture, not a production RPC implementation.
package rpcx

import (
	"context"
	"fmt"
	"sync"
)

type Request struct {
	SKU      string
	Quantity int
}

type Handler func(context.Context, string, Request) error

var backends = struct {
	sync.Mutex
	handlers map[string]Handler
}{handlers: make(map[string]Handler)}

// Register installs an in-memory backend and returns its cleanup function.
// Addresses must be unique while registered.
func Register(addr string, handler Handler) func() {
	backends.Lock()
	defer backends.Unlock()
	if _, exists := backends.handlers[addr]; exists || handler == nil {
		panic("rpcx: duplicate address or nil handler")
	}
	backends.handlers[addr] = handler
	return func() {
		backends.Lock()
		defer backends.Unlock()
		delete(backends.handlers, addr)
	}
}

type Client struct{ handler Handler }

func NewClient(addr string) (*Client, error) {
	backends.Lock()
	defer backends.Unlock()
	handler, ok := backends.handlers[addr]
	if !ok {
		return nil, fmt.Errorf("rpcx: unknown address %q", addr)
	}
	return &Client{handler: handler}, nil
}

func (c *Client) Call(method string, req Request) error {
	return c.handler(context.Background(), method, req)
}
