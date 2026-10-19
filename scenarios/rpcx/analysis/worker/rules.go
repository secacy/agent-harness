package main

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
)

func inspectCalls(root, pkg string, fset *token.FileSet, file *ast.File, info *types.Info, r *report) {
	owners := map[ast.Node]*ast.FuncDecl{}
	direct := map[*ast.SelectorExpr]*ast.CallExpr{}
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				owners[n] = fn
				return true
			})
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				direct[sel] = call
			}
		}
		return true
	})
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Call" {
			return true
		}
		selection := info.Selections[sel]
		if selection == nil {
			return true
		}
		method, ok := selection.Obj().(*types.Func)
		if !ok || method.Pkg() == nil {
			return true
		}
		sdk := method.Pkg().Path()
		_, dynamic := selection.Recv().Underlying().(*types.Interface)
		if !dynamic && sdk != "example.com/rpcx" && sdk != "example.com/rpcx/v2" {
			return true
		}
		pos := fset.Position(sel.Pos())
		path, _ := relative(root, pos.Filename)
		fact := callFact{Package: pkg, File: path, Line: pos.Line, SDK: sdk, Kind: "direct",
			Status: "unknown", Reason: "unsupported_timeout_shape"}
		fn := owners[sel]
		if fn != nil {
			fact.Function = fn.Name.Name
			if object, ok := info.Defs[fn.Name].(*types.Func); ok {
				fact.Function = object.FullName()
			}
		}
		call := direct[sel]
		if !dynamic && call != nil && selection.Kind() != types.MethodExpr {
			index := 0
			if sdk == "example.com/rpcx/v2" {
				index = 1
			}
			if len(call.Args) > index {
				value := info.Types[call.Args[index]].Value
				if value != nil && value.Kind() == constant.String {
					operation := constant.StringVal(value)
					fact.Operation = &operation
				}
			}
		}
		switch {
		case dynamic:
			fact.SDK, fact.Kind, fact.Reason = "", "interface", "interface_dispatch"
		case call == nil || selection.Kind() == types.MethodExpr:
			fact.Kind, fact.Reason = "indirect", "method_value_or_expression"
		case sdk == "example.com/rpcx":
			fact.Status, fact.Reason = "legacy", "v1_call"
		case fn != nil:
			fact.Status, fact.Reason, fact.TimeoutNS = boundary(fn, call, info)
		}
		r.Calls = append(r.Calls, fact)
		if fact.Status == "unknown" {
			r.Gaps = append(r.Gaps, gap{File: path, Line: pos.Line, Reason: fact.Reason})
		}
		return true
	})
}

// 证明从超时派生到 RPC 调用之间的直线前缀；业务处理与调用方链路由其他检查负责。
func boundary(fn *ast.FuncDecl, call *ast.CallExpr, info *types.Info) (string, string, *int64) {
	unknown := func(reason string) (string, string, *int64) { return "unknown", reason, nil }
	body := fn.Body.List
	if len(body) > 0 && cancellationGuard(fn, body[0], info) {
		body = body[1:]
	}
	if len(body) < 3 {
		return unknown("unsupported_timeout_shape")
	}
	assign, ok := body[0].(*ast.AssignStmt)
	if !ok || assign.Tok != token.DEFINE || len(assign.Lhs) != 2 || len(assign.Rhs) != 1 {
		return unknown("unsupported_timeout_shape")
	}
	derive, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok || !packageFunction(derive.Fun, info, "context", "WithTimeout") || len(derive.Args) != 2 {
		return unknown("unsupported_context_derivation")
	}
	parent, ok := derive.Args[0].(*ast.Ident)
	if !ok || !contextParameter(fn, parent, info) {
		return unknown("unproven_parent_context")
	}
	ctx, ctxOK := assign.Lhs[0].(*ast.Ident)
	cancel, cancelOK := assign.Lhs[1].(*ast.Ident)
	if !ctxOK || !cancelOK || info.ObjectOf(ctx) == nil || info.ObjectOf(cancel) == nil {
		return unknown("unproven_derived_bindings")
	}
	deferred, ok := body[1].(*ast.DeferStmt)
	if !ok || len(deferred.Call.Args) != 0 || !sameObject(deferred.Call.Fun, info.ObjectOf(cancel), info) {
		return unknown("unproven_cancel_release")
	}
	if (len(call.Args) != 3 && len(call.Args) != 4) || !sameObject(call.Args[0], info.ObjectOf(ctx), info) {
		return unknown("rpc_context_not_derived")
	}
	// 调用参数与接收者中的任意函数调用都可能先改变取消状态。
	for _, expr := range append([]ast.Expr{call.Fun}, call.Args[1:]...) {
		hasCall := false
		ast.Inspect(expr, func(n ast.Node) bool {
			if nested, ok := n.(*ast.CallExpr); ok && !info.Types[nested.Fun].IsType() {
				hasCall = true
			}
			return true
		})
		if hasCall {
			return unknown("unproven_argument_effects")
		}
	}
	if !directReturn(body[2:], call, info) && !responsePrefix(body[2:], call, info, info.ObjectOf(cancel)) {
		return unknown("unsupported_control_flow")
	}
	value := info.Types[derive.Args[1]].Value
	if value == nil {
		return unknown("nonconstant_timeout")
	}
	ns, ok := constant.Int64Val(value)
	if !ok {
		return unknown("unresolved_timeout_constant")
	}
	if ns != 30_000_000_000 {
		return "violation", "timeout_duration_mismatch", &ns
	}
	return "covered", "direct_timeout_boundary", &ns
}

