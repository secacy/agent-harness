package ghcli

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"strconv"
	"strings"

	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

const boundaryFile = "pkg/cmd/release/delete/outbound.go"

// scope 只接受原公开边界中的局部迁移；无法归一化的辅助函数保持未知。
func scope(base, candidate map[string][]byte, proofs ...scopeProof) quality.Check {
	status := quality.StatusPass
	for name, before := range base {
		after, ok := candidate[name]
		if name == "go.mod" || name == boundaryFile {
			continue
		}
		if !ok || !bytes.Equal(before, after) {
			return check("request_scope", quality.StatusFail, "source outside the RPC boundary changed", "input-snapshots")
		}
	}
	f, parseErr := parser.ParseFile(token.NewFileSet(), "boundary.go", candidate[boundaryFile], parser.ParseComments|parser.SkipObjectResolution)
	if parseErr != nil {
		return check("request_scope", quality.StatusUnknown, "candidate boundary cannot be parsed", "input-snapshots")
	}
	for _, g := range f.Comments {
		for _, c := range g.List {
			for _, prefix := range []string{"//go:", "//line ", "/*line ", "//export "} {
				if strings.HasPrefix(c.Text, prefix) {
					return check("request_scope", quality.StatusUnknown, "semantic directive requires separate proof", "input-snapshots")
				}
			}
		}
	}
	added := false
	for name := range candidate {
		if _, ok := base[name]; !ok {
			status = quality.StatusUnknown
			added = true
		}
	}
	before, err := normalBoundary(base[boundaryFile], false)
	if err {
		return check("request_scope", quality.StatusUnknown, "baseline boundary cannot be normalized", "input-snapshots")
	}
	after, err := normalBoundary(candidate[boundaryFile], true)
	if err || !bytes.Equal(before, after) {
		status = quality.StatusUnknown
		if len(proofs) == 1 && proofs[0].Status == "pass" && proofs[0].BaselineSHA256 == gate.Digest(base[boundaryFile]) && proofs[0].CandidateSHA256 == gate.Digest(candidate[boundaryFile]) {
			status = quality.StatusPass
		}
	}
	if added {
		status = quality.StatusUnknown
	}
	refs := []string{"input-snapshots", "typed-analysis"}
	if len(proofs) == 1 && proofs[0].Status != "" {
		refs = append(refs, "boundary-proof")
	}
	return check("request_scope", status, "bounded boundary preservation: "+status, refs...)
}

func printed(n any) []byte {
	var b bytes.Buffer
	_ = format.Node(&b, token.NewFileSet(), n)
	return b.Bytes()
}

// normalBoundary 只抹去类型迁移、单次上下文派生和对应释放语句，保留其余全部语法。
func normalBoundary(data []byte, migrated bool) ([]byte, bool) {
	f, err := parser.ParseFile(token.NewFileSet(), "boundary.go", data, 0)
	if err != nil {
		return nil, true
	}
	contextNames := map[string]bool{}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if p == "example.com/rpcx/v2" {
			imp.Path.Value = `"example.com/rpcx"`
		}
		if p == "context" {
			alias := "context"
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			contextNames[alias] = true
		}
	}
	// time 仅是期限表达式的必要导入，归一化后删除。
	var decls []ast.Decl
	for _, d := range f.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.IMPORT {
			var specs []ast.Spec
			for _, s := range g.Specs {
				imp := s.(*ast.ImportSpec)
				if imp.Path.Value != `"time"` {
					specs = append(specs, s)
				}
			}
			g.Specs = specs
		}
		decls = append(decls, d)
	}
	f.Decls = decls
	for _, d := range f.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.TYPE {
			for _, s := range g.Specs {
				t := s.(*ast.TypeSpec)
				if t.Name.Name == "outboundCaller" {
					if it, ok := t.Type.(*ast.InterfaceType); ok {
						for _, field := range it.Methods.List {
							if ft, ok := field.Type.(*ast.FuncType); ok && len(field.Names) == 1 && field.Names[0].Name == "Call" && len(ft.Params.List) == 4 {
								ft.Params.List = ft.Params.List[1:]
							}
						}
					}
				}
			}
		}
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "invokeHTTP" {
			continue
		}
		cancelName := ""
		deriveCount := 0
		for _, s := range fn.Body.List {
			if a, ok := s.(*ast.AssignStmt); ok && len(a.Rhs) == 1 && len(a.Lhs) == 2 {
				if c, ok := a.Rhs[0].(*ast.CallExpr); ok {
					if sel, ok := c.Fun.(*ast.SelectorExpr); ok {
						if id, ok := sel.X.(*ast.Ident); ok && contextNames[id.Name] && (sel.Sel.Name == "WithTimeout" || sel.Sel.Name == "WithDeadline") {
							if id, ok := a.Lhs[1].(*ast.Ident); ok {
								cancelName = id.Name
								deriveCount++
							}
						}
					}
				}
			}
		}
		if migrated && deriveCount > 1 {
			return nil, true
		}
		var body []ast.Stmt
		for _, s := range fn.Body.List {
			if a, ok := s.(*ast.AssignStmt); ok && len(a.Rhs) == 1 {
				if c, ok := a.Rhs[0].(*ast.CallExpr); ok {
					if sel, ok := c.Fun.(*ast.SelectorExpr); ok {
						if id, ok := sel.X.(*ast.Ident); ok && contextNames[id.Name] && (sel.Sel.Name == "WithTimeout" || sel.Sel.Name == "WithDeadline") {
							continue
						}
					}
				}
			}
			if d, ok := s.(*ast.DeferStmt); ok {
				if id, ok := d.Call.Fun.(*ast.Ident); ok && id.Name == cancelName {
					continue
				}
			}
			ast.Inspect(s, func(n ast.Node) bool {
				if c, ok := n.(*ast.CallExpr); ok {
					if sel, ok := c.Fun.(*ast.SelectorExpr); ok {
						if sel.Sel.Name == "Dial" {
							sel.Sel.Name = "NewClient"
						}
						if sel.Sel.Name == "Call" && len(c.Args) == 4 {
							c.Args = c.Args[1:]
						}
					}
				}
				return true
			})
			body = append(body, s)
		}
		fn.Body.List = body
	}
	if !migrated && strings.Contains(string(data), "example.com/rpcx/v2") {
		return nil, true
	}
	return printed(f), false
}

// migratedMod 按本发布的单 SDK 替换布局更新模块标识。
func migratedMod(data []byte) []byte {
	s := strings.Replace(string(data), "example.com/rpcx v1.0.0", "example.com/rpcx/v2 v2.0.0", 1)
	s = strings.Replace(s, "replace example.com/rpcx => ../rpcx-v1", "replace example.com/rpcx/v2 => ../rpcx-v2", 1)
	return []byte(s)
}
