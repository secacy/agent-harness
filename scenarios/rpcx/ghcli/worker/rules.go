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

// inspectCalls 只证明一个局部派生、无重赋值的调用，不扩展任意辅助函数或别名数据流。
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
			if len(c.Args) != 4 {
				f.Status = "fail"
				f.Reason = "v2_context_missing"
				r.Calls = append(r.Calls, f)
				return true
			}
			if v := info.Types[c.Args[1]].Value; v != nil && v.Kind() == constant.String {
				s := constant.StringVal(v)
				f.Operation = &s
			}
			ctxID, ok := c.Args[0].(*ast.Ident)
			if !ok {
				r.Calls = append(r.Calls, f)
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
			if derived == nil || cancel == nil || reassigned || !provenReceiver || derivedIndex >= callIndex {
				r.Calls = append(r.Calls, f)
				return true
			}
			deriv, ok := derived.Rhs[0].(*ast.CallExpr)
			if !ok || !namedFunction(deriv.Fun, info, "context", "WithTimeout") || len(deriv.Args) != 2 {
				r.Calls = append(r.Calls, f)
				return true
			}
			parent, ok := deriv.Args[0].(*ast.CallExpr)
			parentOK := false
			if ok && len(parent.Args) == 0 {
				if s, ok := parent.Fun.(*ast.SelectorExpr); ok {
					o := info.ObjectOf(s.Sel)
					if o != nil && o.Pkg() != nil && o.Pkg().Path() == "net/http" && o.Name() == "Context" {
						if id, ok := s.X.(*ast.Ident); ok {
							for _, field := range fn.Type.Params.List {
								for _, p := range field.Names {
									if info.ObjectOf(p) == info.ObjectOf(id) {
										parentOK = true
									}
								}
							}
						}
					}
				}
			}
			if !parentOK {
				f.Status = "fail"
				f.Reason = "request_parent_not_preserved"
				r.Calls = append(r.Calls, f)
				return true
			}
			v := info.Types[deriv.Args[1]].Value
			if v == nil || v.Kind() != constant.Int {
				r.Calls = append(r.Calls, f)
				return true
			}
			duration, ok := constant.Int64Val(v)
			if !ok {
				r.Calls = append(r.Calls, f)
				return true
			}
			f.TimeoutNS = &duration
			if duration != 30000000000 {
				f.Status = "fail"
				f.Reason = "per_call_maximum_not_30s"
				r.Calls = append(r.Calls, f)
				return true
			}
			deferred := false
			for _, s := range fn.Body.List[derivedIndex+1 : callIndex] {
				if d, ok := s.(*ast.DeferStmt); ok && len(d.Call.Args) == 0 && same(d.Call.Fun, cancel, info) {
					deferred = true
				}
			}
			if !deferred {
				r.Calls = append(r.Calls, f)
				return true
			}
			// 衍生与调用间仅接受 defer 以及变量声明，避免任意分支或额外使用。
			safe := true
			for _, s := range fn.Body.List[derivedIndex+1 : callIndex] {
				switch s.(type) {
				case *ast.DeferStmt, *ast.DeclStmt:
				default:
					safe = false
				}
			}
			if safe {
				f.Status = "pass"
				f.Reason = "typed_request_parent_30s_with_release"
			}
			r.Calls = append(r.Calls, f)
			return true
		})
	}
}
