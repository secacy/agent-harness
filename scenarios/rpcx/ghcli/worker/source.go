package main

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
)

const (
	sourcePass    = "request_parent_preserved"
	sourceFail    = "request_parent_not_preserved"
	sourceUnknown = "request_parent_unproven"
)

// sourceNode 使用原文件的字节位置，消费端会重新核对 AST 和摘要。
type sourceNode struct {
	File       string `json:"file"`
	Start      int    `json:"start"`
	End        int    `json:"end"`
	Kind       string `json:"kind"`
	FileSHA256 string `json:"file_sha256"`
	SHA256     string `json:"sha256"`
}

type sourceStep struct {
	Role       string      `json:"role"`
	Node       sourceNode  `json:"node"`
	Definition *sourceNode `json:"definition,omitempty"`
}

type sourceProof struct {
	Call     sourceNode    `json:"call"`
	Parent   *sourceNode   `json:"parent,omitempty"`
	Status   string        `json:"status"`
	Reason   string        `json:"reason"`
	RuleIDs  []string      `json:"rule_ids"`
	Steps    []sourceStep  `json:"steps"`
	Resource resourceProof `json:"resource"`
}

type sourceEnvelope struct {
	SchemaVersion int           `json:"schema_version"`
	Report        report        `json:"report"`
	Proofs        []sourceProof `json:"proofs"`
	Boundary      scopeProof    `json:"boundary"`
}

// origin 仅描述本次派生之前的有限来源，不承担资源所有权证明。
type origin struct {
	status       string
	steps        []sourceStep
	dependencies map[types.Object]bool
}

type sourceTracker struct {
	root     string
	fset     *token.FileSet
	info     *types.Info
	requests map[types.Object]bool
	values   map[types.Object]origin
}

func nodeLocation(root string, fset *token.FileSet, n ast.Node) sourceNode {
	file := fset.File(n.Pos())
	path, _ := relative(root, file.Name())
	return sourceNode{File: path, Start: file.Offset(n.Pos()), End: file.Offset(n.End()), Kind: reflect.TypeOf(n).Elem().Name()}
}

func unknownOrigin() origin { return origin{status: "unknown"} }

