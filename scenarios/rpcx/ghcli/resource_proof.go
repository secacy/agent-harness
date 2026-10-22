package ghcli

import (
	"encoding/hex"
	"errors"
	"go/ast"
	"reflect"

	"github.com/secacy/agent-harness/scenarios/rpcx/analysis"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

type resourcePath struct {
	Exit  string `json:"exit"`
	State string `json:"state"`
}
type resourceProof struct {
	Status string         `json:"status"`
	Reason string         `json:"reason"`
	Mode   string         `json:"mode,omitempty"`
	Paths  []resourcePath `json:"paths,omitempty"`
	Nodes  []sourceNode   `json:"nodes,omitempty"`
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

func validateResourceProof(p resourceProof, call sourceNode, c analysis.Call, indices map[string]*sourceIndex) error {
	if p.Mode != "" && p.Mode != "body" && p.Mode != "defer" {
		return errors.New("unknown resource proof mode")
	}
	unknown := map[string]bool{}
	for _, reason := range []string{"unsupported_binding", "unsupported_derivation", "derivation_must_define_resources", "resource_binding_mismatch", "resource_in_other_rpc_arguments", "unsupported_response_argument", "unsupported_call_statement", "unsupported_pre_call_effect", "unsupported_exit_structure", "unsupported_error_guard", "unsupported_error_return", "unsupported_response_projection", "unproven_error_return", "unproven_error_release", "unsupported_empty_body_guard", "empty_response_not_preserved", "unproven_empty_release", "unsupported_success_structure", "unproven_body_transfer", "success_response_not_preserved", "unsupported_wrapper_constructor", "unsupported_wrapper_methods", "unsupported_wrapper_fields", "wrapper_requires_keyed_fields", "invalid_wrapper_fields", "unproven_cancel_transfer", "close_not_proven_to_preserve_errors", "resource_use_outside_proven_nodes", "unsupported_wrapper_declaration", "remainder_cannot_be_printed"} {
		unknown[reason] = true
	}
	switch p.Status {
	case "unknown":
		if !unknown[p.Reason] || len(p.Paths)+len(p.Nodes) != 0 {
			return errors.New("invalid unknown resource proof")
		}
	case "violated":
		if p.Mode != "body" || c.Status != "fail" || c.Reason != p.Reason || len(p.Paths)+len(p.Nodes) != 0 {
			return errors.New("inconsistent resource violation")
		}
		switch p.Reason {
		case "cancel_before_rpc", "rpc_error_not_preserved", "error_exit_without_release", "empty_exit_without_release", "cancel_before_body_transfer":
		default:
			return errors.New("unknown resource violation")
		}
	case "proven":
		if c.Status != "pass" {
			return errors.New("resource proof cannot override call status")
		}
		var kinds []string
		if p.Mode == "body" {
			if p.Reason != "bounded_body_transfer" || c.Reason != "typed_request_parent_30s_with_body_release" || !reflect.DeepEqual(p.Paths, []resourcePath{{"rpc_error", "released"}, {"empty_body", "released"}, {"response_body", "transferred"}, {"body_close", "released"}}) {
				return errors.New("invalid body resource paths")
			}
			kinds = []string{"AssignStmt", "AssignStmt", "IfStmt", "IfStmt", "AssignStmt", "ReturnStmt", "TypeSpec", "FuncDecl"}
		} else {
			if p.Mode != "defer" || p.Reason != "local_defer_release" || c.Reason != "typed_request_parent_30s_with_release" || len(p.Paths) != 0 {
				return errors.New("invalid local release proof")
			}
			kinds = []string{"AssignStmt", "CallExpr"}
		}
		if len(p.Nodes) != len(kinds) {
			return errors.New("missing resource proof nodes")
		}
		seen := map[sourceNode]bool{}
		var fn *ast.FuncDecl
		for _, d := range indices[call.File].file.Decls {
			if f, ok := d.(*ast.FuncDecl); ok && f.Body != nil {
				tf := indices[call.File].fset.File(f.Pos())
				if tf.Offset(f.Pos()) < call.Start && tf.Offset(f.End()) > call.End {
					fn = f
					break
				}
			}
		}
		if fn == nil {
			return errors.New("resource call has no function")
		}
		for j, n := range p.Nodes {
			i := indices[n.File]
			if i == nil || n.Kind != kinds[j] || seen[n] {
				return errors.New("invalid resource proof node")
			}
			seen[n] = true
			if _, err := i.node(n); err != nil {
				return err
			}
			if j < len(kinds)-2 && n.File != call.File {
				return errors.New("resource proof crosses function source")
			}

			if j < 6 && p.Mode == "body" || p.Mode == "defer" {
				tf := indices[call.File].fset.File(fn.Pos())
				if n.File != call.File || n.Start < tf.Offset(fn.Body.Pos()) || n.End > tf.Offset(fn.Body.End()) {
					return errors.New("resource node is outside call function")
				}
			}
		}
		selected := p.Nodes[1]
		if p.Mode == "defer" {
			if selected != call {
				return errors.New("local release call mismatch")
			}
		} else if selected.Start > call.Start || selected.End < call.End {
			return errors.New("body proof selects another call")
		}
	default:
		return errors.New("missing or invalid resource status")
	}
	if c.Status == "pass" && p.Status != "proven" {
		return errors.New("passed call has no release proof")
	}
	return nil
}

func validateBoundaryProof(p scopeProof, candidate map[string][]byte, baselines []map[string][]byte, indices map[string]*sourceIndex) error {
	var baseline map[string][]byte
	if len(baselines) == 1 {
		baseline = baselines[0]
	}
	if len(baselines) > 1 || p.BaselineSHA256 != gate.Digest(baseline[boundaryFile]) || p.CandidateSHA256 != gate.Digest(candidate[boundaryFile]) || p.Edits == nil {
		return errors.New("boundary proof input mismatch")
	}
	if p.Status != "pass" && p.Status != "unknown" {
		return errors.New("invalid boundary proof status")
	}
	switch p.Reason {
	case "boundary_not_proven", "semantic_directive_unproven", "unexplained_boundary_change":
		if p.Status != "unknown" {
			return errors.New("unproven boundary marked passed")
		}
	case "typed_body_boundary_preserved":
		if p.Status != "pass" {
			return errors.New("inconsistent boundary classification")
		}
	default:
		return errors.New("unknown boundary proof reason")
	}
	hash := func(s string) bool { b, err := hex.DecodeString(s); return err == nil && len(b) == 32 }
	if p.BeforeSHA256 != "" || p.AfterSHA256 != "" {
		if !hash(p.BeforeSHA256) || !hash(p.AfterSHA256) {
			return errors.New("invalid normalized boundary digest")
		}
	}
	if p.Status == "pass" && (indices[boundaryFile] == nil || len(baseline[boundaryFile]) == 0 || len(p.Edits) == 0 || !hash(p.BeforeSHA256) || p.BeforeSHA256 != p.AfterSHA256) {
		return errors.New("passed boundary has no equal complete remainder")
	}
	counts := map[string]int{}
	seen := map[sourceNode]bool{}
	for _, s := range p.Edits {
		if s.Node.File != boundaryFile || seen[s.Node] || s.Definition != nil {
			return errors.New("invalid boundary edit identity")
		}
		seen[s.Node] = true
		counts[s.Role]++
		want := ""
		switch s.Role {
		case "context_alias":
			if s.Node.Kind != "AssignStmt" && s.Node.Kind != "DeclStmt" {
				return errors.New("invalid alias edit")
			}
		case "unreachable_nil_fallback", "dispatch_nil_fallback":
			want = "IfStmt"
		case "timeout_derivation":
			want = "AssignStmt"
		case "timeout_constant", "body_type":
			want = "GenDecl"
		case "body_close":
			want = "FuncDecl"
		case "rpc_return_projection":
			if s.Node.Kind != "AssignStmt" && s.Node.Kind != "IfStmt" && s.Node.Kind != "ReturnStmt" {
				return errors.New("invalid return projection")
			}
		case "unused_time_import", "sdk_import":
			want = "ImportSpec"
		case "sdk_dial":
			want = "CallExpr"
		case "call_signature":
			want = "Field"
		default:
			return errors.New("unknown boundary edit role")
		}
		if want != "" && s.Node.Kind != want {
			return errors.New("boundary edit AST kind mismatch")
		}
		i := indices[s.Node.File]
		if i == nil {
			return errors.New("boundary edit source inactive")
		}
		if _, err := i.node(s.Node); err != nil {
			return err
		}
	}
	if p.Status == "pass" {
		for role, want := range map[string]int{"timeout_derivation": 1, "body_type": 1, "body_close": 1, "rpc_return_projection": 5, "sdk_import": 1, "sdk_dial": 1, "call_signature": 1} {
			if counts[role] != want {
				return errors.New("incomplete boundary edit plan")
			}
		}
	}
	return nil
}
