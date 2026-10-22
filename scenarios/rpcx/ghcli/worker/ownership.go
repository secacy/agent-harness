// 响应资源检查只接受有限的局部释放/转交形式。
package main

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
)

const (
	Proven   = "proven"
	Unknown  = "unknown"
	Violated = "violated"
)

// Path 说明受支持路径上的资源去向，不是候选质量结论。
type Path struct {
	Exit  string `json:"exit"`
	State string `json:"state"`
}

// Result 只对指定的派生和调用负责；父来源、期限和完整业务范围必须另证。
type Result struct {
	nodes         []ast.Node
	wrapper       *ast.TypeSpec
	close         *ast.FuncDecl
	Status        string `json:"status"`
	Reason        string `json:"reason"`
	Paths         []Path `json:"paths,omitempty"`
	CallRemainder string `json:"call_remainder,omitempty"`
}

type inspector struct {
	info                       *types.Info
	files                      []*ast.File
	ctx, cancel, response, err types.Object
	responseExpr               ast.Expr
	allowed                    map[*ast.Ident]bool
}

func result(status, reason string) Result { return Result{Status: status, Reason: reason} }

func object(expr ast.Expr, info *types.Info) types.Object {
	id, ok := expr.(*ast.Ident)
	if !ok {
		return nil
	}
	return info.ObjectOf(id)
}
func named(t types.Type, pkg, name string) bool {
	n, ok := t.(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == pkg && n.Obj().Name() == name
}
func standard(expr ast.Expr, info *types.Info, pkg, name string) bool {
	s, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	o := info.ObjectOf(s.Sel)
	return o != nil && o.Pkg() != nil && o.Pkg().Path() == pkg && o.Name() == name
}

// Analyze 只识别直线 RPC 后的两个退出分支和一个包装器；不改写或执行输入。
func Analyze(files []*ast.File, info *types.Info, fn *ast.FuncDecl, derive *ast.AssignStmt, call *ast.CallExpr) Result {
	if fn == nil || fn.Body == nil || derive == nil || call == nil || len(derive.Lhs) != 2 || len(derive.Rhs) != 1 || len(call.Args) != 4 {
		return result(Unknown, "unsupported_binding")
	}
	d, ok := derive.Rhs[0].(*ast.CallExpr)
	if !ok || !standard(d.Fun, info, "context", "WithTimeout") || len(d.Args) != 2 {
		return result(Unknown, "unsupported_derivation")
	}
	i := inspector{info: info, files: files, allowed: map[*ast.Ident]bool{}}
	for j, lhs := range derive.Lhs {
		id, ok := lhs.(*ast.Ident)
		if !ok || info.Defs[id] == nil || id.Name == "_" {
			return result(Unknown, "derivation_must_define_resources")
		}
		if j == 0 {
			i.ctx = info.Defs[id]
		} else {
			i.cancel = info.Defs[id]
		}
	}
	if !named(i.cancel.Type(), "context", "CancelFunc") || !same(call.Args[0], i.ctx, info) {
		return result(Unknown, "resource_binding_mismatch")
	}
	for _, arg := range call.Args[1:] {
		escaped := false
		ast.Inspect(arg, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && (info.ObjectOf(id) == i.ctx || info.ObjectOf(id) == i.cancel) {
				escaped = true
			}
			return true
		})
		if escaped {
			return result(Unknown, "resource_in_other_rpc_arguments")
		}
	}
	address, ok := call.Args[3].(*ast.UnaryExpr)
	if !ok || address.Op != token.AND {
		return result(Unknown, "unsupported_response_argument")
	}
	i.response = object(address.X, info)
	if i.response == nil {
		return result(Unknown, "unsupported_response_argument")
	}
	deriveIndex, callIndex := -1, -1
	for j, s := range fn.Body.List {
		if s == derive {
			deriveIndex = j
		}
		if a, ok := s.(*ast.AssignStmt); ok && len(a.Rhs) == 1 && a.Rhs[0] == call && len(a.Lhs) == 1 {
			callIndex = j
			i.err = object(a.Lhs[0], info)
		}
	}
	if deriveIndex < 0 || callIndex <= deriveIndex || i.err == nil {
		return result(Unknown, "unsupported_call_statement")
	}
	earlyCancel := false
	for _, s := range fn.Body.List[deriveIndex+1 : callIndex] {
		if i.cancelCall(s) {
			earlyCancel = true
			continue
		}
		return result(Unknown, "unsupported_pre_call_effect")
	}
	if earlyCancel {
		return result(Violated, "cancel_before_rpc")
	}
	tail := fn.Body.List[callIndex+1:]
	if len(tail) < 4 {
		return result(Unknown, "unsupported_exit_structure")
	}
	errorBranch, ok := tail[0].(*ast.IfStmt)
	if !ok || !i.errorGuard(errorBranch) {
		return result(Unknown, "unsupported_error_guard")
	}
	ret, ok := lastReturn(errorBranch.Body.List)
	if !ok || len(ret.Results) != 2 {
		return result(Unknown, "unsupported_error_return")
	}
	i.responseExpr = ret.Results[0]
	if !i.httpResponse(i.responseExpr) {
		return result(Unknown, "unsupported_response_projection")
	}
	if !same(ret.Results[1], i.err, info) {
		if i.nil(ret.Results[1]) {
			return result(Violated, "rpc_error_not_preserved")
		}
		return result(Unknown, "unproven_error_return")
	}
	if len(errorBranch.Body.List) == 1 {
		return result(Violated, "error_exit_without_release")
	}
	if len(errorBranch.Body.List) != 2 || !i.cancelCall(errorBranch.Body.List[0]) {
		return result(Unknown, "unproven_error_release")
	}
	emptyBranch, ok := tail[1].(*ast.IfStmt)
	if !ok || !i.emptyGuard(emptyBranch) {
		return result(Unknown, "unsupported_empty_body_guard")
	}
	ret, ok = lastReturn(emptyBranch.Body.List)
	if !ok || !i.returnSuccess(ret) {
		return result(Unknown, "empty_response_not_preserved")
	}
	if len(emptyBranch.Body.List) == 1 {
		return result(Violated, "empty_exit_without_release")
	}
	if len(emptyBranch.Body.List) != 2 || !i.cancelCall(emptyBranch.Body.List[0]) {
		return result(Unknown, "unproven_empty_release")
	}
	if i.cancelCall(tail[2]) {
		return result(Violated, "cancel_before_body_transfer")
	}
	if len(tail) != 4 {
		return result(Unknown, "unsupported_success_structure")
	}
	transfer, ok := tail[2].(*ast.AssignStmt)
	if !ok || transfer.Tok != token.ASSIGN || len(transfer.Lhs) != 1 || len(transfer.Rhs) != 1 || !i.body(transfer.Lhs[0]) {
		return result(Unknown, "unproven_body_transfer")
	}
	last, ok := tail[3].(*ast.ReturnStmt)
	if !ok || !i.returnSuccess(last) {
		return result(Unknown, "success_response_not_preserved")
	}
	wrap, ok := transfer.Rhs[0].(*ast.UnaryExpr)
	if !ok || wrap.Op != token.AND {
		return result(Unknown, "unsupported_wrapper_constructor")
	}
	literal, ok := wrap.X.(*ast.CompositeLit)
	if !ok {
		return result(Unknown, "unsupported_wrapper_constructor")
	}
	n, ok := info.TypeOf(literal).(*types.Named)
	if !ok || n.Obj().Exported() || n.NumMethods() != 1 {
		return result(Unknown, "unsupported_wrapper_methods")
	}
	structure, ok := n.Underlying().(*types.Struct)
	if !ok || structure.NumFields() != 2 || len(literal.Elts) != 2 {
		return result(Unknown, "unsupported_wrapper_fields")
	}
	var reader, release *types.Var
	for j := 0; j < 2; j++ {
		f := structure.Field(j)
		if f.Embedded() && named(f.Type(), "io", "ReadCloser") {
			reader = f
		}
		if !f.Embedded() && named(f.Type(), "context", "CancelFunc") {
			release = f
		}
	}
	if reader == nil || release == nil {
		return result(Unknown, "unsupported_wrapper_fields")
	}
	fields := map[types.Object]ast.Expr{}
	for _, elt := range literal.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			return result(Unknown, "wrapper_requires_keyed_fields")
		}
		key := object(kv.Key, info)
		if key == nil || fields[key] != nil {
			return result(Unknown, "invalid_wrapper_fields")
		}
		fields[key] = kv.Value
	}
	if !i.body(fields[reader]) || !same(fields[release], i.cancel, info) {
		return result(Unknown, "unproven_cancel_transfer")
	}
	var close *ast.FuncDecl
	for _, file := range files {
		for _, decl := range file.Decls {
			f, ok := decl.(*ast.FuncDecl)
			if ok && info.Defs[f.Name] == n.Method(0) {
				close = f
			}
		}
	}
	if close == nil || !i.closeMethod(close, n, reader, release) {
		return result(Unknown, "close_not_proven_to_preserve_errors")
	}
	// 所有新资源和包装器用途必须落在已识别节点中，不放过其他文件的引用。
	i.allow(call.Args[0])
	i.allow(errorBranch)
	i.allow(emptyBranch)
	i.allow(transfer)
	i.allow(last)
	i.allow(close)
	tracked := map[types.Object]bool{i.ctx: true, i.cancel: true, n.Obj(): true, reader: true, release: true}
	for id, o := range info.Uses {
		if tracked[o] && !i.allowed[id] {
			return result(Unknown, "resource_use_outside_proven_nodes")
		}
	}
	var wrapper *ast.TypeSpec
	for _, file := range files {
		for _, decl := range file.Decls {
			if g, ok := decl.(*ast.GenDecl); ok && g.Tok == token.TYPE {
				for _, sp := range g.Specs {
					if t, ok := sp.(*ast.TypeSpec); ok && info.Defs[t.Name] == n.Obj() {
						wrapper = t
					}
				}
			}
		}
	}
	if wrapper == nil {
		return result(Unknown, "unsupported_wrapper_declaration")
	}
	remainder, err := callRemainder(fn.Body.List[callIndex].(*ast.AssignStmt), call, i.responseExpr)
	if err != nil {
		return result(Unknown, "remainder_cannot_be_printed")
	}
	return Result{Status: Proven, Reason: "bounded_body_transfer", Paths: []Path{{"rpc_error", "released"}, {"empty_body", "released"}, {"response_body", "transferred"}, {"body_close", "released"}}, CallRemainder: remainder, wrapper: wrapper, close: close, nodes: []ast.Node{derive, fn.Body.List[callIndex], errorBranch, emptyBranch, transfer, last, wrapper, close}}
}

