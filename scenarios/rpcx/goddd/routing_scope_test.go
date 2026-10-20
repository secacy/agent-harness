package goddd

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
)

func routingVariant(t *testing.T, named, read bool) map[string][]byte {
	t.Helper()
	candidate := boundedVariant(t)
	data := candidate[routingPath]
	if named {
		data = changeSource(t, data, "type proxyService struct", "const routeTimeout = 30 * time.Second\n\ntype proxyService struct")
		data = changeSource(t, data, "context.WithTimeout(ctx, 30*time.Second)", "context.WithTimeout(ctx, routeTimeout)")
	}
	if read {
		data = changeSource(t, data, "addr string) ServiceMiddleware {", "addr string) ServiceMiddleware {\n_ = ctx")
	}
	candidate[routingPath] = data
	return candidate
}

func TestRoutingScopeBoundedForms(t *testing.T) {
	base, a := variantFixture(t)
	for _, tc := range []struct {
		name        string
		named, read bool
		from, to    string
	}{
		{name: "neither"}, {name: "constant", named: true}, {name: "read", read: true},
		{name: "both", named: true, read: true},
		{name: "fresh_name", named: true, read: true, from: "routeTimeout", to: "localRPCDeadline"},
		{name: "single_const_block", named: true, from: "const routeTimeout = 30 * time.Second", to: "const (routeTimeout = 30 * time.Second)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := routingVariant(t, tc.named, tc.read)
			if tc.from != "" {
				candidate[routingPath] = []byte(strings.ReplaceAll(string(candidate[routingPath]), tc.from, tc.to))
			}
			before := hashes(candidate)
			result, matches, report := requestScope(base, candidate, a)
			if result.Status != quality.StatusPass {
				t.Fatalf("scope=%+v report=%+v", result, report)
			}
			if !tc.named && !tc.read {
				if report != nil || matches[routingPath] != "production-canonical" {
					t.Fatal("raw canonical identity lost")
				}
				return
			}
			if report == nil || !report.Matched || len(report.Gaps) != 0 || report.ExpectedStructureSHA256 != report.RemainingStructureSHA256 {
				t.Fatalf("report=%+v", report)
			}
			if matches[routingPath] != "" {
				t.Fatal("normalized proof mislabeled as raw template")
			}
			want := 0
			if tc.named {
				want++
			}
			if tc.read {
				want++
			}
			if len(report.Facts) != want {
				t.Fatal("unexpected facts", report)
			}
			assertRoutingNodes(t, candidate[routingPath], *report)
			if !reflect.DeepEqual(before, hashes(candidate)) {
				t.Fatal("candidate rewritten")
			}
		})
	}
}

func assertRoutingNodes(t *testing.T, data []byte, report routingScopeReport) {
	t.Helper()
	if report.CandidateFileSHA256 != digest(data) {
		t.Fatal("raw file binding lost")
	}
	for _, fact := range report.Facts {
		if len(fact.Nodes) != 2 || len(fact.RuleIDs) == 0 {
			t.Fatal("incomplete fact", fact)
		}
		for _, n := range fact.Nodes {
			if n.Tree != "candidate" || n.Path != routingPath || n.Offset < 0 || n.End > len(data) || n.End <= n.Offset || digest(data[n.Offset:n.End]) != n.SHA256 {
				t.Fatal("invalid raw node", n)
			}
			prefix := string(data[:n.Offset])
			line := strings.Count(prefix, "\n") + 1
			column := n.Offset - strings.LastIndex(prefix, "\n")
			if n.Line != line || n.Column != column {
				t.Fatal("invalid source position", n)
			}
		}
	}
}

