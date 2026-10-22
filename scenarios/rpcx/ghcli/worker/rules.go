package main

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
)

func namedFunction(expr ast.Expr, info *types.Info, pkg, name string) bool {
	s, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	o := info.ObjectOf(s.Sel)
	return o != nil && o.Pkg() != nil && o.Pkg().Path() == pkg && o.Name() == name
}
func same(expr ast.Expr, object types.Object, info *types.Info) bool {
	id, ok := expr.(*ast.Ident)
	return ok && info.ObjectOf(id) == object
}
func sdkClient(t types.Type) bool {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := t.(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == "example.com/rpcx/v2" && n.Obj().Name() == "Client"
}

// inspectCalls 组合有限父来源、期限与原局部释放模式；响应资源转移仍保持未知。
func inspectCalls(root, pkg string, fset *token.FileSet, file *ast.File, info *types.Info, r *report) {
	for _, d := range file.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := c.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Call" {
				return true
			}
			selection := info.Selections[sel]
			if selection == nil {
				return true
			}
			sig, ok := selection.Obj().Type().(*types.Signature)
			if !ok || sig.Params().Len() < 3 {
				return true
			}
			// 仅记录 SDK 接收者或本切片的本地 RPC 接口，普通业务 Call 不混入迁移库存。
			local := false
			if t, ok := info.TypeOf(sel.X).(*types.Named); ok {
				local = t.Obj().Pkg() != nil && t.Obj().Pkg().Path() == pkg && t.Obj().Name() == "outboundCaller"
			}
			direct := sdkClient(info.TypeOf(sel.X))
			if !local && !direct {
				return true
			}
			path, _ := relative(root, fset.Position(c.Pos()).Filename)
			f := callFact{Package: pkg, File: path, Line: fset.Position(c.Pos()).Line, Function: fn.Name.Name, SDK: "example.com/rpcx/v2", Kind: "bounded", Status: "unknown", Reason: "unsupported_local_flow"}
			proof := sourceProof{Call: nodeLocation(root, fset, c), Status: "unknown", Reason: sourceUnknown, RuleIDs: []string{"CTX-001"}, Steps: []sourceStep{}}
			publish := func() {
				r.Calls = append(r.Calls, f)
				r.proofs = append(r.proofs, proof)
			}
			if len(c.Args) != 4 {
				f.Status = "fail"
				f.Reason = "v2_context_missing"
				publish()
				return true
			}
			if v := info.Types[c.Args[1]].Value; v != nil && v.Kind() == constant.String {
				s := constant.StringVal(v)
				f.Operation = &s
			}
			ctxID, ok := c.Args[0].(*ast.Ident)
			if !ok {
				publish()
				return true
			}
			ctxObj := info.ObjectOf(ctxID)
			var derived *ast.AssignStmt
			var cancel types.Object
			derivedIndex, callIndex := -1, -1
			var receiverSource types.Object
			if id, ok := sel.X.(*ast.Ident); ok {
				receiverSource = info.ObjectOf(id)
			}
			reassigned := false
			provenReceiver := direct
			for i, s := range fn.Body.List {
				if a, ok := s.(*ast.AssignStmt); ok {
					for _, lhs := range a.Lhs {
						if a.Tok != token.DEFINE && same(lhs, ctxObj, info) {
							reassigned = true
						}
						if a.Tok != token.DEFINE && same(lhs, receiverSource, info) {
							reassigned = true
						}
					}
					if len(a.Lhs) == 2 && len(a.Rhs) == 1 && same(a.Lhs[0], ctxObj, info) {
						derived = a
						derivedIndex = i
						if id, ok := a.Lhs[1].(*ast.Ident); ok {
							cancel = info.ObjectOf(id)
						}
					}
				}
				ast.Inspect(s, func(n ast.Node) bool {
					if n == c {
						callIndex = i
					}
					return true
				})
				// 接口只允许单次本地赋值来自真实 SDK 指针。
				if v, ok := s.(*ast.DeclStmt); ok {
					if g, ok := v.Decl.(*ast.GenDecl); ok {
						for _, sp := range g.Specs {
							if v, ok := sp.(*ast.ValueSpec); ok && len(v.Names) == 1 && len(v.Values) == 1 && info.ObjectOf(v.Names[0]) == receiverSource && sdkClient(info.TypeOf(v.Values[0])) {
								provenReceiver = true
							}
						}
					}
				}
			}
			if derived == nil || cancel == nil || reassigned || !provenReceiver || derivedIndex >= callIndex ||
				!directCallStatement(fn.Body.List[callIndex], c) || changedOrEscaped(fn.Body.List[:callIndex], info, ctxObj, receiverSource) {
				publish()
				return true
			}
			deriv, ok := derived.Rhs[0].(*ast.CallExpr)
			if !ok || !namedFunction(deriv.Fun, info, "context", "WithTimeout") || len(deriv.Args) != 2 {
				publish()
				return true
			}
			proof = traceParent(root, fset, fn, info, derivedIndex, deriv.Args[0], c)
			parentStatus := proof.Status
			if parentStatus == "fail" {
				f.Status = "fail"
				f.Reason = "request_parent_not_preserved"
				publish()
				return true
			}
			if parentStatus == "unknown" {
				f.Reason = "request_parent_unproven"
			}
			v := info.Types[deriv.Args[1]].Value
			if v == nil || v.Kind() != constant.Int {
				f.Reason = "timeout_unproven"
				publish()
				return true
			}
			duration, ok := constant.Int64Val(v)
			if !ok {
				publish()
				return true
			}
			f.TimeoutNS = &duration
			if duration != 30000000000 {
				f.Status = "fail"
				f.Reason = "per_call_maximum_not_30s"
				publish()
				return true
			}
			if parentStatus != "pass" {
				publish()
				return true
			}
			deferred := false
			for _, s := range fn.Body.List[derivedIndex+1 : callIndex] {
				if d, ok := s.(*ast.DeferStmt); ok && len(d.Call.Args) == 0 && same(d.Call.Fun, cancel, info) {
					deferred = true
				}
			}
			if !deferred {
				publish()
				return true
			}
			// 衍生与调用间仅接受 defer 以及变量声明，避免任意分支或额外使用。
			safe := true
			for _, s := range fn.Body.List[derivedIndex+1 : callIndex] {
				switch s := s.(type) {
				case *ast.EmptyStmt:
				case *ast.DeferStmt:
					safe = safe && len(s.Call.Args) == 0 && same(s.Call.Fun, cancel, info)
				case *ast.DeclStmt:
					g, ok := s.Decl.(*ast.GenDecl)
					if !ok || g.Tok != token.VAR {
						safe = false
						break
					}
					for _, spec := range g.Specs {
						if len(spec.(*ast.ValueSpec).Values) != 0 {
							safe = false
						}
					}
				default:
					safe = false
				}
			}
			if safe {
				f.Status = "pass"
				f.Reason = "typed_request_parent_30s_with_release"
			}
			publish()
			return true
		})
	}
}

