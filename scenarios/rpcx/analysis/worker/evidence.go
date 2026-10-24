package main

import (
	"go/ast"
	"go/token"
	"go/types"
	"os"
)

// 只为已覆盖边界提供范围归约所需的对象关系，不改变调用覆盖结论。
type boundaryEvidence struct {
	SchemaVersion int            `json:"schema_version"`
	Status        string         `json:"status"`
	Reason        string         `json:"reason,omitempty"`
	Nodes         []boundaryNode `json:"nodes"`
}
type boundaryNode struct {
	Role   string `json:"role"`
	File   string `json:"file"`
	Offset int    `json:"offset"`
	End    int    `json:"end"`
	SHA256 string `json:"sha256"`
}

func boundaryNodes(root string, fs *token.FileSet, file *ast.File, fn *ast.FuncDecl, rpc *ast.CallExpr, info *types.Info) *boundaryEvidence {
	out := &boundaryEvidence{SchemaVersion: 1, Status: "unsupported", Nodes: []boundaryNode{}}
	reject := func(reason string) *boundaryEvidence { out.Reason = reason; out.Nodes = []boundaryNode{}; return out }
	body := fn.Body.List
	at := 0
	if len(body) > 0 && cancellationGuard(fn, body[0], info) {
		at = 1
	}
	assign := body[at].(*ast.AssignStmt)
	derive := assign.Rhs[0].(*ast.CallExpr)
	release := body[at+1].(*ast.DeferStmt)
	parent := derive.Args[0].(*ast.Ident)
	child := assign.Lhs[0].(*ast.Ident)
	cancel := assign.Lhs[1].(*ast.Ident)
	if info.Defs[cancel] == nil {
		return reject("cancel_binding_not_fresh")
	}
	for id, obj := range info.Uses {
		if obj == info.ObjectOf(cancel) && id != release.Call.Fun {
			return reject("extra_cancel_use")
		}
		if obj == info.ObjectOf(child) && id.Pos() > assign.End() && id != rpc.Args[0] {
			return reject("extra_derived_context_use")
		}
	}
	var parentDecl *ast.Ident
	for _, field := range fn.Type.Params.List {
		for _, id := range field.Names {
			if info.Defs[id] == info.ObjectOf(parent) {
				parentDecl = id
			}
		}
	}
	if parentDecl == nil {
		return reject("parent_parameter_unavailable")
	}
	add := func(role string, node ast.Node) bool {
		tf := fs.File(node.Pos())
		if tf == nil {
			return false
		}
		name, err := relative(root, tf.Name())
		if err != nil {
			return false
		}
		data, err := os.ReadFile(tf.Name())
		if err != nil {
			return false
		}
		start, end := tf.Offset(node.Pos()), tf.Offset(node.End())
		if start < 0 || end <= start || end > len(data) {
			return false
		}
		out.Nodes = append(out.Nodes, boundaryNode{role, name, start, end, digest(data[start:end])})
		return true
	}
	for _, entry := range []struct {
		role string
		node ast.Node
	}{{"function", fn}, {"parent", parentDecl}, {"derive", assign}, {"release", release}, {"derived", child}, {"cancel", cancel}, {"rpc", rpc}, {"rpc_context", rpc.Args[0]}, {"duration", derive.Args[1]}} {
		if !add(entry.role, entry.node) {
			return reject("source_node_unavailable")
		}
	}
	if id, ok := derive.Args[1].(*ast.Ident); ok {
		object, ok := info.ObjectOf(id).(*types.Const)
		if !ok || object.Exported() || object.Pkg() == nil || object.Parent() != object.Pkg().Scope() {
			return reject("unsupported_timeout_constant")
		}
		var declaration *ast.GenDecl
		for _, d := range file.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.CONST || g.Lparen.IsValid() || len(g.Specs) != 1 {
				continue
			}
			value, ok := g.Specs[0].(*ast.ValueSpec)
			if !ok || len(value.Names) != 1 || len(value.Values) != 1 || info.Defs[value.Names[0]] != object {
				continue
			}
			if value.Type != nil {
				sel, ok := value.Type.(*ast.SelectorExpr)
				if !ok {
					return reject("unsupported_constant_type")
				}
				typ, ok := info.Uses[sel.Sel].(*types.TypeName)
				if !ok || typ.Pkg() == nil || typ.Pkg().Path() != "time" || typ.Name() != "Duration" {
					return reject("unsupported_constant_type")
				}
			}
			if !durationExpression(value.Values[0], info) {
				return reject("unsupported_constant_expression")
			}
			declaration = g
		}
		if declaration == nil {
			return reject("constant_not_in_adapter_file")
		}
		for use, obj := range info.Uses {
			if obj == object && use != id {
				return reject("extra_timeout_constant_use")
			}
		}
		if !add("constant", declaration) {
			return reject("source_node_unavailable")
		}
	} else if !durationExpression(derive.Args[1], info) {
		return reject("unsupported_duration_expression")
	}
	out.Status = "complete"
	return out
}

func durationExpression(e ast.Expr, info *types.Info) bool {
	if info.Types[e].Value == nil {
		return false
	}
	switch e := e.(type) {
	case *ast.BasicLit:
		return e.Kind == token.INT || e.Kind == token.FLOAT
	case *ast.ParenExpr:
		return durationExpression(e.X, info)
	case *ast.UnaryExpr:
		return durationExpression(e.X, info)
	case *ast.BinaryExpr:
		return durationExpression(e.X, info) && durationExpression(e.Y, info)
	case *ast.SelectorExpr:
		object, ok := info.Uses[e.Sel].(*types.Const)
		return ok && object.Pkg() != nil && object.Pkg().Path() == "time"
	}
	return false
}