func TestRoutingScopeRejectsUnprovenChanges(t *testing.T) {
	base, a := variantFixture(t)
	for _, tc := range []struct{ name, old, new string }{
		{"wrong_duration", "30 * time.Second", "5 * time.Second"},
		{"mutable", "const routeTimeout", "var routeTimeout"},
		{"explicit_type", "routeTimeout =", "routeTimeout time.Duration ="},
		{"conversion", "30 * time.Second", "time.Duration(30) * time.Second"},
		{"iota", "30 * time.Second", "iota * time.Second"},
		{"alias", "30 * time.Second", "time.Second"},
		{"reversed_expression", "30 * time.Second", "time.Second * 30"},
		{"numeric_alias", "30 * time.Second", "0x1e * time.Second"},
		{"multi_value", "routeTimeout = 30 * time.Second", "routeTimeout, otherTimeout = 30 * time.Second, 30 * time.Second"},
		{"const_group", "const routeTimeout = 30 * time.Second", "const (routeTimeout = 30 * time.Second; otherTimeout = 1)"},
		{"extra_const", "type proxyService struct", "const extraTimeout = 1\ntype proxyService struct"},
		{"unused_constant", "context.WithTimeout(ctx, routeTimeout)", "context.WithTimeout(ctx, 30*time.Second)"},
		{"other_use", "var resp FetchRoutesResponse", "_ = routeTimeout\nvar resp FetchRoutesResponse"},
		{"shadow", "ctx, cancel :=", "routeTimeout := 30*time.Second\nctx, cancel :="},
		{"request_use", "From: string(rs.Origin)", "From: string(routeTimeout)"},
		{"wrong_parent", "context.WithTimeout(ctx, routeTimeout)", "context.WithTimeout(context.Background(), routeTimeout)"},
		{"prefix_moved", "ctx, cancel :=", "_ = rs\nctx, cancel :="},
		{"missing_cancel", "defer cancel()", "_ = cancel"},
		{"cancel_argument", "defer cancel()", "defer cancel(ctx)"},
		{"cancel_early", "defer cancel()", "cancel()"},
		{"pointer_receiver", "(s proxyService)", "(s *proxyService)"},
		{"method_signature", "rs shipping.RouteSpecification)", "rs *shipping.RouteSpecification)"},
		{"method_renamed", "func (s proxyService) FetchRoutesForSpecification", "func (s proxyService) OtherFetch"},
		{"duplicate_rpc", "func (s proxyService) FetchRoutesForSpecification", "func (s proxyService) FetchRoutesForSpecification(ctx context.Context, rs shipping.RouteSpecification) []shipping.Itinerary {return nil}\nfunc (s proxyService) FetchRoutesForSpecification"},
		{"duplicate_constructor", "func NewProxyingMiddleware", "func NewProxyingMiddleware(ctx context.Context, addr string) ServiceMiddleware {return nil}\nfunc NewProxyingMiddleware"},
		{"constructor_nonfirst_read", "_ = ctx", "{_ = ctx}"},
		{"constructor_bad_assignment", "_ = ctx", "_ := ctx"},
		{"constructor_call", "_ = ctx", "_ = ctx.Err()"},
		{"constructor_field", "_ = ctx", "_ = ctx.Value"},
		{"constructor_capture", "_ = ctx", "_ = func(){ _ = ctx }"},
		{"constructor_rebind", "_ = ctx", "ctx = context.Background()"},
		{"constructor_save", "_ = ctx", "saved := ctx\n_ = saved"},
		{"constructor_duplicate_read", "_ = ctx", "_ = ctx\n_ = ctx"},
		{"constructor_moved_read", "_ = ctx\n\tclient, err := rpcx.Dial(addr)", "client, err := rpcx.Dial(addr)\n_ = ctx"},
		{"constructor_parameter", "NewProxyingMiddleware(ctx context.Context", "NewProxyingMiddleware(ctx interface{}"},
		{"constructor_changed_address", "rpcx.Dial(addr)", "rpcx.Dial(\"other\")"},
		{"changed_request", "From: string(rs.Origin)", "From: string(rs.Destination)"},
		{"changed_response", "shipping.VoyageNumber(e.Voyage)", "shipping.VoyageNumber(e.Origin)"},
		{"changed_error", "return []shipping.Itinerary{}", "panic(err)"},
		{"extra_helper", "type proxyService struct", "func extraHelper() {}\ntype proxyService struct"},
		{"changed_import", `"time"`, `other "time"`},
		{"directive", "package routing", "//go:build linux\n\npackage routing"},
		{"parse_error", "package routing", "package {"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := routingVariant(t, true, true)
			candidate[routingPath] = changeSource(t, candidate[routingPath], tc.old, tc.new)
			result, matches, report := requestScope(base, candidate, a)
			if result.Status != quality.StatusUnknown || report == nil || report.Matched || len(report.Gaps) == 0 || matches[routingPath] != "" {
				t.Fatalf("scope=%+v report=%+v", result, report)
			}
		})
	}
}