// callRemainder 只投影已证明的调用/返回区间；前缀和其他声明不属于本原型的业务证明。
func callRemainder(assignment *ast.AssignStmt, call *ast.CallExpr, response ast.Expr) (string, error) {
	copyCall := *call
	copyCall.Args = append([]ast.Expr{}, call.Args[1:]...)
	copyAssignment := *assignment
	copyAssignment.Rhs = []ast.Expr{&copyCall}
	block := &ast.BlockStmt{List: []ast.Stmt{&copyAssignment, &ast.ReturnStmt{Results: []ast.Expr{response, assignment.Lhs[0]}}}}
	return renderBlock(block)
}

func renderBlock(block *ast.BlockStmt) (string, error) {
	var buffer bytes.Buffer
	if err := format.Node(&buffer, token.NewFileSet(), block); err != nil {
		return "", err
	}
	// 在新文件重新解析，消除原位置排版影响，不更改原 AST。
	fs := token.NewFileSet()
	file, err := parser.ParseFile(fs, "projection.go", "package projection\nfunc boundary() "+buffer.String(), 0)
	if err != nil {
		return "", err
	}
	buffer.Reset()
	err = format.Node(&buffer, fs, file.Decls[0].(*ast.FuncDecl).Body)
	return buffer.String(), err
}

