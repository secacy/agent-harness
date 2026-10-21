package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2"
)

// 此处候选只验证接口装配；模拟执行不声称业务迁移正确。
func ghcliInterfaceRequest(t *testing.T) Request {
	t.Helper()
	fixtures, e := filepath.Abs("../data/rpcx-l2-ghcli-v1/fixtures")
	if e != nil {
		t.Fatal(e)
	}
	files, e := gate.ReadFiles(filepath.Join(fixtures, "ghcli"))
	if e != nil {
		t.Fatal(e)
	}
	files[ghcliDir+"/outbound.go"] = []byte(strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(string(files[ghcliDir+"/outbound.go"]), `"example.com/rpcx"`, `"example.com/rpcx/v2"`), "rpcx.NewClient(", "rpcx.Dial("), `caller.Call("http.do",`, `caller.Call(request.Context(), "http.do",`))
	files["go.mod"] = []byte(strings.ReplaceAll(strings.ReplaceAll(string(files["go.mod"]), "example.com/rpcx v1.0.0", "example.com/rpcx/v2 v2.0.0"), "replace example.com/rpcx => ../rpcx-v1", "replace example.com/rpcx/v2 => ../rpcx-v2"))
	root := t.TempDir()
	candidate := filepath.Join(root, "candidate")
	if e = gate.CopyFiles(candidate, files); e != nil {
		t.Fatal(e)
	}
	spec, e := filepath.Abs("../data/rpcx-l2-ghcli-v1/specs/v0.6.0.md")
	if e != nil {
		t.Fatal(e)
	}
	return Request{TargetID: "arbitrary-target", RunID: "interface-run", FixturesDir: fixtures, CandidateDir: candidate, CandidateSHA256: gate.TreeHash(files), SpecPath: spec, EvidenceDir: filepath.Join(root, "evidence"), Backend: ghcliFake(t), TrustedLocal: true}
}

func ghcliFake(t *testing.T) fakeExecutor {
	t.Helper()
	host, e := l2.GHCLIMaterials()
	if e != nil {
		t.Fatal(e)
	}
	var inventory struct {
		Modules map[string]testInventory `json:"modules"`
	}
	if e = json.Unmarshal(host["tests.json"], &inventory); e != nil {
		t.Fatal(e)
	}
	tests := inventory.Modules["ghcli"]
	return fakeExecutor{run: func(_ context.Context, req execution.Request) (execution.Result, error) {
		out := execution.Result{}
		if slices.Contains(req.Args, "edit") {
			if strings.Contains(req.Directory, "/baseline/") {
				out.Stdout = []byte(`{"Require":[{"Path":"example.com/rpcx","Version":"v1.0.0"}],"Replace":[{"Old":{"Path":"example.com/rpcx"},"New":{"Path":"../rpcx-v1"}}]}`)
			} else {
				out.Stdout = []byte(`{"Require":[{"Path":"example.com/rpcx/v2","Version":"v2.0.0"}],"Replace":[{"Old":{"Path":"example.com/rpcx/v2"},"New":{"Path":"../rpcx-v2"}}]}`)
			}
		}
		if slices.Contains(req.Args, "test") {
			required := tests.Original
			packages := tests.Packages
			if strings.Contains(req.Directory, "/production/") {
				required = ghcliBehaviorTests()
				if slices.Contains(req.Args, "-run=^TestReferenceActualDefaultExpiry$") {
					required = []string{ghcliPackage + "/TestReferenceActualDefaultExpiry"}
				}
				if slices.Contains(req.Args, "-run=^TestReferenceCommandDefaultExpiry$") {
					required = []string{ghcliPackage + "/TestReferenceCommandDefaultExpiry"}
				}
			}
			out.Stdout = passedEvents(required, packages)
			if !strings.Contains(req.Directory, "/production/") {
				for _, pkg := range tests.Empty {
					b, _ := json.Marshal(map[string]string{"Package": pkg, "Action": "skip"})
					out.Stdout = append(out.Stdout, append(b, '\n')...)
				}
			}
		}
		return out, nil
	}}
}

