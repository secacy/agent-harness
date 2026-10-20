package goddd

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
	"github.com/secacy/agent-harness/scenarios/rpcx/testintegrity"
)

func variantFixture(t *testing.T) (map[string][]byte, adaptations) {
	t.Helper()
	fixtures, err := LoadFixtures("../l2/data/rpcx-l2-goddd-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	base := module(fixtures, "goddd")
	a, err := prepareAdaptations(base)
	if err != nil {
		t.Fatal(err)
	}
	return base, a
}
func changeSource(t *testing.T, data []byte, old, replacement string) []byte {
	t.Helper()
	if n := strings.Count(string(data), old); n != 1 {
		t.Fatalf("expected one source fragment, found %d: %q", n, old)
	}
	return []byte(strings.Replace(string(data), old, replacement, 1))
}
func boundedVariant(t *testing.T) map[string][]byte {
	t.Helper()
	_, a := variantFixture(t)
	candidate := controlCandidate(t, "correct")
	candidate[mockPath] = a.forwarded
	candidate[stubPath] = a.tests[1][stubPath]
	return candidate
}

func TestMockAdaptationRequiresWholeFileMatch(t *testing.T) {
	base, a := variantFixture(t)
	for _, tc := range []struct {
		name, old, new string
		pass           bool
	}{
		{name: "forwarded", pass: true},
		{name: "legacy", old: "s.FetchRoutesFn(ctx, rs)", new: "s.FetchRoutesFn(rs)"},
		{name: "signature_only", old: "s.FetchRoutesFn(ctx, rs)", new: "s.FetchRoutesFn(rs)"},
		{name: "forward_only", old: "func(context.Context, shipping.RouteSpecification)", new: "func(shipping.RouteSpecification)"},
		{name: "background", old: "s.FetchRoutesFn(ctx, rs)", new: "s.FetchRoutesFn(context.Background(), rs)"},
		{name: "other_context", old: "s.FetchRoutesFn(ctx, rs)", new: "s.FetchRoutesFn(nil, rs)"},
		{name: "changed_request", old: "s.FetchRoutesFn(ctx, rs)", new: "s.FetchRoutesFn(ctx, shipping.RouteSpecification{})"},
		{name: "changed_flag", old: "s.FetchRoutesInvoked = true", new: "s.FetchRoutesInvoked = false"},
		{name: "other_method", old: "return r.FindFn(id)", new: "return r.FindFn(\"other\")"},
		{name: "extra_helper", old: "type RoutingService struct", new: "func extra(){}\ntype RoutingService struct"},
		{name: "directive", old: "package mock", new: "//go:build linux\n\npackage mock"},
		{name: "renamed_type", old: "type RoutingService struct", new: "type OtherService struct"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := controlCandidate(t, "correct")
			candidate[mockPath] = a.forwarded
			want := quality.StatusUnknown
			if tc.pass {
				want = quality.StatusPass
			}
			if tc.name == "legacy" {
				candidate[mockPath] = a.production[mockPath]
				want = quality.StatusPass
			} else if tc.old != "" {
				candidate[mockPath] = changeSource(t, candidate[mockPath], tc.old, tc.new)
			}
			result, matches, _ := requestScope(base, candidate, a)
			if result.Status != want {
				t.Fatalf("scope=%+v want=%s", result, want)
			}
			if tc.pass && matches[mockPath] != "production-forwarded" {
				t.Fatal("missing forwarded identity", matches)
			}
			if !tc.pass && tc.name != "legacy" && matches[mockPath] != "" {
				t.Fatal("partial proof recorded", matches)
			}
		})
	}
}

