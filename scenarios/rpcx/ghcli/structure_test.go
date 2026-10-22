package ghcli

import (
	"context"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"os"
	"path/filepath"
	"testing"
)

type noExecution struct{}

func (noExecution) Info() execution.Info { return execution.Info{Kind: execution.KindDocker} }
func (noExecution) Run(context.Context, execution.Request) (execution.Result, error) {
	panic("invalid inputs must not execute")
}

func baselineFixture(t *testing.T) map[string][]byte {
	t.Helper()
	f, err := LoadFixtures("../l2/data/rpcx-l2-ghcli-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestScopeProtectedAndUnsupported(t *testing.T) {
	files := gate.Module(baselineFixture(t), "ghcli")
	copyFiles := func() map[string][]byte {
		o := map[string][]byte{}
		for n, b := range files {
			o[n] = append([]byte{}, b...)
		}
		return o
	}
	if c := scope(files, copyFiles()); c.Status != quality.StatusPass {
		t.Fatal(c)
	}
	changed := copyFiles()
	changed["LICENSE"] = []byte("changed")
	if c := scope(files, changed); c.Status != quality.StatusFail {
		t.Fatal(c)
	}
	added := copyFiles()
	added["helper.go"] = []byte("package example\n")
	if c := scope(files, added); c.Status != quality.StatusUnknown {
		t.Fatal(c)
	}
	for _, prefix := range []string{"//go:noinline\n", "//line changed.go:1\n"} {
		directive := copyFiles()
		directive[boundaryFile] = append([]byte(prefix), directive[boundaryFile]...)
		outside := copyFiles()
		outside[boundaryFile] = directive[boundaryFile]
		outside["LICENSE"] = []byte("changed")
		if c := scope(files, outside); c.Status != quality.StatusFail {
			t.Fatal("directive hid an explicit scope violation", c)
		}
		if c := scope(files, directive); c.Status != quality.StatusUnknown {
			t.Fatal("semantic directive erased", c)
		}
	}
	proof := scopeProof{Status: "pass", BaselineSHA256: gate.Digest(files[boundaryFile]), CandidateSHA256: gate.Digest(files[boundaryFile])}
	if c := scope(files, added, proof); c.Status != quality.StatusUnknown {
		t.Fatal("proof hid additional file", c)
	}
	if c := scope(files, changed, proof); c.Status != quality.StatusFail {
		t.Fatal("proof hid protected change", c)
	}
	unsupported := copyFiles()
	unsupported[boundaryFile] = append(unsupported[boundaryFile], []byte("\nfunc unprovedHelper(){}\n")...)
	if c := scope(files, unsupported); c.Status != quality.StatusUnknown {
		t.Fatal(c)
	}
}

func TestBindingAndPathRejectionsBeforeExecution(t *testing.T) {
	fixtures := baselineFixture(t)
	for _, mode := range []string{"spec", "baseline", "overlap", "evidence-overlap", "cache-overlap", "sdk"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			base := filepath.Join(dir, "base")
			candidate := filepath.Join(dir, "candidate")
			if err := gate.CopyFiles(base, fixtures); err != nil {
				t.Fatal(err)
			}
			if err := gate.CopyFiles(candidate, fixtures); err != nil {
				t.Fatal(err)
			}
			spec := filepath.Join(dir, "spec.md")
			if err := os.WriteFile(spec, Spec(), 0644); err != nil {
				t.Fatal(err)
			}
			req := checks.EvaluationRequest{BaselineDir: filepath.Join(base, "ghcli"), CandidateDir: filepath.Join(candidate, "ghcli"), SpecPath: spec, EvidenceDir: filepath.Join(dir, "evidence"), CacheDir: filepath.Join(dir, "cache"), Backend: noExecution{}}
			switch mode {
			case "spec":
				_ = os.WriteFile(spec, []byte("wrong"), 0644)
			case "baseline":
				_ = os.WriteFile(filepath.Join(req.BaselineDir, "LICENSE"), []byte("wrong"), 0644)
			case "overlap":
				req.CandidateDir = req.BaselineDir
			case "evidence-overlap":
				req.EvidenceDir = req.CandidateDir
			case "cache-overlap":
				req.CacheDir = req.BaselineDir
			case "sdk":
				_ = os.WriteFile(filepath.Join(base, "rpcx-v2/client.go"), []byte("wrong"), 0644)
			}
			if out, err := Evaluate(context.Background(), req); err == nil || out.Verdict != "" {
				t.Fatalf("invalid request accepted: %v", err)
			}
		})
	}
}

func TestRequiresDocker(t *testing.T) {
	if _, err := Evaluate(context.Background(), checks.EvaluationRequest{Backend: execution.Local{}}); err == nil {
		t.Fatal("local backend accepted")
	}
}

func TestBoundaryNormalizerDoesNotEraseExtraDerivations(t *testing.T) {
	source := []byte(`package sample
import "context"
func invokeHTTP(){ctx,cancel:=context.WithTimeout(context.Background(),1);defer cancel();other,release:=context.WithTimeout(ctx,2);_ = other;defer release()}`)
	if _, unsupported := normalBoundary(source, true); !unsupported {
		t.Fatal("multiple context resource derivations were erased")
	}
}