func TestGHCLIReferenceInputBoundary(t *testing.T) {
	for _, mode := range []string{"target", "run", "digest", "spec", "fixtures", "overlap", "existing", "untrusted", "environment", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			req := ghcliInterfaceRequest(t)
			calls := 0
			req.Backend = fakeExecutor{run: func(context.Context, execution.Request) (execution.Result, error) {
				calls++
				return execution.Result{}, errors.New("unexpected execution")
			}}
			ctx := context.Background()
			switch mode {
			case "target":
				req.TargetID = "../target"
			case "run":
				req.RunID = ""
			case "digest":
				req.CandidateSHA256 = strings.Repeat("0", 64)
			case "spec":
				req.SpecPath = filepath.Join(t.TempDir(), "spec.md")
				os.WriteFile(req.SpecPath, []byte("wrong"), 0644)
			case "fixtures":
				req.FixturesDir = t.TempDir()
			case "overlap":
				req.EvidenceDir = filepath.Join(req.CandidateDir, "evidence")
			case "existing":
				os.Mkdir(req.EvidenceDir, 0755)
			case "untrusted":
				req.TrustedLocal = false
			case "environment":
				os.WriteFile(filepath.Join(req.CandidateDir, ".env"), []byte("not-read"), 0644)
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, e := EvaluateGHCLI(ctx, req); e == nil {
				t.Fatal("invalid input accepted")
			}
			if calls != 0 {
				t.Fatal("execution before input authentication")
			}
		})
	}
}