// directCallStatement 排除闭包或条件表达式内部的调用，避免把外层顺序当成实际顺序。
func directCallStatement(stmt ast.Stmt, call *ast.CallExpr) bool {
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		return len(s.Rhs) == 1 && s.Rhs[0] == call
	case *ast.ReturnStmt:
		return len(s.Results) == 1 && s.Results[0] == call
	case *ast.ExprStmt:
		return s.X == call
	}
	return false
}

// changedOrEscaped 同时检查嵌套赋值、部分短声明和地址/闭包逃逸。
func changedOrEscaped(statements []ast.Stmt, info *types.Info, objects ...types.Object) bool {
	invalid := false
	tracked := func(id *ast.Ident) bool {
		if id == nil {
			return false
		}
		object := info.ObjectOf(id)
		for _, want := range objects {
			if want != nil && object == want {
				return true
			}
		}
		return false
	}
	for _, s := range statements {
		ast.Inspect(s, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.AssignStmt:
				for _, lhs := range n.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && info.Defs[id] == nil && tracked(id) {
						invalid = true
					}
				}
			case *ast.UnaryExpr:
				if id, ok := n.X.(*ast.Ident); ok && n.Op == token.AND && tracked(id) {
					invalid = true
				}
			case *ast.FuncLit:
				ast.Inspect(n, func(n ast.Node) bool {
					if id, ok := n.(*ast.Ident); ok && tracked(id) {
						invalid = true
					}
					return true
				})
			}
			return true
		})
	}
	return invalid
}
