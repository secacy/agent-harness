package main

import (
	"bytes"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"testing"
)

// TestSavedCompleteBoundary 只读取已暴露候选和公开基线；逐次核对完整比较不会吞掉业务变化。
func TestSavedCompleteBoundary(t *testing.T) {
	path := os.Getenv("HARNESS_GHCLI_OWNERSHIP_CANDIDATE")
	if path == "" {
		t.Skip("set HARNESS_GHCLI_OWNERSHIP_CANDIDATE for complete-boundary regression")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if digest(data) != "8f060e7d060f1887f06537ef3e6730d75826466f1edf259cb64d65622b499663" {
		t.Fatal("unexpected saved boundary")
	}
	base, err := os.ReadFile("../../l2/data/rpcx-l2-ghcli-v1/fixtures/ghcli/pkg/cmd/release/delete/outbound.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, before, after, want string }{
		{name: "saved", want: "pass"},
		{"operation", `"http.do", outboundRequest`, `"changed", outboundRequest`, "unknown"},
		{"request", `Request: request}, &response`, `Request: request.Clone(request.Context())}, &response`, "unknown"},
		{"dispatch-business", `return fmt.Errorf("invalid outbound request")`, `return fmt.Errorf("changed")`, "unknown"},
		{"extra-declaration", "type releaseBody struct", "var unrelated = 1\n\ntype releaseBody struct", "unknown"},
		{"directive", "func invokeHTTP(", "//go:noinline\nfunc invokeHTTP(", "unknown"},
		{"other-method", "func (body *releaseBody) Close()", "func (body *releaseBody) Extra() {}\n\nfunc (body *releaseBody) Close()", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := string(data)
			if tc.before != "" {
				source = replace(t, source, tc.before, tc.after)
			}
			root := t.TempDir()
			name := filepath.Join(root, "pkg/cmd/release/delete/outbound.go")
			if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(name, []byte(source), 0644); err != nil {
				t.Fatal(err)
			}
			fs := token.NewFileSet()
			f, err := parser.ParseFile(fs, name, source, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}, Implicits: map[ast.Node]types.Object{}}
			if _, err = (&types.Config{Importer: &sdkImporter{standard: importer.Default()}}).Check("example.com/boundary", fs, []*ast.File{f}, info); err != nil {
				t.Fatal(err)
			}
			r := report{baseline: base, boundary: unknownScope(base, []byte(source), "boundary_not_proven")}
			inspectCalls(root, "example.com/boundary", fs, f, info, &r, f)
			if r.boundary.Status != tc.want {
				t.Fatalf("unexpected scope %+v; calls %+v", r.boundary, r.Calls)
			}
			if err := sealScope(root, &r.boundary); err != nil {
				t.Fatal(err)
			}
			if tc.want == "pass" {
				if r.boundary.BeforeSHA256 != r.boundary.AfterSHA256 || len(r.boundary.Edits) < 10 {
					t.Fatalf("incomplete scope %+v", r.boundary)
				}
				for _, e := range r.boundary.Edits {
					if len(e.Node.SHA256) != 64 {
						t.Fatal("unsealed edit")
					}
				}
			}
			after, err := os.ReadFile(name)
			if err != nil || !bytes.Equal(after, []byte(source)) {
				t.Fatal("input changed")
			}
		})
	}
}