func TestRoutingConstantIdentityAcrossProduction(t *testing.T) {
	base, a := variantFixture(t)
	for _, mode := range []string{"exported", "blank", "baseline_field", "baseline_local", "cross_file", "other_method", "node_identity", "invalid_production", "test_use"} {
		t.Run(mode, func(t *testing.T) {
			original := maps.Clone(base)
			candidate := routingVariant(t, true, false)
			switch mode {
			case "exported", "blank":
				name := "RouteTimeout"
				if mode == "blank" {
					name = "_"
				}
				candidate[routingPath] = []byte(strings.ReplaceAll(string(candidate[routingPath]), "routeTimeout", name))
			case "baseline_field":
				original["identity.go"] = []byte("package shipping\ntype old struct {routeTimeout int}")
			case "baseline_local":
				original["identity.go"] = []byte("package shipping\nfunc old(){routeTimeout:=1;_ = routeTimeout}")
			case "cross_file":
				candidate["routing/extra.go"] = []byte("package routing\nvar extra = routeTimeout")
			case "other_method":
				candidate[routingPath] = append(candidate[routingPath], []byte("\nfunc extra() { _ = routeTimeout }\n")...)
			case "node_identity":
				candidate[routingPath] = changeSource(t, candidate[routingPath], "context.WithTimeout(ctx, routeTimeout)", "context.WithTimeout(ctx, 30*time.Second)")
				candidate[routingPath] = append(candidate[routingPath], []byte("\nfunc extra() { _ = routeTimeout }\n")...)
			case "invalid_production":
				candidate["broken.go"] = []byte("package {")
			case "test_use":
				candidate["routing/extra_test.go"] = []byte("package routing\nfunc other(){_ = routeTimeout}")
			}
			report := routingScope(original, candidate, a.production[routingPath])
			if report.Matched != (mode == "test_use") {
				t.Fatalf("mode=%s report=%+v", mode, report)
			}
		})
	}
}

func TestRoutingLocalFactsDoNotProveRemainingStructure(t *testing.T) {
	base, a := variantFixture(t)
	candidate := routingVariant(t, true, true)
	candidate[routingPath] = changeSource(t, candidate[routingPath], "From: string(rs.Origin)", "From: string(rs.Destination)")
	report := routingScope(base, candidate, a.production[routingPath])
	if report.Matched || len(report.Facts) != 2 || !slices.Contains(report.Gaps, "remaining_structure_changed") {
		t.Fatalf("partial facts incorrectly accepted: %+v", report)
	}
}

func TestRoutingEvidenceAndPublicationFailure(t *testing.T) {
	base, _ := variantFixture(t)
	for _, mode := range []string{"normal", "unknown", "blocked"} {
		t.Run(mode, func(t *testing.T) {
			candidate := routingVariant(t, true, true)
			if mode == "unknown" {
				candidate[routingPath] = changeSource(t, candidate[routingPath], "rpcx.Dial(addr)", "rpcx.Dial(\"other\")")
			}
			root := t.TempDir()
			if mode == "blocked" {
				if err := os.Mkdir(filepath.Join(root, "routing-scope.json"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			e := evaluation{req: checks.EvaluationRequest{EvidenceDir: root}, result: checks.Evaluation{SpecSHA256: SpecSHA256(), MaterialsSHA256: SpecSHA256(), BaselineSHA256: treeHash(base), CandidateSHA256: treeHash(candidate), Checks: map[string]quality.Check{}}}
			_, err := e.checkAdaptations(base, candidate)
			if mode == "blocked" {
				if err == nil || e.result.Verdict != "" {
					t.Fatal("publication failure ignored", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(root, "routing-scope.json"))
			if err != nil {
				t.Fatal(err)
			}
			var report routingScopeReport
			if err := json.Unmarshal(data, &report); err != nil {
				t.Fatal(err)
			}
			if report.Binding != e.binding() || report.MaterialsSHA256 != e.result.MaterialsSHA256 || report.Matched != (mode == "normal") {
				t.Fatalf("unbound proof: %+v", report)
			}
			assertRoutingNodes(t, candidate[routingPath], report)
			scope := e.result.Checks["request_scope"]
			for _, f := range scope.Findings {
				if !slices.Contains(f.EvidenceIDs, "routing-scope") {
					t.Fatal("finding lacks proof reference", f)
				}
			}
			data, err = os.ReadFile(filepath.Join(root, "adaptations.json"))
			if err != nil {
				t.Fatal(err)
			}
			var index adaptationEvidence
			if err := json.Unmarshal(data, &index); err != nil {
				t.Fatal(err)
			}
			if index.ProductionMatches[routingPath] != "" {
				t.Fatal("proof mislabeled as canonical")
			}
		})
	}
}