func packageFunction(expr ast.Expr, info *types.Info, pkg, name string) bool {
	var object types.Object
	switch expr := expr.(type) {
	case *ast.SelectorExpr:
		object = info.Uses[expr.Sel]
	case *ast.Ident:
		object = info.Uses[expr]
	}
	fn, ok := object.(*types.Func)
	return ok && fn.Pkg() != nil && fn.Pkg().Path() == pkg && fn.Name() == name
}

func contextParameter(fn *ast.FuncDecl, id *ast.Ident, info *types.Info) bool {
	for _, field := range fn.Type.Params.List {
		for _, name := range field.Names {
			object := info.Defs[name]
			if object == nil || info.Uses[id] != object {
				continue
			}
			typ, ok := types.Unalias(object.Type()).(*types.Named)
			return ok && typ.Obj().Pkg() != nil && typ.Obj().Pkg().Path() == "context" && typ.Obj().Name() == "Context"
		}
	}
	return false
}

func sameObject(expr ast.Expr, object types.Object, info *types.Info) bool {
	id, ok := expr.(*ast.Ident)
	return ok && object != nil && info.Uses[id] == object
}

func directReturn(body []ast.Stmt, call *ast.CallExpr, info *types.Info) bool {
	if len(body) == 1 {
		ret, ok := body[0].(*ast.ReturnStmt)
		return ok && len(ret.Results) == 1 && ret.Results[0] == call
	}
	if len(body) != 2 {
		return false
	}
	guard, ok := body[0].(*ast.IfStmt)
	if !ok || guard.Else != nil || len(guard.Body.List) != 1 {
		return false
	}
	assign, ok := guard.Init.(*ast.AssignStmt)
	if !ok || assign.Tok != token.DEFINE || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 || assign.Rhs[0] != call {
		return false
	}
	errName, ok := assign.Lhs[0].(*ast.Ident)
	if !ok {
		return false
	}
	condition, ok := guard.Cond.(*ast.BinaryExpr)
	if !ok || condition.Op != token.NEQ || !sameObject(condition.X, info.Defs[errName], info) || !isNil(condition.Y, info) {
		return false
	}
	if _, ok := guard.Body.List[0].(*ast.ReturnStmt); !ok {
		return false
	}
	ret, ok := body[1].(*ast.ReturnStmt)
	return ok && len(ret.Results) == 1 && isNil(ret.Results[0], info)
}

func isNil(expr ast.Expr, info *types.Info) bool {
	id, ok := expr.(*ast.Ident)
	return ok && info.Uses[id] == types.Universe.Lookup("nil")
}

