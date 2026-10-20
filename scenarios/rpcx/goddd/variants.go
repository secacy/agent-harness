package goddd

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"maps"
	"reflect"

	"github.com/secacy/agent-harness/scenarios/rpcx/testintegrity"
)

const (
	mockPath = "mock/mock.go"
	stubPath = "booking/service_test.go"
)

type adaptations struct {
	production map[string][]byte
	forwarded  []byte
	tests      [2]map[string][]byte
}

// prepareAdaptations 只从宿主已认证的基线生成固定变体，不读取候选。
func prepareAdaptations(base map[string][]byte) (adaptations, error) {
	var a adaptations
	var err error
	a.production, err = adapt(base, false)
	if err != nil {
		return a, err
	}
	a.forwarded, err = forwardedMock(a.production[mockPath])
	if err != nil {
		return a, err
	}
	a.tests[0], err = adapt(testFiles(base), true)
	if err != nil {
		return a, err
	}
	blank, err := blankContextStub(base[stubPath], a.tests[0][stubPath])
	if err != nil {
		return a, err
	}
	a.tests[1] = maps.Clone(a.tests[0])
	a.tests[1][stubPath] = blank
	return a, nil
}

func parseVariant(path string, data []byte) (*token.FileSet, *ast.File, error) {
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, path, data, parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, fmt.Errorf("unsupported host adaptation in %s: %w", path, err)
	}
	return fs, f, nil
}
func formatVariant(fs *token.FileSet, f *ast.File) ([]byte, error) {
	var out bytes.Buffer
	if err := format.Node(&out, fs, f); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
func variantMethod(f *ast.File, receiver string) (*ast.FuncDecl, error) {
	var found *ast.FuncDecl
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "FetchRoutesForSpecification" || fn.Recv == nil || len(fn.Recv.List) != 1 {
			continue
		}
		ptr, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		id, ok := ptr.X.(*ast.Ident)
		if !ok || id.Name != receiver {
			continue
		}
		if found != nil {
			return nil, errors.New("host adaptation method is ambiguous")
		}
		found = fn
	}
	if found == nil {
		return nil, errors.New("host adaptation method is missing")
	}
	return found, nil
}
func defaultContextImport(f *ast.File) bool {
	count := 0
	for _, imp := range f.Imports {
		if imp.Path.Value == `"context"` {
			if imp.Name != nil {
				return false
			}
			count++
		}
	}
	return count == 1
}
func nodeShape(n ast.Node) string {
	var out bytes.Buffer
	_ = ast.Fprint(&out, nil, n, func(name string, value reflect.Value) bool {
		return value.Type() != reflect.TypeFor[token.Pos]() && ast.NotNilFilter(name, value)
	})
	return out.String()
}

// forwardedMock 要求字段和转发方法同时保持认证形态，再补充两处 ctx。
func forwardedMock(data []byte) ([]byte, error) {
	fs, f, err := parseVariant(mockPath, data)
	if err != nil {
		return nil, err
	}
	if f.Name.Name != "mock" || !defaultContextImport(f) {
		return nil, errors.New("unsupported mock package or context import")
	}
	var callback *ast.FuncType
	types, fields := 0, 0
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			typ, ok := spec.(*ast.TypeSpec)
			if !ok || typ.Name.Name != "RoutingService" {
				continue
			}
			types++
			str, ok := typ.Type.(*ast.StructType)
			if !ok || typ.Assign.IsValid() {
				return nil, errors.New("unsupported mock RoutingService type")
			}
			for _, field := range str.Fields.List {
				if len(field.Names) != 1 || field.Names[0].Name != "FetchRoutesFn" {
					continue
				}
				fields++
				callback, _ = field.Type.(*ast.FuncType)
			}
		}
	}
	expectedType, err := parser.ParseExpr("func(shipping.RouteSpecification) []shipping.Itinerary")
	if err != nil {
		return nil, err
	}
	if types != 1 || fields != 1 || callback == nil || nodeShape(callback) != nodeShape(expectedType) {
		return nil, errors.New("unsupported mock FetchRoutesFn field")
	}
	fn, err := variantMethod(f, "RoutingService")
	if err != nil {
		return nil, err
	}
	_, expected, err := parseVariant(mockPath, []byte(`package mock
func (s *RoutingService) FetchRoutesForSpecification(ctx context.Context, rs shipping.RouteSpecification) []shipping.Itinerary {
 s.FetchRoutesInvoked = true
 return s.FetchRoutesFn(rs)
}`))
	if err != nil {
		return nil, err
	}
	if nodeShape(fn) != nodeShape(expected.Decls[0]) {
		return nil, errors.New("unsupported mock forwarding method")
	}
	callback.Params.List = append([]*ast.Field{{Type: selector("context", "Context")}}, callback.Params.List...)
	call := fn.Body.List[1].(*ast.ReturnStmt).Results[0].(*ast.CallExpr)
	call.Args = append([]ast.Expr{ast.NewIdent("ctx")}, call.Args...)
	return formatVariant(fs, f)
}

