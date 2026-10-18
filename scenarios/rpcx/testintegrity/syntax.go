package testintegrity

import (
	"bytes"
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/scanner"
	"go/token"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type sourceFile struct {
	path       string
	file       *ast.File
	fset       *token.FileSet
	imports    []string
	directives []string
	other      []ast.Decl
	err        error
}

type function struct {
	source *sourceFile
	node   *ast.FuncDecl
}

func parseSources(files map[string][]byte) (map[string]*sourceFile, map[string][]function) {
	sources, functions := map[string]*sourceFile{}, map[string][]function{}
	for _, path := range keys(files) {
		source := &sourceFile{path: path, fset: token.NewFileSet(), imports: []string{}, directives: directives(path, files[path])}
		source.file, source.err = parser.ParseFile(source.fset, path, files[path], parser.SkipObjectResolution)
		sources[path] = source
		if source.err != nil {
			continue
		}
		for _, imp := range source.file.Imports {
			name := ""
			if imp.Name != nil {
				name = imp.Name.Name
			}
			value, _ := strconv.Unquote(imp.Path.Value)
			source.imports = append(source.imports, name+"="+value)
		}
		sort.Strings(source.imports)
		for _, decl := range source.file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				id := filepath.ToSlash(filepath.Dir(path)) + "." + fn.Name.Name
				functions[id] = append(functions[id], function{source: source, node: fn})
			} else if gen, ok := decl.(*ast.GenDecl); !ok || gen.Tok != token.IMPORT {
				source.other = append(source.other, decl)
			}
		}
	}
	return sources, functions
}

func directives(path string, data []byte) []string {
	result := []string{}
	fset := token.NewFileSet()
	file := fset.AddFile(path, -1, len(data))
	var scan scanner.Scanner
	scan.Init(file, data, nil, scanner.ScanComments)
	for {
		_, kind, text := scan.Scan()
		if kind == token.EOF {
			return result
		}
		if kind == token.COMMENT && (constraint.IsGoBuild(text) || constraint.IsPlusBuild(text) || strings.HasPrefix(text, "//go:") || strings.HasPrefix(text, "//line ") || strings.HasPrefix(text, "/*line ") || strings.HasPrefix(text, "//export ")) {
			result = append(result, text)
		}
	}
}

var positionType = reflect.TypeFor[token.Pos]()

func canonical(node ast.Node) string {
	var result bytes.Buffer
	_ = ast.Fprint(&result, nil, node, func(name string, value reflect.Value) bool {
		return value.Type() != positionType && ast.NotNilFilter(name, value)
	})
	return result.String()
}

func (f function) location(tree string) Location {
	return Location{Tree: tree, File: f.source.path, Line: f.source.fset.PositionFor(f.node.Pos(), false).Line, Name: f.node.Name.Name}
}

func testName(name string) bool {
	if !strings.HasPrefix(name, "Test") || name == "TestMain" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(strings.TrimPrefix(name, "Test"))
	return !unicode.IsLower(r)
}

func ordinaryTest(fn function) bool {
	f := fn.node
	if !testName(f.Name.Name) || f.Body == nil || f.Recv != nil || f.Type.TypeParams != nil || f.Type.Results != nil || len(f.Type.Params.List) != 1 || len(f.Type.Params.List[0].Names) != 1 {
		return false
	}
	star, ok := f.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "T" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || id.Name != "testing" {
		return false
	}
	for _, imp := range fn.source.imports {
		if imp == "=testing" || imp == "testing=testing" {
			return true
		}
	}
	return false
}

func testingCall(stmt ast.Stmt, names ...string) bool {
	expr, ok := stmt.(*ast.ExprStmt)
	if !ok {
		return false
	}
	call, ok := expr.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok || id.Name != "t" {
		return false
	}
	for _, name := range names {
		if sel.Sel.Name == name {
			return true
		}
	}
	return false
}

func assertion(stmt ast.Stmt) bool {
	if guard, ok := stmt.(*ast.IfStmt); ok && guard.Init == nil && guard.Else == nil && len(guard.Body.List) == 1 {
		return testingCall(guard.Body.List[0], "Fatal", "Fatalf", "Error", "Errorf", "Fail", "FailNow")
	}
	return testingCall(stmt, "Fatal", "Fatalf", "Error", "Errorf", "Fail", "FailNow")
}

// 只识别冻结基线的已知断言删除；其他节点必须逐字段相同。
// 有额外替换语句时整个匹配失败，不把 helper 重写当作明确删除。
func deletionOnly(a, b reflect.Value) (bool, []ast.Stmt) {
	if !a.IsValid() || !b.IsValid() {
		return a.IsValid() == b.IsValid(), nil
	}
	if a.Type() != b.Type() {
		return false, nil
	}
	if a.Type() == positionType {
		return true, nil
	}
	if a.Type() == reflect.TypeFor[[]ast.Stmt]() {
		removed := []ast.Stmt{}
		i, j := 0, 0
		for i < a.Len() {
			if j < b.Len() {
				ok, nested := deletionOnly(a.Index(i), b.Index(j))
				if ok {
					removed = append(removed, nested...)
					i++
					j++
					continue
				}
			}
			stmt := a.Index(i).Interface().(ast.Stmt)
			if !assertion(stmt) {
				return false, nil
			}
			removed = append(removed, stmt)
			i++
		}
		return j == b.Len(), removed
	}
	var removed []ast.Stmt
	appendChild := func(a, b reflect.Value) bool {
		ok, nested := deletionOnly(a, b)
		removed = append(removed, nested...)
		return ok
	}
	switch a.Kind() {
	case reflect.Pointer, reflect.Interface:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil(), nil
		}
		return deletionOnly(a.Elem(), b.Elem())
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			if !appendChild(a.Field(i), b.Field(i)) {
				return false, nil
			}
		}
	case reflect.Slice:
		if a.Len() != b.Len() {
			return false, nil
		}
		for i := 0; i < a.Len(); i++ {
			if !appendChild(a.Index(i), b.Index(i)) {
				return false, nil
			}
		}
	default:
		return reflect.DeepEqual(a.Interface(), b.Interface()), nil
	}
	return true, removed
}
