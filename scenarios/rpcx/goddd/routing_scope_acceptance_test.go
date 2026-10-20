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

// 完整候选验证与构造器探针显式执行，常规回归只验证结构机制。
func TestGodddRoutingScopeAcceptance(t *testing.T) {
	if os.Getenv("HARNESS_GODDD_ACCEPTANCE") != "1" {
		t.Skip("set HARNESS_GODDD_ACCEPTANCE=1 for routing scope calibration")
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
	if err := os.MkdirAll(output, 0755); err != nil {
		t.Fatal(err)
	}
	fixtures, err := LoadFixtures("../l2/data/rpcx-l2-goddd-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, want, scope string }{
		{"named-and-read", "ReadyForReview", "pass"},
		{"wrong-named-duration", "Rejected", "unknown"},
		{"extra-unused-constant", "NeedsAttention", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := os.MkdirTemp(output, tc.name+"-")
			if err != nil {
				t.Fatal(err)
			}
			t.Log("artifacts:", root)
			candidate := routingVariant(t, true, true)
			switch tc.name {
			case "wrong-named-duration":
				candidate[routingPath] = changeSource(t, candidate[routingPath], "30 * time.Second", "5 * time.Second")
			case "extra-unused-constant":
				candidate[routingPath] = append(candidate[routingPath], []byte("\nconst extraTimeout = 1\n")...)
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
			if result.Verdict != tc.want || result.Checks["request_scope"].Status != tc.scope || result.Checks["test_preservation"].Status != "pass" {
				t.Fatalf("unexpected result: %+v", result)
			}
			if tc.name == "extra-unused-constant" && result.Checks["semantic_behavior"].Status != "pass" {
				t.Fatal("unknown control must pass behavior")
			}
			if tc.name == "wrong-named-duration" && result.Checks["semantic_behavior"].Status != "fail" {
				t.Fatal("wrong duration escaped behavior checks")
			}
			if tc.name == "named-and-read" {
				runConstructorProbe(t, ctx, root, fixtures, candidate, backend)
			}
		})
	}
}

func runConstructorProbe(t *testing.T, ctx context.Context, root string, fixtures, candidate map[string][]byte, backend execution.Executor) {
	t.Helper()
	probeRoot := filepath.Join(root, "constructor-probe")
	if err := copyFiles(probeRoot, fixtures); err != nil {
		t.Fatal(err)
	}
	if err := copyFiles(filepath.Join(probeRoot, "goddd"), candidate); err != nil {
		t.Fatal(err)
	}
	if err := write(filepath.Join(probeRoot, "goddd/routing/constructor_test.go"), []byte(constructorContextProbe)); err != nil {
		t.Fatal(err)
	}
	prepared, err := PrepareBackend(ctx, filepath.Join(root, "probe-modules"), backend)
	if err != nil {
		t.Fatal(err)
	}
	req := execution.Request{Args: []string{"go", "test", "-json", "-count=1", "-run=^TestConstructorCompatibility$", "./routing"}, Directory: filepath.Join(probeRoot, "goddd"), ReadOnlyDirs: []string{filepath.Join(probeRoot, "goddd"), filepath.Join(probeRoot, "rpcx-v1"), filepath.Join(probeRoot, "rpcx-v2")}, CacheDir: filepath.Join(root, "probe-cache"), MaxBytes: 1 << 20}
	r, err := prepared.Run(ctx, req)
	if saveErr := writeJSON(filepath.Join(root, "constructor-probe-execution.json"), r); saveErr != nil {
		t.Fatal(saveErr)
	}
	if err != nil || r.ExitCode != 0 {
		t.Fatalf("constructor probe: %v exit=%d %s %s", err, r.ExitCode, r.Stdout, r.Stderr)
	}
	if err := prepared.Check(ctx); err != nil {
		t.Fatal(err)
	}
}

const constructorContextProbe = `package routing_test
import (
 "context"
 "testing"
 "time"
 "example.com/rpcx/v2"
 shipping "github.com/marcusolsson/goddd"
 "github.com/marcusolsson/goddd/routing"
)
func TestConstructorCompatibility(t *testing.T) {
 type key struct{}
 cancelled,release:=context.WithCancel(context.Background());release()
 for name,constructor:=range map[string]context.Context{"nil":nil,"cancelled":cancelled,"valued":context.WithValue(context.Background(),key{},"constructor")} {
  t.Run(name,func(t *testing.T){
   calls:=0
   var observed context.Context
   started:=time.Now()
   t.Cleanup(rpcx.Register(t.Name(),func(ctx context.Context,method string,request,response any) error {
    calls++;observed=ctx
    deadline,ok:=ctx.Deadline()
    if ctx.Value(key{})!="request" || ctx.Err()!=nil || !ok || deadline.Before(started.Add(30*time.Second)) || deadline.After(time.Now().Add(30*time.Second)) {t.Fatal("constructor context replaced the request context or timeout")}
    if method!="fetch_routes" || request!=(routing.FetchRoutesRequest{From:"SESTO",To:"AUMEL"}) {t.Fatal("RPC arguments changed")}
    return nil
   }))
   service:=routing.NewProxyingMiddleware(constructor,t.Name())(nil)
   request:=context.WithValue(context.Background(),key{},"request")
   service.FetchRoutesForSpecification(request,shipping.RouteSpecification{Origin:shipping.SESTO,Destination:shipping.AUMEL})
   if calls!=1 || observed==nil || observed.Err()!=context.Canceled {t.Fatal("request was not called once and released")}
   stopped,stop:=context.WithCancel(request);stop()
   service.FetchRoutesForSpecification(stopped,shipping.RouteSpecification{})
   if calls!=1 {t.Fatal("cancelled request reached backend")}
  })
 }
}
`