// blankContextStub 只允许新增且原声明中未出现的 ctx 改为下划线。
func blankContextStub(original, canonical []byte) ([]byte, error) {
	_, before, err := parseVariant(stubPath, original)
	if err != nil {
		return nil, err
	}
	old, err := variantMethod(before, "stubRoutingService")
	if err != nil {
		return nil, err
	}
	used := false
	ast.Inspect(old, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == "ctx" {
			used = true
		}
		return !used
	})
	if used || old.Body == nil || old.Type.TypeParams != nil {
		return nil, errors.New("original stub may bind or use ctx")
	}
	fs, after, err := parseVariant(stubPath, canonical)
	if err != nil {
		return nil, err
	}
	fn, err := variantMethod(after, "stubRoutingService")
	if err != nil {
		return nil, err
	}
	if !defaultContextImport(after) || fn.Type.Params == nil || len(fn.Type.Params.List) != len(old.Type.Params.List)+1 {
		return nil, errors.New("unsupported added stub context parameter")
	}
	p := fn.Type.Params.List[0]
	if len(p.Names) != 1 || p.Names[0].Name != "ctx" || nodeShape(p.Type) != nodeShape(selector("context", "Context")) {
		return nil, errors.New("unsupported added stub context identity")
	}
	// 临时去掉新增形参核对整份原声明，避免连带接受原参数或函数体改写。
	params := fn.Type.Params.List
	fn.Type.Params.List = params[1:]
	if nodeShape(fn) != nodeShape(old) {
		return nil, errors.New("stub changed beyond the added context parameter")
	}
	fn.Type.Params.List = params
	p.Names[0].Name = "_"
	return formatVariant(fs, after)
}

// selectIntegrity 不拼接声明结论；保留整份报告，明确违规优先于未知。
func selectIntegrity(reports [2]testintegrity.Report) (int, error) {
	if !reflect.DeepEqual(reports[0].Candidate, reports[1].Candidate) || !reflect.DeepEqual(reports[0].Baseline, reports[1].Baseline) || reports[0].SpecSHA256 != reports[1].SpecSHA256 || reports[0].MaterialSHA256 != reports[1].MaterialSHA256 || reports[0].VerifierVersion != reports[1].VerifierVersion || reports[0].GoVersion != reports[1].GoVersion || reports[0].SchemaVersion != reports[1].SchemaVersion {
		return 0, errors.New("test adaptation reports have different input bindings")
	}
	selected, preserved, violated := 0, false, false
	for i, report := range reports {
		switch report.Status {
		case "preserved":
			preserved = true
			if reports[selected].Status == "unknown" {
				selected = i
			}
		case "violated":
			violated = true
			selected = i
		case "unknown":
		default:
			return 0, errors.New("unsupported test adaptation status")
		}
	}
	if preserved && violated {
		return 0, errors.New("conflicting test adaptation proofs")
	}
	return selected, nil
}