// responsePrefix 只扩展局部请求/响应构造和紧随其后的 RPC；后续业务由范围检查证明。
func responsePrefix(body []ast.Stmt, call *ast.CallExpr, info *types.Info, cancel types.Object) bool {
	if len(body) < 2 || len(call.Args) != 4 {
		return false
	}
	response := call.Args[3]
	if address, ok := response.(*ast.UnaryExpr); ok && address.Op == token.AND {
		response = address.X
	}
	id, ok := response.(*ast.Ident)
	if !ok || info.ObjectOf(id) == nil {
		return false
	}
	target := info.ObjectOf(id)
	declared := false
	rpcFound := false
	for _, statement := range body {
		if rpcFound {
			break
		}
		if assignment, ok := statement.(*ast.AssignStmt); ok && len(assignment.Rhs) == 1 && assignment.Rhs[0] == call {
			rpcFound = assignment.Tok == token.DEFINE && len(assignment.Lhs) == 1
			break
		}
		if guard, ok := statement.(*ast.IfStmt); ok {
			if assignment, ok := guard.Init.(*ast.AssignStmt); ok && len(assignment.Rhs) == 1 && assignment.Rhs[0] == call {
				rpcFound = assignment.Tok == token.DEFINE && len(assignment.Lhs) == 1
				break
			}
		}
		var names []*ast.Ident
		var values []ast.Expr
		switch n := statement.(type) {
		case *ast.DeclStmt:
			d, ok := n.Decl.(*ast.GenDecl)
			if !ok || d.Tok != token.VAR || len(d.Specs) != 1 {
				return false
			}
			v, ok := d.Specs[0].(*ast.ValueSpec)
			if !ok || len(v.Names) != 1 {
				return false
			}
			names, values = v.Names, v.Values
		case *ast.AssignStmt:
			if n.Tok != token.DEFINE || len(n.Lhs) != 1 || len(n.Rhs) != 1 {
				return false
			}
			name, ok := n.Lhs[0].(*ast.Ident)
			if !ok {
				return false
			}
			names, values = []*ast.Ident{name}, n.Rhs
		default:
			return false
		}
		for _, v := range values {
			if !pureLocal(v, info) {
				return false
			}
		}
		for _, name := range names {
			if info.Defs[name] == target {
				declared = true
			}
		}
	}
	stable := true
	for _, statement := range body {
		ast.Inspect(statement, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && info.ObjectOf(id) == cancel {
				stable = false
			}
			return true
		})
	}
	return declared && rpcFound && stable
}

// 仅跳过原 ctx.Err 非空即返回的前置检查，不接受任意条件/副作用。
func cancellationGuard(fn *ast.FuncDecl, statement ast.Stmt, info *types.Info) bool {
	guard, ok := statement.(*ast.IfStmt)
	if !ok || guard.Else != nil || len(guard.Body.List) != 1 {
		return false
	}
	assignment, ok := guard.Init.(*ast.AssignStmt)
	if !ok || assignment.Tok != token.DEFINE || len(assignment.Lhs) != 1 || len(assignment.Rhs) != 1 {
		return false
	}
	call, ok := assignment.Rhs[0].(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Err" {
		return false
	}
	parent, ok := sel.X.(*ast.Ident)
	if !ok || !contextParameter(fn, parent, info) {
		return false
	}
	selection := info.Selections[sel]
	if selection == nil || selection.Obj().Pkg() == nil || selection.Obj().Pkg().Path() != "context" {
		return false
	}
	errName, ok := assignment.Lhs[0].(*ast.Ident)
	if !ok {
		return false
	}
	condition, ok := guard.Cond.(*ast.BinaryExpr)
	if !ok || condition.Op != token.NEQ || !sameObject(condition.X, info.Defs[errName], info) || !isNil(condition.Y, info) {
		return false
	}
	ret, ok := guard.Body.List[0].(*ast.ReturnStmt)
	if !ok {
		return false
	}
	safe := true
	ast.Inspect(ret, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && !info.Types[c.Fun].IsType() {
			if !packageFunction(c.Fun, info, "fmt", "Errorf") {
				safe = false
			}
		}
		return true
	})
	return safe
}

// 局部构造只允许常量、字段读取、类型转换和内建 new；不接受用户函数。
func pureLocal(expr ast.Expr, info *types.Info) bool {
	safe := true
	ast.Inspect(expr, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && !info.Types[call.Fun].IsType() {
			id, ok := call.Fun.(*ast.Ident)
			if !ok || info.Uses[id] != types.Universe.Lookup("new") {
				safe = false
			}
		}
		if _, ok := n.(*ast.FuncLit); ok {
			safe = false
		}
		if unary, ok := n.(*ast.UnaryExpr); ok && unary.Op == token.ARROW {
			safe = false
		}
		return true
	})
	return safe
}
