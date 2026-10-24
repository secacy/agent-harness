package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/testintegrity"
	"github.com/secacy/agent-harness/scenarios/rpcx/translation"
	"github.com/secacy/agent-harness/scenarios/rpcx/workouts"
)

const diagnosticTool = "migration_check"
const diagnosticOutputLimit = 16 * 1024

// ValidateDiagnostics 校验宿主启用范围；零值保留已有任务行为。
func ValidateDiagnostics(profileID string, limit int) error {
	if limit == 0 {
		return nil
	}
	if limit != 2 || (profileID != ProfileTranslation && profileID != ProfileWorkouts) {
		return errors.New("generation diagnostics require Translation or Workouts and a limit of 2")
	}
	return nil
}

type DiagnosticReference struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type diagnosticReport struct {
	SchemaVersion int                  `json:"schema_version"`
	RunID         string               `json:"run_id"`
	TargetID      string               `json:"target_id"`
	ProfileSHA256 string               `json:"profile_sha256"`
	Binding       quality.Binding      `json:"binding"`
	Sequence      int                  `json:"sequence"`
	ToolCallID    string               `json:"tool_call_id"`
	Files         map[string]string    `json:"files"`
	Integrity     testintegrity.Report `json:"test_structure"`
	Response      agent.ToolResult     `json:"response"`
}

type diagnosticFeedback struct {
	Binding  quality.Binding         `json:"binding"`
	Status   string                  `json:"test_structure"`
	Scope    string                  `json:"scope"`
	Total    int                     `json:"total_findings"`
	Omitted  int                     `json:"omitted_findings"`
	Findings []testintegrity.Finding `json:"findings"`
}

func diagnosticSpec() agent.ToolSpec {
	return agent.ToolSpec{Name: diagnosticTool, Description: "Compare current original test structure with the authenticated baseline and permitted API adaptation. At most 2 attempts per run, including invalid requests. Returns preserved, violated or unknown with locations. Does not check production deadlines, execute tests or determine final quality. Later edits invalidate its applicability to the current workspace.", Parameters: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}
}

type diagnosticTools struct {
	run      *runState
	files    agent.ToolExecutor
	attempts int
	// 包内故障注入点；正常入口不设置。
	beforePublish func()
}

func (d *diagnosticTools) Execute(ctx context.Context, call agent.ToolCall) (agent.ToolResult, error) {
	if call.Name != diagnosticTool {
		return d.files.Execute(ctx, call)
	}
	if err := ctx.Err(); err != nil {
		return agent.ToolResult{}, err
	}
	d.attempts++
	toolError := func(message string) (agent.ToolResult, error) {
		return agent.ToolResult{Content: message, IsError: true}, nil
	}
	if d.attempts > d.run.config.DiagnosticLimit {
		return toolError("generation diagnostic attempt limit reached")
	}
	var args map[string]json.RawMessage
	if json.Unmarshal([]byte(call.Arguments), &args) != nil || args == nil || len(args) != 0 {
		return toolError("migration_check requires an empty JSON object")
	}
	r := d.run
	if err := r.checkInputs(ctx); err != nil {
		return agent.ToolResult{}, err
	}
	candidate, err := readTree(ctx, r.path("workspace/"+r.profile.RepositoryName))
	if err != nil {
		if ctx.Err() != nil {
			return agent.ToolResult{}, ctx.Err()
		}
		return toolError("candidate cannot be read within the supported file boundaries")
	}
	base := r.fixtures.module(r.profile.RepositoryName)
	materialFn := translation.TestMaterials
	compare := translation.CompareTests
	if r.profile.ID == ProfileWorkouts {
		materialFn, compare = workouts.TestMaterials, workouts.CompareTests
	}
	material, err := materialFn()
	if err != nil {
		return agent.ToolResult{}, err
	}
	integrity, adapted, err := compare(base, candidate, gate.TreeHash(material))
	if err != nil {
		return agent.ToolResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return agent.ToolResult{}, err
	}
	result := diagnosticReport{SchemaVersion: 1, RunID: r.record.ID, TargetID: r.record.TargetID, ProfileSHA256: r.input.ProfileSHA256,
		Binding: quality.Binding{SpecSHA256: r.input.SpecSHA256, BaselineSHA256: base.hash(), CandidateSHA256: candidate.hash()}, Sequence: d.attempts, ToolCallID: call.ID, Integrity: integrity}
	result.Response, err = renderDiagnostic(result.Binding, integrity)
	if err != nil {
		return agent.ToolResult{}, err
	}
	rel := fmt.Sprintf("generation-diagnostics/check-%04d", d.attempts)
	dir := r.path(rel)
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		return agent.ToolResult{}, err
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return agent.ToolResult{}, err
	}
	// 材料先保存，报告最后发布；不完整目录只保留诊断，不进入有效引用清单。
	for name, files := range map[string]tree{"candidate": candidate, "baseline-tests": gate.TestFiles(base), "adapted-tests": adapted, "tool": material} {
		if err := files.write(ctx, filepath.Join(dir, name)); err != nil {
			return agent.ToolResult{}, err
		}
	}
	if err := writeJSON(filepath.Join(dir, "response.json"), result.Response); err != nil {
		return agent.ToolResult{}, err
	}
	files, err := readTree(ctx, dir)
	if err != nil {
		return agent.ToolResult{}, err
	}
	result.Files = files.manifest()
	if d.beforePublish != nil {
		d.beforePublish()
	}
	if err := r.checkInputs(ctx); err != nil {
		return agent.ToolResult{}, err
	}
	after, err := readTree(ctx, r.path("workspace/"+r.profile.RepositoryName))
	if err != nil || after.hash() != candidate.hash() {
		return agent.ToolResult{}, errors.Join(err, errors.New("candidate changed during generation diagnostics"))
	}
	if err := writeJSON(filepath.Join(dir, "report.json"), result); err != nil {
		return agent.ToolResult{}, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "report.json"))
	if err != nil {
		return agent.ToolResult{}, err
	}
	r.diagnostics = append(r.diagnostics, DiagnosticReference{Path: rel + "/report.json", SHA256: digest(raw)})
	return result.Response, nil
}

func renderDiagnostic(binding quality.Binding, report testintegrity.Report) (agent.ToolResult, error) {
	feedback := diagnosticFeedback{Binding: binding, Status: report.Status, Scope: "Original test structure only; tests were not executed. Production deadlines and business semantics were not checked. Unknown changes may be weaker or equivalent; compare the reported baseline/adapted and candidate declarations. This result applies only to the pinned snapshot, not later edits or final quality.", Total: len(report.Findings), Findings: report.Findings}
	// 二分查找能完整编码的 finding 前缀，避免大量缺口下反复编码整个报告。
	low, high := 0, len(report.Findings)
	var best []byte
	for low <= high {
		count := low + (high-low)/2
		feedback.Findings = report.Findings[:count]
		feedback.Omitted = feedback.Total - count
		raw, err := json.Marshal(feedback)
		if err != nil {
			return agent.ToolResult{}, err
		}
		if len(raw) <= diagnosticOutputLimit {
			best = raw
			low = count + 1
		} else {
			high = count - 1
		}
	}
	if best == nil {
		return agent.ToolResult{}, errors.New("diagnostic binding exceeds feedback limit")
	}
	return agent.ToolResult{Content: string(best)}, nil
}
