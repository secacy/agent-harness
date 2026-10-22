package ghcli

import (
	"encoding/json"
	"go/ast"
	"reflect"
	"testing"

	"github.com/secacy/agent-harness/scenarios/rpcx/analysis"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

// proofFixture 构造协议材料，不执行候选或假称运行时质量检查通过。
func proofFixture(t *testing.T) (sourceEnvelope, map[string][]byte) {
	t.Helper()
	data := []byte(`package demo
func invokeHTTP(request *Request)error{
ctx,cancel:=context.WithTimeout(request.Context(),30);defer cancel()
return client.Call(ctx,"http.do",nil,nil)
}`)
	files := map[string][]byte{"boundary.go": data}
	i, err := indexSource("boundary.go", data)
	if err != nil {
		t.Fatal(err)
	}
	loc := func(n ast.Node) sourceNode {
		f := i.fset.File(n.Pos())
		start, end := f.Offset(n.Pos()), f.Offset(n.End())
		return sourceNode{File: "boundary.go", Start: start, End: end, Kind: reflect.TypeOf(n).Elem().Name(), FileSHA256: gate.Digest(data), SHA256: gate.Digest(data[start:end])}
	}
	fn := i.file.Decls[0].(*ast.FuncDecl)
	def := loc(fn.Type.Params.List[0].Names[0])
	var rpc, parent sourceNode
	ast.Inspect(fn, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			if s, ok := c.Fun.(*ast.SelectorExpr); ok {
				switch s.Sel.Name {
				case "Call":
					rpc = loc(c)
				case "Context":
					parent = loc(c)
				}
			}
		}
		return true
	})
	duration := int64(30_000_000_000)
	operation := "http.do"
	r := analysis.Report{SchemaVersion: 2, Status: "complete", InputSHA256: "input", ToolSHA256: "tool", Config: analysis.Configuration{GoVersion: "go1.26.5", GOOS: "linux", GOARCH: "arm64", CGO: "0", Tags: []string{}},
		Packages: []analysis.Package{{Path: "example.com/demo", Files: map[string]string{"boundary.go": gate.Digest(data)}, Ignored: []string{}}},
		Calls:    []analysis.Call{{Package: "example.com/demo", File: "boundary.go", Line: 4, Function: "invokeHTTP", SDK: "example.com/rpcx/v2", Kind: "bounded", Status: "pass", Reason: "typed_request_parent_30s_with_release", Operation: &operation, TimeoutNS: &duration}}, Gaps: []analysis.Gap{}}
	p := sourceProof{Call: rpc, Parent: &parent, Status: "pass", Reason: "request_parent_preserved", RuleIDs: []string{"CTX-001"}, Steps: []sourceStep{{Role: "request_context", Node: parent, Definition: &def}}}
	p.Resource = resourceProof{Status: "proven", Reason: "local_defer_release", Mode: "defer", Nodes: []sourceNode{loc(fn.Body.List[0]), rpc}}
	return sourceEnvelope{SchemaVersion: 2, Report: r, Proofs: []sourceProof{p}, Boundary: scopeProof{Status: "unknown", Reason: "boundary_not_proven", BaselineSHA256: gate.Digest(nil), CandidateSHA256: gate.Digest(nil), Edits: []sourceStep{}}}, files
}

func TestSourceEnvelopeValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*sourceEnvelope)
	}{
		{"valid", nil},
		{"version", func(e *sourceEnvelope) { e.SchemaVersion = 1 }},
		{"input", func(e *sourceEnvelope) { e.Report.InputSHA256 = "other" }},
		{"tool", func(e *sourceEnvelope) { e.Report.ToolSHA256 = "other" }},
		{"OS", func(e *sourceEnvelope) { e.Report.Config.GOOS = "darwin" }},
		{"arch", func(e *sourceEnvelope) { e.Report.Config.GOARCH = "" }},
		{"go-version", func(e *sourceEnvelope) { e.Report.Config.GoVersion = "" }},
		{"invalid-go-version", func(e *sourceEnvelope) { e.Report.Config.GoVersion = "go1.nonsense" }},
		{"cgo", func(e *sourceEnvelope) { e.Report.Config.CGO = "1" }},
		{"tags", func(e *sourceEnvelope) { e.Report.Config.Tags = []string{"unexpected"} }},
		{"null-tags", func(e *sourceEnvelope) { e.Report.Config.Tags = nil }},
		{"missing-resource", func(e *sourceEnvelope) { e.Proofs[0].Resource = resourceProof{} }},
		{"false-body-proof", func(e *sourceEnvelope) { e.Proofs[0].Resource.Mode = "body" }},
		{"resource-node-hash", func(e *sourceEnvelope) { e.Proofs[0].Resource.Nodes[0].SHA256 = "wrong" }},
		{"false-scope", func(e *sourceEnvelope) { e.Boundary.Status = "pass" }},
		{"scope-input", func(e *sourceEnvelope) { e.Boundary.CandidateSHA256 = "wrong" }},
		{"missing-proof", func(e *sourceEnvelope) { e.Proofs = nil }},
		{"duplicate-proof", func(e *sourceEnvelope) {
			e.Proofs = append(e.Proofs, e.Proofs[0])
			e.Report.Calls = append(e.Report.Calls, e.Report.Calls[0])
		}},
		{"missing-parent", func(e *sourceEnvelope) { e.Proofs[0].Parent = nil }},
		{"status", func(e *sourceEnvelope) { e.Proofs[0].Status = "trusted" }},
		{"reason", func(e *sourceEnvelope) { e.Proofs[0].Reason = "unsupported" }},
		{"rule", func(e *sourceEnvelope) { e.Proofs[0].RuleIDs = []string{"TIME-001"} }},
		{"no-steps", func(e *sourceEnvelope) { e.Proofs[0].Steps = []sourceStep{} }},
		{"role", func(e *sourceEnvelope) { e.Proofs[0].Steps[0].Role = "verified_by_model" }},
		{"false-root", func(e *sourceEnvelope) { e.Proofs[0].Steps[0].Role = "context_alias" }},
		{"duplicate-step", func(e *sourceEnvelope) { e.Proofs[0].Steps = append(e.Proofs[0].Steps, e.Proofs[0].Steps[0]) }},
		{"missing-definition", func(e *sourceEnvelope) { e.Proofs[0].Steps[0].Definition = nil }},
		{"definition-digest", func(e *sourceEnvelope) { e.Proofs[0].Steps[0].Definition.SHA256 = "wrong" }},
		{"definition-kind", func(e *sourceEnvelope) { e.Proofs[0].Steps[0].Definition.Kind = "CallExpr" }},
		{"node-digest", func(e *sourceEnvelope) { e.Proofs[0].Call.SHA256 = "wrong" }},
		{"file-digest", func(e *sourceEnvelope) { e.Proofs[0].Call.FileSHA256 = "wrong" }},
		{"negative-range", func(e *sourceEnvelope) { e.Proofs[0].Call.Start = -1 }},
		{"range", func(e *sourceEnvelope) { e.Proofs[0].Call.End = 1 << 20 }},
		{"ast-kind", func(e *sourceEnvelope) { e.Proofs[0].Call.Kind = "Ident" }},
		{"path", func(e *sourceEnvelope) { e.Proofs[0].Call.File = "../boundary.go" }},
		{"parent-after-call", func(e *sourceEnvelope) { e.Proofs[0].Parent = &e.Proofs[0].Call }},
		{"active-source", func(e *sourceEnvelope) { e.Report.Packages[0].Files["boundary.go"] = "wrong" }},
		{"call-line", func(e *sourceEnvelope) { e.Report.Calls[0].Line = 1 }},
		{"call-function", func(e *sourceEnvelope) { e.Report.Calls[0].Function = "other" }},
		{"call-SDK", func(e *sourceEnvelope) { e.Report.Calls[0].SDK = "example.com/fake" }},
		{"call-status", func(e *sourceEnvelope) { e.Report.Calls[0].Status = "covered" }},
		{"call-reason", func(e *sourceEnvelope) { e.Report.Calls[0].Reason = "model_said_so" }},
		{"call-duration", func(e *sourceEnvelope) { *e.Report.Calls[0].TimeoutNS = 31 }},
		{"gap-path", func(e *sourceEnvelope) { e.Report.Gaps = []analysis.Gap{{File: "../bad.go", Reason: "dynamic_import"}} }},
		{"gap-reason", func(e *sourceEnvelope) {
			e.Report.Gaps = []analysis.Gap{{File: "boundary.go", Reason: "ignored_by_agent"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, files := proofFixture(t)
			if tc.change != nil {
				tc.change(&e)
			}
			data, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			_, err = decodeSourceEnvelope(data, files, "input", "tool")
			if (err == nil) != (tc.change == nil) {
				t.Fatalf("unexpected validation: %v", err)
			}
		})
	}
}

func TestSourceEnvelopeStrictJSON(t *testing.T) {
	e, files := proofFixture(t)
	valid, _ := json.Marshal(e)
	for _, data := range [][]byte{append(append([]byte{}, valid...), []byte(` {}`)...), []byte(`{"schema_version":1,"extra":true}`), []byte(`null`)} {
		if _, err := decodeSourceEnvelope(data, files, "input", "tool"); err == nil {
			t.Fatal("invalid JSON accepted")
		}
	}
	files["omitted.go"] = []byte("package demo")
	if _, err := decodeSourceEnvelope(valid, files, "input", "tool"); err == nil {
		t.Fatal("omitted production source accepted")
	}
}

func TestSourceEnvelopeFailedLoadCannotClaimBoundary(t *testing.T) {
	e, files := proofFixture(t)
	e.Report.Status = "load_failed"
	e.Report.LoadExitCode = 1
	e.Report.Diagnostic = "package loading failed"
	e.Report.Packages = []analysis.Package{}
	e.Report.Calls = []analysis.Call{}
	e.Proofs = []sourceProof{}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = decodeSourceEnvelope(data, files, "input", "tool"); err != nil {
		t.Fatal(err)
	}
	e.Boundary.Status = "pass"
	e.Boundary.Reason = "typed_body_boundary_preserved"
	data, err = json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = decodeSourceEnvelope(data, files, "input", "tool"); err == nil {
		t.Fatal("failed loading published a boundary proof")
	}
}
