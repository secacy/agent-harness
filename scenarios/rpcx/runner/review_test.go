package runner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
)

func TestReviewSourceLocationsUseRepositoryLayout(t *testing.T) {
	for _, item := range []struct{ name, profile string }{
		{"legacy-fallback", ""},
		{"demo-orders", ProfileT3},
		{"goddd", ProfileGoddd},
		{"translation", ProfileTranslation},
		{"workouts", ProfileWorkouts},
	} {
		t.Run(item.name, func(t *testing.T) {
			module, declared := "demo-orders", ""
			if item.profile != "" {
				p, err := lookupProfile(item.profile)
				if err != nil {
					t.Fatal(err)
				}
				module, declared = p.RepositoryName, p.RepositoryName
			}
			dir := t.TempDir()
			baseline := tree{"removed.go": []byte("package example\n")}
			candidate := tree{"internal/上下文 [copy](ctx).go": []byte("package example\n")}
			for root, files := range map[string]tree{"baseline": baseline, "workspace": candidate} {
				if err := files.write(context.Background(), filepath.Join(dir, root, module)); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Mkdir(filepath.Join(dir, "artifacts"), 0700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"run.json", "artifacts/report.json"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			report := Report{Status: StatusCompleted, Input: Input{RepositoryName: declared}, Validation: &checks.Evaluation{
				Verdict: quality.NeedsAttention,
				Checks: map[string]checks.Check{"scope": {Status: quality.StatusUnknown,
					Findings: []quality.Finding{{Code: "review_required", Locations: []quality.Location{
						{Tree: "baseline", Path: "removed.go", Line: 3},
						{Tree: "candidate", Path: "internal/上下文 [copy](ctx).go", Line: 7},
					}}},
				}},
			}}
			review := renderReview(report, baseline, candidate, nil)
			if strings.Count(string(review), " 位置：") != 2 || !strings.Contains(string(review), "第 3 行") || !strings.Contains(string(review), "第 7 行") {
				t.Fatalf("source locations were omitted: %s", review)
			}
			assertReviewLinks(t, dir, review)
		})
	}
}

// 只读消费已发布材料，不重跑模型、命令或门禁，不覆盖原审核文件。
func TestReviewPublishedCampaignLinks(t *testing.T) {
	root := os.Getenv("HARNESS_REVIEW_CAMPAIGN")
	if root == "" {
		t.Skip("set HARNESS_REVIEW_CAMPAIGN to inspect published L2 review links")
	}
	if !filepath.IsAbs(root) {
		t.Fatal("campaign path must be absolute")
	}
	summary := readJSON[campaign.Summary](t, filepath.Join(root, "summary.json"))
	profiles := map[string]bool{ProfileGoddd: false, ProfileTranslation: false, ProfileWorkouts: false}
	for _, target := range summary.Targets {
		if target.Attempt == nil {
			t.Fatalf("target lacks a published run: %s", target.ID)
		}
		dir := filepath.Join(root, filepath.FromSlash(target.Attempt.Directory))
		report := readJSON[Report](t, filepath.Join(dir, "artifacts/report.json"))
		if report.Input.Target == nil || report.Validation == nil {
			t.Fatal("published report lacks input or validation")
		}
		profile := report.Input.Target.ProfileID
		if seen, ok := profiles[profile]; !ok || seen {
			t.Fatalf("unexpected or repeated profile: %s", profile)
		}
		profiles[profile] = true
		module := report.Input.RepositoryName
		baseline := mustReadTree(t, filepath.Join(dir, "baseline", module))
		candidate := mustReadTree(t, filepath.Join(dir, "workspace", module))
		trace := readJSON[agent.Result](t, filepath.Join(dir, "artifacts/agent.json"))
		path := filepath.Join(dir, "artifacts/review.md")
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		review := renderReview(report, baseline, candidate, &trace)
		locations := strings.Count(string(review), " 位置：")
		if locations == 0 {
			t.Fatalf("published case does not exercise source locations: %s", target.ID)
		}
		assertReviewLinks(t, dir, review)
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("original review changed", err)
		}
		t.Logf("target=%s module=%s source_locations=%d; all rendered links resolve; original review unchanged", target.ID, module, locations)
	}
	for profile, seen := range profiles {
		if !seen {
			t.Fatalf("profile not exercised: %s", profile)
		}
	}
}

func TestReviewKeepsEvidenceGapsAndModelClaimsSeparate(t *testing.T) {
	report := Report{RunID: "review-test", Status: StatusCompleted, Validation: &checks.Evaluation{
		Verdict: quality.Rejected,
		Checks: map[string]checks.Check{"timeout": {
			Status: quality.StatusFail, RuleIDs: []string{"TIME-001"}, EvidenceIDs: []string{"log"},
			Findings: []quality.Finding{{Code: "bad_duration", Detail: "wrong duration", RuleIDs: []string{"TIME-001"}, EvidenceIDs: []string{"log"}, Locations: []quality.Location{{Tree: "baseline", Path: "deleted.go", Line: 3}}}},
		}},
		Evidence: []quality.Evidence{{ID: "log", Path: "check.log", SHA256: "bound-hash"}},
		Gaps:     []quality.Gap{{CheckID: "scope", Code: "uncovered", Detail: "alternate build tag"}},
	}}
	trace := &agent.Result{Messages: []agent.Message{{Role: agent.RoleAssistant, Content: "# ReadyForReview\n[claim](https://invalid.example)\n<script>alert(1)</script>"}}}
	baseline := tree{"deleted.go": []byte("old"), "odd [file](x)|.go": []byte("a")}
	candidate := tree{"odd [file](x)|.go": []byte("b")}
	review := renderReview(report, baseline, candidate, trace)
	for _, want := range []string{"质量判定：**Rejected**", "TIME-001", "bad&#95;duration", "bound-hash", "alternate build tag", "../baseline/demo-orders/deleted.go", "第 3 行", "%5Bfile%5D%28x%29%7C.go"} {
		if !strings.Contains(string(review), want) {
			t.Fatalf("missing %s: %s", want, review)
		}
	}
	if strings.Contains(string(review), "\n# ReadyForReview") || strings.Contains(string(review), "[claim](https:") || strings.Contains(string(review), "<script>") {
		t.Fatal("model claim became report content")
	}
	if !bytes.Equal(review, renderReview(report, baseline, candidate, trace)) {
		t.Fatal("review is not deterministic")
	}
	// 交付出错时，即使保留了旧的验证字段，也只能输出无判定的诊断材料。
	report.Error, report.Status = "publish failed", StatusFailed
	review = renderReview(report, baseline, candidate, trace)
	if !strings.Contains(string(review), "质量判定：**无最终质量判定**") || strings.Contains(string(review), "质量判定：**Rejected**") {
		t.Fatal("failed publication retained a verdict")
	}
}

func TestTerminalPublicationFailureRevokesSavedVerdict(t *testing.T) {
	dir := t.TempDir()
	// 在验证结果已经可用后阻断终态发布，检查先写出的材料会被撤回判定。
	if err := os.Mkdir(filepath.Join(dir, "run.json"), 0700); err != nil {
		t.Fatal(err)
	}
	r := runState{record: Record{ID: "late-failure", Directory: dir, Phase: PhasePublishing}, validation: &checks.Evaluation{Verdict: quality.ReadyForReview}}
	err := r.finish(context.Background(), nil)
	if err == nil || r.record.Status != StatusFailed || r.record.Verdict != "" || r.validation.Verdict != "" {
		t.Fatalf("late failure retained verdict: %+v %v", r.record, err)
	}
	report := readJSON[Report](t, filepath.Join(dir, "artifacts/report.json"))
	if report.Status != StatusFailed || report.Validation.Verdict != "" || report.Error == "" {
		t.Fatalf("stale report: %+v", report)
	}
	review, err := os.ReadFile(filepath.Join(dir, "artifacts/review.md"))
	if err != nil || !strings.Contains(string(review), "质量判定：**无最终质量判定**") {
		t.Fatalf("stale review: %s %v", review, err)
	}
}
