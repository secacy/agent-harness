package baseline

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/execution"
)

func translationData(t *testing.T) map[string][]byte {
	t.Helper()
	files, err := read(context.Background(), filepath.Join(projectRoot(t), "scenarios/rpcx/l2/data", TranslationVersion))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestTranslationReleaseAndSourceIdentity(t *testing.T) {
	files := translationData(t)
	if digest(files["freeze.json"]) != "d1e0e20608fed9e45212a6fb5010834028ab82228573a391c78a1500f7be8bdd" {
		t.Fatal("published translation release changed")
	}
	if err := frozenVersion(files, TranslationVersion); err != nil {
		t.Fatal(err)
	}
	if err := provenance(files); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"internal/usecase/translation_test.go", "internal/usecase/mocks_repo_test.go", "internal/entity/task_test.go"} {
		if !reflect.DeepEqual(files["fixtures/translation/"+name], files["provenance/upstream/"+name+".txt"]) {
			t.Fatal("original tests/mock modified:", name)
		}
	}
	for _, sdk := range []string{"rpcx-v1", "rpcx-v2"} {
		source, err := read(context.Background(), filepath.Join(projectRoot(t), "scenarios/rpcx/l2/data", Version, "fixtures", sdk))
		if err != nil {
			t.Fatal(err)
		}
		for name, data := range source {
			if !reflect.DeepEqual(data, files["fixtures/"+sdk+"/"+name]) {
				t.Fatal("shared SDK differs:", sdk, name)
			}
		}
	}
	ds, err := lookupDataset(TranslationVersion)
	if err != nil || len(ds.Configurations) != 2 || ds.Configurations[0].ID != "default" || ds.Configurations[1].ID != "rpcx_alternate" {
		t.Fatal("required build matrix differs", err)
	}
	if err := unpack(files, t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestConfigurationEvidenceRejectsWrongSelection(t *testing.T) {
	ds, _ := lookupDataset(TranslationVersion)
	for _, config := range ds.Configurations {
		t.Run(config.ID, func(t *testing.T) {
			good := configEvents(config.ActiveFile, config.IgnoredFile, true)
			if err := configurationFiles(good, config); err != nil {
				t.Fatal(err)
			}
			for _, data := range [][]byte{nil, configEvents(config.IgnoredFile, config.ActiveFile, true), configEvents(config.ActiveFile, config.IgnoredFile, false), append(good, good...), []byte("broken")} {
				if configurationFiles(data, config) == nil {
					t.Fatal("invalid configuration evidence accepted")
				}
			}
		})
	}
}

func configEvents(active, ignored string, mock bool) []byte {
	files := []string{"translation_test.go"}
	if mock {
		files = append(files, "mocks_repo_test.go")
	}
	a, _ := json.Marshal(map[string]any{"ImportPath": "github.com/evrone/go-clean-template/internal/repo/webapi", "GoFiles": []string{active}, "IgnoredGoFiles": []string{ignored}})
	b, _ := json.Marshal(map[string]any{"ImportPath": "github.com/evrone/go-clean-template/internal/usecase", "XTestGoFiles": files})
	return append(append(a, '\n'), b...)
}

func completeEvents(inventory moduleInventory) []byte {
	var out []byte
	add := func(v map[string]string) { data, _ := json.Marshal(v); out = append(append(out, data...), '\n') }
	for _, id := range append(append([]string{}, inventory.OriginalTests...), inventory.AddedTests...) {
		pos := strings.Index(id, "/Test")
		pkg, name := id[:pos], id[pos+1:]
		for _, action := range []string{"run", "pass"} {
			add(map[string]string{"Action": action, "Package": pkg, "Test": name})
		}
	}
	for _, pkg := range inventory.Packages {
		add(map[string]string{"Action": "pass", "Package": pkg})
	}
	for _, pkg := range inventory.PackagesWithoutTests {
		add(map[string]string{"Action": "skip", "Package": pkg})
	}
	return out
}

// 替身只检验配置调度与失败终态，真实业务命令另以本地/Docker 复验。
func TestTranslationRequiresBothConfigurations(t *testing.T) {
	files := translationData(t)
	var tests testInventory
	if err := decode(files["tests.json"], &tests); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"complete", "alternate-source", "alternate-build", "alternate-test", "missing-events", "cancelled", "mutated-material"} {
		t.Run(mode, func(t *testing.T) {
			output := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			alternateCommands := 0
			backend := &fakeExecutor{execute: func(req execution.Request) (execution.Result, error) {
				alternate := slices.Contains(req.Args, "-tags=rpcx_alternate")
				if alternate {
					alternateCommands++
				}
				module := filepath.Base(req.Directory)
				if alternate && mode == "cancelled" {
					cancel()
				}
				if alternate && mode == "mutated-material" {
					if err := os.WriteFile(filepath.Join(output, "modules", "unexpected"), []byte("changed"), 0644); err != nil {
						t.Fatal(err)
					}
				}
				if slices.Contains(req.Args, "list") {
					active, ignored := "translation_google.go", "translation_rpcx_alternate.go"
					if alternate && mode != "alternate-source" {
						active, ignored = ignored, active
					}
					return execution.Result{ExitCode: 0, Stdout: configEvents(active, ignored, true)}, nil
				}
				if alternate && mode == "alternate-build" && slices.Contains(req.Args, "build") {
					return execution.Result{ExitCode: 1}, nil
				}
				if slices.Contains(req.Args, "test") {
					if alternate && mode == "alternate-test" {
						return execution.Result{ExitCode: 1}, nil
					}
					data := completeEvents(tests.Modules[module])
					if alternate && mode == "missing-events" {
						data = nil
					}
					return execution.Result{ExitCode: 0, Stdout: data}, nil
				}
				return execution.Result{ExitCode: 0}, nil
			}}
			report, err := VerifyVersion(ctx, projectRoot(t), output, TranslationVersion, backend)
			if mode == "complete" {
				if err != nil || !report.Accepted || len(report.Commands) != 14 || alternateCommands != 3 {
					t.Fatalf("incomplete matrix: %+v %v", report, err)
				}
				count := 0
				for _, cmd := range report.Commands {
					count += cmd.PassedTests
				}
				if count != 67 {
					t.Fatal("configuration test count differs", count)
				}
			} else if err == nil || report.Accepted {
				t.Fatalf("invalid alternate config accepted: %+v %v", report, err)
			}
			if _, err := os.Stat(filepath.Join(output, "report.json")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUnknownDatasetStopsBeforeExecution(t *testing.T) {
	backend := &fakeExecutor{}
	report, err := VerifyVersion(context.Background(), projectRoot(t), t.TempDir(), "unknown", backend)
	if err == nil || report != nil || backend.calls != 0 {
		t.Fatal("unsupported dataset was executed")
	}
}

func TestPublishedIdentityCannotBeRewritten(t *testing.T) {
	files := translationData(t)
	files["fixtures/translation/internal/usecase/translation_test.go"] = []byte("package usecase_test\n")
	var m manifest
	if err := decode(files["freeze.json"], &m); err != nil {
		t.Fatal(err)
	}
	m.Files["fixtures/translation/internal/usecase/translation_test.go"] = digest(files["fixtures/translation/internal/usecase/translation_test.go"])
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	files["freeze.json"] = append(data, '\n')
	if err := frozenVersion(files, TranslationVersion); err == nil {
		t.Fatal("recomputed manifest replaced a published identity")
	}
}

func TestTranslationPreparationPatchReplays(t *testing.T) {
	files := translationData(t)
	var origins struct {
		Files []struct{ Source, Action string }
	}
	if err := json.Unmarshal(files["provenance.json"], &origins); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, entry := range origins.Files {
		if entry.Action != "kept" && entry.Action != "modified" {
			continue
		}
		if err := write(filepath.Join(root, filepath.FromSlash(entry.Source)), files["provenance/upstream/"+entry.Source+".txt"]); err != nil {
			t.Fatal(err)
		}
	}
	patch, err := filepath.Abs(filepath.Join(projectRoot(t), "scenarios/rpcx/l2/data", TranslationVersion, "provenance/adaptation.patch"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(context.Background(), "git", "apply", patch)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("replay preparation patch: %v: %s", err, output)
	}
	actual, err := read(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string][]byte{}
	for name, data := range files {
		if relative, ok := strings.CutPrefix(name, "fixtures/translation/"); ok {
			expected[relative] = data
		}
	}
	if !equalFiles(actual, expected) {
		t.Fatal("preparation patch does not reproduce the baseline")
	}
}
