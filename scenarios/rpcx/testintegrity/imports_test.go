package testintegrity

import (
	"path/filepath"
	"strings"
	"testing"
)

func addTimingTest(t *testing.T, files map[string][]byte, path string) {
	t.Helper()
	for _, name := range []string{"time", "errors"} {
		quoted := `"` + name + `"`
		if !strings.Contains(string(files[path]), quoted) {
			mutate(t, files, path, "import (", "import (\n"+quoted)
		}
	}
	files[path] = append(files[path], []byte(`
func TestAddedDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	cancel()
	if !errors.Is(ctx.Err(), context.Canceled) { t.Fatal("cancellation was lost") }
}
`)...)
}

func TestAdditionalImportsKeepOriginalChecks(t *testing.T) {
	baseline := readTests(t, filepath.Join("..", "t3", "fixtures", "demo-orders"))
	for _, sample := range []struct{ name, status, code string }{
		{"allowed", "preserved", ""},
		{"deleted-assertion", "violated", "original_assertion_deleted"},
		{"skip", "violated", "original_test_skipped"},
		{"removed-test", "violated", "original_test_removed"},
		{"helper-rewrite", "unknown", "original_declaration_changed"},
		{"test-init", "unknown", "test_helper_or_entry_added"},
		{"test-main", "unknown", "test_helper_or_entry_added"},
		{"global-state", "unknown", "test_declaration_added"},
		{"build-tag", "unknown", "test_file_scope_changed"},
		{"explicit-alias", "unknown", "test_file_scope_changed"},
		{"dot-import", "unknown", "test_file_scope_changed"},
		{"blank-import", "unknown", "test_file_scope_changed"},
		{"unknown-package", "unknown", "test_file_scope_changed"},
		{"changed-import", "unknown", "test_file_scope_changed"},
		{"removed-import", "unknown", "test_file_scope_changed"},
		{"duplicate-import", "unknown", "test_file_scope_changed"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			candidate, err := AdaptBaseline(baseline)
			if err != nil {
				t.Fatal(err)
			}
			for path := range candidate {
				addTimingTest(t, candidate, path)
			}
			switch sample.name {
			case "allowed":
			case "explicit-alias":
				mutate(t, candidate, handlerPath, `"time"`, `time "time"`)
			case "dot-import":
				mutate(t, candidate, handlerPath, `"time"`, `. "time"`)
			case "blank-import":
				mutate(t, candidate, handlerPath, `"time"`, `_ "time"`)
			case "unknown-package":
				mutate(t, candidate, handlerPath, "import (", "import (\n\"net/http/pprof\"")
			case "removed-import":
				mutate(t, candidate, handlerPath, `"testing"`, "")
			case "duplicate-import":
				mutate(t, candidate, handlerPath, `"time"`, "\"time\"\n\"time\"")
			default:
				mutateCase(t, candidate, sample.name)
			}
			r := check(t, baseline, candidate)
			if r.Status != sample.status || r.VerifierVersion != "rpcx-test-structure-v2" {
				t.Fatalf("unexpected import classification: %+v", r)
			}
			if sample.code == "" {
				if len(r.AddedTests) != 3 || len(r.Findings) != 0 {
					t.Fatalf("missing additions or unexpected gaps: %+v", r)
				}
				for _, decl := range r.Declarations {
					if decl.Status != "preserved" || decl.ExpectedSHA256 != decl.ActualSHA256 {
						t.Fatalf("original declaration changed: %+v", decl)
					}
				}
				return
			}
			for _, finding := range r.Findings {
				if finding.Code == sample.code && len(finding.RuleIDs) == 1 && finding.RuleIDs[0] == "TEST-001" && len(finding.Locations) > 0 {
					return
				}
			}
			t.Fatalf("missing reason, rule or location: %+v", r)
		})
	}
}

func TestAdditionalImportsCannotRebindOriginalNames(t *testing.T) {
	// 使用小型语法样例覆盖原基线尚未含有的同名形态；不扩展 Compare 接受的基线。
	for _, declaration := range []string{
		`func TestOriginal(t *testing.T) { _ = errors.New("old binding") }`,
		`func helper() time.Duration { return 0 }`,
		`var time = source`,
		`func TestOriginal(t *testing.T) { time := 1; _ = time }`,
	} {
		t.Run(declaration, func(t *testing.T) {
			before, _ := parseSources(map[string][]byte{"a_test.go": []byte("package p\nimport \"testing\"\n" + declaration)})
			after, _ := parseSources(map[string][]byte{"a_test.go": []byte("package p\nimport (\"testing\"; \"time\"; \"errors\")\n" + declaration)})
			if importsPreserved(before["a_test.go"], after["a_test.go"], []string{"errors", "time"}) {
				t.Fatal("added import may change an original binding")
			}
		})
	}
}
