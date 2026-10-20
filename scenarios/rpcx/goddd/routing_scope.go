package goddd

import (
	"go/ast"
	"go/token"
	"strings"

	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

const routingPath = "routing/proxying.go"

type routingNode struct {
	Tree   string `json:"tree"`
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Offset int    `json:"offset"`
	End    int    `json:"end"`
	SHA256 string `json:"sha256"`
}
type routingFact struct {
	Code    string        `json:"code"`
	Name    string        `json:"name,omitempty"`
	RuleIDs []string      `json:"rule_ids"`
	Nodes   []routingNode `json:"nodes"`
}
type routingScopeReport struct {
	SchemaVersion            int             `json:"schema_version"`
	Binding                  quality.Binding `json:"binding"`
	MaterialsSHA256          string          `json:"materials_sha256"`
	Path                     string          `json:"path"`
	BaselineFileSHA256       string          `json:"baseline_file_sha256"`
	ExpectedFileSHA256       string          `json:"expected_file_sha256"`
	CandidateFileSHA256      string          `json:"candidate_file_sha256"`
	ExpectedStructureSHA256  string          `json:"expected_structure_sha256,omitempty"`
	RemainingStructureSHA256 string          `json:"remaining_structure_sha256,omitempty"`
	Matched                  bool            `json:"matched"`
	Facts                    []routingFact   `json:"facts"`
	Gaps                     []string        `json:"gaps"`
}

// routingScope 只归约比较副本中的两类节点，原候选及实际执行内容不变。
func routingScope(base, candidate map[string][]byte, expected []byte) routingScopeReport {
	data := candidate[routingPath]
	out := routingScopeReport{SchemaVersion: 1, Path: routingPath, BaselineFileSHA256: digest(base[routingPath]), ExpectedFileSHA256: digest(expected), CandidateFileSHA256: digest(data), Facts: []routingFact{}, Gaps: []string{}}
	gap := func(reason string) routingScopeReport { out.Gaps = append(out.Gaps, reason); return out }
	if directives(data) {
		return gap("semantic_directive")
	}
	fs, file, err := parseVariant(routingPath, data)
	if err != nil {
		return gap("candidate_parse_failed")
	}
	_, host, err := parseVariant(routingPath, expected)
	if err != nil {
		return gap("host_parse_failed")
	}
	// const 只允许一个声明；其他新增声明仍留在完整结构比较中。
	at := -1
	for i, decl := range file.Decls {
		if g, ok := decl.(*ast.GenDecl); ok && g.Tok == token.CONST {
			if at != -1 {
				return gap("multiple_constant_declarations")
			}
			at = i
		}
	}
	if at != -1 {
		declaration := file.Decls[at].(*ast.GenDecl)
		if len(declaration.Specs) != 1 {
			return gap("unsupported_constant_group")
		}
		value, ok := declaration.Specs[0].(*ast.ValueSpec)
		if !ok || len(value.Names) != 1 || len(value.Values) != 1 || value.Type != nil {
			return gap("unsupported_constant_shape")
		}
		id := value.Names[0]
		if id.Name == "_" || ast.IsExported(id.Name) {
			return gap("unsupported_constant_name")
		}
		before, ok := productionIdentifiers(base, id.Name, nil)
		if !ok || len(before) != 0 {
			return gap("constant_name_not_fresh")
		}
		uses, ok := productionIdentifiers(candidate, id.Name, file)
		if !ok {
			return gap("production_parse_failed")
		}
		fn := routingFunction(file, "FetchRoutesForSpecification", true)
		original := routingFunction(host, "FetchRoutesForSpecification", true)
		argument := timeoutConstantArgument(fn, original)
		if argument == nil || argument.Name != id.Name {
			return gap("constant_not_timeout_argument")
		}
		if len(uses) != 2 {
			return gap("constant_has_other_uses")
		}
		for _, use := range uses {
			if use != id && use != argument {
				return gap("constant_binding_unproven")
			}
		}
		expression, ok := value.Values[0].(*ast.BinaryExpr)
		if !ok || expression.Op != token.MUL || !routingSelector(expression.Y, "time", "Second") {
			return gap("unsupported_constant_expression")
		}
		number, ok := expression.X.(*ast.BasicLit)
		if !ok || number.Kind != token.INT || number.Value != "30" {
			return gap("unsupported_constant_duration")
		}
		out.Facts = append(out.Facts, routingFact{Code: "fresh_timeout_constant", Name: id.Name, RuleIDs: []string{"TIME-001", "SCOPE-001"}, Nodes: []routingNode{
			routingLocation(fs, data, "constant_declaration", declaration), routingLocation(fs, data, "timeout_argument", argument),
		}})
		file.Decls = append(file.Decls[:at], file.Decls[at+1:]...)
	}
	ctor := routingFunction(file, "NewProxyingMiddleware", false)
	original := routingFunction(host, "NewProxyingMiddleware", false)
	if ctor == nil || original == nil || ctor.Body == nil || original.Body == nil {
		return gap("constructor_unresolved")
	}
	if len(ctor.Body.List) > 0 && constructorRead(ctor.Body.List[0]) {
		if nodeShape(ctor.Type) != nodeShape(original.Type) || !contextFirstParameter(original) || hasRoutingIdentifier(original.Body, "ctx") {
			return gap("constructor_parameter_unproven")
		}
		statement := ctor.Body.List[0]
		ctor.Body.List = ctor.Body.List[1:]
		if hasRoutingIdentifier(ctor.Body, "ctx") {
			return gap("constructor_context_has_other_uses")
		}
		out.Facts = append(out.Facts, routingFact{Code: "unused_constructor_context_read", Name: "ctx", RuleIDs: []string{"CTX-002", "SCOPE-001"}, Nodes: []routingNode{
			routingLocation(fs, data, "constructor_parameter", ctor.Type.Params.List[0]), routingLocation(fs, data, "parameter_read", statement),
		}})
	}
	normalized, err := formatVariant(fs, file)
	if err != nil {
		return gap("comparison_format_failed")
	}
	out.ExpectedStructureSHA256, err = syntax(routingPath, expected)
	if err != nil {
		return gap("host_structure_unresolved")
	}
	out.RemainingStructureSHA256, err = syntax(routingPath, normalized)
	if err != nil {
		return gap("candidate_structure_unresolved")
	}
	out.Matched = out.ExpectedStructureSHA256 == out.RemainingStructureSHA256
	if !out.Matched {
		out.Gaps = append(out.Gaps, "remaining_structure_changed")
	}
	return out
}

func routingLocation(fs *token.FileSet, data []byte, kind string, node ast.Node) routingNode {
	start, end := fs.PositionFor(node.Pos(), false), fs.PositionFor(node.End(), false)
	return routingNode{Tree: "candidate", Path: routingPath, Kind: kind, Line: start.Line, Column: start.Column, Offset: start.Offset, End: end.Offset, SHA256: digest(data[start.Offset:end.Offset])}
}

// productionIdentifiers 逐节点扫描所有生产文件；当前文件复用 AST，以核对声明和使用的节点身份。
func productionIdentifiers(files map[string][]byte, name string, current *ast.File) ([]*ast.Ident, bool) {
	var found []*ast.Ident
	for _, path := range gate.SortedKeys(files) {
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			continue
		}
		file := current
		if path != routingPath || file == nil {
			_, parsed, err := parseVariant(path, files[path])
			if err != nil {
				return nil, false
			}
			file = parsed
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name == name {
				found = append(found, id)
			}
			return true
		})
	}
	return found, true
}
func routingFunction(file *ast.File, name string, method bool) *ast.FuncDecl {
	var found *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != name {
			continue
		}
		if method {
			if fn.Recv == nil || len(fn.Recv.List) != 1 || !routingIdent(fn.Recv.List[0].Type, "proxyService") {
				continue
			}
		} else if fn.Recv != nil {
			continue
		}
		if found != nil {
			return nil
		}
		found = fn
	}
	return found
}
func routingIdent(expr ast.Expr, name string) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == name
}
func routingSelector(expr ast.Expr, pkg, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	return ok && routingIdent(sel.X, pkg) && sel.Sel.Name == name
}
func contextFirstParameter(fn *ast.FuncDecl) bool {
	if fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
		return false
	}
	p := fn.Type.Params.List[0]
	return len(p.Names) == 1 && p.Names[0].Name == "ctx" && routingSelector(p.Type, "context", "Context")
}
func timeoutConstantArgument(fn, host *ast.FuncDecl) *ast.Ident {
	if fn == nil || host == nil || fn.Body == nil || len(fn.Body.List) < 2 || nodeShape(fn.Type) != nodeShape(host.Type) || nodeShape(fn.Recv) != nodeShape(host.Recv) || !contextFirstParameter(fn) {
		return nil
	}
	a, ok := fn.Body.List[0].(*ast.AssignStmt)
	if !ok || a.Tok != token.DEFINE || len(a.Lhs) != 2 || len(a.Rhs) != 1 || !routingIdent(a.Lhs[0], "ctx") || !routingIdent(a.Lhs[1], "cancel") {
		return nil
	}
	call, ok := a.Rhs[0].(*ast.CallExpr)
	if !ok || !routingSelector(call.Fun, "context", "WithTimeout") || len(call.Args) != 2 || call.Ellipsis.IsValid() || !routingIdent(call.Args[0], "ctx") {
		return nil
	}
	deferred, ok := fn.Body.List[1].(*ast.DeferStmt)
	if !ok || !routingIdent(deferred.Call.Fun, "cancel") || len(deferred.Call.Args) != 0 || deferred.Call.Ellipsis.IsValid() {
		return nil
	}
	id, _ := call.Args[1].(*ast.Ident)
	return id
}
func constructorRead(stmt ast.Stmt) bool {
	a, ok := stmt.(*ast.AssignStmt)
	return ok && a.Tok == token.ASSIGN && len(a.Lhs) == 1 && len(a.Rhs) == 1 && routingIdent(a.Lhs[0], "_") && routingIdent(a.Rhs[0], "ctx")
}
func hasRoutingIdentifier(node ast.Node, name string) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return !found
	})
	return found
}
