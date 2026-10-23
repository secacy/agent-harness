package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
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

// TestGHCLIResponseDeliveryCampaign 用固定开发输入经真实工具交付三种判定，不请求模型服务。
func TestGHCLIResponseDeliveryCampaign(t *testing.T) {
	output := os.Getenv("HARNESS_GHCLI_RESPONSE_DELIVERY_OUTPUT")
	if output == "" {
		t.Skip("set HARNESS_GHCLI_RESPONSE_DELIVERY_OUTPUT for controlled response delivery")
	}
	output, err := gate.Canonical(output)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(output, 0755); err != nil {
		t.Fatal(err)
	}
	scenario, err := closureScenario()
	if err != nil {
		t.Fatal(err)
	}
	fixtures := filepath.Join(scenario, "l2/data/rpcx-l2-ghcli-v1/fixtures")
	baseline, err := gate.ReadFiles(filepath.Join(fixtures, "ghcli"))
	if err != nil {
		t.Fatal(err)
	}
	const boundary = "pkg/cmd/release/delete/outbound.go"
	replace := func(data []byte, old, new string) []byte {
		if bytes.Count(data, []byte(old)) != 1 {
			t.Fatal("fixed edit must match once")
		}
		return bytes.Replace(data, []byte(old), []byte(new), 1)
	}
	direct := replace(baseline[boundary], `"example.com/rpcx"`, `"example.com/rpcx/v2"`)
	direct = replace(direct, `"sync/atomic"`, "\"sync/atomic\"\n\"time\"")
	direct = replace(direct, "Call(string, any, any)", "Call(context.Context, string, any, any)")
	direct = replace(direct, "rpcx.NewClient(address)", "rpcx.Dial(address)")
	direct = replace(direct, `err = caller.Call("http.do",`, `callCtx,cancel:=context.WithTimeout(request.Context(),30*time.Second);defer cancel();err = caller.Call(callCtx,"http.do",`)
	helper := replace(direct, "context.WithTimeout(request.Context(),", "context.WithTimeout(identity(request.Context()),")
	helper = append(helper, []byte("\nfunc identity(c context.Context)context.Context{return c}\n")...)
	saved, err := os.ReadFile(filepath.Join(scenario, "ghcli/regression/testdata/response-v1/outbound.go.txt"))
	if err != nil {
		t.Fatal(err)
	}
	mod := replace(baseline["go.mod"], "example.com/rpcx v1.0.0", "example.com/rpcx/v2 v2.0.0")
	mod = replace(mod, "replace example.com/rpcx => ../rpcx-v1", "replace example.com/rpcx/v2 => ../rpcx-v2")
	cases := []struct {
		id                       string
		source                   []byte
		verdict, behavior, scope string
	}{
		{"ready", direct, quality.ReadyForReview, quality.StatusPass, quality.StatusPass},
		{"response-loss", saved, quality.Rejected, quality.StatusFail, quality.StatusUnknown},
		{"helper", helper, quality.NeedsAttention, quality.StatusPass, quality.StatusUnknown},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 16*time.Minute)
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
	config := campaign.Config{Directory: filepath.Join(output, "campaign"), Backend: backend, Execute: runner.CampaignExecutor(false), Plan: campaign.Plan{ID: "ghcli-response-delivery", Concurrency: 2, ExecutionConcurrency: 2, Profile: profile, Resources: &resources, Budget: budget.Limits{MaxCalls: 9, MaxTokens: 900}}}
	edits := map[string][]agent.ToolCall{}
	pins := map[string]string{}
	for _, tc := range cases {
		files := map[string][]byte{}
		for name, data := range baseline {
			files[name] = data
		}
		files[boundary] = tc.source
		files["go.mod"] = mod
		pins[tc.id] = gate.TreeHash(files)
		input, err := runner.TargetFromGHCLI(tc.id, fixtures, filepath.Join(scenario, "ghcli/specs/v0.6.0.md"))
		if err != nil {
			t.Fatal(err)
		}
		config.Plan.Targets = append(config.Plan.Targets, campaign.Target{Input: input, Budget: budget.Limits{MaxCalls: 3, MaxTokens: 300}, Limits: agent.Limits{MaxModelCalls: 3, MaxToolCalls: 16}, Timeout: 8 * time.Minute})
		// 与既有受控模型共用工具协议；候选只能由 replace_text 写入各自工作区。
		paths := []string{"go.mod", boundary}
		sort.Strings(paths)
		for i, path := range paths {
			edits[tc.id] = append(edits[tc.id], closureCall(fmt.Sprintf("edit-%d", i), "replace_text", map[string]string{"path": path, "old": string(baseline[path]), "new": string(files[path])}))
		}
	}
	config.NewModel = func(input task.Target) (budget.ProfiledModel, error) {
		return &closureModel{profile: profile, edits: edits[input.ID]}, nil
	}
	if err = closureSave(filepath.Join(output, "input-pins.json"), pins); err != nil {
		t.Fatal(err)
	}
	record, summary, err := campaign.Run(ctx, config)
	if err != nil || record.Status != campaign.Completed || summary.Counts.Completed != 3 || summary.Counts.Failed != 0 {
		t.Fatalf("incomplete delivery: %+v, %v", summary.Counts, err)
	}
	for _, verdict := range []string{quality.ReadyForReview, quality.Rejected, quality.NeedsAttention} {
		if summary.Counts.Verdicts[verdict] != 1 {
			t.Fatalf("unexpected verdict counts: %+v", summary.Counts)
		}
	}
	totals := summary.Budget.Totals
	if totals.Calls != 9 || totals.KnownTokens != 180 || totals.ReservedTokens != 0 || totals.UnknownTokens != 0 || len(summary.Budget.Calls) != 9 {
		t.Fatalf("unexpected budget: %+v", totals)
	}
	runs := map[string]string{}
	for _, tc := range cases {
		var attempt *campaign.Attempt
		for _, target := range summary.Targets {
			if target.ID == tc.id {
				attempt = target.Attempt
			}
		}
		if attempt == nil || attempt.Status != campaign.Completed || attempt.Verdict != tc.verdict {
			t.Fatalf("wrong target delivery: %+v", attempt)
		}
		run := filepath.Join(record.Directory, filepath.FromSlash(attempt.Directory))
		runs[tc.id] = run
		var report runner.Report
		if err = closureRead(filepath.Join(run, "artifacts/report.json"), &report); err != nil {
			t.Fatal(err)
		}
		if report.RunID != attempt.RunID || report.Input.Target == nil || report.Input.Target.ID != tc.id || report.CandidateSHA256 != pins[tc.id] || report.Validation == nil || report.Validation.Verdict != tc.verdict {
			t.Fatal("run/report/candidate identity differs")
		}
		v := report.Validation
		if v.Checks["semantic_behavior"].Status != tc.behavior || v.Checks["request_scope"].Status != tc.scope {
			t.Fatalf("behavior/scope boundary differs: %+v", v.Checks)
		}
		if tc.id == "response-loss" {
			found := false
			for _, f := range v.Checks["semantic_behavior"].Findings {
				found = found || f.Code == "response_missing_on_redirect_error"
			}
			if !found {
				t.Fatal("missing response contract finding")
			}
		}
		ref := report.Input.Budget
		account := summary.Budget.Targets[tc.id]
		if ref == nil || ref.CampaignID != record.ID || ref.TargetID != tc.id || ref.RunID != report.RunID || account.RunID != report.RunID || account.Totals.Calls != 3 || account.Totals.KnownTokens != 60 {
			t.Fatal("target budget binding differs")
		}
		for _, call := range summary.Budget.Calls {
			if call.TargetID == tc.id && (call.RunID != report.RunID || call.State != budget.StateSettled || call.Usage == nil || call.Usage.TotalTokens != 20) {
				t.Fatal("call settlement differs")
			}
		}
		replay := filepath.Join(output, "replay-"+tc.id)
		if err = gate.CopyFiles(replay, baseline); err != nil {
			t.Fatal(err)
		}
		patch := exec.CommandContext(ctx, "patch", "-p1", "--batch", "--forward", "-i", filepath.Join(run, "artifacts/candidate.patch"))
		patch.Dir = replay
		raw, patchErr := patch.CombinedOutput()
		if err = gate.Write(filepath.Join(output, "patch-"+tc.id+".log"), raw); err != nil {
			t.Fatal(err)
		}
		if patchErr != nil {
			t.Fatal(patchErr)
		}
		files, err := gate.ReadFiles(replay)
		if err != nil || gate.TreeHash(files) != pins[tc.id] {
			t.Fatal("patch replay differs from delivery")
		}
		checkDeliveryNavigation(t, run)
		review, err := os.ReadFile(filepath.Join(run, "artifacts/review.md"))
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{"../evidence/behavior-observations.json", "../evidence/returns-probe.go.txt", "../evidence/logs/behavior.stdout.log", "../evidence/rule-map.json"} {
			if !strings.Contains(string(review), text) {
				t.Fatalf("missing review locator: %s", text)
			}
		}
		if tc.id == "response-loss" && !strings.Contains(html.UnescapeString(string(review)), "response_missing_on_redirect_error") {
			t.Fatal("review lost specific violation")
		}
	}
	original, err := audit.Inspect(ctx, audit.Request{CampaignDir: record.Directory, OutputDir: filepath.Join(output, "audit")}, runner.InspectRun)
	if err != nil || original.Consistency != "pass" || !original.ScanComplete || original.ResourceState != "recorded_removed" || original.InputsUnchanged == nil || !*original.InputsUnchanged {
		t.Fatalf("campaign audit failed: %+v, %v", original, err)
	}
	checkResponseDeliveryAudit(t, ctx, output, record.Directory, runs, original)
	if err = closureSave(filepath.Join(output, "delivery-result.json"), map[string]any{"campaign": record, "counts": summary.Counts, "budget": totals, "runs": runs, "audit_consistency": original.Consistency, "scope": "controlled fixed-development candidates; no provider or independent reference execution"}); err != nil {
		t.Fatal(err)
	}
}

