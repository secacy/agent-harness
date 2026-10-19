// Package rpcx 提供 L2 场景使用的内存 RPC 客户端。
package rpcx

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Handler 接收业务请求并填充调用者提供的响应指针。
type Handler func(context.Context, string, any, any) error

var backends = struct {
	sync.Mutex
	handlers map[string]Handler
}{handlers: make(map[string]Handler)}

// Register 注册独立地址，返回可重复调用的清理函数。
func Register(addr string, handler Handler) func() {
	backends.Lock()
	defer backends.Unlock()
	if handler == nil || backends.handlers[addr] != nil {
		panic("rpcx: duplicate address or nil handler")
	}
	backends.handlers[addr] = handler
	var once sync.Once
	return func() {
		once.Do(func() {
			backends.Lock()
			defer backends.Unlock()
			delete(backends.handlers, addr)
		})
	}
}

// Client 绑定已注册的内存后端。
type Client struct{ handler Handler }

// NewClient 连接指定后端；调用默认带有 30 秒上限。
func NewClient(addr string) (*Client, error) {
	backends.Lock()
	defer backends.Unlock()
	handler := backends.handlers[addr]
	if handler == nil {
		return nil, fmt.Errorf("rpcx: unknown address %q", addr)
	}
	return &Client{handler: handler}, nil
}

// Call 为每次调用创建默认超时上下文，返回时释放相关资源。
func (c *Client) Call(method string, request, response any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return c.handler(ctx, method, request, response)
}
