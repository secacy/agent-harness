package main

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

type resourceProof struct {
	Status string       `json:"status"`
	Reason string       `json:"reason"`
	Mode   string       `json:"mode,omitempty"`
	Paths  []Path       `json:"paths,omitempty"`
	Nodes  []sourceNode `json:"nodes,omitempty"`
}
type scopeProof struct {
	Status          string       `json:"status"`
	Reason          string       `json:"reason"`
	BaselineSHA256  string       `json:"baseline_sha256"`
	CandidateSHA256 string       `json:"candidate_sha256"`
	BeforeSHA256    string       `json:"before_sha256,omitempty"`
	AfterSHA256     string       `json:"after_sha256,omitempty"`
	Edits           []sourceStep `json:"edits"`
}

func unknownScope(baseline, candidate []byte, reason string) scopeProof {
	p := scopeProof{Status: "unknown", Reason: reason, BaselineSHA256: digest(baseline), Edits: []sourceStep{}}
	if candidate != nil {
		p.CandidateSHA256 = digest(candidate)
	}
	return p
}
func makeResourceProof(root string, fset *token.FileSet, r Result) resourceProof {
	p := resourceProof{Status: r.Status, Reason: r.Reason, Mode: "body", Paths: r.Paths}
	for _, n := range r.nodes {
		p.Nodes = append(p.Nodes, nodeLocation(root, fset, n))
	}
	return p
}

type nodeKey struct {
	start, end int
	kind       string
}

func key(fset *token.FileSet, n ast.Node) nodeKey {
	f := fset.File(n.Pos())
	return nodeKey{f.Offset(n.Pos()), f.Offset(n.End()), reflect.TypeOf(n).Elem().Name()}
}

