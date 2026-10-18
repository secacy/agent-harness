// Package analysisstudy 保存 M2.3 的可复跑选型实验，不向运行时门禁导出能力。
package analysisstudy

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const source = `package study
import (
 "context"
 "time"
 "example.com/rpcx/v2"
)
type Client struct { rpc *rpcx.Client }
func (c *Client) Run(ctx context.Context, alternate bool) error {
 ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
 defer cancel()
 return c.rpc.Call(ctx, "Inventory.Reserve", rpcx.Request{SKU:"study", Quantity:7})
}
`

const trustedTest = `package study
import (
 "context"
 "errors"
 "testing"
 "time"
 "example.com/rpcx/v2"
)
func TestTrusted(t *testing.T) {
 sentinel := errors.New("backend failure")
 called := 0
 var observed context.Context
 before := time.Now()
 cleanup := rpcx.Register(t.Name(), func(ctx context.Context, method string, req rpcx.Request) error {
  called++
  observed = ctx
  after := time.Now()
  deadline, ok := ctx.Deadline()
  if !ok { t.Error("missing deadline") } else if deadline.Before(before.Add(30*time.Second)) || deadline.After(after.Add(30*time.Second)) { t.Error("wrong deadline") }
  if after.Sub(before) > 250*time.Millisecond { t.Fatal("observation window too wide") }
  if method != "Inventory.Reserve" || req.SKU != "study" || req.Quantity != 7 { t.Error("changed request") }
  return sentinel
 })
 defer cleanup()
 rpc, err := rpcx.Dial(t.Name())
 if err != nil { t.Fatal(err) }
 client := &Client{rpc: rpc}
 err = client.Run(context.Background(), false)
 if !errors.Is(err, sentinel) { t.Errorf("lost backend error: %v", err) }
 if called != 1 { t.Fatalf("backend calls = %d", called) }
 if observed.Err() != context.Canceled { t.Error("derived context not released") }
}
`

