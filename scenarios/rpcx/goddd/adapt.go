package goddd

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
)

// adapt 只用于已认证基线，生成允许的 API/context 变换，不消费候选补丁。
func adapt(files map[string][]byte, tests bool) (map[string][]byte, error) {
	out := map[string][]byte{}
	for name, data := range files {
		if strings.HasSuffix(name, "_test.go") != tests {
			continue
		}
		if !strings.HasSuffix(name, ".go") {
			out[name] = data
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, data, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		needContext := false
		for _, imp := range f.Imports {
			if imp.Path.Value == `"example.com/rpcx"` {
				imp.Path.Value = `"example.com/rpcx/v2"`
			}
		}
		ast.Inspect(f, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.FuncDecl:
				if n.Name.Name == "RequestPossibleRoutesForCargo" || n.Name.Name == "FetchRoutesForSpecification" {
					prependContext(n.Type)
					needContext = true
				}
			case *ast.InterfaceType:
				for _, field := range n.Methods.List {
					if len(field.Names) == 1 && (field.Names[0].Name == "RequestPossibleRoutesForCargo" || field.Names[0].Name == "FetchRoutesForSpecification") {
						prependContext(field.Type.(*ast.FuncType))
						needContext = true
					}
				}
			case *ast.CallExpr:
				sel, ok := n.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if sel.Sel.Name == "NewClient" && name == "routing/proxying.go" {
					sel.Sel.Name = "Dial"
				}
				if sel.Sel.Name == "RequestPossibleRoutesForCargo" || sel.Sel.Name == "FetchRoutesForSpecification" {
					var ctx ast.Expr = ast.NewIdent("ctx")
					if tests {
						ctx = &ast.CallExpr{Fun: selector("context", "Background")}
						needContext = true
					}
					n.Args = append([]ast.Expr{ctx}, n.Args...)
				}
				if name == "routing/proxying.go" && sel.Sel.Name == "Call" {
					n.Args = append([]ast.Expr{ast.NewIdent("ctx")}, n.Args...)
				}
			}
			return true
		})
		if name == "server/booking.go" {
			for _, declaration := range f.Decls {
				fn, ok := declaration.(*ast.FuncDecl)
				if !ok || fn.Name.Name != "requestRoutes" {
					continue
				}
				assignment := fn.Body.List[0].(*ast.AssignStmt)
				assignment.Rhs = []ast.Expr{&ast.CallExpr{Fun: selector("r", "Context")}}
			}
		}
		if name == "routing/proxying.go" {
			ast.Inspect(f, func(node ast.Node) bool {
				switch n := node.(type) {
				case *ast.StructType:
					var fields []*ast.Field
					for _, field := range n.Fields.List {
						if sel, ok := field.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "Context" && len(field.Names) == 0 {
							continue
						}
						fields = append(fields, field)
					}
					n.Fields.List = fields
				case *ast.CompositeLit:
					var elements []ast.Expr
					for _, e := range n.Elts {
						if key, ok := e.(*ast.KeyValueExpr); ok {
							if id, ok := key.Key.(*ast.Ident); ok && id.Name == "Context" {
								continue
							}
						}
						elements = append(elements, e)
					}
					n.Elts = elements
				}
				return true
			})
		}
		if needContext {
			addImport(f, "context")
		}
		var b bytes.Buffer
		if err := format.Node(&b, fset, f); err != nil {
			return nil, err
		}
		out[name], err = format.Source(b.Bytes())
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func selector(pkg, name string) *ast.SelectorExpr {
	return &ast.SelectorExpr{X: ast.NewIdent(pkg), Sel: ast.NewIdent(name)}
}
func prependContext(fn *ast.FuncType) {
	fn.Params.List = append([]*ast.Field{{Names: []*ast.Ident{ast.NewIdent("ctx")}, Type: selector("context", "Context")}}, fn.Params.List...)
}
func addImport(f *ast.File, name string) {
	for _, imp := range f.Imports {
		if imp.Path.Value == strconv.Quote(name) {
			return
		}
	}
	spec := &ast.ImportSpec{Path: &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(name)}}
	for _, d := range f.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.IMPORT {
			g.Specs = append([]ast.Spec{spec}, g.Specs...)
			f.Imports = append(f.Imports, spec)
			return
		}
	}
	f.Decls = append([]ast.Decl{&ast.GenDecl{Tok: token.IMPORT, Specs: []ast.Spec{spec}}}, f.Decls...)
	f.Imports = append(f.Imports, spec)
}

func syntax(path string, data []byte) (string, error) {
	f, err := parser.ParseFile(token.NewFileSet(), "source.go", data, parser.SkipObjectResolution)
	if err != nil {
		return "", err
	}
	// 只在 routing 中屏蔽期限设置/释放前缀，实际契约由类型分析和行为分别验证。
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if path != "routing/proxying.go" || !ok || fn.Name.Name != "FetchRoutesForSpecification" || fn.Body == nil || len(fn.Body.List) < 2 {
			continue
		}
		a, ok := fn.Body.List[0].(*ast.AssignStmt)
		if !ok || len(a.Rhs) != 1 {
			continue
		}
		call, ok := a.Rhs[0].(*ast.CallExpr)
		if !ok {
			continue
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "WithTimeout" {
			continue
		}
		if _, ok := fn.Body.List[1].(*ast.DeferStmt); ok {
			fn.Body.List = fn.Body.List[2:]
		}
	}
	var b bytes.Buffer
	err = ast.Fprint(&b, nil, f, func(name string, value reflect.Value) bool {
		return value.Type() != reflect.TypeFor[token.Pos]() && ast.NotNilFilter(name, value)
	})
	return digest(b.Bytes()), err
}
