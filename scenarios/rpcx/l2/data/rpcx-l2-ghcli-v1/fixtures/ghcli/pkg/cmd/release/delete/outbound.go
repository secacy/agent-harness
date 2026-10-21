package delete

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"

	rpcx "example.com/rpcx"
)

type outboundCaller interface {
	Call(string, any, any) error
}

type outboundRequest struct {
	Client  *http.Client
	Request *http.Request
}

type outboundResponse struct{ Response *http.Response }

var outboundSequence atomic.Uint64

// invokeHTTP changes only dispatch; the caller retains response handling and ownership.
func invokeHTTP(client *http.Client, request *http.Request) (*http.Response, error) {
	address := fmt.Sprintf("cli-outbound-%d", outboundSequence.Add(1))
	unregister := rpcx.Register(address, dispatchHTTP)
	defer unregister()
	clientRPC, err := rpcx.NewClient(address)
	if err != nil {
		return nil, err
	}
	var caller outboundCaller = clientRPC
	var response outboundResponse
	err = caller.Call("http.do", outboundRequest{Client: client, Request: request}, &response)
	return response.Response, err
}

func dispatchHTTP(ctx context.Context, method string, request, response any) error {
	if method != "http.do" {
		return fmt.Errorf("unsupported outbound operation %q", method)
	}
	in, ok := request.(outboundRequest)
	if !ok {
		return fmt.Errorf("invalid outbound request")
	}
	out, ok := response.(*outboundResponse)
	if !ok {
		return fmt.Errorf("invalid outbound response")
	}
	parent := in.Request.Context()
	var cancel context.CancelFunc
	// RPC call cancellation ends before the caller consumes the response body.
	// Carry the RPC deadline onto the original parent instead of its call lifetime.
	if deadline, hasDeadline := ctx.Deadline(); hasDeadline {
		parent, cancel = context.WithDeadline(parent, deadline)
	} else {
		parent, cancel = context.WithCancel(parent)
	}
	resp, err := in.Client.Do(in.Request.WithContext(parent))
	out.Response = resp
	if err != nil {
		cancel()
		return err
	}
	if resp.Body == nil {
		cancel()
		return nil
	}
	resp.Body = &cancelBody{ReadCloser: resp.Body, cancel: cancel}
	return nil
}

type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (body *cancelBody) Close() error {
	defer body.cancel()
	return body.ReadCloser.Close()
}
