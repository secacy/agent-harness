package runner

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
)

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
