package audit

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
)

func TestStrictJSONBoundaries(t *testing.T) {
	for _, tc := range []struct{ name, text, code string }{
		{"missing", `{"schema_version":1}`, "invalid_json"},
		{"null", `{"schema_version":null}`, "invalid_json"},
		{"case-sensitive", `{"schema_version":1,"ID":"ignored"}`, "unsupported_format"},
		{"nested-duplicate", `{"schema_version":1,"summary":{"path":"a","path":"b"}}`, "invalid_json"},
		{"depth", strings.Repeat("[", 130) + "0" + strings.Repeat("]", 130), "unsupported_format"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var record RunRecord
			err := decode([]byte(tc.text), &record, 1)
			var f *formatError
			if !errors.As(err, &f) || f.code != tc.code {
				t.Fatalf("%v", err)
			}
		})
	}
}

func TestUnknownEmbeddedVersionIsGap(t *testing.T) {
	f := newFixture(t)
	data := get[map[string]any](t, f.root, "summary.json")
	data["budget"] = map[string]any{"schema_version": float64(9)}
	put(t, f.root, "summary.json", data)
	bytes, err := os.ReadFile(filepath.Join(f.root, "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	f.record.Summary.SHA256 = checksum(bytes)
	put(t, f.root, "campaign.json", f.record)
	r := auditFixture(t, f, nil)
	if len(r.Violations) != 0 || !has(r.Gaps, "unsupported_schema", "summary.json") || r.ExecutionState != "unknown" {
		t.Fatalf("%+v", r)
	}
}

func TestDirectoryAndMissingSourceDrift(t *testing.T) {
	for _, mode := range []string{"membership", "missing-file"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			inspector := func(_ context.Context, v *View, ref RunRef) (RunInspection, error) {
				dependency := "targets/orders/runs"
				if mode == "membership" {
					// 审计已经枚举 runs，之后增加一项必须被复核发现。
					if err := os.Mkdir(filepath.Join(f.root, dependency, "run-2"), 0700); err != nil {
						t.Fatal(err)
					}
				} else {
					dependency = ref.Path + "/not-yet.json"
					if _, err := v.ReadFile(dependency); !errors.Is(err, os.ErrNotExist) {
						t.Fatal(err)
					}
					putBytes(t, f.root, dependency, []byte("{}"))
				}
				return RunInspection{Findings: []Finding{{Severity: Violation, Code: "test_missing_basis", Path: dependency, Reason: "mismatch", Sources: []string{dependency}}}}, nil
			}
			r := auditFixture(t, f, inspector)
			if len(r.Violations) != 0 || r.InputsUnchanged == nil || *r.InputsUnchanged || !has(r.Gaps, "source_changed", "") {
				t.Fatalf("%+v", r)
			}
		})
	}
}

func TestViewRejectsExternalReadsAndCopiesBytes(t *testing.T) {
	f := newFixture(t)
	v := &View{root: f.root, ctx: context.Background(), sources: map[string]*Source{}, data: map[string][]byte{}}
	for _, p := range []string{"/etc/passwd", "../outside", "targets/../campaign.json", "targets\\outside"} {
		if _, err := v.ReadFile(p); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("unsafe read %s: %v", p, err)
		}
	}
	first, err := v.ReadFile("campaign.json")
	if err != nil {
		t.Fatal(err)
	}
	first[0] = 'X'
	second, err := v.ReadFile("campaign.json")
	if err != nil || !json.Valid(second) {
		t.Fatalf("caller modified cached source: %v", err)
	}
	// 声明的原 Campaign 根不需要存在；实际材料只从当前根读取。
	if strings.HasPrefix(f.root, f.record.Directory) {
		t.Fatal("fixture did not exercise relocation")
	}
}

