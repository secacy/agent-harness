package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"example.com/agent-harness/internal/execution"
)

func scenario(t *testing.T, patch string) Request {
	t.Helper()
	root := t.TempDir()
	for _, module := range []string{"demo-orders", "rpcx-v1", "rpcx-v2"} {
		source := filepath.Join("..", "t3", "fixtures", module)
		if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(source, path)
			dest := filepath.Join(root, module, rel)
			if entry.IsDir() {
				return os.MkdirAll(dest, 0755)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(dest, data, 0644)
		}); err != nil {
			t.Fatal(err)
		}
	}
	module := filepath.Join(root, "demo-orders")
	if patch != "" {
		path, err := filepath.Abs(filepath.Join("..", "t3", "cases", "patches", patch+".patch"))
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("git", "apply", path)
		cmd.Dir = module
		if data, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("apply fixture patch: %v: %s", err, data)
		}
	}
	return Request{ModuleDir: module, SDKDirs: []string{filepath.Join(root, "rpcx-v1"), filepath.Join(root, "rpcx-v2")},
		EvidenceDir: filepath.Join(t.TempDir(), "evidence"), Backend: execution.Local{}}
}

func runInspect(t *testing.T, req Request) Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	result, err := Inspect(ctx, req)
	if err != nil {
		t.Fatalf("inspect: %v\n%s\n%s", err, result.Command.Stdout, result.Command.Stderr)
	}
	data, err := json.Marshal(result.Report)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("ANALYSIS %s", data)
	for _, name := range []string{"inputs.json", "execution.json"} {
		data, err := os.ReadFile(filepath.Join(req.EvidenceDir, name))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("ANALYSIS_MATERIAL %s %s", name, data)
	}
	return result
}

func TestInspectFrozenCandidates(t *testing.T) {
	cache := t.TempDir()
	var baseline Report
	for _, sample := range []struct {
		patch                       string
		covered, unknown, violation int
		legacy                      int
	}{
		{"", 0, 0, 0, 2},
		{"correct-timeout", 2, 0, 0, 0},
		{"missing-timeout", 0, 2, 0, 0},
		{"release-missing-timeout", 1, 1, 0, 0},
		{"wrong-duration", 0, 0, 2, 0},
	} {
		name := sample.patch
		if name == "" {
			name = "v1-baseline"
		}
		t.Run(name, func(t *testing.T) {
			req := scenario(t, sample.patch)
			req.CacheDir = cache
			r := runInspect(t, req).Report
			counts := map[string]int{}
			for _, call := range r.Calls {
				counts[call.Status]++
				if call.File != "internal/inventory/client.go" || call.Function == "" || call.Line < 1 {
					t.Fatalf("invalid call location: %+v", call)
				}
				if call.Operation == nil || !strings.HasPrefix(*call.Operation, "Inventory.") {
					t.Fatalf("RPC operation not identified: %+v", call)
				}
			}
			if r.Status != "complete" || len(r.Packages) != 4 || len(r.Calls) != 2 || counts["covered"] != sample.covered || counts["unknown"] != sample.unknown || counts["violation"] != sample.violation || counts["legacy"] != sample.legacy || len(r.Gaps) != sample.unknown {
				t.Fatalf("unexpected result: %+v", r)
			}
			if sample.patch == "" {
				baseline = r
			} else {
				comparison, err := Compare(baseline, r)
				if err != nil || len(comparison.Matches) != 2 || len(comparison.Changes) != 0 || len(comparison.Gaps) != sample.unknown {
					t.Fatalf("unexpected frozen candidate comparison: %+v %v", comparison, err)
				}
				logComparison(t, comparison)
			}
		})
	}
}

