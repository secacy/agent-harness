package analysis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func comparisonReport(legacy bool) Report {
	operation := "Inventory.Reserve"
	ns := int64(30_000_000_000)
	r := Report{SchemaVersion: 2, Status: "complete", InputSHA256: digest([]byte("candidate")), ToolSHA256: digest([]byte("tool")),
		Config:   Configuration{GoVersion: "go1.26.5", GOOS: "linux", GOARCH: "arm64", CGO: "0", Tags: []string{}},
		Packages: []Package{{Path: "demo", Files: map[string]string{"client.go": digest([]byte("source"))}, Ignored: []string{}}},
		Calls: []Call{{Package: "demo", File: "client.go", Line: 10, Function: "(*demo.Client).Reserve", SDK: "example.com/rpcx/v2",
			Kind: "direct", Status: "covered", Reason: "direct_timeout_boundary", TimeoutNS: &ns, Operation: &operation}}, Gaps: []Gap{}}
	if legacy {
		r.InputSHA256 = digest([]byte("baseline"))
		r.Calls[0].SDK, r.Calls[0].Status, r.Calls[0].Reason, r.Calls[0].TimeoutNS = "example.com/rpcx", "legacy", "v1_call", nil
	}
	return r
}

func logComparison(t *testing.T, comparison Comparison) {
	t.Helper()
	data, err := json.Marshal(comparison)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("COMPARISON %s", data)
}

func TestCompareCorrespondenceAndChanges(t *testing.T) {
	for _, sample := range []struct {
		name    string
		mutate  func(*Report, *Report)
		matches int
		changes []string
		gaps    int
	}{
		{"migration", func(_, _ *Report) {}, 1, nil, 0},
		{"line-shift", func(_, c *Report) { c.Calls[0].Line = 100 }, 1, nil, 0},
		{"file-move", func(_, c *Report) {
			c.Packages[0].Files = map[string]string{"moved.go": digest([]byte("source"))}
			c.Calls[0].File = "moved.go"
		}, 1, nil, 0},
		{"added", func(_, c *Report) { c.Calls = append(c.Calls, renamed(c.Calls[0], "Extra")) }, 1, []string{"call_added"}, 0},
		{"removed", func(_, c *Report) { c.Calls = []Call{} }, 0, []string{"call_removed"}, 0},
		{"same-count-replaced", func(_, c *Report) { c.Calls[0] = renamed(c.Calls[0], "Extra") }, 0, []string{"call_added", "call_removed"}, 0},
		{"ambiguous", func(_, c *Report) { c.Calls = append(c.Calls, c.Calls[0]) }, 0, []string{"ambiguous_function_calls"}, 0},
		{"operation-changed", func(_, c *Report) { s := "Inventory.Release"; c.Calls[0].Operation = &s }, 0, []string{"operation_changed"}, 0},
		{"operation-unknown", func(_, c *Report) { c.Calls[0].Operation = nil }, 0, []string{"operation_unresolved"}, 0},
		{"legacy-remains", func(b, c *Report) { c.Calls = b.Calls }, 0, []string{"v1_call_remaining"}, 0},
		{"unidentified", func(_, c *Report) { c.Calls[0].Function = "" }, 0, []string{"unidentified_owner", "call_removed"}, 0},
		{"unknown-preserved", func(_, c *Report) { makeUnknown(c, "unsupported_context_derivation") }, 1, nil, 1},
		{"indirect", func(_, c *Report) { makeUnknown(c, "method_value_or_expression"); c.Calls[0].Kind = "indirect" }, 0, []string{"indirect_correspondence"}, 1},
		{"violation-preserved", func(_, c *Report) {
			ns := int64(3_000_000_000)
			c.Calls[0].Status, c.Calls[0].Reason, c.Calls[0].TimeoutNS = "violation", "timeout_duration_mismatch", &ns
		}, 1, nil, 0},
		{"baseline-gap-preserved", func(b, _ *Report) { b.Gaps = []Gap{{File: "extra.go", Reason: "unlisted_build_file"}} }, 1, nil, 1},
		{"empty-baseline", func(b, c *Report) { b.Calls, c.Calls = []Call{}, []Call{} }, 0, []string{"baseline_calls_empty"}, 0},
		{"tag-set-order", func(b, c *Report) { b.Config.Tags = []string{"b", "a", "a"}; c.Config.Tags = []string{"a", "b"} }, 1, nil, 0},
	} {
		t.Run(sample.name, func(t *testing.T) {
			b, c := comparisonReport(true), comparisonReport(false)
			sample.mutate(&b, &c)
			beforeB, _ := json.Marshal(b)
			beforeC, _ := json.Marshal(c)
			r, err := Compare(b, c)
			if err != nil {
				t.Fatal(err)
			}
			var reasons []string
			for _, change := range r.Changes {
				reasons = append(reasons, change.Reason)
			}
			if len(r.Matches) != sample.matches || !reflect.DeepEqual(reasons, sample.changes) || len(r.Gaps) != sample.gaps {
				t.Fatalf("unexpected comparison: %+v", r)
			}
			if r.Baseline.RecordSHA256 != digest(beforeB) || r.Candidate.RecordSHA256 != digest(beforeC) {
				t.Fatal("comparison did not bind the consumed records")
			}
			afterB, _ := json.Marshal(b)
			afterC, _ := json.Marshal(c)
			if string(beforeB) != string(afterB) || string(beforeC) != string(afterC) {
				t.Fatal("comparison mutated its inputs")
			}
			if len(r.Matches) != 0 && (r.Matches[0].CandidateStatus != c.Calls[0].Status || r.Matches[0].CandidateReason != c.Calls[0].Reason) {
				t.Fatal("pairing erased the candidate analysis state")
			}
		})
	}
}

