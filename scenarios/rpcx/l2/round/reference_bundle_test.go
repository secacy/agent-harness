package round

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/baseline"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
)

func TestGHCLIReferenceBundleRequiresCompleteBoundRepresentation(t *testing.T) {
	for _, mode := range []string{"valid", "missing-manifest-pin", "missing-index-pin", "missing-chunk-pin", "missing-chunk-file", "bad-order", "bad-size", "bad-logical", "bad-host", "changed-bytes", "extra-part", "wrong-version"} {
		t.Run(mode, func(t *testing.T) {
			root, rootErr := gate.Canonical(t.TempDir())
			if rootErr != nil {
				t.Fatal(rootErr)
			}
			chunk := []byte("synthetic mechanism chunk; no executable archive\n")
			metadata := []byte("synthetic module manifest\n")
			index := map[string]any{"schema_version": 1, "version": baseline.GHCLIVersion, "manifest_sha256": gate.Digest(metadata), "logical_size": len(chunk), "logical_sha256": gate.Digest(chunk), "chunks": []map[string]any{{"name": "dependencies/modules-000.tar.gz.part", "size": len(chunk), "sha256": gate.Digest(chunk)}}}
			chunks := index["chunks"].([]map[string]any)
			switch mode {
			case "bad-order":
				chunks[0]["name"] = "dependencies/modules-001.tar.gz.part"
			case "bad-size":
				chunks[0]["size"] = len(chunk) + 1
			case "bad-logical":
				index["logical_sha256"] = strings.Repeat("b", 64)
			case "wrong-version":
				index["version"] = "unknown"
			}
			raw, _ := json.Marshal(index)
			files := map[string][]byte{"dependencies/modules.json": metadata, "dependencies/bundle.json": raw, "dependencies/modules-000.tar.gz.part": chunk}
			host, err := l2.HostMaterialsSHA256Version(files, baseline.GHCLIVersion)
			if err != nil {
				t.Fatal(err)
			}
			report := evaluation.Report{HostMaterialsSHA256: host, Artifacts: map[string]string{}}
			for name, data := range files {
				artifact := "modules.bundle/" + filepath.Base(name)
				if name == "dependencies/modules.json" {
					artifact = "modules.json"
				}
				report.Artifacts[artifact] = gate.Digest(data)
				if err := gate.Write(filepath.Join(root, artifact), data); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "missing-manifest-pin":
				delete(report.Artifacts, "modules.json")
			case "missing-index-pin":
				delete(report.Artifacts, "modules.bundle/bundle.json")
			case "missing-chunk-pin":
				delete(report.Artifacts, "modules.bundle/modules-000.tar.gz.part")
			case "missing-chunk-file":
				if err := os.Remove(filepath.Join(root, "modules.bundle/modules-000.tar.gz.part")); err != nil {
					t.Fatal(err)
				}
			case "bad-host":
				report.HostMaterialsSHA256 = strings.Repeat("c", 64)
			case "changed-bytes":
				if err := os.WriteFile(filepath.Join(root, "modules.bundle/modules-000.tar.gz.part"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "extra-part":
				report.Artifacts["modules.bundle/modules-001.tar.gz.part"] = gate.Digest(chunk)
			}
			reader := &reader{ctx: context.Background(), output: t.TempDir(), sources: map[string]Source{}, trees: map[string]string{}}
			present, err := readGHCLIReferenceBundle(reader, root, report)
			switch mode {
			case "valid":
				if err != nil || !present || len(reader.sources) != 3 {
					t.Fatalf("bundle not consumed: %t %v", present, err)
				}
				if err = reader.unchanged(); err != nil {
					t.Fatal(err)
				}
			case "missing-chunk-file":
				if err != nil || present {
					t.Fatalf("missing evidence became conclusive: %t %v", present, err)
				}
			default:
				if err == nil {
					t.Fatal("incomplete or mismatched dependency representation accepted")
				}
			}
		})
	}
}

func TestGHCLIReferenceBundleConsumesPublishedChunks(t *testing.T) {
	files, err := l2.GHCLIMaterials()
	if err != nil {
		t.Fatal(err)
	}
	root, err := gate.Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	host, err := l2.HostMaterialsSHA256Version(files, baseline.GHCLIVersion)
	if err != nil {
		t.Fatal(err)
	}
	report := evaluation.Report{HostMaterialsSHA256: host, Artifacts: map[string]string{}}
	total := 0
	for name, data := range files {
		if !strings.HasPrefix(name, "dependencies/") {
			continue
		}
		artifact := "modules.bundle/" + filepath.Base(name)
		if name == "dependencies/modules.json" {
			artifact = "modules.json"
		}
		report.Artifacts[artifact] = gate.Digest(data)
		total += len(data)
		if err = gate.Write(filepath.Join(root, artifact), data); err != nil {
			t.Fatal(err)
		}
	}
	r := &reader{ctx: context.Background(), output: t.TempDir(), sources: map[string]Source{}, trees: map[string]string{}}
	present, err := readGHCLIReferenceBundle(r, root, report)
	if err != nil || !present || len(r.sources) != 7 || total <= 32<<20 {
		t.Fatalf("published dependency representation not consumed: present=%t files=%d bytes=%d err=%v", present, len(r.sources), total, err)
	}
	if err = r.unchanged(); err != nil {
		t.Fatal(err)
	}
}