func requestPointer(t types.Type) bool {
	p, ok := t.(*types.Pointer)
	if !ok {
		return false
	}
	n, ok := p.Elem().(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "net/http" && n.Obj().Name() == "Request"
}

func (t *sourceTracker) step(role string, n ast.Node, object types.Object) sourceStep {
	s := sourceStep{Role: role, Node: nodeLocation(t.root, t.fset, n)}
	if object != nil && object.Pos().IsValid() {
		file := t.fset.File(object.Pos())
		path, _ := relative(t.root, file.Name())
		definition := sourceNode{File: path, Start: file.Offset(object.Pos()), End: file.Offset(object.Pos()) + len(object.Name()), Kind: "Ident"}
		s.Definition = &definition
	}
	return s
}

// sealProofs 将位置绑定到原字节；文件读取或范围错误直接使 worker 失败。
func sealProofs(root string, proofs []sourceProof) error {
	files := map[string][]byte{}
	seal := func(n *sourceNode) error {
		if !filepath.IsLocal(n.File) {
			return fmt.Errorf("invalid proof source path: %s", n.File)
		}
		data, ok := files[n.File]
		if !ok {
			var err error
			data, err = os.ReadFile(filepath.Join(root, n.File))
			if err != nil {
				return err
			}
			files[n.File] = data
		}
		if n.Start < 0 || n.End <= n.Start || n.End > len(data) {
			return fmt.Errorf("invalid proof source range: %s", n.File)
		}
		n.FileSHA256 = digest(data)
		n.SHA256 = digest(data[n.Start:n.End])
		return nil
	}
	for i := range proofs {
		p := &proofs[i]
		if err := seal(&p.Call); err != nil {
			return err
		}
		if p.Parent != nil {
			if err := seal(p.Parent); err != nil {
				return err
			}
		}
		for j := range p.Resource.Nodes {
			if err := seal(&p.Resource.Nodes[j]); err != nil {
				return err
			}
		}
		for j := range p.Steps {
			s := &p.Steps[j]
			if err := seal(&s.Node); err != nil {
				return err
			}
			if s.Definition != nil {
				if err := seal(s.Definition); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (t *sourceTracker) expression(expr ast.Expr) origin {
	if id, ok := expr.(*ast.Ident); ok {
		if v, exists := t.values[t.info.ObjectOf(id)]; exists {
			return v
		}
		return unknownOrigin()
	}
	c, ok := expr.(*ast.CallExpr)
	if !ok {
		return unknownOrigin()
	}
	if (len(c.Args) == 0 && (namedFunction(c.Fun, t.info, "context", "Background") || namedFunction(c.Fun, t.info, "context", "TODO"))) ||
		(len(c.Args) == 1 && namedFunction(c.Fun, t.info, "context", "WithoutCancel")) {
		return origin{status: "fail", steps: []sourceStep{t.step("detached_context", c, nil)}, dependencies: map[types.Object]bool{}}
	}
	if len(c.Args) != 0 || !namedFunction(c.Fun, t.info, "net/http", "Context") {
		return unknownOrigin()
	}
	s := c.Fun.(*ast.SelectorExpr)
	id, ok := s.X.(*ast.Ident)
	if !ok || !t.requests[t.info.ObjectOf(id)] {
		return unknownOrigin()
	}
	object := t.info.ObjectOf(id)
	return origin{status: "pass", steps: []sourceStep{t.step("request_context", c, object)}, dependencies: map[types.Object]bool{object: true}}
}

// invalidate 对未支持语句中的引用保守失效，也使依赖它们的别名失效。
func (t *sourceTracker) invalidate(n ast.Node) {
	mentioned := map[types.Object]bool{}
	ast.Inspect(n, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			mentioned[t.info.ObjectOf(id)] = true
		}
		return true
	})
	for object := range mentioned {
		if object != nil {
			t.requests[object] = false
		}
	}
	for object, v := range t.values {
		for dependency := range v.dependencies {
			if mentioned[dependency] {
				t.values[object] = unknownOrigin()
				break
			}
		}
	}
}

func (t *sourceTracker) declare(id *ast.Ident, rhs ast.Expr, node ast.Node) bool {
	object := t.info.Defs[id]
	if object == nil || id.Name == "_" {
		return false
	}
	v := t.expression(rhs)
	if v.status == "unknown" {
		return false
	}
	dependencies := map[types.Object]bool{object: true}
	for dependency := range v.dependencies {
		dependencies[dependency] = true
	}
	v.dependencies = dependencies
	v.steps = append(append([]sourceStep{}, v.steps...), t.step("context_alias", node, object))
	t.values[object] = v
	return true
}

// nilFallback 仅接受标准请求 context 的不可达兜底分支。
func (t *sourceTracker) nilFallback(s *ast.IfStmt) bool {
	if s.Init != nil || s.Else != nil || len(s.Body.List) != 1 {
		return false
	}
	b, ok := s.Cond.(*ast.BinaryExpr)
	if !ok || b.Op != token.EQL {
		return false
	}
	x, y := b.X, b.Y
	isNil := func(e ast.Expr) bool {
		id, ok := e.(*ast.Ident)
		return ok && t.info.ObjectOf(id) == types.Universe.Lookup("nil")
	}
	if isNil(x) {
		x, y = y, x
	}
	id, ok := x.(*ast.Ident)
	if !ok || !isNil(y) {
		return false
	}
	object := t.info.ObjectOf(id)
	v, exists := t.values[object]
	if !exists || v.status != "pass" {
		return false
	}
	a, ok := s.Body.List[0].(*ast.AssignStmt)
	if !ok || a.Tok != token.ASSIGN || len(a.Lhs) != 1 || len(a.Rhs) != 1 || !same(a.Lhs[0], object, t.info) {
		return false
	}
	c, ok := a.Rhs[0].(*ast.CallExpr)
	if !ok || len(c.Args) != 0 || !namedFunction(c.Fun, t.info, "context", "Background") {
		return false
	}
	v.steps = append(append([]sourceStep{}, v.steps...), t.step("unreachable_nil_fallback", s, object))
	t.values[object] = v
	return true
}

func traceParent(root string, fset *token.FileSet, fn *ast.FuncDecl, info *types.Info, derivedIndex int, expr ast.Expr, call ast.Node) sourceProof {
	t := sourceTracker{root: root, fset: fset, info: info, requests: map[types.Object]bool{}, values: map[types.Object]origin{}}
	for _, field := range fn.Type.Params.List {
		for _, id := range field.Names {
			object := info.ObjectOf(id)
			if requestPointer(object.Type()) {
				t.requests[object] = true
			}
		}
	}
	for _, s := range fn.Body.List[:derivedIndex] {
		accepted := false
		switch s := s.(type) {
		case *ast.AssignStmt:
			if s.Tok == token.DEFINE && len(s.Lhs) == 1 && len(s.Rhs) == 1 {
				if id, ok := s.Lhs[0].(*ast.Ident); ok {
					accepted = t.declare(id, s.Rhs[0], s)
				}
			}
		case *ast.DeclStmt:
			if g, ok := s.Decl.(*ast.GenDecl); ok && g.Tok == token.VAR && len(g.Specs) == 1 {
				v := g.Specs[0].(*ast.ValueSpec)
				if len(v.Names) == 1 && len(v.Values) == 1 {
					accepted = t.declare(v.Names[0], v.Values[0], s)
				}
			}
		case *ast.IfStmt:
			accepted = t.nilFallback(s)
		}
		if !accepted {
			t.invalidate(s)
		}
	}
	v := t.expression(expr)
	reason := sourceUnknown
	if v.status == "pass" {
		reason = sourcePass
	} else if v.status == "fail" {
		reason = sourceFail
	}
	parent := nodeLocation(root, fset, expr)
	steps := v.steps
	if steps == nil {
		steps = []sourceStep{}
	}
	return sourceProof{Call: nodeLocation(root, fset, call), Parent: &parent, Status: v.status, Reason: reason, RuleIDs: []string{"CTX-001"}, Steps: steps}
}
