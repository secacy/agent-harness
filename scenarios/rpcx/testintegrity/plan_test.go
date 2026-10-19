package testintegrity

import (
	"strings"
	"testing"
)

func TestHostPlanPreservesSharedTestData(t *testing.T) {
	source := `package sample
 import "testing"
 var cases = []int{1,2}
 type fixture struct {value int}
 func TestTable(t *testing.T) { if len(cases)!=2 { t.Fatal("cases") } }
 `
	base := map[string][]byte{"table_test.go": []byte(source)}
	binding, err := bind(base)
	if err != nil {
		t.Fatal(err)
	}
	plan := Plan{Version: "host-v1", SpecSHA256: strings.Repeat("a", 64), MaterialSHA256: strings.Repeat("b", 64), BaselineFiles: binding.Files}
	for _, c := range []struct{ name, source, status string }{
		{"unchanged", source, "preserved"},
		{"table-value", strings.Replace(source, "[]int{1,2}", "[]int{1,3}", 1), "unknown"},
		{"table-removed", strings.Replace(source, "var cases = []int{1,2}", "", 1), "unknown"},
		{"new-init", source + "\nfunc init() {cases=nil}\n", "unknown"},
		{"deleted-test", strings.Replace(source, `func TestTable(t *testing.T) { if len(cases)!=2 { t.Fatal("cases") } }`, "", 1), "violated"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, err := CompareAdapted(base, base, map[string][]byte{"table_test.go": []byte(c.source)}, plan)
			if err != nil || r.Status != c.status {
				t.Fatalf("%+v %v", r, err)
			}
		})
	}
	plan.BaselineFiles = map[string]string{"table_test.go": strings.Repeat("c", 64)}
	if _, err := CompareAdapted(base, base, base, plan); err == nil {
		t.Fatal("unbound host baseline accepted")
	}
}
