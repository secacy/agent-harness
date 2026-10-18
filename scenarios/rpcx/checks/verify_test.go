package checks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestProtectedPaths(t *testing.T) {
	for _, operation := range []string{"unchanged", "modify", "add", "delete", "rename"} {
		t.Run(operation, func(t *testing.T) {
			baseline, candidate := t.TempDir(), t.TempDir()
			writeTestFile(t, baseline, "internal/pb/orders.pb.go", "original")
			writeTestFile(t, candidate, "internal/pb/orders.pb.go", "original")
			switch operation {
			case "modify":
				writeTestFile(t, candidate, "internal/pb/orders.pb.go", "changed")
			case "add":
				writeTestFile(t, candidate, "internal/pb/new.pb.go", "new")
			case "delete":
				if err := os.RemoveAll(filepath.Join(candidate, "internal/pb")); err != nil {
					t.Fatal(err)
				}
			case "rename":
				if err := os.Rename(filepath.Join(candidate, "internal/pb/orders.pb.go"), filepath.Join(candidate, "internal/pb/renamed.pb.go")); err != nil {
					t.Fatal(err)
				}
			}
			got := protectedCheck(baseline, candidate)
			want := "fail"
			if operation == "unchanged" {
				want = "pass"
			}
			if got.Status != want {
				t.Fatalf("%+v, want %s", got, want)
			}
		})
	}
}

func TestPreservationRequiresExecutionAndOriginalSubtests(t *testing.T) {
	name := "example.com/demo-orders/internal/orders/TestCancel"
	baselineNames := map[string]bool{name: true}
	baselineEvents := map[string]string{name: "pass", name + "/failure": "pass"}
	for _, mode := range []string{"passed", "deleted", "skipped", "not-run", "missing-subtest", "failed"} {
		t.Run(mode, func(t *testing.T) {
			names := map[string]bool{name: true}
			states := map[string]string{name: "pass", name + "/failure": "pass"}
			switch mode {
			case "deleted":
				delete(names, name)
			case "skipped":
				states[name] = "skip"
			case "not-run":
				delete(states, name)
			case "missing-subtest":
				delete(states, name+"/failure")
			case "failed":
				states[name] = "fail"
			}
			got := preservationCheck(baselineNames, baselineEvents, names, states)
			if (got.Status == "pass") != (mode == "passed") {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func TestProbeCannotPassWithoutExecutingEveryCase(t *testing.T) {
	var events strings.Builder
	for _, operation := range []string{"Place", "Cancel"} {
		for _, mode := range []string{"metadata", "already-cancelled", "cancel-in-backend"} {
			data, err := json.Marshal(testEvent{Action: "pass", Package: "example.com/demo-orders/cmd/server", Test: "TestHarnessRequestContext/" + operation + "/" + mode})
			if err != nil {
				t.Fatal(err)
			}
			events.Write(data)
			events.WriteByte('\n')
		}
	}
	data := []byte(events.String())
	if got := probeCheck(data, Command{}); got.Status != "pass" {
		t.Fatalf("%+v", got)
	}
	for _, output := range [][]byte{nil, []byte(strings.ReplaceAll(events.String(), `"pass"`, `"skip"`))} {
		if got := probeCheck(output, Command{}); got.Status != "fail" {
			t.Fatalf("empty/skipped probes passed: %+v", got)
		}
	}
	if got := probeCheck(data, Command{TimedOut: true}); got.Status != "blocked" {
		t.Fatalf("%+v", got)
	}
}

func TestContextPolicyRecognizesAliasesAndDetachedContexts(t *testing.T) {
	for _, call := range []string{"Background()", "TODO()", "WithoutCancel(ctx)"} {
		t.Run(call, func(t *testing.T) {
			root := t.TempDir()
			writeTestFile(t, root, "client.go", "package demo\nimport c \"context\"\nfunc call(ctx c.Context) { _ = c."+call+" }\n")
			if err := contextPolicy(root); err == nil {
				t.Fatal("detached context accepted")
			}
		})
	}
	root := t.TempDir()
	writeTestFile(t, root, "client.go", "package demo\nimport c \"context\"\nfunc call(ctx c.Context) { _, cancel := c.WithCancel(ctx); defer cancel() }\n")
	writeTestFile(t, root, "client_test.go", "package demo\nimport \"context\"\nvar testContext = context.Background()\n")
	if err := contextPolicy(root); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshot(root); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestManifestRejectsIncompleteAndTrailingDocuments(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "manifest.yaml")
	for _, content := range []string{
		`{"schema_version":1,"spec":"spec.md","cases":[{"id":"case","patch":"x.patch","expected":{"checks":{},"verdict":"Rejected"}}]}`,
		`{"schema_version":1,"spec":"spec.md","cases":[]} {}`,
	} {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readManifest(path); err == nil {
			t.Fatal("invalid manifest accepted")
		}
	}
}