func (i *inspector) allow(n ast.Node) {
	ast.Inspect(n, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			i.allowed[id] = true
		}
		return true
	})
}
func (i *inspector) nil(expr ast.Expr) bool { return same(expr, types.Universe.Lookup("nil"), i.info) }
func (i *inspector) cancelCall(s ast.Stmt) bool {
	e, ok := s.(*ast.ExprStmt)
	if !ok {
		return false
	}
	c, ok := e.X.(*ast.CallExpr)
	return ok && len(c.Args) == 0 && same(c.Fun, i.cancel, i.info)
}
func (i *inspector) errorGuard(s *ast.IfStmt) bool {
	b, ok := s.Cond.(*ast.BinaryExpr)
	return ok && s.Init == nil && s.Else == nil && b.Op == token.NEQ && same(b.X, i.err, i.info) && i.nil(b.Y)
}
func lastReturn(statements []ast.Stmt) (*ast.ReturnStmt, bool) {
	if len(statements) == 0 {
		return nil, false
	}
	r, ok := statements[len(statements)-1].(*ast.ReturnStmt)
	return r, ok
}
func (i *inspector) httpResponse(expr ast.Expr) bool {
	s, ok := expr.(*ast.SelectorExpr)
	if !ok || !same(s.X, i.response, i.info) {
		return false
	}
	p, ok := i.info.TypeOf(s).(*types.Pointer)
	return ok && named(p.Elem(), "net/http", "Response")
}
func (i *inspector) sameResponse(expr ast.Expr) bool {
	a, ok := i.responseExpr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	b, ok := expr.(*ast.SelectorExpr)
	return ok && same(b.X, i.response, i.info) && i.info.ObjectOf(a.Sel) == i.info.ObjectOf(b.Sel)
}
func (i *inspector) body(expr ast.Expr) bool {
	s, ok := expr.(*ast.SelectorExpr)
	if !ok || !i.sameResponse(s.X) {
		return false
	}
	selection := i.info.Selections[s]
	return selection != nil && selection.Kind() == types.FieldVal && selection.Obj().Pkg() != nil && selection.Obj().Pkg().Path() == "net/http" && selection.Obj().Name() == "Body"
}
func (i *inspector) emptyGuard(s *ast.IfStmt) bool {
	b, ok := s.Cond.(*ast.BinaryExpr)
	if !ok || s.Init != nil || s.Else != nil || b.Op != token.LOR {
		return false
	}
	left, lok := b.X.(*ast.BinaryExpr)
	right, rok := b.Y.(*ast.BinaryExpr)
	return lok && rok && left.Op == token.EQL && right.Op == token.EQL && i.sameResponse(left.X) && i.nil(left.Y) && i.body(right.X) && i.nil(right.Y)
}
func (i *inspector) returnSuccess(s *ast.ReturnStmt) bool {
	return len(s.Results) == 2 && i.sameResponse(s.Results[0]) && i.nil(s.Results[1])
}

