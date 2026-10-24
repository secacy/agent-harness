package main

import (
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func evidenceImports() boundaryImports {
	imports := boundaryImports{}
	cp := types.NewPackage("context", "context")
	imports["context"] = cp
	ct := types.NewNamed(types.NewTypeName(0, cp, "Context", nil), types.NewInterfaceType(nil, nil).Complete(), nil)
	cp.Scope().Insert(ct.Obj())
	cancel := types.NewNamed(types.NewTypeName(0, cp, "CancelFunc", nil), types.NewSignatureType(nil, nil, nil, nil, nil, false), nil)
	cp.Scope().Insert(cancel.Obj())
	tp := types.NewPackage("time", "time")
	imports["time"] = tp
	duration := types.NewNamed(types.NewTypeName(0, tp, "Duration", nil), types.Typ[types.Int64], nil)
	tp.Scope().Insert(duration.Obj())
	tp.Scope().Insert(types.NewConst(0, tp, "Second", duration, constant.MakeInt64(1000000000)))
	cp.Scope().Insert(types.NewFunc(0, cp, "WithTimeout", types.NewSignatureType(nil, nil, nil, types.NewTuple(types.NewVar(0, cp, "parent", ct), types.NewVar(0, cp, "duration", duration)), types.NewTuple(types.NewVar(0, cp, "ctx", ct), types.NewVar(0, cp, "cancel", cancel)), false)))
	cp.MarkComplete()
	tp.MarkComplete()
	return imports
}
func TestBoundaryEvidenceObjectUses(t *testing.T) {
	original := `package p
 import("context";"time")
 const timeout=30*time.Second
 type client struct{}
 func(*client) Call(context.Context,string,any,any)error{return nil}
 func f(ctx context.Context,c *client)error{
 callCtx,cancel:=context.WithTimeout(ctx,timeout)
 defer cancel()
 return c.Call(callCtx,"op",nil,nil)
 }`
	for _, tc := range []struct{ name, old, new, status, proof string }{
		{"private-constant", "", "", "covered", "complete"},
		{"inline-duration", "ctx,timeout)", "ctx,30*time.Second)", "covered", "complete"},
		{"parent-shadow", "callCtx", "ctx", "covered", "complete"},
		{"typed-constant", "const timeout=", "const timeout time.Duration=", "covered", "complete"},
		{"extra-constant-use", "type client struct{}", "func other()time.Duration{return timeout};type client struct{}", "covered", "unsupported"},
		{"extra-child-use", `"op",nil,nil`, `"op",callCtx,nil`, "covered", "unsupported"},
		{"extra-cancel-use", `"op",nil,nil`, `"op",cancel,nil`, "covered", "unsupported"},
		{"exported-constant", "timeout", "Timeout", "covered", "unsupported"},
		{"constant-conversion", "30*time.Second", "time.Duration(30*time.Second)", "covered", "unsupported"},
		{"wrong-duration", "30*time.Second", "5*time.Second", "violation", ""},
		{"runtime-duration", "const timeout=", "var timeout=", "unknown", ""},
		{"early-cancel", "defer cancel()", "cancel();defer cancel()", "unknown", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := original
			if tc.old != "" {
				text = strings.ReplaceAll(text, tc.old, tc.new)
			}
			root := t.TempDir()
			path := filepath.Join(root, "p.go")
			if err := os.WriteFile(path, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			fs := token.NewFileSet()
			file, err := parser.ParseFile(fs, path, text, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
			config := types.Config{Importer: evidenceImports()}
			if _, err := config.Check("p", fs, []*ast.File{file}, info); err != nil {
				t.Fatal(err)
			}
			var fn *ast.FuncDecl
			var rpc *ast.CallExpr
			for _, d := range file.Decls {
				if f, ok := d.(*ast.FuncDecl); ok && f.Name.Name == "f" {
					fn = f
				}
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok {
					if s, ok := c.Fun.(*ast.SelectorExpr); ok && s.Sel.Name == "Call" {
						rpc = c
					}
				}
				return true
			})
			status, reason, _ := boundary(fn, rpc, info)
			if status != tc.status {
				t.Fatal(status, reason)
			}
			if status != "covered" {
				return
			}
			e := boundaryNodes(root, fs, file, fn, rpc, info)
			if e.Status != tc.proof || (e.Status == "complete" && len(e.Nodes) < 9) || (e.Status == "unsupported" && (e.Reason == "" || len(e.Nodes) != 0)) {
				t.Fatalf("%+v", e)
			}
		})
	}
}
