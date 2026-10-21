package main

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
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
	for _, mode := range []string{"bounded", "wrong-parent", "wrong-duration", "helper", "missing-release", "spoof"} {
		t.Run(mode, func(t *testing.T) {
			parent := "request.Context()"
			duration := "30*time.Second"
			release := "defer cancel()"
			ctx := "ctx"
			if mode == "wrong-parent" {
				parent = "context.Background()"
			}
			if mode == "wrong-duration" {
				duration = "31*time.Second"
			}
			if mode == "missing-release" {
				release = "_ = cancel"
			}
			if mode == "helper" {
				ctx = "identity(ctx)"
			}
			source := `package demo
import("context";"net/http";"time";rpcx "example.com/rpcx/v2")
type outboundCaller interface{Call(context.Context,string,any,any)error}
func identity(c context.Context)context.Context{return c}
func invokeHTTP(request *http.Request)error{client,_:=rpcx.Dial("address");var caller outboundCaller=client;ctx,cancel:=context.WithTimeout(` + parent + `,` + duration + `);` + release + `;return caller.Call(` + ctx + `,"http.do",nil,nil)}`
			if mode == "spoof" {
				source = strings.Replace(source, "var caller outboundCaller=client", "var caller outboundCaller=client;caller=client", 1)
			}
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
			want := "unknown"
			if mode == "bounded" {
				want = "pass"
			}
			if mode == "wrong-parent" || mode == "wrong-duration" {
				want = "fail"
			}
			if r.Calls[0].Status != want {
				t.Fatalf("got %+v want %s", r.Calls[0], want)
			}
		})
	}
}
