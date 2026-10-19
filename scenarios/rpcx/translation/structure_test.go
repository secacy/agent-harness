package translation

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/testintegrity"
)

func controlCandidate(t *testing.T, id string) map[string][]byte {
	t.Helper()
	fixtures, err := LoadFixtures("../l2/data/rpcx-l2-translation-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err = gate.CopyFiles(root, gate.Module(fixtures, "translation")); err != nil {
		t.Fatal(err)
	}
	patch, err := filepath.Abs("cases/patches/" + id + ".patch")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(context.Background(), "git", "apply", patch)
	cmd.Dir = root
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("replay calibration: %v: %s", err, data)
	}
	files, err := gate.ReadFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	return files
}
func TestStructureCalibration(t *testing.T) {
	fixtures, err := LoadFixtures("../l2/data/rpcx-l2-translation-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	base := gate.Module(fixtures, "translation")
	adapted, err := adapt(gate.TestFiles(base))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"correct", "alternate-missing-timeout", "mapping-helper", "deleted-assertion"} {
		t.Run(id, func(t *testing.T) {
			candidate := controlCandidate(t, id)
			integrity, err := testintegrity.CompareAdapted(gate.TestFiles(base), adapted, gate.TestFiles(candidate), testintegrity.Plan{Version: "test", SpecSHA256: SpecSHA256(), MaterialSHA256: SpecSHA256(), BaselineFiles: gate.Hashes(gate.TestFiles(base))})
			if err != nil {
				t.Fatal(err)
			}
			want := "preserved"
			if id == "deleted-assertion" {
				want = "violated"
			}
			if integrity.Status != want {
				t.Fatalf("integrity=%s findings=%+v", integrity.Status, integrity.Findings)
			}
			wantScope := quality.StatusPass
			if id == "mapping-helper" {
				wantScope = quality.StatusUnknown
			}
			if result := scope(base, candidate); result.Status != wantScope || result.Findings[0].Code != "request_scope_"+wantScope {
				t.Fatalf("scope=%+v", result)
			}
		})
	}
}
