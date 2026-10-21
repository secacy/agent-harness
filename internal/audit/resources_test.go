package audit

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/writer"
)

func TestResourceRecordsAreBoundToOriginalPlanAndRun(t *testing.T) {
	for _, mode := range []string{"pending", "removed", "wrong-owner", "wrong-plan", "wrong-witness", "no-dispatch", "null-witness", "extra", "reformatted", "link", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.plan.Resources = &execution.ResourcePolicy{Protocol: execution.ResourceProtocol, Context: "test-context", EngineID: "test-engine"}
			f.record.SchemaVersion = 2
			f.record.WriterProtocol = writer.Protocol
			putBytes(t, f.root, writer.FileName, []byte{})
			f.save(t)
			registry := execution.ResourceRegistry{SchemaVersion: 1, CampaignID: f.record.ID, PlanSHA256: f.record.PlanSHA256, Policy: *f.plan.Resources}
			id := strings.Repeat("1", 32)
			p := "resources/" + id
			intent := execution.ResourceIntent{SchemaVersion: 1, CommandID: id, Name: "harness-owned-" + id, Owner: execution.ResourceOwner{Registry: registry, TargetID: "orders", RunID: "run-1", RunDirectory: runPath("orders", "run-1")}, Image: "sha256:" + strings.Repeat("b", 64), ArgsSHA256: strings.Repeat("c", 64), Mounts: []execution.ResourceMount{{Source: "/not-read/repo", Destination: "/workspace/repo", ReadOnly: true}}, CreatedAt: f.run.StartedAt}
			put(t, f.root, "resources/registry.json", registry)
			switch mode {
			case "wrong-owner":
				intent.Owner.TargetID = "another"
			case "wrong-plan":
				intent.Owner.Registry.PlanSHA256 = strings.Repeat("f", 64)
			}
			put(t, f.root, p+"/intent.json", intent)
			h := execution.ResourceDigest(intent)
			if mode != "no-dispatch" {
				put(t, f.root, p+"/dispatch.json", execution.ResourceDispatch{SchemaVersion: 1, IntentSHA256: h})
			}
			if mode == "removed" || mode == "wrong-witness" {
				sha := h
				if mode == "wrong-witness" {
					sha = strings.Repeat("f", 64)
				}
				put(t, f.root, p+"/created.json", execution.ResourceCreated{SchemaVersion: 1, IntentSHA256: sha, ContainerID: strings.Repeat("a", 64)})
				put(t, f.root, p+"/removed.json", execution.ResourceRemoved{SchemaVersion: 1, IntentSHA256: h, ContainerID: strings.Repeat("a", 64)})
			}
			switch mode {
			case "null-witness":
				putBytes(t, f.root, p+"/dispatch.json", []byte("null\n"))
			case "extra":
				putBytes(t, f.root, p+"/unexpected", []byte("x"))
			case "reformatted":
				b, err := os.ReadFile(filepath.Join(f.root, p, "intent.json"))
				if err != nil {
					t.Fatal(err)
				}
				putBytes(t, f.root, p+"/intent.json", append(b, ' '))
			case "link":
				if err := os.Remove(filepath.Join(f.root, p, "intent.json")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("../registry.json", filepath.Join(f.root, p, "intent.json")); err != nil {
					t.Fatal(err)
				}
			case "legacy":
				f.record.SchemaVersion = 1
				f.record.WriterProtocol = ""
				put(t, f.root, "campaign.json", f.record)
			}
			r := auditFixture(t, f, func(context.Context, *View, RunRef) (RunInspection, error) { return RunInspection{Complete: true}, nil })
			if mode == "pending" || mode == "no-dispatch" || mode == "removed" {
				want := "recorded_pending"
				if mode == "removed" {
					want = "recorded_removed"
				}
				if r.Consistency != "pass" || r.ResourceState != want {
					t.Fatal(r)
				}
			} else if r.Consistency == "pass" || r.ResourceState != "unknown" {
				t.Fatal("invalid resource record accepted", mode, r)
			}
		})
	}
}