func TestEmptyInitializationAndPrerequisiteConflicts(t *testing.T) {
	t.Run("empty-campaign", func(t *testing.T) {
		f := newFixture(t)
		entries, _ := os.ReadDir(f.root)
		for _, e := range entries {
			os.RemoveAll(filepath.Join(f.root, e.Name()))
		}
		r := auditFixture(t, f, nil)
		if len(r.Violations) != 0 || r.Consistency != "inconclusive" || !has(r.Gaps, "initialization_incomplete", "campaign.json") {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("ledger-without-campaign", func(t *testing.T) {
		f := newFixture(t)
		os.Remove(filepath.Join(f.root, "campaign.json"))
		r := auditFixture(t, f, nil)
		assertViolation(t, r, "prerequisite_missing", "campaign.json")
	})
	t.Run("running-without-budget", func(t *testing.T) {
		f := newFixture(t)
		f.record.Status = "Running"
		f.record.FinishedAt = nil
		f.target = campaign.TargetRecord{ID: "orders", State: campaign.Active}
		f.save(t)
		os.Remove(filepath.Join(f.root, ledgerPath))
		r := auditFixture(t, f, nil)
		assertViolation(t, r, "prerequisite_missing", ledgerPath)
	})
	t.Run("missing-referenced-run-directory", func(t *testing.T) {
		f := newFixture(t)
		os.RemoveAll(filepath.Join(f.root, runPath("orders", "run-1")))
		r := auditFixture(t, f, nil)
		assertViolation(t, r, "target_attempt_mismatch", targetPath("orders"))
	})
}

func TestCancelledAndFailedTerminalPrefixes(t *testing.T) {
	for _, status := range []string{"Cancelled", "Failed"} {
		t.Run(status, func(t *testing.T) {
			f := newFixture(t)
			f.record.Status = campaign.Status(status)
			f.record.Error = "stopped"
			f.run.Status = status
			f.run.Error = "stopped"
			f.run.Verdict = ""
			f.target.Attempt.Status = campaign.Status(status)
			f.target.Attempt.Verdict = ""
			f.target.Error = "stopped"
			f.save(t)
			r := auditFixture(t, f, nil)
			if len(r.Violations) != 0 || r.ExecutionState != "terminal_with_failures" {
				t.Fatalf("%+v", r)
			}
		})
	}
}

// 业务函数在下一单元接入；此替身只证明宿主回调的结果与来源契约。
func TestInspectorCompletionAndSourceRequirements(t *testing.T) {
	f := newFixture(t)
	complete := func(_ context.Context, v *View, ref RunRef) (RunInspection, error) {
		_, err := v.ReadFile(ref.Path + "/artifacts/report.json")
		return RunInspection{Complete: err == nil}, err
	}
	r := auditFixture(t, f, complete)
	if r.Consistency != "pass" || !r.ScanComplete {
		t.Fatalf("%+v", r)
	}
	unverified := func(context.Context, *View, RunRef) (RunInspection, error) {
		return RunInspection{Complete: true, Findings: []Finding{{Severity: Violation, Code: "test_unread_source", Path: "unread.json", Sources: []string{"unread.json"}, Reason: "unsupported assertion"}}}, nil
	}
	r = auditFixture(t, f, unverified)
	if r.Consistency != "inconclusive" || len(r.Violations) != 0 || !has(r.Gaps, "source_unverified", "") {
		t.Fatalf("%+v", r)
	}
}

func TestAuditArchive(t *testing.T) {
	root := os.Getenv("HARNESS_AUDIT_CAMPAIGN")
	if root == "" {
		t.Skip("set HARNESS_AUDIT_CAMPAIGN for a read-only archived-campaign check")
	}
	output := os.Getenv("HARNESS_AUDIT_OUTPUT")
	if output == "" {
		output = filepath.Join(t.TempDir(), "audit")
	}
	before := sourceTree(t, root)
	r, err := Inspect(context.Background(), Request{root, output}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Violations) != 0 || !has(r.Gaps, "run_inspector_missing", "") {
		t.Fatalf("unexpected core archive result: %+v", r)
	}
	if !reflect.DeepEqual(before, sourceTree(t, root)) {
		t.Fatal("archive changed during audit")
	}
	t.Logf("consistency=%s execution=%s gaps=%d sources=%d output=%s", r.Consistency, r.ExecutionState, len(r.Gaps), len(r.Sources), output)
}

func TestViewTotalReadLimits(t *testing.T) {
	f := newFixture(t)
	v := &View{root: f.root, ctx: context.Background(), sources: map[string]*Source{}, data: map[string][]byte{}, bytes: maxBytes}
	if _, err := v.ReadFile("campaign.json"); !errors.Is(err, errReadLimit) {
		t.Fatalf("expected aggregate byte limit, got %v", err)
	}
	if len(v.sources) != 0 {
		t.Fatal("over-limit file was accepted as an observed source")
	}
}

func TestNoCallBindingAndTerminalPublication(t *testing.T) {
	t.Run("bound-zero-calls", func(t *testing.T) {
		f := newFixture(t)
		f.ledger.Calls = []budget.Call{}
		f.ledger.Totals = budget.Totals{}
		a := f.ledger.Targets["orders"]
		a.Totals = budget.Totals{}
		f.ledger.Targets["orders"] = a
		f.save(t)
		r := auditFixture(t, f, nil)
		if len(r.Violations) != 0 {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("completed-before-publication", func(t *testing.T) {
		f := newFixture(t)
		f.run.Phase = "Generating"
		f.save(t)
		r := auditFixture(t, f, nil)
		assertViolation(t, r, "terminal_claim_incomplete", runPath("orders", "run-1")+"/run.json")
	})
}