func TestInspectUnsupportedShapesRemainVisible(t *testing.T) {
	cache := t.TempDir()
	for _, sample := range []struct{ name, old, new, reason string }{
		{"import-alias", "rpcx.", "wire.", ""},
		{"type-alias", "rpc *rpcx.Client", "rpc *Transport", ""},
		{"cross-file", "type Client struct{ rpc *rpcx.Client }", "", ""},
		{"helper", "context.WithTimeout(ctx, 30*time.Second)", "timeout(ctx)", "unsupported_context_derivation"},
		{"wrong-context", `c.rpc.Call(ctx,`, `c.rpc.Call(context.WithoutCancel(ctx),`, "rpc_context_not_derived"},
		{"background-parent", "context.WithTimeout(ctx,", "context.WithTimeout(context.Background(),", "unproven_parent_context"},
		{"constant", "30*time.Second", "(15+15)*time.Second", ""},
		{"defer-removed", "defer cancel()", "_ = cancel", "unproven_cancel_release"},
		{"early-cancel", "defer cancel()", "cancel()", "unproven_cancel_release"},
		{"method-value", "if err := c.rpc.Call(ctx,", "invoke := c.rpc.Call\n\tif err := invoke(ctx,", "method_value_or_expression"},
		{"argument-helper", "rpcx.Request{SKU: sku, Quantity: n}", "request(ctx, sku, n)", "unproven_argument_effects"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			req := scenario(t, "correct-timeout")
			req.CacheDir = cache
			path := filepath.Join(req.ModuleDir, "internal", "inventory", "client.go")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			source := strings.ReplaceAll(string(data), sample.old, sample.new)
			if source == string(data) {
				t.Fatal("fixture mutation did not match the original source")
			}
			switch sample.name {
			case "import-alias":
				source = strings.Replace(source, `"example.com/rpcx/v2"`, `wire "example.com/rpcx/v2"`, 1)
			case "type-alias":
				source += "\ntype Transport = rpcx.Client\n"
			case "cross-file":
				if err := os.WriteFile(filepath.Join(filepath.Dir(path), "types.go"), []byte("package inventory\nimport \"example.com/rpcx/v2\"\ntype Client struct{ rpc *rpcx.Client }\n"), 0644); err != nil {
					t.Fatal(err)
				}
			case "helper":
				source += "\nfunc timeout(ctx context.Context) (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, 30*time.Second) }\n"
			case "argument-helper":
				source += "\nfunc request(ctx context.Context, sku string, n int) rpcx.Request { return rpcx.Request{SKU: sku, Quantity:n} }\n"
			}
			if err := os.WriteFile(path, []byte(source), 0644); err != nil {
				t.Fatal(err)
			}
			r := runInspect(t, req).Report
			if len(r.Calls) != 2 {
				t.Fatalf("unexpected call inventory: %+v", r)
			}
			for _, call := range r.Calls {
				if sample.reason == "" {
					if call.Status != "covered" || call.TimeoutNS == nil || *call.TimeoutNS != 30_000_000_000 {
						t.Fatalf("constant expression not supported: %+v", call)
					}
				} else if call.Status != "unknown" || call.Reason != sample.reason {
					t.Fatalf("unsupported shape was hidden: %+v", call)
				}
			}
		})
	}
}

func TestInspectBuildFailureDoesNotProduceCoverage(t *testing.T) {
	req := scenario(t, "correct-timeout")
	if err := os.WriteFile(filepath.Join(req.ModuleDir, "broken.go"), []byte("package broken\nnot valid Go"), 0644); err != nil {
		t.Fatal(err)
	}
	r := runInspect(t, req).Report
	if r.Status != "load_failed" || r.LoadExitCode == 0 || r.Diagnostic == "" || len(r.Calls)+len(r.Packages) != 0 {
		t.Fatalf("invalid build failure: %+v", r)
	}
}

