package ghcli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/version"
	"io"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/secacy/agent-harness/scenarios/rpcx/analysis"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

// 以下为 GHCLI 信封消费端；共享 analysis.Report 的格式保持不变。
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
	SchemaVersion int             `json:"schema_version"`
	Report        analysis.Report `json:"report"`
	Proofs        []sourceProof   `json:"proofs"`
	Boundary      scopeProof      `json:"boundary"`
}

type sourceIndex struct {
	fset  *token.FileSet
	file  *ast.File
	data  []byte
	nodes map[sourceSpan]ast.Node
}

type sourceSpan struct {
	start, end int
	kind       string
}

func indexSource(path string, data []byte) (*sourceIndex, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, data, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	i := &sourceIndex{fset: fset, file: f, data: data, nodes: map[sourceSpan]ast.Node{}}
	ast.Inspect(f, func(n ast.Node) bool {
		if n != nil {
			tf := fset.File(n.Pos())
			i.nodes[sourceSpan{tf.Offset(n.Pos()), tf.Offset(n.End()), reflect.TypeOf(n).Elem().Name()}] = n
		}
		return true
	})
	return i, nil
}

func (i *sourceIndex) node(n sourceNode) (ast.Node, error) {
	if n.Start < 0 || n.End <= n.Start || n.End > len(i.data) || n.FileSHA256 != gate.Digest(i.data) || n.SHA256 != gate.Digest(i.data[n.Start:n.End]) {
		return nil, errors.New("source proof node digest or range mismatch")
	}
	node := i.nodes[sourceSpan{n.Start, n.End, n.Kind}]
	if node == nil || reflect.TypeOf(node).Elem().Name() != n.Kind {
		return nil, errors.New("source proof AST node mismatch")
	}
	return node, nil
}

func decodeSourceEnvelope(data []byte, candidate map[string][]byte, inputSHA, toolSHA string, baselines ...map[string][]byte) (sourceEnvelope, error) {
	var e sourceEnvelope
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&e); err != nil {
		return e, fmt.Errorf("decode GHCLI source proof: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return e, errors.New("GHCLI source proof has trailing content")
	}
	if err := validateSourceEnvelope(e, candidate, inputSHA, toolSHA, baselines...); err != nil {
		return e, err
	}
	return e, nil
}

