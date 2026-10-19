package main

import (
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

type boundaryImports map[string]*types.Package

func (p boundaryImports) Import(name string) (*types.Package, error) { return p[name], nil }

// 使用可控的类型身份验证局部证明范围，不依赖外部模块或标准库导出缓存。
func TestResponseBoundaryProof(t *testing.T) {
	imports := boundaryImports{}
	cp := types.NewPackage("context", "context")
	imports["context"] = cp
	contextType := types.NewNamed(types.NewTypeName(0, cp, "Context", nil), types.NewInterfaceType([]*types.Func{types.NewFunc(0, cp, "Err", types.NewSignatureType(nil, nil, nil, nil, types.NewTuple(types.NewVar(0, cp, "", types.Universe.Lookup("error").Type())), false))}, nil).Complete(), nil)
	cp.Scope().Insert(contextType.Obj())
	cancelType := types.NewNamed(types.NewTypeName(0, cp, "CancelFunc", nil), types.NewSignatureType(nil, nil, nil, nil, nil, false), nil)
	cp.Scope().Insert(cancelType.Obj())
	tp := types.NewPackage("time", "time")
	imports["time"] = tp
	duration := types.NewNamed(types.NewTypeName(0, tp, "Duration", nil), types.Typ[types.Int64], nil)
	tp.Scope().Insert(duration.Obj())
	tp.Scope().Insert(types.NewConst(0, tp, "Second", duration, constant.MakeInt64(1_000_000_000)))
	sig := types.NewSignatureType(nil, nil, nil, types.NewTuple(types.NewVar(0, cp, "parent", contextType), types.NewVar(0, cp, "timeout", duration)), types.NewTuple(types.NewVar(0, cp, "ctx", contextType), types.NewVar(0, cp, "cancel", cancelType)), false)
	cp.Scope().Insert(types.NewFunc(0, cp, "WithTimeout", sig))
	cp.MarkComplete()
	tp.MarkComplete()
	source := `package p
 import("context";"time")
 type client struct{}
 func (*client) Call(context.Context,string,any,any) error { return nil }
 func helper() int { return 1 }
 func f(ctx context.Context,c *client) int {
 ctx,cancel:=context.WithTimeout(ctx,30*time.Second)
 defer cancel()
 var response int
 err:=c.Call(ctx,"fetch_routes",int(1),&response)
 if err!=nil { return 0 }
 return response
 }`
	cases := []struct{ name, old, new, status string }{
		{"response", "", "", "covered"},
		{"ctx-precheck", "ctx,cancel:=context.WithTimeout(ctx,30*time.Second)", "if err:=ctx.Err();err!=nil{return 0};ctx,cancel:=context.WithTimeout(ctx,30*time.Second)", "covered"},
		{"unsafe-precheck", "ctx,cancel:=context.WithTimeout(ctx,30*time.Second)", "if err:=ctx.Err();err!=nil{helper();return 0};ctx,cancel:=context.WithTimeout(ctx,30*time.Second)", "unknown"},
		{"request-pointer", "var response int\n err:=c.Call(ctx,\"fetch_routes\",int(1),&response)", "request:=int(1);response:=new(int)\n err:=c.Call(ctx,\"fetch_routes\",request,response)", "covered"},
		{"side-effect-request", "var response int", "request:=helper();_ = request;var response int", "unknown"},
		{"blocking-request", "var response int", "var response = <-(chan int)(nil)", "unknown"},

		{"wrong-duration", "30*time.Second", "31*time.Second", "violation"},
		{"argument-call", "int(1)", "helper()", "unknown"},
		{"early-cancel", "var response int", "cancel();var response int", "unknown"},
		{"initialized-response", "var response int", "var response = helper()", "unknown"},
		{"other-response", "&response", "new(int)", "unknown"},
		{"later-cancel", "if err!=nil", "cancel();if err!=nil", "unknown"},
		{"defer-other", "defer cancel()", "defer func(){ _ = cancel }()", "unknown"},
		{"missing-prefix", "ctx,cancel:=context.WithTimeout(ctx,30*time.Second)\n defer cancel()", "_ = 30*time.Second", "unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			text := source
			if c.old != "" {
				text = strings.Replace(text, c.old, c.new, 1)
				if c.name == "request-pointer" {
					text = strings.Replace(text, "return response", "return *response", 1)
				}
			}
			fs := token.NewFileSet()
			file, err := parser.ParseFile(fs, "boundary.go", text, 0)
			if err != nil {
				t.Fatal(err)
			}
			info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
			config := types.Config{Importer: imports}
			if _, err = config.Check("p", fs, []*ast.File{file}, info); err != nil {
				t.Fatal(err)
			}
			for _, d := range file.Decls {
				if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "f" {
					var rpc *ast.CallExpr
					ast.Inspect(fn, func(n ast.Node) bool {
						if call, ok := n.(*ast.CallExpr); ok {
							if s, ok := call.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "Call" {
								rpc = call
							}
						}
						return true
					})
					status, reason, _ := boundary(fn, rpc, info)
					if status != c.status {
						t.Fatalf("%s: %s", status, reason)
					}
				}
			}
		})
	}
}