func TestGHCLIReferenceExecutionBinding(t *testing.T) {
	for _, mode := range []string{"pass", "backend-error", "candidate-drift", "copy-drift", "log-drift", "dependency-artifact-drift"} {
		t.Run(mode, func(t *testing.T) {
			req := ghcliInterfaceRequest(t)
			base := req.Backend
			calls := 0
			productionChecked := false
			if mode == "protected" {
				os.WriteFile(filepath.Join(req.CandidateDir, "LICENSE"), []byte("changed"), 0644)
				f, _ := gate.ReadFiles(req.CandidateDir)
				req.CandidateSHA256 = gate.TreeHash(f)
			}
			req.Backend = fakeExecutor{run: func(ctx context.Context, r execution.Request) (execution.Result, error) {
				calls++
				if len(r.ReadOnlyDirs) != 4 || !slices.Contains(r.Args, "env") || !strings.Contains(strings.Join(r.Args, " "), "GOMODCACHE=") {
					t.Error("bounded dependency backend not assembled")
				}
				if strings.Contains(r.Directory, "/production/") {
					productionChecked = true
					f, e := gate.ReadFiles(r.Directory)
					if e != nil {
						t.Fatal(e)
					}
					for n := range gate.TestFiles(f) {
						if n != ghcliDir+"/reference_test.go" && n != ghcliDir+"/reference_command_expiry_test.go" {
							t.Fatal("candidate tests entered trusted production copy")
						}
					}
				}
				if mode == "backend-error" {
					return execution.Result{ExitCode: -1}, context.DeadlineExceeded
				}
				if calls == 1 {
					switch mode {
					case "candidate-drift":
						os.WriteFile(filepath.Join(req.CandidateDir, "LICENSE"), []byte("drift"), 0644)
					case "copy-drift":
						os.WriteFile(filepath.Join(req.EvidenceDir, "production", "ghcli", "LICENSE"), []byte("drift"), 0644)
					}
				}
				if calls == 2 && mode == "dependency-artifact-drift" {
					os.WriteFile(filepath.Join(req.EvidenceDir, "modules.bundle/bundle.json"), []byte("drift"), 0644)
				}
				if calls == 2 && mode == "log-drift" {
					os.WriteFile(filepath.Join(req.EvidenceDir, "logs/go-version.stdout.log"), []byte("drift"), 0644)
				}
				out, e := base.Run(ctx, r)
				if mode == "dependency" && slices.Contains(r.Args, "edit") && !strings.Contains(r.Directory, "/baseline/") {
					out.Stdout = []byte(`{}`)
				}
				if mode == "candidate-build" && slices.Contains(r.Args, "build") && strings.Contains(r.Directory, "/candidate/") {
					out.ExitCode = 1
				}
				if strings.Contains(r.Directory, "/production/") && slices.Contains(r.Args, "test") {
					switch mode {
					case "incomplete-events":
						out.Stdout = nil
					case "assertion-failure":
						out.ExitCode = 1
						out.Stdout = []byte(`{"Action":"run","Package":"` + ghcliPackage + `","Test":"TestReferenceMappingAndRelease"}` + "\n" + `{"Action":"fail","Package":"` + ghcliPackage + `","Test":"TestReferenceMappingAndRelease"}` + "\n")
					case "watchdog":
						out.ExitCode = 1
						out.Stdout = []byte("panic: test timed out")
					}
				}
				return out, e
			}}
			report, e := EvaluateGHCLI(context.Background(), req)
			want := Inconclusive
			if mode == "pass" {
				want = Pass
			}
			if slices.Contains([]string{"protected", "dependency", "candidate-build", "assertion-failure"}, mode) {
				want = Fail
			}
			if report.Automatic != want {
				t.Fatalf("status %s want %s: %v", report.Automatic, want, e)
			}
			if mode == "pass" {
				ghcliAssertStandardArtifacts(t, req.EvidenceDir, report)
				if e != nil || !productionChecked || len(report.Commands) != 11 || report.ProductionSHA256 == report.CandidateSHA256 || len(report.SDKs) != 2 || report.HostMaterialsSHA256 == "" {
					t.Fatalf("incomplete interface binding: %d %v", len(report.Commands), e)
				}
				identity, e := PublishedIdentity(GHCLIDataVersion)
				if e != nil || identity.ReferenceSHA256 != report.ReferenceSHA256 || identity.ToolSHA256 != report.ToolSHA256 {
					t.Fatal("publication mismatch")
				}
				reference, e := os.ReadFile(filepath.Join(req.EvidenceDir, "reference.json"))
				if e != nil {
					t.Fatal(e)
				}
				review, e := os.ReadFile(filepath.Join(req.EvidenceDir, "review-template.json"))
				if e != nil {
					t.Fatal(e)
				}
				if assessment, e := Assess(reference, review); e != nil || assessment.Status != Inconclusive {
					t.Fatalf("review template: %v", e)
				}
				for _, field := range []string{"reference", "host", "sdk", "production", "schema"} {
					tampered := report
					tampered.SDKs = map[string]string{}
					for k, v := range report.SDKs {
						tampered.SDKs[k] = v
					}
					switch field {
					case "reference":
						tampered.ReferenceSHA256 = strings.Repeat("0", 64)
					case "host":
						tampered.HostMaterialsSHA256 = ""
					case "sdk":
						tampered.SDKs["rpcx-v2"] = ""
					case "production":
						tampered.ProductionSHA256 = ""
					case "schema":
						tampered.SchemaVersion = 2
					}
					b, _ := json.Marshal(tampered)
					r := reviewTemplate(tampered, gate.Digest(b))
					rb, _ := json.Marshal(r)
					if _, e := Assess(b, rb); e == nil {
						t.Fatal("unbound report accepted")
					}
				}
			}
		})
	}
}

