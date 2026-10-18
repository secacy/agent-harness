package inventory

import (
	"fmt"

	"example.com/rpcx"
)

type Client struct{ rpc *rpcx.Client }

func New(addr string) (*Client, error) {
	c, err := rpcx.NewClient(addr)
	if err != nil {
		return nil, err
	}
	return &Client{rpc: c}, nil
}

func (c *Client) Reserve(sku string, n int) error {
	if err := c.rpc.Call("Inventory.Reserve", rpcx.Request{SKU: sku, Quantity: n}); err != nil {
		return fmt.Errorf("reserve inventory: %w", err)
	}
	return nil
}

func (c *Client) Release(sku string, n int) error {
	if err := c.rpc.Call("Inventory.Release", rpcx.Request{SKU: sku, Quantity: n}); err != nil {
		return fmt.Errorf("release inventory: %w", err)
	}
	return nil
}
