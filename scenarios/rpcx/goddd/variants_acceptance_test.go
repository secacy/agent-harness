package goddd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
)

// 新变体完整验证及 mock 直接传播均经显式开关执行，避免常规回归重复等待期限。
func TestGodddContextVariantsAcceptance(t *testing.T) {
	if os.Getenv("HARNESS_GODDD_ACCEPTANCE") != "1" {
		t.Skip("set HARNESS_GODDD_ACCEPTANCE=1 for context variants")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	var backend execution.Executor = execution.Local{}
	if os.Getenv("HARNESS_GODDD_BACKEND") == "docker" {
		d, err := execution.NewDocker(ctx, execution.DockerConfig{})
		if err != nil {
			t.Fatal(err)
		}
		backend = d
	}
	output := os.Getenv("HARNESS_GODDD_OUTPUT")
	if output == "" {
		output = t.TempDir()
	}
	output, err := filepath.Abs(output)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(output, 0755); err != nil {
		t.Fatal(err)
	}
	fixtures, err := LoadFixtures("../l2/data/rpcx-l2-goddd-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, want string }{
		{"forwarded-blank", "ReadyForReview"}, {"wrong-duration", "Rejected"}, {"wrong-mock-context", "NeedsAttention"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := os.MkdirTemp(output, tc.name+"-")
			if err != nil {
				t.Fatal(err)
			}
			t.Log("artifacts:", root)
			candidate := boundedVariant(t)
			if tc.name == "wrong-duration" {
				candidate["routing/proxying.go"] = changeSource(t, candidate["routing/proxying.go"], "30*time.Second", "5*time.Second")
			}
			if tc.name == "wrong-mock-context" {
				candidate[mockPath] = changeSource(t, candidate[mockPath], "s.FetchRoutesFn(ctx, rs)", "s.FetchRoutesFn(context.Background(), rs)")
			}
			for _, tree := range []string{"baseline", "candidate"} {
				if err := copyFiles(filepath.Join(root, tree), fixtures); err != nil {
					t.Fatal(err)
				}
			}
			if err := copyFiles(filepath.Join(root, "candidate/goddd"), candidate); err != nil {
				t.Fatal(err)
			}
			spec := filepath.Join(root, "spec.md")
			if err := write(spec, Spec()); err != nil {
				t.Fatal(err)
			}
			result, err := Evaluate(ctx, checks.EvaluationRequest{BaselineDir: filepath.Join(root, "baseline/goddd"), CandidateDir: filepath.Join(root, "candidate/goddd"), SpecPath: spec, EvidenceDir: filepath.Join(root, "evidence"), Backend: backend})
			if saveErr := writeJSON(filepath.Join(root, "evaluation.json"), result); saveErr != nil {
				t.Fatal(saveErr)
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.Verdict != tc.want || result.Checks["test_preservation"].Status != "pass" {
				t.Fatalf("verdict=%s want=%s checks=%+v", result.Verdict, tc.want, result.Checks)
			}
			if tc.name == "wrong-mock-context" && result.Checks["request_scope"].Status != "unknown" {
				t.Fatal("unproven mock context accepted")
			}
			if tc.name == "forwarded-blank" && result.Checks["request_scope"].Status != "pass" {
				t.Fatal("bounded variant not proved")
			}
			if tc.name == "forwarded-blank" {
				// 单独执行可信宿主探针，不把新增文件注入候选的质量检查。
				probeRoot := filepath.Join(root, "mock-probe")
				if err := copyFiles(probeRoot, fixtures); err != nil {
					t.Fatal(err)
				}
				if err := copyFiles(filepath.Join(probeRoot, "goddd"), candidate); err != nil {
					t.Fatal(err)
				}
				if err := write(filepath.Join(probeRoot, "goddd/mock/forwarding_test.go"), []byte(mockContextProbe)); err != nil {
					t.Fatal(err)
				}
				prepared, err := PrepareBackend(ctx, filepath.Join(root, "probe-modules"), backend)
				if err != nil {
					t.Fatal(err)
				}
				req := execution.Request{Args: []string{"go", "test", "-json", "-count=1", "-run=^TestForwardedContext$", "./mock"}, Directory: filepath.Join(probeRoot, "goddd"), ReadOnlyDirs: []string{filepath.Join(probeRoot, "goddd"), filepath.Join(probeRoot, "rpcx-v1"), filepath.Join(probeRoot, "rpcx-v2")}, CacheDir: filepath.Join(root, "probe-cache"), MaxBytes: 1 << 20}
				r, err := prepared.Run(ctx, req)
				if saveErr := writeJSON(filepath.Join(root, "mock-probe-execution.json"), r); saveErr != nil {
					t.Fatal(saveErr)
				}
				if err != nil || r.ExitCode != 0 {
					t.Fatalf("mock propagation probe: %v exit=%d %s %s", err, r.ExitCode, r.Stdout, r.Stderr)
				}
				if err := prepared.Check(ctx); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

const mockContextProbe = `package mock
import (
 "context"
 "reflect"
 "testing"
 shipping "github.com/marcusolsson/goddd"
)
func TestForwardedContext(t *testing.T) {
 type key struct{}
 for _,cancelled:=range []bool{false,true} {
  ctx,cancel:=context.WithCancel(context.WithValue(context.Background(),key{},"value"))
  if cancelled {cancel()}
  rs:=shipping.RouteSpecification{Origin:shipping.SESTO,Destination:shipping.AUMEL}
  routes:=[]shipping.Itinerary{{Legs:[]shipping.Leg{{LoadLocation:shipping.SESTO}}}}
  calls:=0
  mock:=RoutingService{}
  mock.FetchRoutesFn=func(actual context.Context,request shipping.RouteSpecification) []shipping.Itinerary {
   calls++
   if actual!=ctx || actual.Value(key{})!="value" || (actual.Err()!=nil)!=cancelled || !reflect.DeepEqual(request,rs) || !mock.FetchRoutesInvoked {t.Fatal("callback arguments or invocation flag changed")}
   return routes
  }
  got:=mock.FetchRoutesForSpecification(ctx,rs)
  if calls!=1 || !reflect.DeepEqual(got,routes) {t.Fatal("callback result or call count changed")}
  cancel()
 }
}
`
