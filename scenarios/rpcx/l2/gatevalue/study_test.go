package gatevalue

import (
	"context"
	"encoding/json"
	"github.com/secacy/agent-harness/scenarios/rpcx/runner"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

func TestEvents(t *testing.T) {
	base := `{"Action":"start","Package":"p"}
{"Action":"run","Package":"p","Test":"TestA"}
{"Action":"pass","Package":"p","Test":"TestA"}
{"Action":"pass","Package":"p"}
`
	for _, tc := range []struct {
		name, raw, want string
		code            int
	}{
		{"pass", base, "supported", 0},
		{"missing-test", strings.ReplaceAll(base, `"TestA"`, `"TestB"`), "insufficient", 0},
		{"missing-package-end", strings.TrimSuffix(base, "{\"Action\":\"pass\",\"Package\":\"p\"}\n"), "insufficient", 0},
		{"duplicate", base + `{"Action":"pass","Package":"p"}`, "insufficient", 0},
		{"skip", strings.ReplaceAll(base, `"pass"`, `"skip"`), "insufficient", 0},
		{"failure", strings.ReplaceAll(base, `"pass"`, `"fail"`), "failed", 1},
		{"false-zero", strings.ReplaceAll(base, `"pass"`, `"fail"`), "insufficient", 0},
		{"extra-package", base + `{"Action":"start","Package":"q"}`, "insufficient", 0},
		{"truncated", base + `{`, "insufficient", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := testEvents([]byte(tc.raw), []string{"p/TestA"}, tc.code); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}
func TestReaderRejectsMissingChangedAndForeignInputs(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := reader{root: root, output: filepath.Join(root, "output"), sources: map[string]string{}}
	if err := gate.Write(filepath.Join(root, "source"), []byte("original")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.read("source", gate.Digest([]byte("original"))); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../foreign", "missing"} {
		if _, err := r.read(name, ""); err == nil {
			t.Fatal("invalid path accepted")
		}
	}
	if err := gate.Write(filepath.Join(root, "source"), []byte("changed")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.read("source", ""); err == nil {
		t.Fatal("source drift accepted")
	}
	if err := r.tree(".", "bad-candidate"); err == nil {
		t.Fatal("wrong candidate accepted")
	}
}

// TestHistoricalStudy 只读已固定材料；无 Go 子进程、Docker 或模型执行。
func TestHistoricalStudy(t *testing.T) {
	output := os.Getenv("HARNESS_GATE_VALUE_OUTPUT")
	if output == "" {
		t.Skip("set HARNESS_GATE_VALUE_OUTPUT for historical study")
	}
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	report, err := Run(context.Background(), root, "docs/experiments/gate-value-review-v0.1.0/candidates.json", output)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Rows) != 5 || !report.InputsUnchanged {
		t.Fatal("incomplete study")
	}
	for _, row := range report.Rows {
		var count int
		var duration int64
		for _, cost := range row.Costs {
			count += cost.Count
			duration += cost.Milliseconds
		}
		var commandTime int64
		for _, cmd := range row.Commands {
			commandTime += cmd.Milliseconds
		}
		if count != len(row.Commands) || duration != commandTime {
			t.Fatal("cost categories do not partition commands")
		}
		if row.BuildTests != "supported" || row.Error != "" {
			t.Errorf("%s/%s: %s %s", row.Case.Cohort, row.Case.Target, row.BuildTests, row.Error)
		}
	}
}

// TestHistoricalMutations 只篡改消费文件的独立副本，并绕过外层摘要演练事件/清单缺口。
func TestHistoricalMutations(t *testing.T) {
	source := os.Getenv("HARNESS_GATE_VALUE_REPORT")
	if source == "" {
		t.Skip("set HARNESS_GATE_VALUE_REPORT for archived-source mutation checks")
	}
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	var original Report
	if err = json.Unmarshal(data, &original); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"missing-log", "changed-log", "missing-test", "candidate-mismatch", "missing-command"} {
		t.Run(mode, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			c := original.Rows[0].Case
			if mode == "missing-command" {
				c = original.Rows[2].Case
			}
			run := filepath.Dir(filepath.Dir(c.Report))
			refdir := filepath.Dir(c.Reference)
			for _, s := range original.Sources {
				if strings.HasPrefix(s.Path, run+"/") || strings.HasPrefix(s.Path, refdir+"/") || s.Path == c.Review {
					raw, err := os.ReadFile(filepath.Join(root, s.Path))
					if err != nil {
						t.Fatal(err)
					}
					if err = gate.Write(filepath.Join(base, s.Path), raw); err != nil {
						t.Fatal(err)
					}
				}
			}
			raw, err := os.ReadFile(filepath.Join(base, c.Report))
			if err != nil {
				t.Fatal(err)
			}
			var rep runner.Report
			if err = json.Unmarshal(raw, &rep); err != nil {
				t.Fatal(err)
			}
			name := "logs/candidate-test.stdout.log"
			path := filepath.Join(base, run, "evidence", name)
			switch mode {
			case "missing-log":
				if err = os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "changed-log":
				if err = gate.Write(path, []byte("garbage")); err != nil {
					t.Fatal(err)
				}
			case "candidate-mismatch":
				if err = gate.Write(filepath.Join(base, run, "workspace/ghcli/extra.go"), []byte("changed")); err != nil {
					t.Fatal(err)
				}
			case "missing-test":
				raw, err = os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				lines := strings.Split(string(raw), "\n")
				kept := []string{}
				removed := ""
				for _, line := range lines {
					if line == "" {
						continue
					}
					var e struct{ Test string }
					if json.Unmarshal([]byte(line), &e) != nil {
						t.Fatal("invalid original events")
					}
					if removed == "" && e.Test != "" {
						removed = e.Test
					}
					if e.Test != removed {
						kept = append(kept, line)
					}
				}
				raw = []byte(strings.Join(kept, "\n") + "\n")
				if err = gate.Write(path, raw); err != nil {
					t.Fatal(err)
				}
				for i := range rep.Validation.Evidence {
					if rep.Validation.Evidence[i].Path == name {
						rep.Validation.Evidence[i].SHA256 = gate.Digest(raw)
					}
				}
			case "missing-command":
				filtered := rep.Validation.Commands[:0]
				for _, cmd := range rep.Validation.Commands {
					if cmd.Log != "logs/rpcx_alternate-candidate-tests.stdout.log" {
						filtered = append(filtered, cmd)
					}
				}
				rep.Validation.Commands = filtered
			}
			if mode == "missing-test" || mode == "missing-command" {
				raw, err = json.Marshal(rep)
				if err != nil {
					t.Fatal(err)
				}
				if err = gate.Write(filepath.Join(base, c.Report), raw); err != nil {
					t.Fatal(err)
				}
				c.ReportSHA = gate.Digest(raw)
			}
			row := Row{Case: c, BuildTests: "insufficient"}
			r := reader{root: base, output: filepath.Join(base, "output"), sources: map[string]string{}}
			if err = r.analyze(c, &row); err == nil || row.BuildTests == "supported" {
				t.Fatalf("mutation accepted: %v %+v", err, row)
			}
		})
	}

	for _, source := range original.Sources {
		data, err := os.ReadFile(filepath.Join(root, source.Path))
		if err != nil || gate.Digest(data) != source.SHA {
			t.Fatalf("original source changed: %s", source.Path)
		}
	}
}

func TestOutputOverlapDoesNotCreateDirectory(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := Manifest{Schema: 1, Kind: "historical_candidate_review", Cases: []Case{{Report: "campaign/targets/t/runs/r/artifacts/report.json", Reference: "reference/report.json", Review: "reviews/t.json"}}}
	if err = gate.WriteJSON(filepath.Join(root, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "campaign", "new-output")
	if _, err = Run(context.Background(), root, "manifest.json", output); err == nil {
		t.Fatal("overlap accepted")
	}
	if _, err = os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("output created inside historical input")
	}
}
