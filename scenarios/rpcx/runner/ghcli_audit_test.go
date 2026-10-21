package runner

import (
	"context"
	"encoding/json"
	"github.com/secacy/agent-harness/internal/audit"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2"
	"path/filepath"
	"strings"
	"testing"
)

func TestGHCLIAuditBundleActualBytesAndOrder(t *testing.T) {
	f := newAuditFixture(t, ProfileT1T2)
	p, err := lookupProfile(ProfileGHCLI)
	if err != nil {
		t.Fatal(err)
	}
	material, err := l2.GHCLIMaterials()
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range material {
		if strings.HasPrefix(name, "dependencies/") {
			auditBytes(t, f.dir, "bundle/"+strings.TrimPrefix(name, "dependencies/"), data)
		}
	}
	for _, mode := range []string{"valid", "order", "size", "digest", "logical", "manifest", "actual"} {
		t.Run(mode, func(t *testing.T) {
			index := map[string]any{}
			if err := json.Unmarshal(material["dependencies/bundle.json"], &index); err != nil {
				t.Fatal(err)
			}
			chunks := index["chunks"].([]any)
			switch mode {
			case "order":
				chunks[0], chunks[1] = chunks[1], chunks[0]
			case "size":
				chunks[0].(map[string]any)["size"] = float64(1)
			case "digest":
				chunks[0].(map[string]any)["sha256"] = strings.Repeat("0", 64)
			case "logical":
				index["logical_sha256"] = strings.Repeat("0", 64)
			case "manifest":
				index["manifest_sha256"] = strings.Repeat("0", 64)
			}
			if mode == "valid" || mode == "actual" {
				auditBytes(t, f.dir, "bundle/bundle.json", material["dependencies/bundle.json"])
			} else {
				auditPut(t, f.dir, "bundle/bundle.json", index)
			}
			if mode == "actual" {
				auditBytes(t, f.dir, "bundle/modules-000.tar.gz.part", []byte("wrong actual bytes"))
			} else {
				auditBytes(t, f.dir, "bundle/modules-000.tar.gz.part", material["dependencies/modules-000.tar.gz.part"])
			}
			var findings []audit.Finding
			inspector := func(_ context.Context, v *audit.View, ref audit.RunRef) (audit.RunInspection, error) {
				a := &runAudit{view: v, ref: ref, profile: p}
				a.checkGHCLIBundle("bundle/modules.json", "bundle")
				findings = a.findings
				return audit.RunInspection{Complete: true, Findings: findings}, nil
			}
			if _, err := audit.Inspect(context.Background(), audit.Request{CampaignDir: f.root, OutputDir: filepath.Join(t.TempDir(), "audit")}, inspector); err != nil {
				t.Fatal(err)
			}
			if mode == "valid" {
				if len(findings) != 0 {
					t.Fatal("valid published bundle failed")
				}
			} else if !auditHas(findings, "host_material_mismatch") {
				t.Fatal("invalid bundle accepted", mode)
			}
		})
	}
}
