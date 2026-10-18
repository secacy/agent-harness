// Package rpcx 是用于迁移验证的内存 SDK 夹具。
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

// Register 注册内存后端并返回清理函数；存续期间地址必须唯一。
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

func Dial(addr string) (*Client, error) {
	backends.Lock()
	defer backends.Unlock()
	handler, ok := backends.handlers[addr]
	if !ok {
		return nil, fmt.Errorf("rpcx: unknown address %q", addr)
	}
	return &Client{handler: handler}, nil
}

func (c *Client) Call(ctx context.Context, method string, req Request) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.handler(ctx, method, req)
}