func validateSourceEnvelope(e sourceEnvelope, candidate map[string][]byte, inputSHA, toolSHA string, baselines ...map[string][]byte) error {
	r := e.Report
	if e.SchemaVersion != 2 || e.Proofs == nil || r.SchemaVersion != 2 || r.InputSHA256 != inputSHA || r.ToolSHA256 != toolSHA ||
		r.Config.GOOS != "linux" || (r.Config.GOARCH != "amd64" && r.Config.GOARCH != "arm64") || !version.IsValid(r.Config.GoVersion) ||
		r.Config.CGO != "0" || r.Config.Tags == nil || len(r.Config.Tags) != 0 || r.Packages == nil || r.Calls == nil || r.Gaps == nil {
		return errors.New("GHCLI source proof version, configuration or binding mismatch")
	}
	if r.Status == "load_failed" {
		if r.LoadExitCode == 0 || r.Diagnostic == "" || len(r.Packages)+len(r.Calls)+len(r.Gaps)+len(e.Proofs) != 0 || e.Boundary.Status != "unknown" || e.Boundary.Reason != "boundary_not_proven" || len(e.Boundary.Edits) != 0 {
			return errors.New("invalid GHCLI failed-load record")
		}
		return validateBoundaryProof(e.Boundary, candidate, baselines, nil)
	}
	if r.Status != "complete" || r.LoadExitCode != 0 || r.Diagnostic != "" || len(r.Packages) == 0 || len(e.Proofs) != len(r.Calls) {
		return errors.New("incomplete GHCLI source proof")
	}
	active := map[string]string{}
	indices := map[string]*sourceIndex{}
	known := func(path string) bool {
		_, ok := candidate[path]
		return ok && filepath.IsLocal(path) && filepath.ToSlash(filepath.Clean(path)) == path
	}
	for _, p := range r.Packages {
		if p.Path == "" || len(p.Files) == 0 {
			return errors.New("invalid GHCLI proof package")
		}
		for path, hash := range p.Files {
			if !known(path) || active[path] != "" || hash != gate.Digest(candidate[path]) {
				return errors.New("GHCLI proof active source mismatch")
			}
			i, err := indexSource(path, candidate[path])
			if err != nil {
				return err
			}
			indices[path] = i
			active[path] = p.Path
		}
		for _, path := range p.Ignored {
			if !known(path) {
				return errors.New("invalid GHCLI ignored source")
			}
		}
	}
	accounted := map[string]bool{}
	for path := range active {
		accounted[path] = true
	}
	for _, g := range r.Gaps {
		if !known(g.File) || g.Line < 0 || (g.Reason != "dynamic_import" && g.Reason != "inactive_build_file" && g.Reason != "unlisted_build_file") {
			return errors.New("invalid GHCLI coverage gap")
		}
		accounted[g.File] = true
	}
	for path := range candidate {
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") && !accounted[path] {
			return errors.New("GHCLI production source omitted from proof")
		}
	}
	if err := validateBoundaryProof(e.Boundary, candidate, baselines, indices); err != nil {
		return err
	}
	used := make([]bool, len(r.Calls))
	seenCalls := map[sourceNode]bool{}
	for _, p := range e.Proofs {
		if seenCalls[p.Call] {
			return errors.New("duplicate GHCLI proof call")
		}
		seenCalls[p.Call] = true
		i := indices[p.Call.File]
		if i == nil {
			return errors.New("proof call is outside active sources")
		}
		n, err := i.node(p.Call)
		if err != nil {
			return err
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return errors.New("proof does not identify a call")
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Call" {
			return errors.New("proof does not identify an RPC selector")
		}
		var fn *ast.FuncDecl
		for _, d := range i.file.Decls {
			if f, ok := d.(*ast.FuncDecl); ok && f.Body != nil && f.Body.Pos() < call.Pos() && call.End() < f.Body.End() {
				fn = f
				break
			}
		}
		if fn == nil {
			return errors.New("proof call has no containing function")
		}
		line := i.fset.PositionFor(call.Pos(), false).Line
		match := -1
		for j, c := range r.Calls {
			if !used[j] && c.File == p.Call.File && c.Line == line && c.Function == fn.Name.Name && c.Package == active[c.File] {
				match = j
				break
			}
		}
		if match < 0 {
			return errors.New("source proof has no matching RPC fact")
		}
		used[match] = true
		if err := validateResourceProof(p.Resource, p.Call, r.Calls[match], indices); err != nil {
			return err
		}
		if err := validateSourceProof(p, r.Calls[match], fn, indices); err != nil {
			return err
		}
	}

	if e.Boundary.Status == "pass" {
		if len(e.Proofs) != 1 || e.Proofs[0].Status != "pass" || e.Proofs[0].Call.File != boundaryFile || e.Proofs[0].Resource.Status != "proven" || e.Proofs[0].Resource.Mode != "body" || r.Calls[0].Function != "invokeHTTP" || r.Calls[0].Operation == nil || *r.Calls[0].Operation != "http.do" {
			return errors.New("boundary proof has no matching body/source proof")
		}
	}
	return nil
}