// 先证明迁移副本可审计，再逐次篡改和恢复；所有检查都经过实际 Campaign 审计入口。
func checkResponseDeliveryAudit(t *testing.T, ctx context.Context, output, source string, runs map[string]string, original audit.Report) {
	t.Helper()
	clone := filepath.Join(output, "audit-copy")
	for _, s := range original.Sources {
		if !filepath.IsLocal(s.Path) {
			t.Fatal("unsafe audit source")
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
				t.Fatal("audit source changed")
			}
			if err = gate.Write(path, raw); err != nil {
				t.Fatal(err)
			}
		}
	}
	inspect := func(name string) audit.Report {
		r, err := audit.Inspect(ctx, audit.Request{CampaignDir: clone, OutputDir: filepath.Join(output, "audit-copy-"+name)}, runner.InspectRun)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	before := inspect("before")
	if before.Consistency != "pass" || !before.ScanComplete {
		t.Fatalf("audit copy failed: %+v", before)
	}
	relative := func(id string) string {
		rel, err := filepath.Rel(source, runs[id])
		if err != nil || !filepath.IsLocal(rel) {
			t.Fatal("invalid target path")
		}
		return filepath.Join(clone, rel)
	}
	rejected, ready := relative("response-loss"), relative("ready")
	read := func(path string) []byte {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	observationPath := filepath.Join(rejected, "evidence/behavior-observations.json")
	reportPath := filepath.Join(rejected, "artifacts/report.json")
	stdoutPath := filepath.Join(rejected, "evidence/logs/behavior.stdout.log")
	sdkPath := filepath.Join(rejected, "evidence/trusted/rpcx-v2/go.mod")
	originals := map[string][]byte{}
	for _, path := range []string{observationPath, reportPath, stdoutPath, sdkPath} {
		originals[path] = read(path)
	}
	var results []map[string]any
	for _, mode := range []string{"deleted-observation", "changed-observation", "changed-log", "cross-target", "changed-sdk"} {
		t.Run("audit-"+mode, func(t *testing.T) {
			var report runner.Report
			if err := json.Unmarshal(originals[reportPath], &report); err != nil {
				t.Fatal(err)
			}
			var observation map[string]any
			if err := json.Unmarshal(originals[observationPath], &observation); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "deleted-observation":
				if err := os.Remove(observationPath); err != nil {
					t.Fatal(err)
				}
			case "changed-observation":
				observation["observation"].(map[string]any)["status"] = "pass"
			case "changed-log":
				data := bytes.ReplaceAll(originals[stdoutPath], []byte("response_missing_on_redirect_error"), []byte("response_properties_changed"))
				if bytes.Equal(data, originals[stdoutPath]) {
					t.Fatal("log mutation did not change bytes")
				}
				if err := gate.Write(stdoutPath, data); err != nil {
					t.Fatal(err)
				}
				observation["stdout_sha256"] = gate.Digest(data)
			case "cross-target":
				if err := json.Unmarshal(read(filepath.Join(ready, "evidence/behavior-observations.json")), &observation); err != nil {
					t.Fatal(err)
				}
			case "changed-sdk":
				if err := gate.Write(sdkPath, append(append([]byte{}, originals[sdkPath]...), '\n')); err != nil {
					t.Fatal(err)
				}
			}
			if mode != "deleted-observation" {
				if err := closureSave(observationPath, observation); err != nil {
					t.Fatal(err)
				}
			}
			// 重算报告内摘要，要求观察审计也能独立发现错误，不只依赖最外层报告摘要。
			for i, e := range report.Validation.Evidence {
				if e.ID == "behavior-observations" && mode != "deleted-observation" {
					report.Validation.Evidence[i].SHA256 = gate.Digest(read(observationPath))
				}
				if e.ID == "behavior-stdout" && mode == "changed-log" {
					report.Validation.Evidence[i].SHA256 = gate.Digest(read(stdoutPath))
				}
			}
			if err := closureSave(reportPath, report); err != nil {
				t.Fatal(err)
			}
			result := inspect(mode)
			found := false
			for _, f := range result.Violations {
				if f.Code == "behavior_evidence_mismatch" {
					found = true
					dependency := ""
					if mode == "changed-log" {
						dependency = stdoutPath
					}
					if mode == "changed-sdk" {
						dependency = sdkPath
					}
					if dependency != "" {
						relative, err := filepath.Rel(clone, dependency)
						if err != nil {
							t.Fatal(err)
						}
						present := false
						for _, source := range f.Sources {
							present = present || source == filepath.ToSlash(relative)
						}
						if !present {
							t.Fatalf("behavior violation omitted consumed dependency: %s", relative)
						}
					}
				}
			}
			if result.Consistency != "violated" || !found {
				t.Fatalf("semantic audit failed to detect %s: %+v", mode, result)
			}
			results = append(results, map[string]any{"case": mode, "consistency": result.Consistency, "behavior_violation_located": found, "scan_complete": result.ScanComplete})
			for path, raw := range originals {
				if err := gate.Write(path, raw); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	restored := inspect("restored")
	if restored.Consistency != "pass" || !restored.ScanComplete {
		t.Fatalf("restored copy failed: %+v", restored)
	}
	for _, s := range original.Sources {
		if s.Kind == "file" {
			if gate.Digest(read(filepath.Join(source, filepath.FromSlash(s.Path)))) != s.SHA256 {
				t.Fatal("original campaign changed")
			}
		}
	}
	if err := closureSave(filepath.Join(output, "audit-tamper-results.json"), map[string]any{"copy_before": before.Consistency, "cases": results, "copy_restored": restored.Consistency, "original_unchanged": true}); err != nil {
		t.Fatal(err)
	}
}

// TestGHCLIResponseDeliveryAudit 在已停写批次上复验最终审计实现，不重新生成候选。
func TestGHCLIResponseDeliveryAudit(t *testing.T) {
	source, output := os.Getenv("HARNESS_GHCLI_RESPONSE_AUDIT_CAMPAIGN"), os.Getenv("HARNESS_GHCLI_RESPONSE_AUDIT_OUTPUT")
	if source == "" || output == "" {
		t.Skip("set HARNESS_GHCLI_RESPONSE_AUDIT_CAMPAIGN and HARNESS_GHCLI_RESPONSE_AUDIT_OUTPUT for offline delivery audit")
	}
	var err error
	source, err = gate.Canonical(source)
	if err != nil {
		t.Fatal(err)
	}
	output, err = gate.Canonical(output)
	if err != nil || gate.Overlap(source, output) {
		t.Fatal("audit output overlaps input or is invalid")
	}
	if err = os.Mkdir(output, 0755); err != nil {
		t.Fatal(err)
	}
	var summary campaign.Summary
	if err = closureRead(filepath.Join(source, "summary.json"), &summary); err != nil {
		t.Fatal(err)
	}
	runs := map[string]string{}
	for _, target := range summary.Targets {
		if target.Attempt != nil {
			runs[target.ID] = filepath.Join(source, filepath.FromSlash(target.Attempt.Directory))
		}
	}
	for _, id := range []string{"ready", "response-loss", "helper"} {
		if runs[id] == "" {
			t.Fatal("response delivery target is missing")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	original, err := audit.Inspect(ctx, audit.Request{CampaignDir: source, OutputDir: filepath.Join(output, "audit")}, runner.InspectRun)
	if err != nil || original.Consistency != "pass" || !original.ScanComplete {
		t.Fatalf("final reader rejected campaign: %+v, %v", original, err)
	}
	checkResponseDeliveryAudit(t, ctx, output, source, runs, original)
}