func (i *inspector) closeMethod(fn *ast.FuncDecl, n *types.Named, reader, release *types.Var) bool {
	if fn.Recv == nil || len(fn.Recv.List) != 1 || len(fn.Recv.List[0].Names) != 1 || fn.Body == nil || len(fn.Body.List) != 2 {
		return false
	}
	sig := i.info.Defs[fn.Name].Type().(*types.Signature)
	if fn.Name.Name != "Close" || sig.Params().Len() != 0 || sig.Results().Len() != 1 || !types.Identical(sig.Results().At(0).Type(), types.Universe.Lookup("error").Type()) {
		return false
	}
	receiver := i.info.Defs[fn.Recv.List[0].Names[0]]
	p, ok := receiver.Type().(*types.Pointer)
	if !ok || p.Elem() != n {
		return false
	}
	field := func(expr ast.Expr, o types.Object) bool {
		s, ok := expr.(*ast.SelectorExpr)
		return ok && same(s.X, receiver, i.info) && i.info.ObjectOf(s.Sel) == o
	}
	d, ok := fn.Body.List[0].(*ast.DeferStmt)
	if !ok || len(d.Call.Args) != 0 || !field(d.Call.Fun, release) {
		return false
	}
	r, ok := fn.Body.List[1].(*ast.ReturnStmt)
	if !ok || len(r.Results) != 1 {
		return false
	}
	c, ok := r.Results[0].(*ast.CallExpr)
	if !ok || len(c.Args) != 0 || !standard(c.Fun, i.info, "io", "Close") {
		return false
	}
	s := c.Fun.(*ast.SelectorExpr)
	return field(s.X, reader)
}
