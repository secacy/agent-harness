package runner_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/audit"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/internal/task"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/runner"
)

// TestGHCLIProofDeliveryCampaign 重放已暴露候选，检验新证明的预算、发布、导航和审计链路，不运行模型或参考。
func TestGHCLIProofDeliveryCampaign(t *testing.T) {
	source, output := os.Getenv("HARNESS_GHCLI_DELIVERY_CANDIDATE"), os.Getenv("HARNESS_GHCLI_DELIVERY_OUTPUT")
	if source == "" || output == "" {
		t.Skip("set HARNESS_GHCLI_DELIVERY_CANDIDATE and HARNESS_GHCLI_DELIVERY_OUTPUT for controlled Docker delivery")
	}
	var err error
	source, err = gate.Canonical(source)
	if err != nil {
		t.Fatal(err)
	}
	output, err = gate.Canonical(output)
	if err != nil {
		t.Fatal(err)
	}
	if gate.Overlap(source, output) {
		t.Fatal("delivery output overlaps the saved candidate")
	}
	candidate, err := gate.ReadFiles(source)
	if err != nil {
		t.Fatal(err)
	}
	const savedSHA = "39390941b07031512fd3eac31104acfcb281409e545819d94cf158ecb4c74de9"
	if gate.TreeHash(candidate) != savedSHA {
		t.Fatal("unexpected saved candidate")
	}
	t.Cleanup(func() {
		after, err := gate.ReadFiles(source)
		if err != nil || gate.TreeHash(after) != savedSHA {
			t.Errorf("saved candidate changed: %v", err)
		}
	})
	if err = os.Mkdir(output, 0755); err != nil {
		t.Fatal(err)
	}
	scenario, err := closureScenario()
	if err != nil {
		t.Fatal(err)
	}
	fixtures := filepath.Join(scenario, "l2/data/rpcx-l2-ghcli-v1/fixtures")
	spec := filepath.Join(scenario, "ghcli/specs/v0.6.0.md")
	baseline, err := gate.ReadFiles(filepath.Join(fixtures, "ghcli"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	backend, err := execution.NewDocker(ctx, execution.DockerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	resources, err := backend.ResourcePolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	profile := budget.Profile{Provider: "controlled", Endpoint: "in-process", Model: "scripted-test-model", ContextTokens: 90, MaxOutputTokens: 10, SourceSHA256: strings.Repeat("e", 64), VerifiedAt: time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)}
	config := campaign.Config{Directory: filepath.Join(output, "campaign"), Backend: backend, Execute: runner.CampaignExecutor(false), Plan: campaign.Plan{ID: "ghcli-proof-delivery", Concurrency: 2, ExecutionConcurrency: 2, Profile: profile, Resources: &resources, Budget: budget.Limits{MaxCalls: 6, MaxTokens: 600}}}
	candidates := map[string]map[string][]byte{}
	edits := map[string][]agent.ToolCall{}
	for _, id := range []string{"saved", "helper"} {
		files := map[string][]byte{}
		for name, data := range candidate {
			files[name] = append([]byte{}, data...)
		}
		if id == "helper" {
			const path = "pkg/cmd/release/delete/outbound.go"
			before := []byte("parent := request.Context()")
			if bytes.Count(files[path], before) != 1 {
				t.Fatal("parent mutation is not unique")
			}
			files[path] = bytes.Replace(files[path], before, []byte("parent := identity(request.Context())"), 1)
			files[path] = append(files[path], []byte("\nfunc identity(c context.Context) context.Context { return c }\n")...)
		}
		candidates[id] = files
		input, err := runner.TargetFromGHCLI(id, fixtures, spec)
		if err != nil {
			t.Fatal(err)
		}
		config.Plan.Targets = append(config.Plan.Targets, campaign.Target{Input: input, Budget: budget.Limits{MaxCalls: 3, MaxTokens: 300}, Limits: agent.Limits{MaxModelCalls: 3, MaxToolCalls: 16}, Timeout: 8 * time.Minute})
		var paths []string
		for name, data := range files {
			if !bytes.Equal(baseline[name], data) {
				paths = append(paths, name)
			}
		}
		sort.Strings(paths)
		for i, path := range paths {
			if len(baseline[path]) == 0 {
				t.Fatal("replay requires an existing baseline file")
			}
			edits[id] = append(edits[id], closureCall(fmt.Sprintf("edit-%d", i), "replace_text", map[string]string{"path": path, "old": string(baseline[path]), "new": string(files[path])}))
		}
	}
	config.NewModel = func(input task.Target) (budget.ProfiledModel, error) {
		return &closureModel{profile: profile, edits: edits[input.ID]}, nil
	}
	pins := map[string]string{}
	for id, files := range candidates {
		pins[id] = gate.TreeHash(files)
	}
	if err = closureSave(filepath.Join(output, "input-pins.json"), pins); err != nil {
		t.Fatal(err)
	}
	record, summary, err := campaign.Run(ctx, config)
	if err != nil || record.Status != campaign.Completed || summary.Counts.Completed != 2 || summary.Counts.Failed != 0 || summary.Counts.Verdicts[quality.ReadyForReview] != 1 || summary.Counts.Verdicts[quality.NeedsAttention] != 1 {
		t.Fatalf("unexpected campaign: %+v, %v", summary.Counts, err)
	}
	totals := summary.Budget.Totals
	if totals.Calls != 6 || totals.KnownTokens != 120 || totals.ReservedTokens != 0 || totals.UnknownTokens != 0 || len(summary.Budget.Calls) != 6 {
		t.Fatalf("unexpected budget: %+v", totals)
	}
	runs := map[string]string{}
	for _, target := range summary.Targets {
		if target.Attempt == nil || target.Attempt.Status != campaign.Completed {
			t.Fatal("missing completed attempt")
		}
		run := filepath.Join(record.Directory, filepath.FromSlash(target.Attempt.Directory))
		runs[target.ID] = run
		var report runner.Report
		if err = closureRead(filepath.Join(run, "artifacts/report.json"), &report); err != nil {
			t.Fatal(err)
		}
		want := quality.ReadyForReview
		if target.ID == "helper" {
			want = quality.NeedsAttention
		}
		if target.Attempt.Verdict != want || report.Validation == nil || report.Validation.Verdict != want || report.RunID != target.Attempt.RunID || report.Input.Target == nil || report.Input.Target.ID != target.ID || report.CandidateSHA256 != pins[target.ID] {
			t.Fatal("published candidate or quality identity differs")
		}
		binding := report.Input.Budget
		if binding == nil || binding.CampaignID != record.ID || binding.TargetID != target.ID || binding.RunID != report.RunID {
			t.Fatal("published budget identity differs")
		}
		account := summary.Budget.Targets[target.ID]
		if account.RunID != report.RunID || account.Totals.Calls != 3 || account.Totals.KnownTokens != 60 {
			t.Fatal("target ledger differs")
		}
		for _, call := range summary.Budget.Calls {
			if call.TargetID == target.ID && (call.RunID != report.RunID || call.State != budget.StateSettled || call.Usage == nil || call.Usage.TotalTokens != 20) {
				t.Fatal("call identity or settlement differs")
			}
		}
		var proof struct {
			SchemaVersion int `json:"schema_version"`
			Report        struct {
				ToolSHA256 string `json:"tool_sha256"`
			}
			Proofs []struct {
				Status   string
				Resource struct{ Status string }
			}
			Boundary struct{ Status string }
		}
		if err = closureRead(filepath.Join(run, "evidence/boundary-proof.json"), &proof); err != nil {
			t.Fatal(err)
		}
		sourceStatus, resourceStatus, boundaryStatus := "pass", "proven", "pass"
		if target.ID == "helper" {
			sourceStatus, resourceStatus, boundaryStatus = "unknown", "unknown", "unknown"
		}
		if proof.SchemaVersion != 2 || proof.Report.ToolSHA256 != report.Validation.MaterialsSHA256 || len(proof.Proofs) != 1 || proof.Proofs[0].Status != sourceStatus || proof.Proofs[0].Resource.Status != resourceStatus || proof.Boundary.Status != boundaryStatus {
			t.Fatal("new source/resource/scope evidence differs from delivery")
		}
		// 在全新基线上应用真实补丁，核对交付摘要而非仅检查补丁存在。
		replay := filepath.Join(output, "replay-"+target.ID)
		if err = gate.CopyFiles(replay, baseline); err != nil {
			t.Fatal(err)
		}
		patch := exec.CommandContext(ctx, "patch", "-p1", "--batch", "--forward", "-i", filepath.Join(run, "artifacts/candidate.patch"))
		patch.Dir = replay
		raw, err := patch.CombinedOutput()
		if writeErr := os.WriteFile(filepath.Join(output, "patch-"+target.ID+".log"), raw, 0600); writeErr != nil {
			t.Fatal(writeErr)
		}
		if err != nil {
			t.Fatalf("patch replay failed: %v", err)
		}
		replayFiles, err := gate.ReadFiles(replay)
		if err != nil || gate.TreeHash(replayFiles) != pins[target.ID] {
			t.Fatal("replayed patch differs from delivered candidate")
		}
		checkDeliveryNavigation(t, run)
	}
	original, err := audit.Inspect(ctx, audit.Request{CampaignDir: record.Directory, OutputDir: filepath.Join(output, "audit")}, runner.InspectRun)
	if err != nil || original.Consistency != "pass" || !original.ScanComplete || original.ResourceState != "recorded_removed" || original.InputsUnchanged == nil || !*original.InputsUnchanged {
		t.Fatalf("campaign audit failed: %s, %v, %+v", original.Consistency, err, original.Violations)
	}
	proofPath, err := filepath.Rel(record.Directory, filepath.Join(runs["saved"], "evidence/boundary-proof.json"))
	if err != nil {
		t.Fatal(err)
	}
	checkDeliveryProofTamper(t, ctx, output, record.Directory, filepath.ToSlash(proofPath), original)
	if err = closureSave(filepath.Join(output, "delivery-result.json"), map[string]any{"campaign": record, "counts": summary.Counts, "budget": totals, "runs": runs, "audit_consistency": original.Consistency, "scope": "controlled replay of already exposed candidates; no model or independent reference execution"}); err != nil {
		t.Fatal(err)
	}
}

func checkDeliveryNavigation(t *testing.T, run string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(run, "artifacts/review.md"))
	if err != nil {
		t.Fatal(err)
	}
	links := regexp.MustCompile(`\]\(([^)]+)\)`).FindAllSubmatch(raw, -1)
	found := false
	for _, link := range links {
		name := strings.SplitN(string(link[1]), "#", 2)[0]
		if name == "" {
			continue
		}
		resolved, err := gate.Canonical(filepath.Join(run, "artifacts", filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(run, resolved)
		if err != nil || !filepath.IsLocal(rel) {
			t.Fatal("review link escapes the run")
		}
		if _, err = os.Stat(resolved); err != nil {
			t.Fatal("review target is missing", err)
		}
		if strings.HasSuffix(name, "boundary-proof.json") {
			found = true
		}
	}
	if !found {
		t.Fatal("review does not expose the new boundary proof")
	}
}

// checkDeliveryProofTamper 只复制审计实际消费的字节，先证明副本可审计，再篡改其中一份新证据。
func checkDeliveryProofTamper(t *testing.T, ctx context.Context, output, source, proofPath string, original audit.Report) {
	t.Helper()
	clone := filepath.Join(output, "audit-copy")
	selected := false
	for _, s := range original.Sources {
		if !filepath.IsLocal(s.Path) {
			t.Fatal("unsafe audit source path")
		}
		path := filepath.Join(clone, filepath.FromSlash(s.Path))
		switch s.Kind {
		case "directory":
			if err := os.MkdirAll(path, 0755); err != nil {
				t.Fatal(err)
			}
		case "file":
			raw, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(s.Path)))
			if err != nil || gate.Digest(raw) != s.SHA256 {
				t.Fatal("audit source changed before copying")
			}
			if err = gate.Write(path, raw); err != nil {
				t.Fatal(err)
			}
			if s.Path == proofPath {
				selected = true
			}
		}
	}
	if !selected {
		t.Fatal("original audit did not consume boundary-proof.json")
	}
	baseline, err := audit.Inspect(ctx, audit.Request{CampaignDir: clone, OutputDir: filepath.Join(output, "audit-copy-before")}, runner.InspectRun)
	if err != nil || baseline.Consistency != "pass" || !baseline.ScanComplete {
		t.Fatalf("unchanged audit copy failed: %s, %v", baseline.Consistency, err)
	}
	chosen := filepath.Join(clone, filepath.FromSlash(proofPath))
	raw, err := os.ReadFile(chosen)
	if err != nil {
		t.Fatal(err)
	}
	before := gate.Digest(raw)
	// 保持 JSON 可读，只改变来源/资源信封的字节，验证审计的摘要义务。
	altered := append(append([]byte{}, raw...), '\n')
	if err = os.WriteFile(chosen, altered, 0600); err != nil {
		t.Fatal(err)
	}
	tampered, err := audit.Inspect(ctx, audit.Request{CampaignDir: clone, OutputDir: filepath.Join(output, "audit-copy-tampered")}, runner.InspectRun)
	found := false
	for _, f := range tampered.Violations {
		if f.Path == proofPath {
			found = true
		}
	}
	if err != nil || tampered.Consistency != "violated" || !tampered.ScanComplete || !found {
		t.Fatalf("changed proof was not located: %s, %v, %+v", tampered.Consistency, err, tampered.Violations)
	}
	// 复核全部原始审计文件仍是原摘要，不把副本的失败写回原批次。
	for _, s := range original.Sources {
		if s.Kind == "file" {
			raw, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(s.Path)))
			if err != nil || gate.Digest(raw) != s.SHA256 {
				t.Fatal("original campaign changed during tamper test")
			}
		}
	}
	if err = closureSave(filepath.Join(output, "proof-tamper.json"), map[string]any{"path": proofPath, "before_sha256": before, "after_sha256": gate.Digest(altered), "copy_before": baseline.Consistency, "copy_after": tampered.Consistency, "violation_located": found, "original_unchanged": true}); err != nil {
		t.Fatal(err)
	}
}
