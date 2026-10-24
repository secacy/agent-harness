package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/audit"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/translation"
)

func TestTranslationScopeVersionIsolation(t *testing.T) {
	old, err := lookupProfile(ProfileTranslation)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := lookupProfile(ProfileTranslationScope)
	if err != nil {
		t.Fatal(err)
	}
	if old.Verifier != translation.VerifierVersion || old.hash() != "4d71ed028b28ebe7a66ca1e2bc10a328d4990ebd5bd606eb35db50a8ec7e3279" || latest.Verifier != translation.ScopeVerifierVersion || latest.ID == old.ID {
		t.Fatal("profile version was overwritten")
	}
	raw, _ := json.Marshal(old)
	var copyProfile profile
	json.Unmarshal(raw, &copyProfile)
	copyProfile.ID = latest.ID
	copyProfile.Verifier = latest.Verifier
	raw, _ = json.Marshal(copyProfile)
	other, _ := json.Marshal(latest)
	if !bytes.Equal(raw, other) {
		t.Fatal("new scope profile changed unrelated inputs")
	}
	if ValidateDiagnostics(latest.ID, 2) != nil {
		t.Fatal("new profile lost original diagnostic configuration")
	}
}

// 固定输入的脚本交付覆盖三类判定和已见真实候选，不调用模型或独立参考。
func TestTranslationScopeDelivery(t *testing.T) {
	if os.Getenv("HARNESS_TRANSLATION_SCOPE_ACCEPTANCE") != "1" {
		t.Skip("set HARNESS_TRANSLATION_SCOPE_ACCEPTANCE=1 for scope delivery")
	}
	cases := heterogeneousCases(t)
	base := cases["translation"]
	ids := []string{"correct", "wrong-timeout", "helper", "recent"}
	for _, id := range ids {
		c := base
		c.input.ID = id
		c.input.ProfileID = ProfileTranslationScope
		if id == "helper" {
			patch := "mapping-helper"
			layout := t.TempDir()
			if err := c.before.write(context.Background(), layout); err != nil {
				t.Fatal(err)
			}
			p, err := filepath.Abs("../translation/cases/patches/" + patch + ".patch")
			if err != nil {
				t.Fatal(err)
			}
			applyPatch(t, layout, p)
			c.candidate = mustReadTree(t, layout)
		}
		if id == "wrong-timeout" {
			path := "internal/repo/webapi/translation_rpcx_alternate.go"
			c.candidate = map[string][]byte{}
			for name, data := range base.candidate {
				c.candidate[name] = append([]byte{}, data...)
			}
			c.candidate[path] = []byte(strings.Replace(string(c.candidate[path]), "30*time.Second", "5*time.Second", 1))
		}
		if id == "recent" {
			path := "../../../runs/translation-handoff-dev-v0.1.0/campaign/targets/translation/runs/rpcx-agent-1586805421/workspace/translation"
			c.candidate = mustReadTree(t, path)
			if c.candidate.hash() != "7d7dd9cad65b36c67609295eed106aa6f5e6c64608015efd048928b3951de9cc" {
				t.Fatal("saved candidate identity changed")
			}
		}
		c.edits = nil
		for _, name := range sortedKeys(c.candidate) {
			if !bytes.Equal(c.before[name], c.candidate[name]) {
				c.edits = append(c.edits, replacement(t, fmt.Sprint(len(c.edits)), name, c.before[name], c.candidate[name]))
			}
		}
		cases[id] = c
	}
	var backend execution.Executor = execution.Local{}
	if os.Getenv("HARNESS_TRANSLATION_SCOPE_BACKEND") == "docker" {
		docker, err := execution.NewDocker(context.Background(), execution.DockerConfig{})
		if err != nil {
			t.Fatal(err)
		}
		backend = docker
	}
	config := heterogeneousCampaign(t, "translation-scope-v2", ids, cases, backend)
	record, summary, err := campaign.Run(context.Background(), config)
	if err != nil || record.Status != campaign.Completed || summary.Counts.Completed != 4 || summary.Counts.Verdicts["ReadyForReview"] != 2 || summary.Counts.Verdicts["Rejected"] != 1 || summary.Counts.Verdicts["NeedsAttention"] != 1 {
		t.Fatal(record, summary.Counts, err)
	}
	wants := map[string]string{"correct": "ReadyForReview", "wrong-timeout": "Rejected", "helper": "NeedsAttention", "recent": "ReadyForReview"}
	for _, target := range summary.Targets {
		report := campaignReport(t, record.Directory, target)
		dir := filepath.Join(record.Directory, filepath.FromSlash(target.Attempt.Directory))
		proof := readJSON[translation.ScopeProof](t, filepath.Join(dir, "evidence/scope-proof.json"))
		if target.Attempt.Verdict != wants[target.ID] || report.Validation.VerifierVersion != translation.ScopeVerifierVersion || report.CandidateSHA256 != cases[target.ID].candidate.hash() || proof.Binding.CandidateSHA256 != report.CandidateSHA256 || proof.Matched != (wants[target.ID] == "ReadyForReview") {
			t.Fatal(target.ID, target.Attempt.Verdict, proof)
		}
		replay := t.TempDir()
		if err := cases[target.ID].before.write(context.Background(), replay); err != nil {
			t.Fatal(err)
		}
		applyPatch(t, replay, filepath.Join(dir, "artifacts/candidate.patch"))
		if mustReadTree(t, replay).hash() != report.CandidateSHA256 {
			t.Fatal("patch replay differs")
		}
		review, err := os.ReadFile(filepath.Join(dir, "artifacts/review.md"))
		if err != nil {
			t.Fatal(err)
		}
		assertReviewLinks(t, dir, review)
	}
	result, err := audit.Inspect(context.Background(), audit.Request{CampaignDir: record.Directory, OutputDir: filepath.Join(filepath.Dir(record.Directory), "audit")}, InspectRun)
	if err != nil || result.Consistency != "pass" {
		t.Fatal(result.Violations, err)
	}
	// 只对一个复制的完成产物做结构自洽的证据篡改，原 Campaign 保留。
	target := summary.Targets[0]
	run := filepath.Join(record.Directory, filepath.FromSlash(target.Attempt.Directory))
	for _, mode := range []string{"matched", "node", "analysis", "worker"} {
		t.Run("audit-"+mode, func(t *testing.T) {
			copyRoot := t.TempDir()
			for _, source := range result.Sources {
				if source.Kind != "file" {
					continue
				}
				raw, err := os.ReadFile(filepath.Join(record.Directory, filepath.FromSlash(source.Path)))
				if err != nil {
					t.Fatal(err)
				}
				auditBytes(t, copyRoot, source.Path, raw)
			}
			relative := target.Attempt.Directory
			path := filepath.Join(copyRoot, filepath.FromSlash(relative), "evidence/scope-proof.json")
			p := readJSON[translation.ScopeProof](t, path)
			switch mode {
			case "matched":
				p.Matched = false
			case "node":
				for i := range p.Files {
					if len(p.Files[i].Nodes) > 0 {
						p.Files[i].Nodes[0].End++
						break
					}
				}
			case "analysis":
				binding := p.Analyses["default"]
				binding.CandidateSHA256 = "forged"
				p.Analyses["default"] = binding
			case "worker":
				p.WorkerSHA256 = "forged"
			}
			if err := writeJSON(path, p); err != nil {
				t.Fatal(err)
			}
			inspected, err := audit.Inspect(context.Background(), audit.Request{CampaignDir: copyRoot, OutputDir: filepath.Join(t.TempDir(), "audit")}, InspectRun)
			if err != nil || !auditHas(inspected.Violations, "scope_proof_mismatch") {
				t.Fatal("scope tamper undetected", inspected.Violations, err)
			}
		})
	}
	t.Log("TRANSLATION_SCOPE_CAMPAIGN", record.Directory, "first-run", run)
}
