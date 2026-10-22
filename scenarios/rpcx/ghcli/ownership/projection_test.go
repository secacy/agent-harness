package ownership

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectionPreservesBusinessChanges(t *testing.T) {
	s := fixture(t)
	good := inspect(t, s)
	if good.Status != Proven || !strings.Contains(good.CallRemainder, `client.Call("http.do", request, &response)`) {
		t.Fatal(good)
	}
	for _, tc := range []struct{ before, after string }{
		{`"http.do", request, &response`, `"changed", request, &response`},
		{`"http.do", request, &response`, `"http.do", request.Clone(request.Context()), &response`},
	} {
		r := inspect(t, replace(t, s, tc.before, tc.after))
		if r.Status != Proven || r.CallRemainder == good.CallRemainder {
			t.Fatalf("business change vanished from projection: %+v", r)
		}
	}
}

// TestSavedBoundaryStudy 仅研究已暴露的边界文件，不执行候选或离线参考。
func TestSavedBoundaryStudy(t *testing.T) {
	source := os.Getenv("HARNESS_GHCLI_OWNERSHIP_CANDIDATE")
	if source == "" {
		t.Skip("set HARNESS_GHCLI_OWNERSHIP_CANDIDATE and HARNESS_GHCLI_OWNERSHIP_SAVED_OUTPUT for saved-boundary study")
	}
	output := os.Getenv("HARNESS_GHCLI_OWNERSHIP_SAVED_OUTPUT")
	if output == "" {
		t.Fatal("HARNESS_GHCLI_OWNERSHIP_SAVED_OUTPUT is required")
	}
	input, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(input)) != "8f060e7d060f1887f06537ef3e6730d75826466f1edf259cb64d65622b499663" {
		t.Fatal("unexpected saved boundary digest")
	}
	output, err = filepath.Abs(output)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(output, 0755); err != nil {
		t.Fatal(err)
	}
	r := inspect(t, string(input))
	if r.Status != Proven {
		t.Fatalf("saved boundary not supported by prototype: %+v", r)
	}
	base, err := os.ReadFile("../../l2/data/rpcx-l2-ghcli-v1/fixtures/ghcli/pkg/cmd/release/delete/outbound.go")
	if err != nil {
		t.Fatal(err)
	}
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, "baseline.go", base, 0)
	if err != nil {
		t.Fatal(err)
	}
	var remainder string
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == "invokeHTTP" {
			remainder, err = renderBlock(&ast.BlockStmt{List: fn.Body.List[len(fn.Body.List)-2:]})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if remainder == "" || r.CallRemainder != remainder {
		t.Fatalf("call/return remainder differs:\n%s\nwant:\n%s", r.CallRemainder, remainder)
	}
	after, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(after, input) {
		t.Fatal("saved input changed")
	}
	data, err := json.MarshalIndent(map[string]any{"analysis": r, "call_and_return_match_baseline": true, "candidate_file_sha256": fmt.Sprintf("%x", sha256.Sum256(input)), "baseline_file_sha256": fmt.Sprintf("%x", sha256.Sum256(base)), "coverage": "outbound.go only; no whole-package loading, source/timeout proof, dispatch or full-scope comparison; not a gate verdict"}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(output, "result.json"), append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
}