// proveBoundary 在类型仍可用时构造只用于比较的副本，任何无法解释的剩余修改均保留未知。
func proveBoundary(root string, fset *token.FileSet, file *ast.File, info *types.Info, fn *ast.FuncDecl, derive *ast.AssignStmt, call *ast.CallExpr, parent sourceProof, ownership Result, baseline []byte) scopeProof {
	path, err := relative(root, fset.File(file.Pos()).Name())
	if err != nil || path != "pkg/cmd/release/delete/outbound.go" {
		return unknownScope(baseline, nil, "boundary_not_proven")
	}
	data, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		return unknownScope(baseline, nil, "boundary_not_proven")
	}
	out := unknownScope(baseline, data, "boundary_not_proven")
	if ownership.Status != Proven || parent.Status != "pass" || fn.Name.Name != "invokeHTTP" || len(baseline) == 0 {
		return out
	}
	baseSet := token.NewFileSet()
	base, err := parser.ParseFile(baseSet, "baseline.go", baseline, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return out
	}
	// 当前认证边界没有语义指令；新增指令不能按普通注释消失。
	if directives(file) || directives(base) {
		out.Reason = "semantic_directive_unproven"
		return out
	}
	cloneSet := token.NewFileSet()
	clone, err := parser.ParseFile(cloneSet, "candidate.go", data, parser.SkipObjectResolution)
	if err != nil {
		return out
	}
	base, err = parser.ParseFile(token.NewFileSet(), "baseline.go", baseline, parser.SkipObjectResolution)
	if err != nil {
		return out
	}
	original := map[nodeKey]ast.Node{}
	ast.Inspect(file, func(n ast.Node) bool {
		if n != nil {
			original[key(fset, n)] = n
		}
		return true
	})
	erase := map[nodeKey]bool{}
	edit := func(role string, n ast.Node) {
		erase[key(fset, n)] = true
		out.Edits = append(out.Edits, sourceStep{Role: role, Node: nodeLocation(root, fset, n)})
	}
	aliases := map[types.Object]bool{}
	allowed := map[*ast.Ident]bool{}
	allow := func(n ast.Node) {
		ast.Inspect(n, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				allowed[id] = true
			}
			return true
		})
	}
	for _, step := range parent.Steps {
		if step.Role != "context_alias" && step.Role != "unreachable_nil_fallback" {
			continue
		}
		n := original[nodeKey{step.Node.Start, step.Node.End, step.Node.Kind}]
		if n == nil {
			return out
		}
		edit(step.Role, n)
		allow(n)
		if step.Role == "context_alias" {
			ast.Inspect(n, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok && info.Defs[id] != nil {
					aliases[info.Defs[id]] = true
				}
				return true
			})
		}
	}
	d := derive.Rhs[0].(*ast.CallExpr)
	allow(d.Args[0])
	for id, o := range info.Uses {
		if aliases[o] && !allowed[id] {
			return out
		}
	}
	edit("timeout_derivation", derive)
	// 只移除本次期限独占的私有常量，其他声明仍进入完整比较。
	if id, ok := d.Args[1].(*ast.Ident); ok {
		constant, ok := info.ObjectOf(id).(*types.Const)
		if !ok || constant.Exported() {
			return out
		}
		for use, o := range info.Uses {
			if o == constant && use != id {
				return out
			}
		}
		found := false
		for _, decl := range file.Decls {
			g, ok := decl.(*ast.GenDecl)
			if !ok || g.Tok != token.CONST || len(g.Specs) != 1 {
				continue
			}
			v, ok := g.Specs[0].(*ast.ValueSpec)
			if ok && len(v.Names) == 1 && len(v.Values) == 1 && info.Defs[v.Names[0]] == constant {
				edit("timeout_constant", g)
				found = true
			}
		}
		if !found {
			return out
		}
	}
	var wrapperDecl *ast.GenDecl
	for _, decl := range file.Decls {
		g, ok := decl.(*ast.GenDecl)
		if ok && g.Tok == token.TYPE && len(g.Specs) == 1 && g.Specs[0] == ownership.wrapper {
			wrapperDecl = g
		}
	}
	if wrapperDecl == nil || ownership.close == nil || nodeLocation(root, fset, ownership.close).File != path {
		return out
	}
	edit("body_type", wrapperDecl)
	edit("body_close", ownership.close)
	var callIndex int
	found := false
	for j, s := range fn.Body.List {
		if a, ok := s.(*ast.AssignStmt); ok && len(a.Rhs) == 1 && a.Rhs[0] == call {
			callIndex = j
			found = true
		}
	}
	if !found {
		return out
	}
	for _, s := range fn.Body.List[callIndex:] {
		edit("rpc_return_projection", s)
	}
	// 只抹去真实标准库 Context 返回值的不可达 nil 分支，不推断任意辅助函数。
	for _, decl := range file.Decls {
		f, ok := decl.(*ast.FuncDecl)
		if !ok || f.Name.Name != "dispatchHTTP" || f.Body == nil {
			continue
		}
		for j, s := range f.Body.List {
			a, ok := s.(*ast.AssignStmt)
			if !ok || a.Tok != token.DEFINE || len(a.Lhs) != 1 || len(a.Rhs) != 1 || j+1 >= len(f.Body.List) {
				continue
			}
			c, ok := a.Rhs[0].(*ast.CallExpr)
			if !ok || len(c.Args) != 0 || !standard(c.Fun, info, "net/http", "Context") {
				continue
			}
			guard, ok := f.Body.List[j+1].(*ast.IfStmt)
			if !ok {
				continue
			}
			obj := object(a.Lhs[0], info)
			t := sourceTracker{root: root, fset: fset, info: info, values: map[types.Object]origin{obj: {status: "pass"}}}
			if t.nilFallback(guard) {
				edit("dispatch_nil_fallback", guard)
			}
		}
	}
	// time 导入仅在其全部用途都属于已解释节点时消去。
	consumed := map[*ast.Ident]bool{}
	for k := range erase {
		if n := original[k]; n != nil {
			ast.Inspect(n, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok {
					consumed[id] = true
				}
				return true
			})
		}
	}
	for _, imp := range file.Imports {
		if imp.Path.Value != `"time"` {
			continue
		}
		var pkg types.Object
		if imp.Name == nil {
			pkg = info.Implicits[imp]
		} else {
			pkg = info.Defs[imp.Name]
		}
		// Implicits 在旧加载器未收集时，可从真实包选择的使用对象恢复。
		if pkg == nil {
			for id, o := range info.Uses {
				if p, ok := o.(*types.PkgName); ok && p.Imported().Path() == "time" {
					pkg = info.ObjectOf(id)
					break
				}
			}
		}
		if pkg == nil {
			return out
		}
		for id, o := range info.Uses {
			if o == pkg && !consumed[id] {
				return out
			}
		}
		edit("unused_time_import", imp)
	}
	var target *ast.FuncDecl
	var decls []ast.Decl
	for _, decl := range clone.Decls {
		if erase[key(cloneSet, decl)] {
			continue
		}
		if g, ok := decl.(*ast.GenDecl); ok && g.Tok == token.IMPORT {
			var specs []ast.Spec
			for _, spec := range g.Specs {
				imp := spec.(*ast.ImportSpec)
				if erase[key(cloneSet, imp)] {
					continue
				}
				if imp.Path.Value == `"example.com/rpcx/v2"` {
					imp.Path.Value = `"example.com/rpcx"`
					out.Edits = append(out.Edits, sourceStep{Role: "sdk_import", Node: nodeLocation(root, fset, original[key(cloneSet, spec)])})
				}
				specs = append(specs, imp)
			}
			g.Specs = specs
		}
		if f, ok := decl.(*ast.FuncDecl); ok {
			if f.Name.Name == fn.Name.Name {
				target = f
			}
			if f.Name.Name == "dispatchHTTP" {
				var body []ast.Stmt
				for _, s := range f.Body.List {
					if !erase[key(cloneSet, s)] {
						body = append(body, s)
					}
				}
				f.Body.List = body
			}
		}
		decls = append(decls, decl)
	}
	clone.Decls = decls
	if target == nil {
		return out
	}
	// 仅按类型身份适配 SDK 构造器和被认证的本地 Call 接口。
	ast.Inspect(clone, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if o, ok := original[key(cloneSet, c)].(*ast.CallExpr); ok && standard(o.Fun, info, "example.com/rpcx/v2", "Dial") {
				c.Fun.(*ast.SelectorExpr).Sel.Name = "NewClient"
				out.Edits = append(out.Edits, sourceStep{Role: "sdk_dial", Node: nodeLocation(root, fset, o)})
			}
		}
		return true
	})
	selection := info.Selections[call.Fun.(*ast.SelectorExpr)]
	if selection != nil {
		for _, decl := range clone.Decls {
			g, ok := decl.(*ast.GenDecl)
			if !ok || g.Tok != token.TYPE {
				continue
			}
			for _, spec := range g.Specs {
				t := spec.(*ast.TypeSpec)
				old, ok := original[key(cloneSet, t)].(*ast.TypeSpec)
				if !ok {
					continue
				}
				n, ok := info.Defs[old.Name].Type().(*types.Named)
				if !ok {
					continue
				}
				it, ok := n.Underlying().(*types.Interface)
				if !ok || it.NumExplicitMethods() != 1 || it.NumEmbeddeds() != 0 || it.ExplicitMethod(0) != selection.Obj() {
					continue
				}
				a, ok := t.Type.(*ast.InterfaceType)
				if !ok || len(a.Methods.List) != 1 {
					continue
				}
				ft, ok := a.Methods.List[0].Type.(*ast.FuncType)
				if !ok || len(ft.Params.List) != 4 {
					continue
				}
				oldFT := old.Type.(*ast.InterfaceType).Methods.List[0].Type.(*ast.FuncType)
				if !named(info.TypeOf(oldFT.Params.List[0].Type), "context", "Context") {
					continue
				}
				out.Edits = append(out.Edits, sourceStep{Role: "call_signature", Node: nodeLocation(root, fset, oldFT.Params.List[0])})
				ft.Params.List = ft.Params.List[1:]
			}
		}
	}
	var body []ast.Stmt
	for j, s := range target.Body.List {
		if j >= callIndex {
			break
		}
		if !erase[key(cloneSet, s)] {
			body = append(body, s)
		}
	}
	project, err := parser.ParseFile(token.NewFileSet(), "projection.go", "package p\nfunc boundary() "+ownership.CallRemainder, 0)
	if err != nil {
		return out
	}
	target.Body.List = append(body, project.Decls[0].(*ast.FuncDecl).Body.List...)
	before, err := boundaryBytes(base)
	if err != nil {
		return out
	}
	after, err := boundaryBytes(clone)
	if err != nil {
		return out
	}
	out.BeforeSHA256 = digest(before)
	out.AfterSHA256 = digest(after)
	if !bytes.Equal(before, after) {
		out.Reason = "unexplained_boundary_change"
		return out
	}
	out.Status = "pass"
	out.Reason = "typed_body_boundary_preserved"
	return out
}

func boundaryBytes(file *ast.File) ([]byte, error) {
	var b bytes.Buffer
	err := format.Node(&b, token.NewFileSet(), file)
	return b.Bytes(), err
}
func directives(file *ast.File) bool {
	for _, group := range file.Comments {
		for _, c := range group.List {
			for _, prefix := range []string{"//go:", "//line ", "/*line ", "//export "} {
				if strings.HasPrefix(c.Text, prefix) {
					return true
				}
			}
		}
	}
	return false
}
func sealScope(root string, p *scopeProof) error {
	if len(p.Edits) == 0 {
		return nil
	}
	proof := sourceProof{Call: p.Edits[0].Node, Steps: p.Edits}
	if err := sealProofs(root, []sourceProof{proof}); err != nil {
		return err
	}
	return nil
}
