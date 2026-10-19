package translation

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"reflect"
	"strings"

	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

var adapters = []string{"internal/repo/webapi/translation_google.go", "internal/repo/webapi/translation_rpcx_alternate.go"}

// adapt 仅从宿主认证基线生成 API 适配，不读取参考补丁或候选预期。
func adapt(files map[string][]byte) (map[string][]byte, error) {
	out := map[string][]byte{}
	for name, data := range files {
		if !strings.HasSuffix(name, ".go") {
			out[name] = data
			continue
		}
		fs := token.NewFileSet()
		f, err := parser.ParseFile(fs, name, data, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		for _, imp := range f.Imports {
			if imp.Path.Value == `"example.com/rpcx"` {
				imp.Path.Value = `"example.com/rpcx/v2"`
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "rpcx" && sel.Sel.Name == "NewClient" {
				sel.Sel.Name = "Dial"
			}
			if (name == adapters[0] || name == adapters[1]) && sel.Sel.Name == "Call" {
				call.Args = append([]ast.Expr{ast.NewIdent("ctx")}, call.Args...)
			}
			return true
		})
		var b bytes.Buffer
		if err = format.Node(&b, fs, f); err != nil {
			return nil, err
		}
		out[name] = b.Bytes()
	}
	return out, nil
}
func syntax(name string, data []byte) (string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), name, data, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return "", err
	}
	// 标签由单独的配置证据与原注释核对；仅剥离实际边界里新增的期限前缀和 time 导入。
	if name == adapters[0] || name == adapters[1] {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "Translate" || fn.Body == nil {
				continue
			}
			body := fn.Body.List
			at := 0
			if len(body) > 0 {
				if _, ok := body[0].(*ast.IfStmt); ok {
					at = 1
				}
			}
			if len(body) > at+1 {
				if a, ok := body[at].(*ast.AssignStmt); ok && len(a.Rhs) == 1 {
					if call, ok := a.Rhs[0].(*ast.CallExpr); ok {
						if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "WithTimeout" {
							if _, ok := body[at+1].(*ast.DeferStmt); ok {
								fn.Body.List = append(body[:at], body[at+2:]...)
							}
						}
					}
				}
			}
		}
		var imports []*ast.ImportSpec
		for _, imp := range f.Imports {
			if imp.Path.Value != `"time"` {
				imports = append(imports, imp)
			}
		}
		f.Imports = imports
		for _, decl := range f.Decls {
			if d, ok := decl.(*ast.GenDecl); ok && d.Tok == token.IMPORT {
				var specs []ast.Spec
				for _, spec := range d.Specs {
					if spec.(*ast.ImportSpec).Path.Value != `"time"` {
						specs = append(specs, spec)
					}
				}
				d.Specs = specs
			}
		}
	}
	var b bytes.Buffer
	err = ast.Fprint(&b, nil, f, func(name string, value reflect.Value) bool {
		return value.Type() != reflect.TypeFor[token.Pos]() && ast.NotNilFilter(name, value)
	})
	return gate.Digest(b.Bytes()), err
}
func scope(base, candidate map[string][]byte) quality.Check {
	expected, err := adapt(base)
	out := check("request_scope", quality.StatusPass, "production changes match the bounded API/context adaptation", "input-snapshots", "scope")
	if err != nil {
		return check("request_scope", quality.StatusUnknown, err.Error(), "input-snapshots", "scope")
	}
	paths := map[string]bool{}
	for name := range base {
		paths[name] = true
	}
	for name := range candidate {
		paths[name] = true
	}
	for _, name := range gate.SortedKeys(paths) {
		if strings.HasSuffix(name, "_test.go") || name == "go.mod" {
			continue
		}
		proved := bytes.Equal(expected[name], candidate[name])
		if strings.HasSuffix(name, ".go") && base[name] != nil && candidate[name] != nil {
			a, e1 := syntax(name, expected[name])
			b, e2 := syntax(name, candidate[name])
			proved = e1 == nil && e2 == nil && a == b
		}
		if !proved {
			out.Status = quality.StatusUnknown
			out.Findings = append(out.Findings, quality.Finding{Code: "unmodeled_production_change", Detail: "production structure requires review: " + name, RuleIDs: rules("request_scope"), Locations: []quality.Location{{Tree: "candidate", Path: name}}, EvidenceIDs: out.EvidenceIDs})
		}
	}
	out.Detail = "production structure: " + out.Status
	out.Findings[0].Code = "request_scope_" + out.Status
	out.Findings[0].Detail = out.Detail
	return out
}
