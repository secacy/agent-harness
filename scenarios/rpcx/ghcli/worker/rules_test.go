package main

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"
)

type stubImporter struct{ types.Importer }

func (s stubImporter) Import(path string) (*types.Package, error) {
	if path != "example.com/rpcx/v2" {
		return s.Importer.Import(path)
	}
	fs := token.NewFileSet()
	f, _ := parser.ParseFile(fs, "sdk.go", `package rpcx;import "context";type Client struct{};func(*Client)Call(context.Context,string,any,any)error{return nil};func Dial(string)(*Client,error){return nil,nil}`, 0)
	return (&types.Config{Importer: s.Importer}).Check(path, fs, []*ast.File{f}, nil)
}

func TestTypedBoundedContextRules(t *testing.T) {
	for _, tc := range []struct {
		name, setup, parent, duration, release, callContext, receiver string
		wantStatus, wantReason                                        string
	}{
		{name: "bounded", wantStatus: "pass", wantReason: "typed_request_parent_30s_with_release"},
		{name: "background", parent: "context.Background()", wantStatus: "fail", wantReason: "request_parent_not_preserved"},
		{name: "todo", parent: "context.TODO()", wantStatus: "fail", wantReason: "request_parent_not_preserved"},
		{name: "without-cancel", parent: "context.WithoutCancel(request.Context())", wantStatus: "fail", wantReason: "request_parent_not_preserved"},
		{name: "wrong-duration", duration: "31*time.Second", wantStatus: "fail", wantReason: "per_call_maximum_not_30s"},
		{name: "request-alias", setup: "parent := request.Context()", parent: "parent", wantStatus: "pass", wantReason: "typed_request_parent_30s_with_release"},
		{name: "request-alias-nil-fallback", setup: "parent := request.Context(); if parent == nil { parent = context.Background() }", parent: "parent", wantStatus: "pass", wantReason: "typed_request_parent_30s_with_release"},
		{name: "background-alias", setup: "parent := context.Background()", parent: "parent", wantStatus: "fail", wantReason: "request_parent_not_preserved"},
		{name: "helper-parent", parent: "identity(request.Context())", wantStatus: "unknown", wantReason: "request_parent_unproven"},
		{name: "other-request", setup: "other := &http.Request{}", parent: "other.Context()", wantStatus: "unknown", wantReason: "request_parent_unproven"},
		{name: "same-name-context-method", setup: "other := requestLike{}", parent: "other.Context()", wantStatus: "unknown", wantReason: "request_parent_unproven"},
		{name: "same-name-background-function", parent: "pretend.Background()", wantStatus: "unknown", wantReason: "request_parent_unproven"},
		{name: "unknown-parent-wrong-duration", setup: "parent := identity(request.Context())", parent: "parent", duration: "31*time.Second", wantStatus: "fail", wantReason: "per_call_maximum_not_30s"},
		{name: "alias-parent-wrong-duration", setup: "parent := request.Context()", parent: "parent", duration: "31*time.Second", wantStatus: "fail", wantReason: "per_call_maximum_not_30s"},
		{name: "alias-parent-nonconstant-duration", setup: "parent := request.Context(); duration := 30*time.Second", parent: "parent", duration: "duration", wantStatus: "unknown", wantReason: "timeout_unproven"},
		{name: "missing-release", release: "_ = cancel", wantStatus: "unknown", wantReason: "unsupported_local_flow"},
		{name: "helper-call-context", callContext: "identity(ctx)", wantStatus: "unknown", wantReason: "unsupported_local_flow"},
		{name: "reassigned-receiver", receiver: "caller = client", wantStatus: "unknown", wantReason: "unsupported_local_flow"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.parent == "" {
				tc.parent = "request.Context()"
			}
			if tc.duration == "" {
				tc.duration = "30*time.Second"
			}
			if tc.release == "" {
				tc.release = "defer cancel()"
			}
			if tc.callContext == "" {
				tc.callContext = "ctx"
			}
			source := `package demo
import("context";"net/http";"time";rpcx "example.com/rpcx/v2")
type outboundCaller interface{Call(context.Context,string,any,any)error}
func identity(c context.Context)context.Context{return c}
type requestLike struct{}
func(requestLike)Context()context.Context{return context.Background()}
type contextLike struct{}
func(contextLike)Background()context.Context{return context.Background()}
var pretend contextLike
func invokeHTTP(request *http.Request)error{
client,_:=rpcx.Dial("address");var caller outboundCaller=client;` + tc.receiver + `;
` + tc.setup + `;
ctx,cancel:=context.WithTimeout(` + tc.parent + `,` + tc.duration + `);` + tc.release + `;
return caller.Call(` + tc.callContext + `,"http.do",nil,nil)}`
			fs := token.NewFileSet()
			f, err := parser.ParseFile(fs, "/module/boundary.go", source, 0)
			if err != nil {
				t.Fatal(err)
			}
			info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
			if _, err = (&types.Config{Importer: stubImporter{importer.Default()}}).Check("example.com/demo", fs, []*ast.File{f}, info); err != nil {
				t.Fatal(err)
			}
			r := report{}
			inspectCalls("/module", "example.com/demo", fs, f, info, &r)
			if len(r.Calls) != 1 {
				t.Fatal(r.Calls)
			}
			if fact := r.Calls[0]; fact.Status != tc.wantStatus || fact.Reason != tc.wantReason {
				t.Fatalf("got %+v; want %s / %s", fact, tc.wantStatus, tc.wantReason)
			}
		})
	}
}