func TestHostVariantShapeGuards(t *testing.T) {
	base, a := variantFixture(t)
	for _, tc := range []struct{ name, old, new string }{
		{"field_type", "func(shipping.RouteSpecification) []shipping.Itinerary", "func(string) []shipping.Itinerary"},
		{"field_missing", "FetchRoutesFn      func", "OtherFn            func"},
		{"method_body", "return s.FetchRoutesFn(rs)", "return nil"},
		{"wrong_receiver", "(s *RoutingService)", "(s *OtherService)"},
		{"context_alias", `"context"`, `ctxpkg "context"`},
		{"duplicate_type", "type RoutingService struct", "type RoutingService struct{}\ntype RoutingService struct"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := changeSource(t, a.production[mockPath], tc.old, tc.new)
			if _, err := forwardedMock(data); err == nil {
				t.Fatal("invalid host shape accepted")
			}
		})
	}
	for _, tc := range []struct{ name, old, new string }{
		{"local_ctx", "legs := []shipping.Leg{", "ctx := 1\n _ = ctx\nlegs := []shipping.Leg{"},
		{"closure_ctx", "legs := []shipping.Leg{", "_ = func(ctx int){}\nlegs := []shipping.Leg{"},
		{"field_ctx", "legs := []shipping.Leg{", "_ = struct{ctx int}{}\nlegs := []shipping.Leg{"},
		{"existing_parameter", "(rs shipping.RouteSpecification)", "(ctx context.Context, rs shipping.RouteSpecification)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := changeSource(t, base[stubPath], tc.old, tc.new)
			if _, err := blankContextStub(original, a.tests[0][stubPath]); err == nil {
				t.Fatal("original ctx binding accepted")
			}
		})
	}
	for _, tc := range []struct{ name, old, new string }{
		{"changed_old_parameter", "ctx context.Context, rs shipping.RouteSpecification", "ctx context.Context, other shipping.RouteSpecification"},
		{"changed_type", "ctx context.Context, rs shipping.RouteSpecification", "ctx string, rs shipping.RouteSpecification"},
		{"parameter_order", "ctx context.Context, rs shipping.RouteSpecification", "rs shipping.RouteSpecification, ctx context.Context"},
		{"added_body_use", "legs := []shipping.Leg{", "_ = ctx\nlegs := []shipping.Leg{"},
		{"missing_method", "(s *stubRoutingService)", "(s *OtherStub)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			canonical := changeSource(t, a.tests[0][stubPath], tc.old, tc.new)
			if _, err := blankContextStub(base[stubPath], canonical); err == nil {
				t.Fatal("unproven test adaptation accepted")
			}
		})
	}
}

func TestUnusedContextKeepsOriginalTestLogic(t *testing.T) {
	base, a := variantFixture(t)
	candidate := boundedVariant(t)
	for _, tc := range []struct{ name, old, new, want string }{
		{name: "blank", want: "preserved"},
		{name: "canonical", want: "preserved"},
		{name: "other_name", old: "_ context.Context", new: "requestCtx context.Context", want: "unknown"},
		{name: "changed_argument", old: "rs.Origin", new: "rs.Destination", want: "unknown"},
		{name: "added_call", old: "legs := []shipping.Leg{", new: "_ = context.Background()\nlegs := []shipping.Leg{", want: "unknown"},
		{name: "deleted_assertion", old: "if c.TrackingID != id {\n\t\tt.Errorf(\"c.TrackingID = %s; want = %s\", c.TrackingID, id)\n\t}", want: "violated"},
		{name: "skip", old: "func TestBookNewCargo(t *testing.T) {", new: "func TestBookNewCargo(t *testing.T) {\nt.Skip(\"skip\")", want: "violated"},
		{name: "new_init", old: "type stubRoutingService struct{}", new: "func init(){}\ntype stubRoutingService struct{}", want: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := maps.Clone(testFiles(candidate))
			if tc.name == "canonical" {
				files[stubPath] = a.tests[0][stubPath]
			} else if tc.old != "" {
				files[stubPath] = changeSource(t, files[stubPath], tc.old, tc.new)
			}
			plan := testintegrity.Plan{Version: "test", SpecSHA256: SpecSHA256(), MaterialSHA256: SpecSHA256(), BaselineFiles: hashes(testFiles(base))}
			var reports [2]testintegrity.Report
			for i := range reports {
				var err error
				reports[i], err = testintegrity.CompareAdapted(testFiles(base), a.tests[i], files, plan)
				if err != nil {
					t.Fatal(err)
				}
			}
			selected, err := selectIntegrity(reports)
			if err != nil || reports[selected].Status != tc.want {
				t.Fatalf("selection=%d err=%v reports=%+v", selected, err, reports)
			}
			if !reflect.DeepEqual(reports[selected].Candidate.Files, hashes(files)) {
				t.Fatal("candidate hashes were rewritten")
			}
		})
	}
}

