package testintegrity

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const handlerPath = "cmd/server/handler_test.go"
const createdGuard = "if w.Code != http.StatusCreated {\n\t\tt.Fatalf(\"status %d: %s\", w.Code, w.Body)\n\t}"

func readTests(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = data
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func fixture(t *testing.T, patch string) string {
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
	return module
}

func check(t *testing.T, baseline, candidate map[string][]byte) Report {
	t.Helper()
	before, _ := json.Marshal(baseline)
	r, err := Compare(baseline, candidate)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(baseline)
	if string(before) != string(after) {
		t.Fatal("comparison modified trusted baseline bytes")
	}
	data, _ := json.Marshal(r)
	t.Logf("TEST_INTEGRITY %s", data)
	return r
}

func TestFrozenCandidatesKeepTheOriginalTests(t *testing.T) {
	baseline := readTests(t, filepath.Join("..", "t3", "fixtures", "demo-orders"))
	for _, patch := range []string{"correct-timeout", "missing-timeout", "release-missing-timeout", "wrong-duration"} {
		t.Run(patch, func(t *testing.T) {
			candidate := readTests(t, fixture(t, patch))
			r := check(t, baseline, candidate)
			if r.Status != "preserved" || len(r.Declarations) != 12 || len(r.Findings) != 0 {
				t.Fatalf("legitimate test adaptation was not preserved: %+v", r)
			}
		})
	}
}

func mutate(t *testing.T, files map[string][]byte, path, old, new string) {
	t.Helper()
	before := string(files[path])
	after := strings.Replace(before, old, new, 1)
	if before == after {
		t.Fatalf("fixture mutation did not match %s", path)
	}
	files[path] = []byte(after)
}

func mutateCase(t *testing.T, files map[string][]byte, name string) {
	t.Helper()
	switch name {
	case "deleted-assertion":
		mutate(t, files, handlerPath, createdGuard, "")
	case "deleted-nested-assertion":
		guard := "if req.SKU != \"book\" || req.Quantity != 2 {\n\t\t\t\tt.Fatalf(\"unexpected release: %+v\", req)\n\t\t\t}"
		mutate(t, files, "internal/orders/service_test.go", guard, "")
	case "deleted-two-assertions":
		mutateCase(t, files, "deleted-assertion")
		mutate(t, files, handlerPath, "if w.Code != http.StatusNoContent {\n\t\tt.Fatalf(\"status %d: %s\", w.Code, w.Body)\n\t}", "")
	case "deleted-assertion-with-init":
		mutate(t, files, handlerPath, "if _, ok := h.service.Get(\"o1\"); !ok {\n\t\tt.Fatal(\"order missing\")\n\t}", "")
	case "deletion-plus-new-test":
		mutateCase(t, files, "deleted-assertion")
		files[handlerPath] = append(files[handlerPath], []byte("\nfunc TestExtra(t *testing.T) { w := httptest.NewRecorder(); "+createdGuard+" }\n")...)
	case "helper-rewrite":
		mutate(t, files, handlerPath, createdGuard, "assertCreated(t, w)")
		files[handlerPath] = append(files[handlerPath], []byte("\nfunc assertCreated(t *testing.T, w *httptest.ResponseRecorder) { "+createdGuard+" }\n")...)
	case "rewritten-condition":
		mutate(t, files, handlerPath, "w.Code != http.StatusCreated", "!(w.Code == http.StatusCreated)")
	case "skip":
		mutate(t, files, handlerPath, "func TestPlace(t *testing.T) {", "func TestPlace(t *testing.T) { t.Skip(\"skipped\");")
	case "conditional-skip":
		mutate(t, files, handlerPath, "func TestPlace(t *testing.T) {", "func TestPlace(t *testing.T) { if false { t.SkipNow() };")
	case "removed-test":
		mutate(t, files, handlerPath, "func TestPlace(t *testing.T)", "func removedPlace(t *testing.T)")
	case "changed-helper":
		mutate(t, files, handlerPath, "t.Helper()", "t.Helper(); t.Log(\"changed\")")
	case "test-main":
		files[handlerPath] = append(files[handlerPath], []byte("\nfunc TestMain(m *testing.M) {}\n")...)
	case "test-init":
		files[handlerPath] = append(files[handlerPath], []byte("\nfunc init() {}\n")...)
	case "build-tag":
		files[handlerPath] = append([]byte("//go:build extra\n\n"), files[handlerPath]...)
	case "legacy-build-tag":
		files[handlerPath] = append([]byte("// +build extra\n\n"), files[handlerPath]...)
	case "line-directive":
		files[handlerPath] = append([]byte("//line fake.go:9000\n"), files[handlerPath]...)
	case "ordinary-comment":
		files[handlerPath] = append([]byte("// 普通说明不影响执行。\n\n"), files[handlerPath]...)
	case "added-test":
		files[handlerPath] = append(files[handlerPath], []byte("\nfunc TestExtra(t *testing.T) { t.Log(\"extra\") }\n")...)
	case "added-file":
		files["cmd/server/extra_test.go"] = []byte("package main\nimport \"testing\"\nfunc TestExtra(t *testing.T) {}\n")
	case "duplicate-test":
		files[handlerPath] = append(files[handlerPath], []byte("\nfunc TestPlace(t *testing.T) {}\n")...)
	case "changed-import":
		mutate(t, files, handlerPath, `"testing"`, `testing "testing"`)
	case "syntax-error":
		files[handlerPath] = []byte("package main\nfunc TestPlace(")
	case "moved-file":
		files["cmd/server/moved_test.go"] = files[handlerPath]
		delete(files, handlerPath)
	case "global-state":
		files[handlerPath] = append(files[handlerPath], []byte("\nvar testFlag = true\n")...)
	case "deletion-with-rewrite":
		mutateCase(t, files, "deleted-assertion")
		mutateCase(t, files, "changed-helper")
	case "fail-and-unknown":
		mutateCase(t, files, "removed-test")
		mutateCase(t, files, "build-tag")
	default:
		t.Fatalf("unknown mutation: %s", name)
	}
}

func TestTestChangesAreClassifiedConservatively(t *testing.T) {
	baseline := readTests(t, filepath.Join("..", "t3", "fixtures", "demo-orders"))
	for _, sample := range []struct{ name, status, code string }{
		{"deleted-assertion", "violated", "original_assertion_deleted"},
		{"deleted-nested-assertion", "violated", "original_assertion_deleted"},
		{"deleted-two-assertions", "violated", "original_assertion_deleted"},
		{"deleted-assertion-with-init", "unknown", "original_declaration_changed"},
		{"deletion-plus-new-test", "violated", "original_assertion_deleted"},
		{"helper-rewrite", "unknown", "original_declaration_changed"},
		{"rewritten-condition", "unknown", "original_declaration_changed"},
		{"skip", "violated", "original_test_skipped"},
		{"conditional-skip", "unknown", "original_declaration_changed"},
		{"removed-test", "violated", "original_test_removed"},
		{"changed-helper", "unknown", "original_declaration_changed"},
		{"test-main", "unknown", "test_helper_or_entry_added"},
		{"test-init", "unknown", "test_helper_or_entry_added"},
		{"build-tag", "unknown", "test_file_scope_changed"},
		{"legacy-build-tag", "unknown", "test_file_scope_changed"},
		{"line-directive", "unknown", "test_file_scope_changed"},
		{"ordinary-comment", "preserved", ""},
		{"added-test", "preserved", ""},
		{"added-file", "unknown", "test_file_added"},
		{"duplicate-test", "unknown", "original_declaration_unresolved"},
		{"changed-import", "unknown", "test_file_scope_changed"},
		{"syntax-error", "unknown", "candidate_test_parse_failed"},
		{"moved-file", "unknown", "original_declaration_moved"},
		{"global-state", "unknown", "test_declaration_added"},
		{"deletion-with-rewrite", "unknown", "assertion_replacement_unproven"},
		{"fail-and-unknown", "violated", "original_test_removed"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			candidate, err := AdaptBaseline(baseline)
			if err != nil {
				t.Fatal(err)
			}
			mutateCase(t, candidate, sample.name)
			r := check(t, baseline, candidate)
			found := sample.code == ""
			for _, finding := range r.Findings {
				if finding.Code == sample.code {
					found = true
				}
				for _, loc := range finding.Locations {
					if strings.Contains(loc.File, "fake.go") || loc.Line >= 9000 {
						t.Fatalf("line directive forged physical location: %+v", loc)
					}
				}
			}
			if r.Status != sample.status || !found {
				t.Fatalf("unexpected test change classification: %+v", r)
			}
		})
	}
}

func TestUnsupportedInputsHaveNoJudgment(t *testing.T) {
	for _, bad := range []string{"baseline-content", "baseline-inventory", "absolute-path", "traversal", "not-a-test", "oversized"} {
		t.Run(bad, func(t *testing.T) {
			b := readTests(t, filepath.Join("..", "t3", "fixtures", "demo-orders"))
			c, err := AdaptBaseline(b)
			if err != nil {
				t.Fatal(err)
			}
			switch bad {
			case "baseline-content":
				b[handlerPath] = append(b[handlerPath], '\n')
			case "baseline-inventory":
				delete(b, handlerPath)
			case "absolute-path":
				c["/outside_test.go"] = []byte("package main")
			case "traversal":
				c["../outside_test.go"] = []byte("package main")
			case "not-a-test":
				c["other.go"] = []byte("package main")
			case "oversized":
				c[handlerPath] = make([]byte, (1<<20)+1)
			}
			r, err := Compare(b, c)
			if err == nil || r.Status != "" || r.SchemaVersion != 0 {
				t.Fatalf("invalid input produced judgment: %+v %v", r, err)
			}
		})
	}
}