func TestGHCLIReferenceDecisionBoundaries(t *testing.T) {
	for _, mode := range []string{"missing", "failure", "watchdog", "invalid-json", "skip"} {
		t.Run(mode, func(t *testing.T) {
			data := passedEvents(ghcliBehaviorTests(), []string{ghcliPackage})
			exit := 0
			want := Inconclusive
			switch mode {
			case "missing":
				data = nil
			case "failure":
				data = []byte(`{"Action":"run","Package":"` + ghcliPackage + `","Test":"TestReferenceMappingAndRelease"}` + "\n" + `{"Action":"fail","Package":"` + ghcliPackage + `","Test":"TestReferenceMappingAndRelease"}` + "\n")
				exit = 1
				want = Fail
			case "watchdog":
				data = []byte("panic: test timed out")
				exit = 1
			case "invalid-json":
				data = []byte("unexpected output")
			case "skip":
				data = []byte(strings.ReplaceAll(string(data), `"Action":"pass"`, `"Action":"skip"`))
			}
			if got := ghcliObserve(data, exit, ghcliBehaviorTests()); got.Status != want {
				t.Fatalf("status %s want %s", got.Status, want)
			}
		})
	}
	base := map[string][]byte{"LICENSE": {}, "go.mod": []byte("baseline")}
	if ghcliProtected(base, map[string][]byte{"go.mod": []byte("migration")}).Status != Fail {
		t.Fatal("missing empty protected file accepted")
	}
	if ghcliProtected(base, map[string][]byte{"LICENSE": {}, "go.mod": []byte("migration"), "extra.go": []byte("package extra")}).Status != Fail {
		t.Fatal("extra production source accepted")
	}
	if ghcliContract(map[string][]byte{"main.go": []byte(`package main; import rpcx "example.com/rpcx";func f(){rpcx.NewClient("")}`)}).Status != Fail {
		t.Fatal("old SDK contract accepted")
	}
	before := []byte(`{"Require":[{"Path":"example.com/rpcx","Version":"v1.0.0"}],"Replace":[{"Old":{"Path":"example.com/rpcx"},"New":{"Path":"../rpcx-v1"}}]}`)
	for _, after := range []string{`{}`, string(before), `{"Require":[{"Path":"example.com/rpcx/v2","Version":"v2.0.0"}],"Replace":[{"Old":{"Path":"example.com/rpcx/v2"},"New":{"Path":"../../outside"}}]}`} {
		if ghcliDependencyObservation(before, []byte(after), 0).Status != Fail {
			t.Fatal("unexpected module contract accepted")
		}
	}
}

func TestGHCLIReferenceGoModEncoding(t *testing.T) {
	root := t.TempDir()
	read := func(module string) []byte {
		t.Helper()
		if e := os.WriteFile(filepath.Join(root, "go.mod"), []byte(module), 0644); e != nil {
			t.Fatal(e)
		}
		cmd := exec.Command("go", "mod", "edit", "-json")
		cmd.Dir = root
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "GOCACHE=" + filepath.Join(root, "cache")}
		b, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("go mod edit: %v %s", e, b)
		}
		return b
	}
	before := read("module example.com/business\ngo 1.23.0\nrequire example.com/rpcx v1.0.0\nreplace example.com/rpcx => ../rpcx-v1\n")
	after := read("module example.com/business\ngo 1.23.0\nrequire example.com/rpcx/v2 v2.0.0\nreplace example.com/rpcx/v2 => ../rpcx-v2\n")
	if got := ghcliDependencyObservation(before, after, 0); got.Status != Pass {
		t.Fatalf("actual Go module encoding rejected: %+v", got)
	}
}