func validateSourceProof(p sourceProof, c analysis.Call, fn *ast.FuncDecl, indices map[string]*sourceIndex) error {
	reasons := map[string]string{"pass": "request_parent_preserved", "fail": "request_parent_not_preserved", "unknown": "request_parent_unproven"}
	if reasons[p.Status] == "" || p.Reason != reasons[p.Status] || !reflect.DeepEqual(p.RuleIDs, []string{"CTX-001"}) || p.Steps == nil || c.Kind != "bounded" || c.SDK != "example.com/rpcx/v2" {
		return errors.New("invalid GHCLI source proof classification")
	}
	if p.Parent == nil {
		if p.Status != "unknown" || len(p.Steps) != 0 {
			return errors.New("missing proof parent")
		}
	} else {
		i := indices[p.Parent.File]
		if i == nil || p.Parent.File != p.Call.File || p.Parent.End >= p.Call.Start {
			return errors.New("invalid proof parent location")
		}
		n, err := i.node(*p.Parent)
		if err != nil {
			return err
		}
		if _, ok := n.(ast.Expr); !ok || n.Pos() < fn.Body.Pos() {
			return errors.New("proof parent is outside its function")
		}
	}
	seen := map[sourceNode]bool{}
	for j, s := range p.Steps {
		i := indices[s.Node.File]
		if i == nil || s.Node.File != p.Call.File || s.Node.End >= p.Call.Start || seen[s.Node] {
			return errors.New("invalid or duplicate source proof step")
		}
		seen[s.Node] = true
		n, err := i.node(s.Node)
		if err != nil {
			return err
		}
		if n.Pos() < fn.Body.Pos() {
			return errors.New("source proof step is outside its function")
		}
		want := ""
		switch s.Role {
		case "request_context":
			want = "CallExpr"
			if j != 0 || p.Status != "pass" {
				return errors.New("invalid request proof root")
			}
		case "detached_context":
			want = "CallExpr"
			if j != 0 || p.Status != "fail" {
				return errors.New("invalid detached proof root")
			}
		case "context_alias":
			if s.Node.Kind != "AssignStmt" && s.Node.Kind != "DeclStmt" {
				return errors.New("invalid alias node")
			}
		case "unreachable_nil_fallback":
			want = "IfStmt"
			if p.Status != "pass" {
				return errors.New("invalid unreachable branch proof")
			}
		default:
			return errors.New("unknown GHCLI source proof role")
		}
		if j == 0 && s.Role != "request_context" && s.Role != "detached_context" {
			return errors.New("missing source proof root")
		}
		if want != "" && s.Node.Kind != want {
			return errors.New("source proof role and AST mismatch")
		}
		if s.Role != "detached_context" {
			if s.Definition == nil || s.Definition.File != p.Call.File || s.Definition.Kind != "Ident" {
				return errors.New("missing source object definition")
			}
			def, err := i.node(*s.Definition)
			if err != nil {
				return err
			}
			if def.Pos() < fn.Pos() || def.End() > n.End() {
				return errors.New("source definition is outside its function or use")
			}
		} else if s.Definition != nil {
			return errors.New("detached context cannot claim a request definition")
		}
	}
	if p.Status == "unknown" && len(p.Steps) != 0 || p.Status != "unknown" && (p.Parent == nil || len(p.Steps) == 0) {
		return errors.New("source proof has inconsistent steps")
	}
	switch c.Status {
	case "pass":
		if p.Status != "pass" || (c.Reason != "typed_request_parent_30s_with_release" && c.Reason != "typed_request_parent_30s_with_body_release") || c.TimeoutNS == nil || *c.TimeoutNS != 30_000_000_000 {
			return errors.New("invalid passed GHCLI call")
		}
	case "fail":
		switch c.Reason {
		case "request_parent_not_preserved":
			if p.Status != "fail" {
				return errors.New("unproven parent violation")
			}
		case "per_call_maximum_not_30s":
			if c.TimeoutNS == nil || *c.TimeoutNS == 30_000_000_000 {
				return errors.New("invalid duration violation")
			}
		case "v2_context_missing":
		default:
			if p.Resource.Status != "violated" || p.Resource.Reason != c.Reason {
				return errors.New("unknown GHCLI violation reason")
			}
		}
	case "unknown":
		if p.Status == "fail" || (c.Reason != "unsupported_local_flow" && c.Reason != "request_parent_unproven" && c.Reason != "timeout_unproven") {
			return errors.New("invalid unknown GHCLI call")
		}
	default:
		return errors.New("invalid GHCLI call status")
	}
	return nil
}