func renamed(call Call, name string) Call {
	call.Function = "(*demo.Client)." + name
	return call
}

func makeUnknown(r *Report, reason string) {
	r.Calls[0].Status, r.Calls[0].Reason, r.Calls[0].TimeoutNS = "unknown", reason, nil
	r.Gaps = []Gap{{File: r.Calls[0].File, Line: r.Calls[0].Line, Reason: reason}}
}

func TestCompareRejectsIncompatibleOrMalformedReports(t *testing.T) {
	for _, corruption := range []string{"schema", "load-failed", "input-digest", "tool-digest", "tags", "platform", "tool-version", "source-hash", "outside-call", "missing-gap", "missing-calls", "invalid-kind"} {
		t.Run(corruption, func(t *testing.T) {
			b, c := comparisonReport(true), comparisonReport(false)
			switch corruption {
			case "schema":
				c.SchemaVersion = 1
			case "load-failed":
				c.Status, c.LoadExitCode, c.Diagnostic = "load_failed", 1, "loader failed"
			case "input-digest":
				c.InputSHA256 = "invalid"
			case "tool-digest":
				c.ToolSHA256 = "invalid"
			case "tags":
				c.Config.Tags = []string{"extra"}
			case "platform":
				c.Config.GOOS = "darwin"
			case "tool-version":
				c.ToolSHA256 = digest([]byte("other tool"))
			case "source-hash":
				c.Packages[0].Files["client.go"] = "invalid"
			case "outside-call":
				c.Calls[0].File = "../outside.go"
			case "missing-gap":
				c.Calls[0].Status = "unknown"
			case "missing-calls":
				c.Calls = nil
			case "invalid-kind":
				c.Calls[0].Kind = "guessed"
			}
			r, err := Compare(b, c)
			if err == nil || r.SchemaVersion != 0 {
				t.Fatalf("bad report became usable comparison: %+v %v", r, err)
			}
		})
	}
}

func TestCompareOrderingDoesNotGuessCorrespondence(t *testing.T) {
	b, c := comparisonReport(true), comparisonReport(false)
	b.Calls = append(b.Calls, renamed(b.Calls[0], "Release"))
	c.Calls = append(c.Calls, renamed(c.Calls[0], "Release"))
	first, err := Compare(b, c)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(b.Calls)
	slices.Reverse(c.Calls)
	second, err := Compare(b, c)
	if err != nil || !reflect.DeepEqual(first.Matches, second.Matches) || !reflect.DeepEqual(first.Changes, second.Changes) || !reflect.DeepEqual(first.Gaps, second.Gaps) {
		t.Fatalf("input order changed correspondence: %+v %v", second, err)
	}
	if first.Baseline.RecordSHA256 == second.Baseline.RecordSHA256 {
		t.Fatal("record binding lost the actual input array order")
	}
}