func TestInputMutationInvalidatesPublishedAnalysis(t *testing.T) {
	req := scenario(t, "correct-timeout")
	req.Backend = backendFunc(func(ctx context.Context, command execution.Request) (execution.Result, error) {
		result, err := (execution.Local{}).Run(ctx, command)
		if err == nil {
			err = os.WriteFile(filepath.Join(req.ModuleDir, "changed.txt"), []byte("changed after worker finished"), 0644)
		}
		return result, err
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	result, err := Inspect(ctx, req)
	if err == nil || !strings.Contains(err.Error(), "inputs changed") || result.Report.SchemaVersion != 0 {
		t.Fatalf("stale analysis became usable: %+v %v", result.Report, err)
	}
	if _, err := os.Stat(filepath.Join(req.EvidenceDir, "analysis.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale published analysis was retained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(req.EvidenceDir, "stdout.json")); err != nil {
		t.Fatalf("partial command evidence lost: %v", err)
	}
}

func addTaggedFiles(t *testing.T, req Request) {
	t.Helper()
	for _, path := range []string{"internal/inventory/extra.go", "internal/extra/extra.go"} {
		full := filepath.Join(req.ModuleDir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		pkg := filepath.Base(filepath.Dir(path))
		source := "//go:build extra\n\npackage " + pkg + "\nimport (\"context\"; \"example.com/rpcx/v2\")\nfunc extra(c *rpcx.Client, ctx context.Context) error { return c.Call(ctx, \"Extra\", rpcx.Request{}) }\n"
		if err := os.WriteFile(full, []byte(source), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInspectInactivePackagesAndExplicitTags(t *testing.T) {
	req := scenario(t, "correct-timeout")
	req.CacheDir = t.TempDir()
	addTaggedFiles(t, req)
	first := runInspect(t, req).Report
	if len(first.Calls) != 2 || len(first.Gaps) != 2 {
		t.Fatalf("inactive files were omitted: %+v", first)
	}
	reasons := map[string]bool{}
	for _, gap := range first.Gaps {
		reasons[gap.Reason] = true
	}
	if !reasons["inactive_build_file"] || !reasons["unlisted_build_file"] {
		t.Fatalf("missing package-listing gap: %+v", first.Gaps)
	}
	req.EvidenceDir = filepath.Join(t.TempDir(), "tagged-evidence")
	req.Tags = []string{"extra"}
	second := runInspect(t, req).Report
	if len(second.Calls) != 4 || len(second.Packages) != 5 || len(second.Gaps) != 2 {
		t.Fatalf("tagged SDK calls not identified: %+v", second)
	}
}

func TestValidateRejectsCorruptAnalysisFacts(t *testing.T) {
	for _, corruption := range []string{"binding", "tool", "configuration", "source-hash", "outside-path", "missing-source", "missing-collections", "missing-gap", "invented-covered", "invalid-status"} {
		t.Run(corruption, func(t *testing.T) {
			ns := int64(30_000_000_000)
			r := Report{SchemaVersion: 2, Status: "complete", InputSHA256: "input", ToolSHA256: "tool",
				Config:   Configuration{GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, CGO: "0", Tags: []string{}},
				Packages: []Package{{Path: "demo", Files: map[string]string{"client.go": "source"}, Ignored: []string{}}},
				Calls:    []Call{{Package: "demo", File: "client.go", Line: 1, SDK: "example.com/rpcx/v2", Kind: "direct", Status: "covered", Reason: "direct_timeout_boundary", TimeoutNS: &ns}}, Gaps: []Gap{}}
			inputs := map[string]string{"demo/client.go": "source"}
			if err := validate(r, inputs, "demo", "input", "tool", []string{}, execution.KindLocal); err != nil {
				t.Fatalf("invalid test setup: %v", err)
			}
			switch corruption {
			case "binding":
				r.InputSHA256 = "other"
			case "tool":
				r.ToolSHA256 = "other"
			case "configuration":
				r.Config.Tags = []string{"extra"}
			case "source-hash":
				r.Packages[0].Files["client.go"] = "other"
			case "outside-path":
				r.Calls[0].File = "../other.go"
			case "missing-source":
				inputs["demo/omitted.go"] = "source"
			case "missing-collections":
				r.Calls = nil
			case "missing-gap":
				r.Calls[0].Status = "unknown"
			case "invented-covered":
				ns = 3_000_000_000
			case "invalid-status":
				r.Calls[0].Status = "pass"
			}
			if err := validate(r, inputs, "demo", "input", "tool", []string{}, execution.KindLocal); err == nil {
				t.Fatal("corrupt protocol was accepted")
			}
		})
	}
}

type backendFunc func(context.Context, execution.Request) (execution.Result, error)

func (f backendFunc) Run(ctx context.Context, req execution.Request) (execution.Result, error) {
	return f(ctx, req)
}
func (backendFunc) Info() execution.Info { return execution.Info{Kind: execution.KindLocal} }

func TestInspectFailureAndInputProtection(t *testing.T) {
	for _, failure := range []string{"backend-error", "invalid-json", "input-changed", "evidence-overlap", "cache-alias", "symlink-input"} {
		t.Run(failure, func(t *testing.T) {
			req := scenario(t, "correct-timeout")
			called := false
			req.Backend = backendFunc(func(context.Context, execution.Request) (execution.Result, error) {
				called = true
				if failure == "backend-error" {
					return execution.Result{Stdout: []byte("partial")}, context.Canceled
				}
				if failure == "input-changed" {
					if err := os.WriteFile(filepath.Join(req.ModuleDir, "changed.txt"), []byte("changed"), 0644); err != nil {
						t.Fatal(err)
					}
				}
				return execution.Result{Stdout: []byte("garbage")}, nil
			})
			switch failure {
			case "evidence-overlap":
				req.EvidenceDir = filepath.Join(req.ModuleDir, "evidence")
			case "cache-alias":
				link := filepath.Join(t.TempDir(), "alias")
				if err := os.Symlink(req.ModuleDir, link); err != nil {
					t.Fatal(err)
				}
				req.CacheDir = filepath.Join(link, "cache")
			case "symlink-input":
				if err := os.Symlink(os.DevNull, filepath.Join(req.ModuleDir, "link")); err != nil {
					t.Fatal(err)
				}
			}
			r, err := Inspect(context.Background(), req)
			if err == nil || r.Report.SchemaVersion != 0 {
				t.Fatalf("failure became analysis success: %+v %v", r.Report, err)
			}
			if failure == "backend-error" && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost backend cancellation: %v", err)
			}
			if strings.HasSuffix(failure, "overlap") || failure == "cache-alias" || failure == "symlink-input" {
				if called {
					t.Fatal("backend ran despite invalid input/output paths")
				}
			}
		})
	}
}

func TestInspectDockerConfigurationAndCoverage(t *testing.T) {
	if os.Getenv("HARNESS_DOCKER_TEST") != "1" {
		t.Skip("set HARNESS_DOCKER_TEST=1 to run real Docker integration")
	}
	backend, err := execution.NewDocker(context.Background(), execution.DockerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	baselineReq := scenario(t, "")
	baselineReq.Backend = backend
	baseline := runInspect(t, baselineReq).Report
	for _, patch := range []string{"correct-timeout", "wrong-duration", "explicit-tags"} {
		t.Run(patch, func(t *testing.T) {
			input := patch
			if patch == "explicit-tags" {
				input = "correct-timeout"
			}
			req := scenario(t, input)
			req.Backend = backend
			if patch != "wrong-duration" {
				addTaggedFiles(t, req)
			}
			if patch == "explicit-tags" {
				req.Tags = []string{"extra"}
			}
			r := runInspect(t, req)
			want := "covered"
			if patch == "wrong-duration" {
				want = "violation"
			}
			wantCalls, wantGaps := 2, 0
			if patch == "correct-timeout" {
				wantGaps = 2
			} else if patch == "explicit-tags" {
				wantCalls, wantGaps = 4, 2
			}
			if r.Report.Config.GOOS != "linux" || r.Command.ContainerID == "" || len(r.Report.Calls) != wantCalls || len(r.Report.Gaps) != wantGaps {
				t.Fatalf("missing actual container analysis: %+v", r)
			}
			for _, call := range r.Report.Calls {
				if call.Status != want && !(patch == "explicit-tags" && call.Status == "unknown") {
					t.Fatalf("unexpected container coverage: %+v", call)
				}
			}
			if patch != "explicit-tags" {
				comparison, err := Compare(baseline, r.Report)
				if err != nil || len(comparison.Matches) != 2 || len(comparison.Changes) != 0 || len(comparison.Gaps) != wantGaps {
					t.Fatalf("unexpected Docker comparison: %+v %v", comparison, err)
				}
				logComparison(t, comparison)
			} else if _, err := Compare(baseline, r.Report); err == nil {
				t.Fatal("different build tags were silently compared")
			}
			cmd := exec.Command("docker", "inspect", r.Command.ContainerID)
			if err := cmd.Run(); err == nil {
				t.Fatal("analysis container was not removed")
			}
		})
	}
}
