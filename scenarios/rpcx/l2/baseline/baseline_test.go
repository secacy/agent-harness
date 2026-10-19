package baseline

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/execution"
)

func projectRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestFrozenReleaseAndOriginalTests(t *testing.T) {
	files, err := read(context.Background(), filepath.Join(projectRoot(t), "scenarios/rpcx/l2/data", Version))
	if err != nil {
		t.Fatal(err)
	}
	if err := frozen(files); err != nil {
		t.Fatal(err)
	}
	if err := provenance(files); err != nil {
		t.Fatal(err)
	}
	var origins struct {
		Files []struct {
			Source       string `json:"source"`
			SourceSHA256 string `json:"source_sha256"`
			Action       string `json:"action"`
			Target       string `json:"target"`
			TargetSHA256 string `json:"target_sha256"`
		} `json:"files"`
	}
	if err := json.Unmarshal(files["provenance.json"], &origins); err != nil {
		t.Fatal(err)
	}
	originalTestFiles := 0
	for _, origin := range origins.Files {
		if origin.Target != "" && digest(files[origin.Target]) != origin.TargetSHA256 {
			t.Fatalf("provenance target mismatch: %s", origin.Target)
		}
		if origin.Action == "kept" && origin.SourceSHA256 != origin.TargetSHA256 {
			t.Fatalf("retained source changed: %s", origin.Source)
		}
		if origin.Action == "modified" && digest(files["provenance/upstream/"+origin.Source+".txt"]) != origin.SourceSHA256 {
			t.Fatalf("original material missing: %s", origin.Source)
		}
		if origin.Action == "kept" && strings.HasSuffix(origin.Source, "_test.go") {
			originalTestFiles++
		}
	}
	if originalTestFiles != 3 {
		t.Fatalf("got %d original test files, want 3", originalTestFiles)
	}
	if err := unpack(files, t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidDataStopsBeforeExecution(t *testing.T) {
	for _, mode := range []string{"modified", "extra", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			dataRoot := filepath.Join(root, "scenarios/rpcx/l2/data", Version)
			files, err := read(context.Background(), filepath.Join(projectRoot(t), "scenarios/rpcx/l2/data", Version))
			if err != nil {
				t.Fatal(err)
			}
			for name, data := range files {
				if err := write(filepath.Join(dataRoot, name), data); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "modified":
				err = os.WriteFile(filepath.Join(dataRoot, "fixtures/goddd/booking/service_test.go"), []byte("package booking\n"), 0644)
			case "extra":
				err = os.WriteFile(filepath.Join(dataRoot, "unlisted.txt"), []byte("extra"), 0644)
			case "symlink":
				err = os.Symlink("tests.json", filepath.Join(dataRoot, "linked.json"))
			}
			if err != nil {
				t.Fatal(err)
			}
			backend := &fakeExecutor{}
			report, err := Verify(context.Background(), root, t.TempDir(), backend)
			if err == nil || report == nil || report.Accepted || backend.calls != 0 {
				t.Fatalf("err=%v report=%+v calls=%d", err, report, backend.calls)
			}
		})
	}
}

type fakeExecutor struct {
	calls   int
	execute func(execution.Request) (execution.Result, error)
}

func (f *fakeExecutor) Info() execution.Info { return execution.Info{Kind: execution.KindLocal} }
func (f *fakeExecutor) Run(_ context.Context, req execution.Request) (execution.Result, error) {
	f.calls++
	if f.execute != nil {
		return f.execute(req)
	}
	return execution.Result{ExitCode: 0}, nil
}

func TestTestEventsRejectIncompleteEvidence(t *testing.T) {
	inventory := moduleInventory{Packages: []string{"p"}, OriginalTests: []string{"p/TestBusiness"}}
	good := "{\"Action\":\"run\",\"Package\":\"p\",\"Test\":\"TestBusiness\"}\n" +
		"{\"Action\":\"pass\",\"Package\":\"p\",\"Test\":\"TestBusiness\"}\n" +
		"{\"Action\":\"pass\",\"Package\":\"p\"}\n"
	if count, err := testEvents([]byte(good), inventory); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	cases := map[string]string{
		"empty":         "",
		"missing-test":  "{\"Action\":\"pass\",\"Package\":\"p\"}\n",
		"no-package":    strings.TrimSuffix(good, "{\"Action\":\"pass\",\"Package\":\"p\"}\n"),
		"skipped":       strings.Replace(good, "\"pass\"", "\"skip\"", 1),
		"duplicate":     good + good,
		"failed":        strings.ReplaceAll(good, "\"pass\"", "\"fail\""),
		"invalid-json":  good + "not JSON",
		"wrong-package": strings.ReplaceAll(good, "\"p\"", "\"other\""),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := testEvents([]byte(data), inventory); err == nil {
				t.Fatal("incomplete evidence accepted")
			}
		})
	}
}

