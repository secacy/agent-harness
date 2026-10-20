package workouts

import (
	"context"
	"maps"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/testintegrity"
)

func controlCandidate(t *testing.T, id string) map[string][]byte {
	t.Helper()
	fixtures, err := LoadFixtures("../l2/data/rpcx-l2-workouts-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err = gate.CopyFiles(root, gate.Module(fixtures, "trainings")); err != nil {
		t.Fatal(err)
	}
	patch, err := filepath.Abs("cases/patches/" + id + ".patch")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(context.Background(), "git", "apply", patch)
	cmd.Dir = root
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("replay calibration: %v: %s", err, data)
	}
	files, err := gate.ReadFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestScopeImportOrder(t *testing.T) {
	fixtures, err := LoadFixtures("../l2/data/rpcx-l2-workouts-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	base := gate.Module(fixtures, "trainings")
	correct := controlCandidate(t, "correct")
	name := "adapters/trainer_grpc.go"
	imports := "import (\n\t\"context\"\n\t\"example.com/rpcx/v2\"\n\t\"time\"\n)"
	grouped := "import (\n\t\"time\"\n\t\"context\"\n\n\t\"example.com/rpcx/v2\"\n)"
	source := replaceSource(t, string(correct[name]), imports, grouped)
	call := `return s.client.Call(ctx, "cancel_training", CancelTrainingRequest{Time: trainingTime}, &struct{}{})`
	for _, tc := range []struct {
		name, old, replacement string
		pass                   bool
	}{
		{name: "reordered_and_grouped", pass: true},
		{name: "reordered", old: grouped, replacement: "import (\n\t\"example.com/rpcx/v2\"\n\t\"time\"\n\t\"context\"\n)", pass: true},
		{name: "added_import", old: `"context"`, replacement: "\"context\"\n\t\"fmt\""},
		{name: "deleted_import", old: "\t\"context\"\n", replacement: ""},
		{name: "duplicate_import", old: `"context"`, replacement: "\"context\"\n\t\"context\""},
		{name: "changed_path", old: `"example.com/rpcx/v2"`, replacement: `"example.com/other/v2"`},
		{name: "explicit_default_alias", old: `"example.com/rpcx/v2"`, replacement: `rpcx "example.com/rpcx/v2"`},
		{name: "renamed_alias", old: `"example.com/rpcx/v2"`, replacement: `other "example.com/rpcx/v2"`},
		{name: "blank_import", old: `"example.com/rpcx/v2"`, replacement: `_ "example.com/rpcx/v2"`},
		{name: "dot_import", old: `"example.com/rpcx/v2"`, replacement: `. "example.com/rpcx/v2"`},
		{name: "split_blocks", old: grouped, replacement: "import (\n\t\"context\"\n\t\"time\"\n)\nimport \"example.com/rpcx/v2\""},
		{name: "changed_method", old: `"cancel_training"`, replacement: `"cancel_other"`},
		{name: "changed_request", old: `Time: trainingTime`, replacement: `Time: trainingTime.Add(time.Hour)`},
		{name: "changed_error_handling", old: call, replacement: strings.Replace(call, "return ", "_ = ", 1) + "\n\treturn nil"},
		{name: "added_helper", old: "type TrainerGrpc struct", replacement: "func extra() {}\n\ntype TrainerGrpc struct"},
		{name: "go_directive", old: "package adapters", replacement: "//go:build linux\n\npackage adapters"},
		{name: "legacy_build_directive", old: "package adapters", replacement: "// +build linux\n\npackage adapters"},
		{name: "line_directive", old: "package adapters", replacement: "//line generated.go:1\npackage adapters"},
		{name: "block_line_directive", old: "package adapters", replacement: "/*line generated.go:1*/\npackage adapters"},
		{name: "export_directive", old: "package adapters", replacement: "//export extra\npackage adapters"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := source
			if tc.old != "" {
				data = replaceSource(t, data, tc.old, tc.replacement)
			}
			candidate := maps.Clone(correct)
			candidate[name] = []byte(data)
			want := quality.StatusUnknown
			if tc.pass {
				want = quality.StatusPass
			}
			if result := scope(base, candidate); result.Status != want {
				t.Fatalf("scope=%+v, want %s", result, want)
			}
		})
	}
}

func TestSyntaxImportAliases(t *testing.T) {
	// 相同路径的别名也参与排序；排序不得去重或丢失别名身份。
	a := []byte("package p\nimport ( a \"example.com/p\"; _ \"example.com/p\"; . \"example.com/q\" )\n")
	b := []byte("package p\nimport ( . \"example.com/q\"; _ \"example.com/p\"; a \"example.com/p\" )\n")
	left, err := syntax("p.go", a)
	if err != nil {
		t.Fatal(err)
	}
	right, err := syntax("p.go", b)
	if err != nil || left != right {
		t.Fatalf("identical import identities differ: %v", err)
	}
	b = []byte(strings.Replace(string(b), `a "example.com/p"`, `b "example.com/p"`, 1))
	right, err = syntax("p.go", b)
	if err != nil || left == right {
		t.Fatalf("changed alias lost from fingerprint: %v", err)
	}
}

func replaceSource(t *testing.T, source, old, replacement string) string {
	t.Helper()
	if count := strings.Count(source, old); count != 1 {
		t.Fatalf("expected one source fragment, found %d: %q", count, old)
	}
	return strings.Replace(source, old, replacement, 1)
}
func TestStructureCalibration(t *testing.T) {
	fixtures, err := LoadFixtures("../l2/data/rpcx-l2-workouts-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	base := gate.Module(fixtures, "trainings")
	adapted, err := adapt(gate.TestFiles(base))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"correct", "trainer-missing-timeout", "mapping-helper", "deleted-assertion"} {
		t.Run(id, func(t *testing.T) {
			candidate := controlCandidate(t, id)
			integrity, err := testintegrity.CompareAdapted(gate.TestFiles(base), adapted, gate.TestFiles(candidate), testintegrity.Plan{Version: "test", SpecSHA256: SpecSHA256(), MaterialSHA256: SpecSHA256(), BaselineFiles: gate.Hashes(gate.TestFiles(base))})
			if err != nil {
				t.Fatal(err)
			}
			want := "preserved"
			if id == "deleted-assertion" {
				want = "violated"
			}
			if integrity.Status != want {
				t.Fatalf("integrity=%s findings=%+v", integrity.Status, integrity.Findings)
			}
			wantScope := quality.StatusPass
			if id == "mapping-helper" {
				wantScope = quality.StatusUnknown
			}
			if result := scope(base, candidate); result.Status != wantScope || result.Findings[0].Code != "request_scope_"+wantScope {
				t.Fatalf("scope=%+v", result)
			}
		})
	}
}
