package main

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sdkImporter 使用公开夹具的真实 SDK 源码，只导入标准库，不装载业务依赖。
type sdkImporter struct {
	standard types.Importer
	sdk      *types.Package
}

func (s *sdkImporter) Import(path string) (*types.Package, error) {
	if path != "example.com/rpcx/v2" {
		return s.standard.Import(path)
	}
	if s.sdk != nil {
		return s.sdk, nil
	}
	fs := token.NewFileSet()
	data, err := os.ReadFile("../../l2/data/rpcx-l2-ghcli-v1/fixtures/rpcx-v2/client.go")
	if err != nil {
		return nil, err
	}
	f, err := parser.ParseFile(fs, "sdk.go", data, 0)
	if err != nil {
		return nil, err
	}
	s.sdk, err = (&types.Config{Importer: s.standard}).Check(path, fs, []*ast.File{f}, nil)
	return s.sdk, err
}

func fixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/boundary.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func inspectOwnership(t *testing.T, source string, extra ...string) Result {
	t.Helper()
	fs := token.NewFileSet()
	files := []*ast.File{}
	for j, code := range append([]string{source}, extra...) {
		f, err := parser.ParseFile(fs, filepath.Join("/study", string(rune('a'+j))+".go"), code, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
	if _, err := (&types.Config{Importer: &sdkImporter{standard: importer.Default()}}).Check("example.com/ownership-study", fs, files, info); err != nil {
		t.Fatal(err)
	}
	var fn *ast.FuncDecl
	var derive *ast.AssignStmt
	var rpc *ast.CallExpr
	for _, d := range files[0].Decls {
		f, ok := d.(*ast.FuncDecl)
		if !ok || f.Body == nil {
			continue
		}
		for _, s := range f.Body.List {
			a, ok := s.(*ast.AssignStmt)
			if !ok || len(a.Rhs) != 1 {
				continue
			}
			c, ok := a.Rhs[0].(*ast.CallExpr)
			if !ok {
				continue
			}
			if standard(c.Fun, info, "context", "WithTimeout") {
				derive = a
				fn = f
			}
			if selector, ok := c.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Call" {
				rpc = c
			}
		}
	}
	var before bytes.Buffer
	for _, f := range files {
		if err := format.Node(&before, fs, f); err != nil {
			t.Fatal(err)
		}
	}
	r := Analyze(files, info, fn, derive, rpc)
	var after bytes.Buffer
	for _, f := range files {
		if err := format.Node(&after, fs, f); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(before.Bytes(), after.Bytes()) {
		t.Fatal("analysis changed the input AST")
	}
	return r
}

func replace(t *testing.T, s, before, after string) string {
	t.Helper()
	if strings.Count(s, before) != 1 {
		t.Fatalf("mutation must be unique: %q", before)
	}
	return strings.Replace(s, before, after, 1)
}

func TestBoundedBodyOwnership(t *testing.T) {
	for _, tc := range []struct{ name, before, after, status, reason string }{
		{"correct", "", "", Proven, "bounded_body_transfer"},
		{"missing-error-release", "cancel()\n        return response.Response, err", "return response.Response, err", Violated, "error_exit_without_release"},
		{"missing-empty-release", "cancel()\n        return response.Response, nil", "return response.Response, nil", Violated, "empty_exit_without_release"},
		{"swallowed-RPC-error", "return response.Response, err\n    }", "return response.Response, nil\n    }", Violated, "rpc_error_not_preserved"},
		{"aliased-error", "return response.Response, err\n    }", "savedErr:=err; return response.Response, savedErr\n    }", Unknown, "unproven_error_return"},
		{"early-cancel", "err = client.Call", "cancel()\n    err = client.Call", Violated, "cancel_before_rpc"},
		{"cancel-before-transfer", "response.Response.Body = &releaseBody", "cancel()\n    response.Response.Body = &releaseBody", Violated, "cancel_before_body_transfer"},
		{"swallowed-close-error", "return body.ReadCloser.Close()", "_ = body.ReadCloser.Close(); return nil", Unknown, "close_not_proven_to_preserve_errors"},
		{"close-before-registering-release", "defer body.release()\n    return body.ReadCloser.Close()", "err := body.ReadCloser.Close(); body.release(); return err", Unknown, "close_not_proven_to_preserve_errors"},
		{"helper-release", "cancel()\n        return response.Response, err", "drop(cancel)\n        return response.Response, err", Unknown, "unproven_error_release"},
		{"wrong-cancel-field", "release: cancel}", "release: func(){}}", Unknown, "unproven_cancel_transfer"},
		{"error-response-replaced", "return response.Response, err\n    }", "return nil, err\n    }", Unknown, "unsupported_response_projection"},
		{"success-response-replaced", "return response.Response, nil\n}", "return nil, nil\n}", Unknown, "success_response_not_preserved"},
		{"nil-guard-order", "response.Response == nil || response.Response.Body == nil", "response.Response.Body == nil || response.Response == nil", Unknown, "unsupported_empty_body_guard"},
		{"nil-guard-and", "response.Response == nil || response.Response.Body == nil", "response.Response == nil && response.Response.Body == nil", Unknown, "unsupported_empty_body_guard"},
		{"extra-response-field-local-only", "type outboundResponse struct { Response *http.Response }", "type outboundResponse struct { Response *http.Response; Other *http.Response }", Proven, "bounded_body_transfer"},
		{"reassigned-cancel", "err = client.Call", "cancel = func(){}\n    err = client.Call", Unknown, "unsupported_pre_call_effect"},
		{"unrelated-defer", "err = client.Call", "defer cancel()\n    err = client.Call", Unknown, "unsupported_pre_call_effect"},
		{"unknown-pre-call-helper", "err = client.Call", "drop(cancel)\n    err = client.Call", Unknown, "unsupported_pre_call_effect"},
		{"rewrite-after-cancel", "err = client.Call", "cancel();callCtx=context.Background()\n    err = client.Call", Unknown, "unsupported_pre_call_effect"},
		{"cancel-in-request", `"http.do", request, &response`, `"http.do", cancel, &response`, Unknown, "resource_in_other_rpc_arguments"},
		{"ctx-in-request", `"http.do", request, &response`, `"http.do", callCtx, &response`, Unknown, "resource_in_other_rpc_arguments"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture(t)
			if tc.before != "" {
				s = replace(t, s, tc.before, tc.after)
			}
			if strings.Contains(s, "drop(") {
				s += "\nfunc drop(c context.CancelFunc){c()}\n"
			}
			r := inspectOwnership(t, s)
			if r.Status != tc.status || r.Reason != tc.reason {
				t.Fatalf("got %+v; want %s/%s", r, tc.status, tc.reason)
			}
			if r.Status == Proven && len(r.Paths) != 4 {
				t.Fatal(r)
			}
		})
	}
}

func TestOwnershipUnknownUses(t *testing.T) {
	for _, tc := range []struct{ name, addition, extra string }{
		{"override-read", "\nfunc(body *releaseBody)Read(p []byte)(int,error){return 0,io.EOF}\n", ""},
		{"extra-method", "\nfunc(body *releaseBody)Detach(){}\n", ""},
		{"escape-type", "\nvar escaped *releaseBody\n", ""},
		{"another-file", "", "package sample\nvar escaped *releaseBody"},
		{"rewrite-cancel", "\nfunc mutate(b *releaseBody){b.release=func(){}}\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture(t) + tc.addition
			var r Result
			if tc.extra != "" {
				r = inspectOwnership(t, s, tc.extra)
			} else {
				r = inspectOwnership(t, s)
			}
			if r.Status != Unknown {
				t.Fatalf("unsupported resource use accepted: %+v", r)
			}
		})
	}
}

func TestOwnershipNamesAreNotIdentity(t *testing.T) {
	s := fixture(t)
	for before, after := range map[string]string{"releaseBody": "bodyHandle", "release": "dispose", "callCtx": "bounded", "cancel": "finish", "response": "outcome", "body": "handle"} {
		s = strings.ReplaceAll(s, before, after)
	}
	if r := inspectOwnership(t, s); r.Status != Proven {
		t.Fatal(r)
	}
}