func studyContext(t *testing.T) (context.Context, string) {
	t.Helper()
	if os.Getenv("HARNESS_ANALYSIS_STUDY") != "1" {
		t.Skip("set HARNESS_ANALYSIS_STUDY=1 to run the trusted offline prototypes")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	return ctx, t.TempDir()
}

func record(t *testing.T, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("STUDY %s", data)
}

func TestIdentificationStudy(t *testing.T) {
	ctx, cache := studyContext(t)
	materials := map[string]string{}
	for _, name := range []string{"study_test.go", "identify_test.go", "load_test.go", "../t3/fixtures/rpcx-v2/client.go", "../t3/fixtures/rpcx-v2/go.mod"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		materials[name] = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	record(t, map[string]any{"go": runtime.Version(), "goos": runtime.GOOS, "goarch": runtime.GOARCH, "cgo": false, "tags": []string{}, "backend": "trusted-local", "materials_sha256": materials})
	type sample struct {
		name, code, extra string
		astSDK, typedSDK  int
		astGap, typedGap  int
		other, ignored    int
	}
	samples := []sample{
		{name: "direct", code: source, astSDK: 1, typedSDK: 1},
		{name: "import-alias", code: strings.ReplaceAll(strings.ReplaceAll(source, `"example.com/rpcx/v2"`, `wire "example.com/rpcx/v2"`), "rpcx.", "wire."), astSDK: 1, typedSDK: 1},
		{name: "same-name-other", code: source + "\ntype Other struct{}\nfunc (*Other) Call() {}\nfunc unrelated(o *Other) { o.Call() }\n", astSDK: 1, typedSDK: 1, other: 1},
		{name: "shadow", code: strings.Replace(source, "ctx, cancel :=", "{ c := &Other{}; c.Call() }; ctx, cancel :=", 1) + "\ntype Other struct{}\nfunc (*Other) Call() {}\n", astSDK: 1, typedSDK: 1, other: 1},
		{name: "constant-expression", code: strings.Replace(source, "30*time.Second", "(15+15)*time.Second", 1), astSDK: 1, typedSDK: 1, astGap: 1},
		{name: "named-constant", code: strings.Replace(source, "30*time.Second", "timeout", 1) + "\nconst timeout = time.Minute/2\n", astSDK: 1, typedSDK: 1, astGap: 1},
		{name: "type-alias", code: strings.Replace(source, "rpc *rpcx.Client", "rpc *Transport", 1) + "\ntype Transport = rpcx.Client\n", typedSDK: 1, astGap: 1},
		{name: "cross-file-field", code: strings.Replace(source, "type Client struct { rpc *rpcx.Client }", "", 1), extra: "package study\nimport \"example.com/rpcx/v2\"\ntype Client struct { rpc *rpcx.Client }\n", typedSDK: 1, astGap: 1},
		{name: "helper", code: helperSource(), astSDK: 1, typedSDK: 1},
		{name: "method-value", code: strings.Replace(source, "return c.rpc.Call(ctx,", "invoke := c.rpc.Call\n return invoke(ctx,", 1), astSDK: 1, typedSDK: 1, astGap: 1, typedGap: 1},
		{name: "interface-dispatch", code: strings.Replace(source, "rpc *rpcx.Client", "rpc interface { Call(context.Context, string, rpcx.Request) error }", 1), astGap: 1, typedGap: 1},
		{name: "inactive-tag", code: source, extra: "//go:build extra\n\npackage study\nimport (\"context\"; \"example.com/rpcx/v2\")\nfunc extra(c *rpcx.Client, ctx context.Context) error { return c.Call(ctx, \"Other\", rpcx.Request{}) }\n", astSDK: 1, typedSDK: 1, ignored: 1},
	}
	for _, sample := range samples {
		t.Run(sample.name, func(t *testing.T) {
			dir := fixture(t, sample.code)
			if sample.extra != "" {
				writeFile(t, filepath.Join(dir, "extra.go"), sample.extra)
			}
			p, err := load(ctx, dir, cache)
			if err != nil {
				t.Fatal(err)
			}
			syntax, typed := identify(p, false), identify(p, true)
			if len(syntax.SDK) != sample.astSDK || len(typed.SDK) != sample.typedSDK || len(syntax.Unknown) != sample.astGap || len(typed.Unknown) != sample.typedGap || syntax.Other != sample.other || typed.Other != sample.other || len(p.ignored) != sample.ignored {
				t.Fatalf("unexpected observations: AST=%+v types=%+v ignored=%v", syntax, typed, p.ignored)
			}
			if len(typed.Durations) != 1 || typed.Durations[0] != int64(30*time.Second) {
				t.Fatalf("wrong typed duration: %v", typed.Durations)
			}
			record(t, map[string]any{"sample": sample.name, "ast": syntax, "types": typed, "ignored": p.ignored, "list_export_ms": p.listMS, "type_check_ms": p.typeMS})
			if sample.ignored > 0 {
				active, err := load(ctx, dir, cache, "extra")
				if err != nil {
					t.Fatal(err)
				}
				facts := identify(active, true)
				if len(facts.SDK) != 2 || len(active.ignored) != 0 {
					t.Fatalf("tagged call not discovered: %+v", facts)
				}
				record(t, map[string]any{"sample": "active-tag-control", "tags": []string{"extra"}, "types": facts})
			}
		})
	}
	// 加载失败没有可消费的分析结论；取消保留原执行错误。
	t.Run("missing-dependency", func(t *testing.T) {
		dir := fixture(t, strings.Replace(source, `"example.com/rpcx/v2"`, `"example.invalid/missing"`, 1))
		p, err := load(ctx, dir, cache)
		if err == nil || p.info != nil {
			t.Fatalf("unexpected partial success: %v", err)
		}
		record(t, map[string]any{"sample": "missing-dependency", "load_error": true, "analysis_available": false})
	})
	t.Run("cancelled", func(t *testing.T) {
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		_, err := load(cancelled, fixture(t, source), cache)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation: %v", err)
		}
	})
}

func helperSource() string {
	return strings.Replace(source, "context.WithTimeout(ctx, 30*time.Second)", "deadline(ctx)", 1) + "\nfunc deadline(ctx context.Context) (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, 30*time.Second) }\n"
}

func TestComplementaryStudy(t *testing.T) {
	ctx, cache := studyContext(t)
	for _, sample := range []struct {
		name, code string
		calls      int
		probePass  bool
	}{
		{"direct", source, 1, true},
		{"helper", helperSource(), 1, true},
		{"swallowed-error", strings.Replace(strings.Replace(source, "return c.rpc.Call", "_ = c.rpc.Call", 1), "Quantity:7})\n}", "Quantity:7})\n return nil\n}", 1), 1, false},
		{"unvisited-call", strings.Replace(source, "ctx, cancel :=", "if alternate { return c.rpc.Call(ctx, \"Inventory.Reserve\", rpcx.Request{SKU:\"study\", Quantity:7}) }; ctx, cancel :=", 1), 2, true},
	} {
		t.Run(sample.name, func(t *testing.T) {
			dir := fixture(t, sample.code)
			p, err := load(ctx, dir, cache)
			if err != nil {
				t.Fatal(err)
			}
			facts := identify(p, true)
			if len(facts.SDK) != sample.calls || len(facts.Durations) != 1 || facts.Durations[0] != int64(30*time.Second) {
				t.Fatalf("unexpected inventory: %+v", facts)
			}
			writeFile(t, filepath.Join(dir, "trusted_test.go"), trustedTest)
			start := time.Now()
			passed, run := runTest(t, ctx, dir, cache)
			if passed != sample.probePass {
				t.Fatalf("unexpected probe result: %s\n%s", run.Stdout, run.Stderr)
			}
			if !passed && !strings.Contains(string(run.Stdout), "lost backend error") {
				t.Fatalf("failure does not demonstrate swallowed error: %s", run.Stdout)
			}
			record(t, map[string]any{"sample": sample.name, "types": facts, "probe_pass": passed, "probe_ms": time.Since(start).Milliseconds()})
			if !passed {
				t.Logf("PROBE_FAILURE %s", run.Stdout)
			}
			if sample.name == "unvisited-call" {
				writeFile(t, filepath.Join(dir, "trusted_test.go"), strings.Replace(trustedTest, "Background(), false", "Background(), true", 1))
				passed, run := runTest(t, ctx, dir, cache)
				if passed || !strings.Contains(string(run.Stdout), "missing deadline") {
					t.Fatalf("alternate path must expose missing timeout: %s", run.Stdout)
				}
				record(t, map[string]any{"sample": "unvisited-call-control", "probe_pass": false, "reason": "missing deadline on alternate path"})
				t.Logf("PROBE_FAILURE %s", run.Stdout)
			}
		})
	}
}

func TestTrustedCopyStudy(t *testing.T) {
	ctx, cache := studyContext(t)
	for _, sample := range []struct {
		name, candidateTest, production string
		mixedPass, isolatedPass         bool
	}{
		{"normal", "package study\nimport \"testing\"\nfunc TestCandidate(t *testing.T) {}\n", source, true, true},
		{"test-main-exit", "package study\nimport (\"os\"; \"testing\")\nfunc TestMain(m *testing.M) { os.Exit(0) }\n", source, false, true},
		{"test-init-exit", "package study\nimport \"os\"\nfunc init() { os.Exit(0) }\n", source, false, true},
		{"inactive-test-init", "//go:build extra\n\npackage study\nimport \"os\"\nfunc init() { os.Exit(0) }\n", source, true, true},
		{"production-init-exit", "package study\n", strings.Replace(source, `"context"`, "\"context\"\n\"os\"", 1) + "\nfunc init() { os.Exit(0) }\n", false, false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			mixed := fixture(t, sample.production)
			writeFile(t, filepath.Join(mixed, "candidate_test.go"), sample.candidateTest)
			writeFile(t, filepath.Join(mixed, "trusted_test.go"), trustedTest)
			mixedPass, run := runTest(t, ctx, mixed, cache)
			if mixedPass != sample.mixedPass || run.ExitCode != 0 {
				t.Fatalf("unexpected mixed outcome: %s\n%s", run.Stdout, run.Stderr)
			}
			// 当前样例的生产集合恰为 client.go 和 go.mod；复制实际字节并核对摘要。
			isolated := fixture(t, "")
			bindings := map[string]string{}
			for _, name := range []string{"client.go", "go.mod"} {
				data, err := os.ReadFile(filepath.Join(mixed, name))
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(isolated, name), string(data))
				copy, err := os.ReadFile(filepath.Join(isolated, name))
				if err != nil || sha256.Sum256(data) != sha256.Sum256(copy) {
					t.Fatalf("copy mismatch: %s: %v", name, err)
				}
				bindings[name] = fmt.Sprintf("%x", sha256.Sum256(data))
			}
			writeFile(t, filepath.Join(isolated, "trusted_test.go"), trustedTest)
			isolatedPass, isolatedRun := runTest(t, ctx, isolated, cache)
			if isolatedPass != sample.isolatedPass || isolatedRun.ExitCode != 0 {
				t.Fatalf("isolated probe failed: %s\n%s", isolatedRun.Stdout, isolatedRun.Stderr)
			}
			record(t, map[string]any{"sample": sample.name, "mixed_pass": mixedPass, "mixed_exit": run.ExitCode, "isolated_pass": isolatedPass, "production_sha256": bindings})
		})
	}
}