func TestModuleArchiveRejectsInvalidMembers(t *testing.T) {
	for _, mode := range []string{"parent", "absolute", "symlink", "duplicate", "missing", "wrong-hash"} {
		t.Run(mode, func(t *testing.T) {
			name := "module/file.go"
			if mode == "parent" {
				name = "../escape"
			} else if mode == "absolute" {
				name = "/escape"
			}
			var buffer bytes.Buffer
			gz := gzip.NewWriter(&buffer)
			tarball := tar.NewWriter(gz)
			if mode != "missing" {
				h := &tar.Header{Name: name, Size: 1, Mode: 0644, Typeflag: tar.TypeReg}
				if mode == "symlink" {
					h.Typeflag, h.Linkname, h.Size = tar.TypeSymlink, "../escape", 0
				}
				copies := 1
				if mode == "duplicate" {
					copies = 2
				}
				for range copies {
					if err := tarball.WriteHeader(h); err != nil {
						t.Fatal(err)
					}
					if h.Size != 0 {
						if _, err := tarball.Write([]byte("x")); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			if err := tarball.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			hash := digest([]byte("x"))
			if mode == "wrong-hash" {
				hash = digest([]byte("y"))
			}
			metadata, err := json.Marshal(map[string]any{"schema_version": 1, "modules": []any{}, "files": map[string]string{name: hash}, "expanded_bytes": 1})
			if err != nil {
				t.Fatal(err)
			}
			files := map[string][]byte{"dependencies/modules.json": metadata, "dependencies/modules.tar.gz": buffer.Bytes()}
			if err := unpack(files, t.TempDir()); err == nil {
				t.Fatal("invalid archive accepted")
			}
		})
	}
}

func TestProvenanceRejectsMismatchedOriginal(t *testing.T) {
	files, err := read(context.Background(), filepath.Join(projectRoot(t), "scenarios/rpcx/l2/data", Version))
	if err != nil {
		t.Fatal(err)
	}
	files["provenance/upstream/go.mod.txt"] = []byte("module changed\n")
	if err := provenance(files); err == nil {
		t.Fatal("changed original material accepted")
	}
}

func TestFailedCommandPreservesEvidenceAndDetectsInputChanges(t *testing.T) {
	for _, mutate := range []bool{false, true} {
		name := "command-failure"
		if mutate {
			name = "modified-workspace"
		}
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			backend := &fakeExecutor{execute: func(req execution.Request) (execution.Result, error) {
				if mutate {
					filename := filepath.Join(directory, "workspace/goddd/booking/service.go")
					if err := os.WriteFile(filename, []byte("package booking\n"), 0644); err != nil {
						t.Fatal(err)
					}
				}
				return execution.Result{ExitCode: 1, Stderr: []byte("injected command failure")}, nil
			}}
			report, err := Verify(context.Background(), projectRoot(t), directory, backend)
			if err == nil || report.Accepted || len(report.Commands) != 1 || report.InputsUnchanged == mutate {
				t.Fatalf("err=%v report=%+v", err, report)
			}
			if report.Commands[0].StderrSHA256 != digest([]byte("injected command failure")) {
				t.Fatal("failure log identity lost")
			}
			if _, err := os.Stat(filepath.Join(directory, "report.json")); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInterruptedVerificationCannotPublishAcceptance(t *testing.T) {
	directory := t.TempDir()
	backend := &fakeExecutor{execute: func(execution.Request) (execution.Result, error) {
		panic("injected host panic")
	}}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("host panic was hidden")
			}
		}()
		_, _ = Verify(context.Background(), projectRoot(t), directory, backend)
	}()
	data, err := os.ReadFile(filepath.Join(directory, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	if report.Accepted || report.Error == "" {
		t.Fatalf("interrupted report falsely accepted: %+v", report)
	}
}