func TestCompareActualStructuralChanges(t *testing.T) {
	cache := t.TempDir()
	baseReq := scenario(t, "")
	baseReq.CacheDir = cache
	baseline := runInspect(t, baseReq).Report
	for _, change := range []string{"added", "removed", "same-count-replaced", "operation-changed", "operation-constant", "operation-dynamic", "ambiguous", "non-sdk-replacement", "file-move", "arguments-changed"} {
		t.Run(change, func(t *testing.T) {
			req := scenario(t, "correct-timeout")
			req.CacheDir = cache
			path := filepath.Join(req.ModuleDir, "internal/inventory/client.go")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			source := string(data)
			if change == "removed" || change == "same-count-replaced" || change == "non-sdk-replacement" || change == "file-move" {
				start := strings.Index(source, "func (c *Client) Release(")
				if start < 0 {
					t.Fatal("fixture Release function missing")
				}
				if change == "file-move" {
					header := "package inventory\nimport(\"context\";\"fmt\";\"time\";\"example.com/rpcx/v2\")\n"
					if err := os.WriteFile(filepath.Join(filepath.Dir(path), "release.go"), []byte(header+source[start:]), 0644); err != nil {
						t.Fatal(err)
					}
					source = source[:start]
				} else {
					source = source[:start] + "func (c *Client) Release(ctx context.Context, sku string, n int) error { return nil }\n"
					if change == "non-sdk-replacement" {
						source = source[:start] + "func (c *Client) Release(ctx context.Context, sku string, n int) error { return (other{}).Call() }\ntype other struct{}\nfunc (other) Call() error { return nil }\n"
					}
				}
			}
			if change == "added" || change == "same-count-replaced" {
				source += "\nfunc (c *Client) Extra(ctx context.Context) error { return c.rpc.Call(ctx, \"Inventory.Extra\", rpcx.Request{}) }\n"
			}
			if change == "operation-changed" {
				source = strings.Replace(source, `"Inventory.Reserve"`, `"Inventory.Release"`, 1)
			}
			if change == "operation-constant" {
				source = strings.Replace(source, `"Inventory.Reserve"`, "reserveOperation", 1) + "\nconst reserveOperation = \"Inventory.\" + \"Reserve\"\n"
			}
			if change == "operation-dynamic" {
				source = strings.Replace(source, `"Inventory.Reserve"`, "operation()", 1) + "\nfunc operation() string { return \"Inventory.Reserve\" }\n"
			}
			if change == "ambiguous" {
				branch := "if sku == \"other\" { return c.rpc.Call(ctx, \"Inventory.Reserve\", rpcx.Request{SKU:sku, Quantity:n}) }; "
				source = strings.Replace(source, "ctx, cancel :=", branch+"ctx, cancel :=", 1)
			}
			if change == "arguments-changed" {
				source = strings.ReplaceAll(source, "Quantity: n", "Quantity: n+1")
			}
			if err := os.WriteFile(path, []byte(source), 0644); err != nil {
				t.Fatal(err)
			}
			candidate := runInspect(t, req).Report
			comparison, err := Compare(baseline, candidate)
			if err != nil {
				t.Fatal(err)
			}
			wantMatches, wantChanges := 1, 1
			if change == "added" {
				wantMatches = 2
			} else if change == "same-count-replaced" {
				wantChanges = 2
			} else if change == "file-move" || change == "operation-constant" || change == "arguments-changed" {
				wantMatches, wantChanges = 2, 0
			}
			if len(comparison.Matches) != wantMatches || len(comparison.Changes) != wantChanges {
				t.Fatalf("unexpected actual comparison: %+v", comparison)
			}
			wantReasons := map[string][]string{
				"added": {"call_added"}, "removed": {"call_removed"},
				"same-count-replaced": {"call_added", "call_removed"}, "operation-changed": {"operation_changed"},
				"operation-dynamic": {"operation_unresolved"}, "ambiguous": {"ambiguous_function_calls"},
				"non-sdk-replacement": {"call_removed"},
			}
			var reasons []string
			for _, item := range comparison.Changes {
				reasons = append(reasons, item.Reason)
			}
			if !reflect.DeepEqual(reasons, wantReasons[change]) {
				t.Fatalf("wrong structural change reason: %v", reasons)
			}
			if change == "same-count-replaced" && len(candidate.Calls) != len(baseline.Calls) {
				t.Fatal("same-count control no longer has equal call counts")
			}
			logComparison(t, comparison)
		})
	}
}
