package analysis

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
)

// BoundaryEvidence 只授权指定节点的范围比较，不代表完整业务等价。
type BoundaryEvidence struct {
	SchemaVersion int            `json:"schema_version"`
	Status        string         `json:"status"`
	Reason        string         `json:"reason,omitempty"`
	Nodes         []BoundaryNode `json:"nodes"`
}
type BoundaryNode struct {
	Role   string `json:"role"`
	File   string `json:"file"`
	Offset int    `json:"offset"`
	End    int    `json:"end"`
	SHA256 string `json:"sha256"`
}

// WorkerMaterials 返回实际隔离分析程序；供新增证据绑定原始工具字节。
func WorkerMaterials() (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, name := range []string{"main.go", "rules.go", "evidence.go"} {
		data, err := material.ReadFile("worker/" + name)
		if err != nil {
			return nil, err
		}
		out[name] = data
	}
	return out, nil
}

// ValidateBoundaryEvidence 校验可信 worker 的增量协议和原文锚点，不在宿主重做类型加载。
func ValidateBoundaryEvidence(r Report, sources map[string][]byte) error {
	for _, call := range r.Calls {
		if call.Boundary == nil {
			continue
		}
		if _, err := BoundaryNodes(call, sources[call.File]); err != nil {
			return err
		}
		found := false
		for _, pkg := range r.Packages {
			if pkg.Path == call.Package && pkg.Files[call.File] == digest(sources[call.File]) {
				found = true
			}
		}
		if !found {
			return errors.New("boundary evidence source differs from active package")
		}
	}
	return nil
}

// BoundaryNodes 解析已绑定的原文，返回角色对应节点；调用方只能编辑自己的比较副本。
func BoundaryNodes(call Call, source []byte) (map[string]ast.Node, error) {
	stage := "header"
	fail := func() (map[string]ast.Node, error) {
		return nil, fmt.Errorf("invalid boundary evidence binding or nodes: %s", stage)
	}
	b := call.Boundary
	if b == nil || b.SchemaVersion != 1 || call.Status != "covered" || call.Kind != "direct" || call.SDK != "example.com/rpcx/v2" || call.Reason != "direct_timeout_boundary" || call.TimeoutNS == nil || *call.TimeoutNS != 30000000000 {
		return fail()
	}
	if b.Status == "unsupported" {
		if b.Reason == "" || len(b.Nodes) != 0 {
			return fail()
		}
		return nil, nil
	}
	if b.Status != "complete" || b.Reason != "" || len(b.Nodes) < 9 || len(b.Nodes) > 10 {
		return fail()
	}
	stage = "parse"
	fs := token.NewFileSet()
	file, err := parser.ParseFile(fs, call.File, source, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return fail()
	}
	tf := fs.File(file.Pos())
	allowed := map[string]bool{"function": true, "parent": true, "derive": true, "release": true, "derived": true, "cancel": true, "rpc": true, "rpc_context": true, "duration": true, "constant": true}
	nodes := map[string]ast.Node{}
	for _, n := range b.Nodes {
		stage = "node " + n.Role
		if !allowed[n.Role] || nodes[n.Role] != nil || n.File != call.File || n.Offset < 0 || n.End <= n.Offset || n.End > len(source) || digest(source[n.Offset:n.End]) != n.SHA256 {
			return fail()
		}
		ast.Inspect(file, func(a ast.Node) bool {
			if a != nil && tf.Offset(a.Pos()) == n.Offset && tf.Offset(a.End()) == n.End {
				nodes[n.Role] = a
			}
			return true
		})
		if nodes[n.Role] == nil {
			return fail()
		}
	}
	for role := range allowed {
		if role != "constant" && nodes[role] == nil {
			return fail()
		}
	}
	stage = "function and parent"
	fn, ok := nodes["function"].(*ast.FuncDecl)
	if !ok || fn.Body == nil {
		return fail()
	}
	parent, ok := nodes["parent"].(*ast.Ident)
	if !ok {
		return fail()
	}
	parameter := false
	for _, field := range fn.Type.Params.List {
		for _, id := range field.Names {
			if id == parent {
				parameter = true
			}
		}
	}
	if !parameter {
		return fail()
	}
	stage = "derive"
	assign, ok := nodes["derive"].(*ast.AssignStmt)
	if !ok || assign.Tok != token.DEFINE || len(assign.Lhs) != 2 || len(assign.Rhs) != 1 || assign.Lhs[0] != nodes["derived"] || assign.Lhs[1] != nodes["cancel"] {
		return fail()
	}
	derive, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok || len(derive.Args) != 2 || derive.Args[1] != nodes["duration"] {
		return fail()
	}
	id, ok := derive.Args[0].(*ast.Ident)
	if !ok || id.Name != parent.Name {
		return fail()
	}
	child, ok := nodes["derived"].(*ast.Ident)
	if !ok {
		return fail()
	}
	cancel, ok := nodes["cancel"].(*ast.Ident)
	if !ok {
		return fail()
	}
	stage = "release"
	release, ok := nodes["release"].(*ast.DeferStmt)
	if !ok || len(release.Call.Args) != 0 {
		return fail()
	}
	id, ok = release.Call.Fun.(*ast.Ident)
	if !ok || id.Name != cancel.Name {
		return fail()
	}
	stage = "rpc"
	rpc, ok := nodes["rpc"].(*ast.CallExpr)
	if !ok || (len(rpc.Args) != 3 && len(rpc.Args) != 4) || rpc.Args[0] != nodes["rpc_context"] || fs.Position(rpc.Pos()).Line != call.Line {
		return fail()
	}
	id, ok = rpc.Args[0].(*ast.Ident)
	if !ok || id.Name != child.Name {
		return fail()
	}
	stage = "statement ownership"
	at := -1
	for i, s := range fn.Body.List {
		if s == assign {
			at = i
		}
	}
	if at < 0 || at > 1 || len(fn.Body.List) <= at+1 || fn.Body.List[at+1] != release || rpc.Pos() <= release.End() || rpc.End() > fn.End() {
		return fail()
	}
	if n := nodes["constant"]; n != nil {
		g, ok := n.(*ast.GenDecl)
		if !ok || g.Tok != token.CONST || g.Lparen.IsValid() || len(g.Specs) != 1 {
			return fail()
		}
		v, ok := g.Specs[0].(*ast.ValueSpec)
		if !ok || len(v.Names) != 1 || len(v.Values) != 1 || ast.IsExported(v.Names[0].Name) {
			return fail()
		}
		duration, ok := nodes["duration"].(*ast.Ident)
		if !ok || duration.Name != v.Names[0].Name {
			return fail()
		}
		top := false
		for _, d := range file.Decls {
			if d == g {
				top = true
			}
		}
		if !top {
			return fail()
		}
	}
	nodes["file"] = file
	return nodes, nil
}

// ScopeMaterials 绑定新范围证明消费端与生产端的实际源码。
func ScopeMaterials() (map[string][]byte, error) {
	out, err := WorkerMaterials()
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"types.go", "boundary_evidence.go", "compare.go", "inspect.go"} {
		data, err := material.ReadFile(name)
		if err != nil {
			return nil, err
		}
		out["host/"+name] = data
	}
	return out, nil
}
