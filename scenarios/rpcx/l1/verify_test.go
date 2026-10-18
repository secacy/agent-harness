package l1

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/t3data"
)

func TestDatasetAndReplay(t *testing.T) {
	data, err := dataset(context.Background(), ".")
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err := decode(data["manifest.json"], &m); err != nil {
		t.Fatal(err)
	}
	if err := validateManifest(m, data); err != nil {
		t.Fatal(err)
	}
	base, err := readTree(context.Background(), "../t3/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range m.Cases {
		t.Run(c.ID, func(t *testing.T) {
			if !strings.Contains(string(data["reviews.md"]), "## "+c.ID+"\n") {
				t.Fatal("missing manual review basis")
			}
			var first string
			for range 2 {
				root := t.TempDir()
				if err := writeTree(root, base); err != nil {
					t.Fatal(err)
				}
				patch := filepath.Join(t.TempDir(), "candidate.patch")
				if err := os.WriteFile(patch, data[c.Patch], 0644); err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command("git", "apply", patch)
				cmd.Dir = root
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("apply: %v %s", err, output)
				}
				files, err := readTree(context.Background(), root)
				if err != nil {
					t.Fatal(err)
				}
				if first == "" {
					first = treeHash(files)
				} else if first != treeHash(files) {
					t.Fatal("patch replay differs")
				}
			}
		})
	}
}

func synthetic(slow bool, action string) []byte {
	data := []byte{}
	for _, id := range referenceNames(slow) {
		at := strings.Index(id, "/Test")
		pkg, name := id[:at], id[at+1:]
		for _, a := range []string{"run", action} {
			row, _ := json.Marshal(map[string]string{"Package": pkg, "Test": name, "Action": a})
			data = append(data, append(row, '\n')...)
		}
	}
	end, _ := json.Marshal(map[string]string{"Package": strings.TrimSuffix(pkg, "/"), "Action": "pass"})
	return append(data, append(end, '\n')...)
}

func TestReferenceProtocolRejectsFalseSuccess(t *testing.T) {
	for _, mode := range []string{"pass", "empty", "duplicate", "no-run", "skip", "panic", "fail-without-finding", "nonzero", "garbage"} {
		t.Run(mode, func(t *testing.T) {
			data, exit := synthetic(false, "pass"), 0
			switch mode {
			case "empty":
				data = nil
			case "duplicate":
				data = append(data, data...)
			case "no-run":
				data = []byte(strings.ReplaceAll(string(data), `"Action":"run"`, `"Action":"output"`))
			case "skip":
				data = synthetic(false, "skip")
			case "panic":
				data = append(data, []byte(`{"Action":"output","Output":"panic: stopped"}`+"\n")...)
			case "fail-without-finding":
				data, exit = synthetic(false, "fail"), 1
			case "nonzero":
				exit = 1
			case "garbage":
				data = append(data, []byte("invalid")...)
			}
			o, err := observe(data, exit, false)
			if mode == "pass" {
				if err != nil || o.Status != "pass" {
					t.Fatal(o, err)
				}
				return
			}
			if mode == "skip" {
				if err != nil || o.Status != "inconclusive" {
					t.Fatal(o, err)
				}
				return
			}
			if err == nil {
				t.Fatal("invalid reference protocol accepted", o)
			}
		})
	}
}

func TestDevelopmentRestrictions(t *testing.T) {
	for _, cfg := range []Config{{}, {Backend: execution.Local{}}, {Backend: execution.Local{}, TrustedLocal: true, Quick: true}, {Backend: execution.Local{}, TrustedLocal: true, Cases: []string{"correct-timeout"}}} {
		if r, err := Verify(context.Background(), cfg); err == nil || r != nil {
			t.Fatal("invalid config accepted", r, err)
		}
	}
}

func TestExpectedCannotSupplyAResult(t *testing.T) {
	want := Expected{Verdict: "ReadyForReview", Checks: map[string]string{}, ReferenceBuild: "pass", ReferenceFast: "pass"}
	if differences := compare(want, Result{}, false); len(differences) == 0 {
		t.Fatal("empty actual result matched expected")
	}
}

