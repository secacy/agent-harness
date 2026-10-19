package goddd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/testintegrity"
)

func controlCandidate(t *testing.T, id string) map[string][]byte {
	t.Helper()
	fixtures, err := LoadFixtures("../l2/data/rpcx-l2-goddd-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := copyFiles(root, module(fixtures, "goddd")); err != nil {
		t.Fatal(err)
	}
	patch, err := filepath.Abs("cases/patches/" + id + ".patch")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(context.Background(), "git", "apply", patch)
	cmd.Dir = root
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("replay control: %v: %s", err, data)
	}
	files, err := readFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestGodddAdaptationAndScope(t *testing.T) {
	fixtures, err := LoadFixtures("../l2/data/rpcx-l2-goddd-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	base := module(fixtures, "goddd")
	if digest(Spec()) != SpecificationSHA256 {
		t.Fatal("published specification changed")
	}
	adapted, err := adapt(testFiles(base), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"correct", "missing-timeout", "mapping-helper", "deleted-assertion"} {
		t.Run(id, func(t *testing.T) {
			candidate := controlCandidate(t, id)
			report, err := testintegrity.CompareAdapted(testFiles(base), adapted, testFiles(candidate), testintegrity.Plan{Version: "test", SpecSHA256: SpecSHA256(), MaterialSHA256: SpecSHA256(), BaselineFiles: hashes(testFiles(base))})
			if err != nil {
				t.Fatal(err)
			}
			want := "preserved"
			if id == "deleted-assertion" {
				want = "violated"
			}
			if report.Status != want {
				t.Fatalf("test integrity=%s findings=%+v", report.Status, report.Findings)
			}
			scope := requestScope(base, candidate)
			status := quality.StatusPass
			if id == "mapping-helper" {
				status = quality.StatusUnknown
			}
			if scope.Status != status {
				t.Fatalf("scope=%+v", scope)
			}
		})
	}
}

func TestModuleDeclarations(t *testing.T) {
	fixtures, err := LoadFixtures("../l2/data/rpcx-l2-goddd-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	base := module(fixtures, "goddd")
	correct := controlCandidate(t, "correct")
	a, err := modEntries(migratedMod(base["go.mod"]))
	if err != nil {
		t.Fatal(err)
	}
	b, err := modEntries(correct["go.mod"])
	if err != nil {
		t.Fatal(err)
	}
	quoted, err := modEntries([]byte(strings.ReplaceAll(string(correct["go.mod"]), "../rpcx-v2", `"../rpcx-v2"`)))
	if err != nil || !reflect.DeepEqual(quoted, b) {
		t.Fatal("equivalent quoted module path differs", err)
	}
	if len(a) != len(b) {
		t.Fatal("dependency inventory differs")
	}
	for key, value := range a {
		if b[key] != value {
			t.Fatalf("dependency changed: %s", key)
		}
	}
	if _, err := modEntries([]byte("module x\nreplace a => ../a\nreplace a => ../b\n")); err == nil {
		t.Fatal("duplicate replacement accepted")
	}
}

func TestPlanPinsBaseline(t *testing.T) {
	fixtures, err := LoadFixtures("../l2/data/rpcx-l2-goddd-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	base := testFiles(module(fixtures, "goddd"))
	adapted, err := adapt(base, true)
	if err != nil {
		t.Fatal(err)
	}
	base["cargo_test.go"] = []byte("package shipping\n")
	_, err = testintegrity.CompareAdapted(base, adapted, adapted, testintegrity.Plan{Version: "test", SpecSHA256: SpecSHA256(), MaterialSHA256: SpecSHA256(), BaselineFiles: hashes(testFiles(module(fixtures, "goddd")))})
	if err == nil {
		t.Fatal("unregistered baseline accepted")
	}
}

func TestFixtureDataIsIndependent(t *testing.T) {
	a, err := LoadFixtures("../l2/data/rpcx-l2-goddd-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	a["goddd/go.mod"][0] = '!'
	b, err := LoadFixtures("../l2/data/rpcx-l2-goddd-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	if b["goddd/go.mod"][0] == '!' {
		t.Fatal("caller mutated shared fixture material")
	}
	if _, err := os.Stat("../l2/data/rpcx-l2-goddd-v1/freeze.json"); err != nil {
		t.Fatal(err)
	}
}

// 同名 mock 方法不属于已证明的 RPC 期限前缀，不能借用 routing 的屏蔽规则。
func TestTimeoutPrefixIsScopedToRPCBoundary(t *testing.T) {
	fixtures, err := LoadFixtures("../l2/data/rpcx-l2-goddd-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	candidate := controlCandidate(t, "correct")
	candidate["mock/mock.go"] = []byte(strings.Replace(string(candidate["mock/mock.go"]), " []shipping.Itinerary {\n", " []shipping.Itinerary {\n ctx,cancel:=context.WithTimeout(ctx,1); defer cancel()\n", 1))
	if requestScope(module(fixtures, "goddd"), candidate).Status != quality.StatusUnknown {
		t.Fatal("unmodeled mock change accepted")
	}
}
