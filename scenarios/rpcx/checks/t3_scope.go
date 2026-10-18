package checks

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"slices"
	"strings"

	"example.com/agent-harness/internal/quality"
)

// requestScope 只在已认证的基线上生成有限适配，不归一化候选的任意改写。
// RPC 函数体由类型分析和行为检查组合验证；其他生产结构须由明确变换解释。
func requestScope(baseline, candidate map[string][]byte) Check {
	check := t3Check("request_scope", "pass", "all production changes are within the declared request-chain adaptation", "input-snapshots", "request-scope")
	files := map[string]bool{}
	for path := range baseline {
		files[path] = true
	}
	for path := range candidate {
		files[path] = true
	}
	for _, path := range sortedPaths(files) {
		if strings.HasSuffix(path, "_test.go") || path == "go.mod" || strings.HasPrefix(path, "internal/pb/") {
			continue
		}
		if bytes.Equal(baseline[path], candidate[path]) {
			continue
		}
		reason := "unmodeled_production_change"
		if strings.HasSuffix(path, ".go") && baseline[path] != nil && candidate[path] != nil {
			before, e1 := parser.ParseFile(token.NewFileSet(), path, baseline[path], parser.SkipObjectResolution)
			after, e2 := parser.ParseFile(token.NewFileSet(), path, candidate[path], parser.SkipObjectResolution)
			if e1 == nil && e2 == nil {
				adaptProduction(path, before)
				if path == "internal/inventory/client.go" {
					maskRPCBodies(before)
					maskRPCBodies(after)
				}
				if syntaxDigest(before) == syntaxDigest(after) && !hasDirectives(candidate[path]) {
					continue
				}
				reason = "request_chain_or_file_structure_unproven"
			}
		}
		check.Status = quality.StatusUnknown
		check.Findings = append(check.Findings, quality.Finding{Code: reason, Detail: "production change requires review: " + path,
			RuleIDs: check.RuleIDs, Locations: []quality.Location{{Tree: "candidate", Path: path}}, EvidenceIDs: check.EvidenceIDs})
	}
	check.Detail = "production changes within the declared request-chain adaptation: " + check.Status
	check.Findings[0].Detail, check.Findings[0].Code = check.Detail, "request_scope_"+check.Status
	return check
}

func adaptProduction(path string, file *ast.File) {
	if path == "internal/inventory/client.go" {
		for _, imp := range file.Imports {
			if imp.Path.Value == `"example.com/rpcx"` {
				imp.Path.Value = `"example.com/rpcx/v2"`
			}
		}
		// 保持原分组中的导入顺序；与 gofmt 的排序结果一致。
		for _, decl := range file.Decls {
			if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.IMPORT {
				gen.Specs = append([]ast.Spec{&ast.ImportSpec{Path: &ast.BasicLit{Kind: token.STRING, Value: `"context"`}}}, gen.Specs...)
				gen.Specs = append(gen.Specs[:2], append([]ast.Spec{&ast.ImportSpec{Path: &ast.BasicLit{Kind: token.STRING, Value: `"time"`}}}, gen.Specs[2:]...)...)
				file.Imports = []*ast.ImportSpec{}
				for _, spec := range gen.Specs {
					file.Imports = append(file.Imports, spec.(*ast.ImportSpec))
				}
			}
		}
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if (path == "internal/inventory/client.go" && (fn.Name.Name == "Reserve" || fn.Name.Name == "Release")) || (path == "internal/orders/service.go" && fn.Name.Name == "Cancel") {
			field := &ast.Field{Names: []*ast.Ident{ast.NewIdent("ctx")}, Type: &ast.SelectorExpr{X: ast.NewIdent("context"), Sel: ast.NewIdent("Context")}}
			fn.Type.Params.List = append([]*ast.Field{field}, fn.Type.Params.List...)
		}
		ast.Inspect(fn, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if path == "internal/inventory/client.go" && fn.Name.Name == "New" && sel.Sel.Name == "NewClient" {
				sel.Sel.Name = "Dial"
			}
			if path == "internal/orders/service.go" && (sel.Sel.Name == "Reserve" || sel.Sel.Name == "Release") {
				call.Args = append([]ast.Expr{ast.NewIdent("ctx")}, call.Args...)
			}
			if path == "cmd/server/handler.go" && fn.Name.Name == "Cancel" && sel.Sel.Name == "Cancel" {
				ctx := &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("r"), Sel: ast.NewIdent("Context")}}
				call.Args = append([]ast.Expr{ctx}, call.Args...)
			}
			return true
		})
	}
}

func maskRPCBodies(file *ast.File) {
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && (fn.Name.Name == "Reserve" || fn.Name.Name == "Release") {
			fn.Body = nil
		}
	}
}

func syntaxDigest(node ast.Node) string {
	var buf bytes.Buffer
	_ = ast.Fprint(&buf, nil, node, func(name string, value reflect.Value) bool {
		return value.Type() != reflect.TypeFor[token.Pos]() && ast.NotNilFilter(name, value)
	})
	return digest(buf.Bytes())
}

func hasDirectives(data []byte) bool {
	// 保守处理所有指令样式；未知指令也不能被当作普通注释消除。
	return bytes.Contains(data, []byte("//go:")) || bytes.Contains(data, []byte("// +build")) || bytes.Contains(data, []byte("//line ")) || bytes.Contains(data, []byte("/*line ")) || bytes.Contains(data, []byte("//export "))
}

func sortedPaths[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
