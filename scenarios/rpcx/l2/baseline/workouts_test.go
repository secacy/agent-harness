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

func workoutsData(t *testing.T) map[string][]byte {
	t.Helper()
	files, err := read(context.Background(), filepath.Join(projectRoot(t), "scenarios/rpcx/l2/data", WorkoutsVersion))
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestWorkoutsReleaseAndOriginalTests(t *testing.T) {
	files := workoutsData(t)
	if digest(files["freeze.json"]) != "5efa3908ad6f80a8673a4893ef28bb6213a4a8d9a281bce4498726aa69282cb2" {
		t.Fatal("published Workouts release changed")
	}
	if err := frozenVersion(files, WorkoutsVersion); err != nil {
		t.Fatal(err)
	}
	if err := provenance(files); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"app/command/cancel_training.go", "app/command/cancel_training_test.go", "domain/training/cancel_test.go", "domain/training/reschedule_test.go", "domain/training/training_test.go", "domain/training/user_test.go"} {
		if !reflect.DeepEqual(files["fixtures/trainings/"+name], files["provenance/upstream/internal/trainings/"+name+".txt"]) {
			t.Fatal("original callback/tests changed:", name)
		}
	}
	old, err := read(context.Background(), filepath.Join(projectRoot(t), "scenarios/rpcx/l2/data", Version, "fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range old {
		if strings.HasPrefix(name, "rpcx-") && !reflect.DeepEqual(data, files["fixtures/"+name]) {
			t.Fatal("shared SDK changed:", name)
		}
	}
	ds, err := lookupDataset(WorkoutsVersion)
	if err != nil || !reflect.DeepEqual(ds.modules(), []string{"rpcx-v1", "rpcx-v2", "common", "trainings"}) {
		t.Fatal("module closure differs", err)
	}
	if err := unpack(files, t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

// 替身只检查多模块编排与错误分支，不代替真实业务测试。
func TestWorkoutsRequiresLocalModule(t *testing.T) {
	files := workoutsData(t)
	var inventory testInventory
	if err := decode(files["tests.json"], &inventory); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"complete", "common-build", "common-events", "missing-common", "cancelled", "business-events"} {
		t.Run(mode, func(t *testing.T) {
			output, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			modules := []string{}
			backend := &fakeExecutor{execute: func(req execution.Request) (execution.Result, error) {
				module := filepath.Base(req.Directory)
				modules = append(modules, module)
				if len(req.ReadOnlyDirs) != 5 || !slices.Contains(req.ReadOnlyDirs, filepath.Join(output, "workspace/common")) {
					t.Fatalf("local module not mounted: %+v", req)
				}
				if module == "common" {
					switch mode {
					case "common-build":
						if slices.Contains(req.Args, "build") {
							return execution.Result{ExitCode: 1}, nil
						}
					case "missing-common":
						if err := os.Remove(filepath.Join(output, "workspace/common/go.mod")); err != nil && !os.IsNotExist(err) {
							t.Fatal(err)
						}
					case "cancelled":
						cancel()
					}
				}
				if slices.Contains(req.Args, "test") {
					data := completeEvents(inventory.Modules[module])
					if module == "common" && mode == "common-events" || module == "trainings" && mode == "business-events" {
						data = nil
					}
					return execution.Result{ExitCode: 0, Stdout: data}, nil
				}
				return execution.Result{ExitCode: 0}, nil
			}}
			report, err := VerifyVersion(ctx, projectRoot(t), output, WorkoutsVersion, backend)
			if mode == "complete" {
				if err != nil || !report.Accepted || len(report.Commands) != 12 {
					t.Fatalf("incomplete closure: %+v %v", report, err)
				}
				count := 0
				for _, cmd := range report.Commands {
					count += cmd.PassedTests
				}
				if count != 55 {
					t.Fatal("unexpected test count:", count)
				}
			} else if err == nil || report.Accepted {
				t.Fatalf("invalid module execution accepted: %+v %v", report, err)
			}
			if mode == "common-build" || mode == "common-events" || mode == "cancelled" {
				if slices.Contains(modules, "trainings") {
					t.Fatal("failed common reached business commands")
				}
			}
			if _, err := os.Stat(filepath.Join(output, "report.json")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWorkoutsPreparationPatchReplays(t *testing.T) {
	files := workoutsData(t)
	var origin struct {
		Files []struct{ Source, Action, Target string }
	}
	if err := json.Unmarshal(files["provenance.json"], &origin); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, f := range origin.Files {
		if f.Action != "kept" && f.Action != "modified" {
			continue
		}
		if err := write(filepath.Join(root, strings.TrimPrefix(f.Target, "fixtures/")), files["provenance/upstream/"+f.Source+".txt"]); err != nil {
			t.Fatal(err)
		}
	}
	patch, err := filepath.Abs(filepath.Join(projectRoot(t), "scenarios/rpcx/l2/data", WorkoutsVersion, "provenance/adaptation.patch"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(context.Background(), "git", "apply", patch)
	cmd.Dir = root
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("replay preparation: %v: %s", err, output)
	}
	actual, err := read(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string][]byte{}
	for name, data := range files {
		if rel, ok := strings.CutPrefix(name, "fixtures/"); ok && (strings.HasPrefix(rel, "common/") || strings.HasPrefix(rel, "trainings/")) {
			expected[rel] = data
		}
	}
	if !equalFiles(expected, actual) {
		t.Fatal("preparation patch does not reproduce both local modules")
	}
}