func TestReferenceFailureMustHaveAnAttributableRule(t *testing.T) {
	for _, missing := range []bool{false, true} {
		var data []byte
		row := func(value map[string]string) {
			source, _ := json.Marshal(value)
			data = append(data, append(source, '\n')...)
		}
		for i, id := range referenceNames(false) {
			at := strings.Index(id, "/Test")
			packageName, name := id[:at], id[at+1:]
			row(map[string]string{"Package": packageName, "Test": name, "Action": "run"})
			if !(missing && i == 0) {
				row(map[string]string{"Package": packageName, "Test": name, "Action": "output", "Output": `REFERENCE_FINDING {"rule":"TIME-001","detail":"missing deadline"}`})
			}
			row(map[string]string{"Package": packageName, "Test": name, "Action": "fail"})
		}
		row(map[string]string{"Package": strings.TrimSuffix(pkg, "/"), "Action": "fail"})
		o, err := observe(data, 1, false)
		if missing {
			if err == nil {
				t.Fatal("another test's finding explained an unrelated failure")
			}
		} else if err != nil || o.Status != "fail" || len(o.Rules) != 1 {
			t.Fatal(o, err)
		}
	}
}

type forbiddenExecution struct{ calls int }

func (*forbiddenExecution) Info() execution.Info { return execution.Info{Kind: "test"} }
func (b *forbiddenExecution) Run(context.Context, execution.Request) (execution.Result, error) {
	b.calls++
	return execution.Result{}, errors.New("unexpected execution")
}

func TestFrozenInputGuardsStopBeforeExecution(t *testing.T) {
	source, err := dataset(context.Background(), ".")
	if err != nil {
		t.Fatal(err)
	}
	freeze, err := os.ReadFile("freeze.json")
	if err != nil {
		t.Fatal(err)
	}
	t3, err := filepath.Abs("../t3")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"manifest", "patch", "oracle", "lock", "output-overlap"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			if err := writeTree(root, source); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "freeze.json"), freeze, 0644); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "manifest":
				if err := os.WriteFile(filepath.Join(root, "manifest.json"), append(source["manifest.json"], '\n'), 0644); err != nil {
					t.Fatal(err)
				}
			case "patch":
				if err := os.WriteFile(filepath.Join(root, "patches/correct-timeout.patch"), append(source["patches/correct-timeout.patch"], '\n'), 0644); err != nil {
					t.Fatal(err)
				}
			case "oracle":
				if err := os.WriteFile(filepath.Join(root, "oracle/alternate_test.go.txt"), append(source["oracle/alternate_test.go.txt"], '\n'), 0644); err != nil {
					t.Fatal(err)
				}
			case "lock":
				if err := os.WriteFile(filepath.Join(root, "freeze.json"), []byte("{}"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			backend := &forbiddenExecution{}
			cfg := Config{Directory: root, T3Directory: t3, RunsDir: t.TempDir(), Backend: backend}
			if mode == "output-overlap" {
				cfg.RunsDir = filepath.Join(root, "runs")
			}
			if r, err := Verify(context.Background(), cfg); err == nil || r != nil || backend.calls != 0 {
				t.Fatal("tampered inputs reached execution", r, err, backend.calls)
			}
		})
	}
}

func TestT3DataGuardsStopBeforeExecution(t *testing.T) {
	for _, path := range []string{"specs/v0.2.0.md", "fixtures/demo-orders/go.mod", "oracle/expiry_test.go.txt", "fixtures/extra.go", "freeze.json"} {
		t.Run(path, func(t *testing.T) {
			files, err := t3data.Read(context.Background(), "../t3")
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			if err := writeTree(root, files); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, path), []byte("changed"), 0644); err != nil {
				t.Fatal(err)
			}
			backend := &forbiddenExecution{}
			if r, err := Verify(context.Background(), Config{Directory: ".", T3Directory: root, RunsDir: t.TempDir(), Backend: backend}); err == nil || r != nil || backend.calls != 0 {
				t.Fatal("changed T3 data reached execution", r, err, backend.calls)
			}
		})
	}
}
