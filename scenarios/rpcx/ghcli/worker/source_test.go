package main

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

func inspectSource(t *testing.T, setup, parent, between, call string, transforms ...func(string) string) report {
	t.Helper()
	if parent == "" {
		parent = "parent"
	}
	if call == "" {
		call = `return caller.Call(ctx,"http.do",nil,nil)`
	}
	source := `package demo
import("context";"net/http";"time";rpcx "example.com/rpcx/v2")
type outboundCaller interface{Call(context.Context,string,any,any)error}
func use(any){};func mutate(any)int{return 0};func identity(c context.Context)context.Context{return c}
func replacement()*http.Request{return &http.Request{}}
func invokeHTTP(request *http.Request, fallback context.Context)error{
client,_:=rpcx.Dial("address");var caller outboundCaller=client;
` + setup + `;
ctx,cancel:=context.WithTimeout(` + parent + `,30*time.Second);defer cancel();
` + between + `;` + call + `}`
	for _, transform := range transforms {
		source = transform(source)
	}
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, "/module/boundary.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
	if _, err = (&types.Config{Importer: stubImporter{importer.Default()}}).Check("example.com/demo", fs, []*ast.File{f}, info); err != nil {
		t.Fatal(err)
	}
	r := report{}
	inspectCalls("/module", "example.com/demo", fs, f, info, &r)
	if len(r.Calls) != 1 || len(r.proofs) != 1 {
		t.Fatalf("unexpected inventory: %+v", r)
	}
	return r
}

func TestParentSources(t *testing.T) {
	for _, tc := range []struct {
		name, setup, parent, status string
		guards                      int
	}{
		{"direct", "", "request.Context()", "pass", 0},
		{"alias", "parent:=request.Context()", "", "pass", 0},
		{"two-aliases", "first:=request.Context();parent:=first", "", "pass", 0},
		{"var-alias", "var parent context.Context=request.Context()", "", "pass", 0},
		{"nil-fallback", "parent:=request.Context();if parent==nil{parent=context.Background()}", "", "pass", 1},
		{"reversed-nil", "parent:=request.Context();if nil==parent{parent=context.Background()}", "", "pass", 1},
		{"copied-nil", "first:=request.Context();parent:=first;if parent==nil{parent=context.Background()}", "", "pass", 1},
		{"background", "parent:=context.Background()", "", "fail", 0},
		{"todo", "parent:=context.TODO()", "", "fail", 0},
		{"without-cancel", "parent:=context.WithoutCancel(request.Context())", "", "fail", 0},
		{"detached-alias", "first:=context.Background();parent:=first", "", "fail", 0},
		{"reassignment", "parent:=request.Context();parent=context.Background()", "", "unknown", 0},
		{"partial-short-declaration", "parent:=request.Context();parent,n:=context.Background(),1;_=n", "", "unknown", 0},
		{"request-reassignment", "request=replacement()", "request.Context()", "unknown", 0},
		{"nested-request-write", "if fallback!=nil{request=replacement()}", "request.Context()", "unknown", 0},
		{"request-escape", "use(request)", "request.Context()", "unknown", 0},
		{"address-escape", "parent:=request.Context();use(&parent)", "", "unknown", 0},
		{"helper-use", "parent:=request.Context();use(parent)", "", "unknown", 0},
		{"deferred-use", "parent:=request.Context();defer use(parent)", "", "unknown", 0},
		{"goroutine-use", "parent:=request.Context();go use(parent)", "", "unknown", 0},
		{"closure-capture", "parent:=request.Context();f:=func(){use(parent)};_=f", "", "unknown", 0},
		{"helper-result", "parent:=identity(request.Context())", "", "unknown", 0},
		{"ordinary-context", "parent:=fallback;if parent==nil{parent=context.Background()}", "", "unknown", 0},
		{"fallback-else", "parent:=request.Context();if parent==nil{parent=context.Background()}else{use(parent)}", "", "unknown", 0},
		{"fallback-extra-effect", "parent:=request.Context();if parent==nil{use(parent);parent=context.Background()}", "", "unknown", 0},
		{"fallback-init", "parent:=request.Context();if n:=1;parent==nil{_=n;parent=context.Background()}", "", "unknown", 0},
		{"fallback-other-condition", "parent:=request.Context();if fallback==nil{parent=context.Background()}", "", "unknown", 0},
		{"fallback-todo", "parent:=request.Context();if parent==nil{parent=context.TODO()}", "", "unknown", 0},
		{"fallback-after-write", "parent:=request.Context();parent=fallback;if parent==nil{parent=context.Background()}", "", "unknown", 0},
		{"shadowed-parent", "parent:=request.Context();{parent:=context.Background();use(parent)}", "", "pass", 0},
		{"shadowed-nil", "var nil context.Context;parent:=request.Context();if parent==nil{parent=context.Background()}", "", "unknown", 0},
		{"request-mutated-after-alias", "parent:=request.Context();request=replacement()", "", "unknown", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := inspectSource(t, tc.setup, tc.parent, "", "")
			p := r.proofs[0]
			if p.Status != tc.status {
				t.Fatalf("source: %+v", p)
			}
			want := tc.status
			if r.Calls[0].Status != want {
				t.Fatalf("call: %+v", r.Calls[0])
			}
			guards := 0
			for _, s := range p.Steps {
				if s.Role == "unreachable_nil_fallback" {
					guards++
				}
				if s.Node.End <= s.Node.Start {
					t.Fatal(s)
				}
			}
			if guards != tc.guards {
				t.Fatalf("guards=%d: %+v", guards, p)
			}
		})
	}
}

func TestSourceCannotWidenReleaseProof(t *testing.T) {
	for _, tc := range []struct{ name, between, call string }{
		{"nested-context-write", "if true {ctx=context.Background()}", ""},
		{"nested-receiver-write", "if true {caller=client}", ""},
		{"partial-context-write", "ctx,n:=context.Background(),1;_=n", ""},
		{"initializer-effect", "var n=mutate(ctx);_=n", ""},
		{"extra-defer", "defer use(ctx)", ""},
		{"address", "use(&ctx)", ""},
		{"nested-call", "", `return func()error{return caller.Call(ctx,"http.do",nil,nil)}()`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := inspectSource(t, "parent:=request.Context()", "", tc.between, tc.call)
			if r.Calls[0].Status == "pass" {
				t.Fatalf("unsupported release flow passed: %+v", r.Calls[0])
			}
		})
	}
}

func TestSourceDefinitionLocations(t *testing.T) {
	// 每条别名证据关联原定义，不能只保存变量名字。
	r := inspectSource(t, `var first context.Context=request.Context();parent:=first`, "", "", "")
	if r.proofs[0].Status != "pass" {
		t.Fatal(r)
	}
	for _, step := range r.proofs[0].Steps {
		if step.Role == "context_alias" && (step.Definition == nil || !strings.HasSuffix(step.Definition.File, "boundary.go")) {
			t.Fatal(step)
		}
	}
}

func TestSourceImportAlias(t *testing.T) {
	r := inspectSource(t, "parent:=request.Context()", "", "", "", func(source string) string {
		source = strings.ReplaceAll(source, `"context"`, `ctxlib "context"`)
		return strings.ReplaceAll(source, "context.", "ctxlib.")
	})
	if r.Calls[0].Status != "pass" || r.proofs[0].Status != "pass" {
		t.Fatal(r)
	}
}