// 检查实际装配结果及通用消费者所需的稳定相对路径，而非只核对汇总摘要。
func ghcliAssertStandardArtifacts(t *testing.T, evidence string, report Report) {
	t.Helper()
	spec, err := os.ReadFile(filepath.Join(evidence, "materials/spec.md"))
	if err != nil {
		t.Fatalf("standard specification artifact missing: %v", err)
	}
	if gate.Digest(spec) != report.SpecSHA256 || report.Artifacts["materials/spec.md"] != report.SpecSHA256 {
		t.Fatal("standard specification artifact identity missing")
	}
	if _, err = os.Stat(filepath.Join(evidence, "materials/spec/spec.md")); !os.IsNotExist(err) {
		t.Fatal("nonstandard specification artifact retained")
	}
	host, err := l2.GHCLIMaterials()
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string][]byte{"modules.json": host["dependencies/modules.json"], "modules.bundle/bundle.json": host["dependencies/bundle.json"]}
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("modules-%03d.tar.gz.part", i)
		expected["modules.bundle/"+name] = host["dependencies/"+name]
	}
	if len(expected) != 7 {
		t.Fatal("incomplete host dependency references")
	}
	actual := 0
	for name, hash := range report.Artifacts {
		if name == "modules.json" || strings.HasPrefix(name, "modules.bundle/") {
			actual++
			want, ok := expected[name]
			if !ok || len(want) == 0 || hash != gate.Digest(want) {
				t.Fatalf("unexpected dependency reference: %s", name)
			}
			data, e := os.ReadFile(filepath.Join(evidence, filepath.FromSlash(name)))
			if e != nil || gate.Digest(data) != hash {
				t.Fatalf("dependency reference does not bind actual archive: %s %v", name, e)
			}
		}
	}
	if actual != 7 {
		t.Fatalf("dependency reference count %d want 7", actual)
	}
}

func TestGHCLIReferencePublishedScope(t *testing.T) {
	data, err := ghcliData()
	if err != nil {
		t.Fatal(err)
	}
	if GHCLIVersion != "rpcx-l2-reference-ghcli-v2" || GHCLIDataVersion != "rpcx-l2-reference-ghcli-data-v2" {
		t.Fatal("active reference version not advanced")
	}
	old, err := loadReferenceData("data/ghcli-v1", "rpcx-l2-reference-ghcli-data-v1", "3a4891984417a8bb82c1ad2f69693f019b982d1c5c123e1cbd5efd3f28c513a0", ghcliSpecSHA256)
	if err != nil || len(old) != 5 {
		t.Fatal("prior reference archive changed")
	}
	if _, err = PublishedIdentity("rpcx-l2-reference-ghcli-data-v1"); err == nil {
		t.Fatal("current API claims to rerun archived reference version")
	}
	file, err := parser.ParseFile(token.NewFileSet(), "reference.go", data["probes_test.go.txt"], 0)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	diagnosticCalls := 0
	for _, decl := range file.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		found[function.Name.Name] = true
		if function.Name.Name != "TestReferenceCommandOrderAndStop" {
			continue
		}
		ast.Inspect(function, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			literal, ok := call.Args[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			message, e := strconv.Unquote(literal.Value)
			if e != nil {
				return true
			}
			if strings.Contains(message, "DIAGNOSTIC_UNPUBLISHED_UPSTREAM_CONTEXT") {
				if selector.Sel.Name != "Logf" {
					t.Error("upstream context diagnostic can affect acceptance")
				}
				diagnosticCalls++
			}
			if strings.Contains(message, "command parent context lost") {
				t.Error("unpublished upstream context requirement retained")
			}
			return true
		})
	}
	if diagnosticCalls != 1 {
		t.Fatal("unpublished condition not retained as diagnostic")
	}
	for _, name := range []string{"TestReferenceMappingAndRelease", "TestReferenceParentContexts", "TestReferenceResponseBodyParentCancellation", "TestReferenceLongParentCeiling", "TestReferenceFreshPerCallCeilings", "TestReferenceCommandOrderAndStop", "TestReferenceActualDefaultExpiry"} {
		if !found[name] {
			t.Fatal("required published boundary observation absent")
		}
	}
	required := ghcliBehaviorTests()
	for _, test := range []string{"ResponseBodyParentCancellation/cancel", "ResponseBodyParentCancellation/short", "LongParentCeiling"} {
		if !slices.Contains(required, ghcliPackage+"/TestReference"+test) {
			t.Fatal("published boundary observation omitted from required events")
		}
	}
	var protocol struct {
		ScopeAuthority      string   `json:"scope_authority"`
		AutomaticExclusions []string `json:"automatic_exclusions"`
	}
	if err = json.Unmarshal(data["protocol.json"], &protocol); err != nil || protocol.ScopeAuthority == "" || len(protocol.AutomaticExclusions) != 1 {
		t.Fatal("published scope authority not recorded")
	}
}