func TestIntegritySelectionDoesNotMergeOrHideConflicts(t *testing.T) {
	for _, tc := range []struct {
		a, b  string
		index int
		fail  bool
	}{
		{"unknown", "preserved", 1, false}, {"preserved", "unknown", 0, false},
		{"violated", "unknown", 0, false}, {"unknown", "violated", 1, false},
		{"unknown", "unknown", 0, false}, {"preserved", "preserved", 0, false},
		{"violated", "preserved", 0, true}, {"preserved", "violated", 0, true}, {"invalid", "unknown", 0, true},
	} {
		t.Run(tc.a+"/"+tc.b, func(t *testing.T) {
			// 声明各自即使有 preserved，整份 unknown 报告仍不得拼出通过。
			reports := [2]testintegrity.Report{{Status: tc.a, Declarations: []testintegrity.Declaration{{ID: "a", Status: "preserved"}}}, {Status: tc.b, Declarations: []testintegrity.Declaration{{ID: "b", Status: "preserved"}}}}
			selected, err := selectIntegrity(reports)
			if (err != nil) != tc.fail || err == nil && selected != tc.index {
				t.Fatalf("selection=%d err=%v", selected, err)
			}
		})
	}
	reports := [2]testintegrity.Report{{Status: "preserved"}, {Status: "unknown", Candidate: testintegrity.Binding{SHA256: "different"}}}
	if _, err := selectIntegrity(reports); err == nil {
		t.Fatal("mismatched candidate binding accepted")
	}
}

func TestAdaptationEvidenceAndPublicationFailure(t *testing.T) {
	base, _ := variantFixture(t)
	candidate := boundedVariant(t)
	for _, mode := range []string{"normal", "variant-write", "report-write", "selection-write", "selected-tests-write"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			blocked := map[string]string{"variant-write": "adaptations/production-forwarded/mock/mock.go", "report-write": "adaptations/tests-blank-context-report.json", "selection-write": "adaptations.json", "selected-tests-write": "adapted-tests"}[mode]
			if blocked != "" {
				p := filepath.Join(root, blocked)
				if mode == "selected-tests-write" {
					if err := os.WriteFile(p, []byte("blocked"), 0600); err != nil {
						t.Fatal(err)
					}
				} else if err := os.MkdirAll(p, 0700); err != nil {
					t.Fatal(err)
				}
			}
			e := evaluation{req: checks.EvaluationRequest{EvidenceDir: root}, result: checks.Evaluation{SpecSHA256: SpecSHA256(), MaterialsSHA256: SpecSHA256(), BaselineSHA256: treeHash(base), CandidateSHA256: treeHash(candidate), Checks: map[string]quality.Check{}}}
			selected, err := e.checkAdaptations(base, candidate)
			if mode != "normal" {
				if err == nil || e.result.Verdict != "" {
					t.Fatal("publication failure ignored", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var index adaptationEvidence
			data, err := os.ReadFile(filepath.Join(root, "adaptations.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(data, &index); err != nil {
				t.Fatal(err)
			}
			if index.TestVariant != "tests-blank-context" || index.ProductionMatches[mockPath] != "production-forwarded" {
				t.Fatalf("wrong selection: %+v", index)
			}
			actual, err := readFiles(filepath.Join(root, "adapted-tests"))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(hashes(actual), hashes(selected)) {
				t.Fatal("saved tests differ from executed tests")
			}
			for _, v := range index.Variants {
				files, err := readFiles(filepath.Join(root, v.Path))
				if err != nil || !reflect.DeepEqual(hashes(files), v.Files) {
					t.Fatal("variant bytes differ", v.ID, err)
				}
			}
			for _, ev := range e.result.Evidence {
				data, err := os.ReadFile(filepath.Join(root, ev.Path))
				if err != nil || digest(data) != ev.SHA256 || ev.Binding != e.binding() {
					t.Fatal("unbound adaptation evidence", ev.ID, err)
				}
			}
			for _, key := range []string{"test_preservation", "request_scope"} {
				if e.result.Checks[key].Status != quality.StatusPass {
					t.Fatal("bounded adaptation failed", key)
				}
			}
		})
	}
}
