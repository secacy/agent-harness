package runner

import (
	"errors"
	"path/filepath"
	"slices"

	"github.com/secacy/agent-harness/internal/audit"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
	"github.com/secacy/agent-harness/scenarios/rpcx/ghcli"
)

func (a *runAudit) checkGHCLIBehavior(v checks.Evaluation) {
	if a.profile.ID != ProfileGHCLI {
		return
	}
	tool := a.trees["evidence/tool"]
	_, versioned := tool["behavior_evidence.go"]
	declared := false
	for _, e := range v.Evidence {
		declared = declared || e.ID == "behavior-observations"
	}
	status := v.Checks["semantic_behavior"].Status
	executed := v.Verdict != "" && slices.ContainsFunc(v.Commands, func(c checks.Command) bool { return c.Log == "logs/behavior.stdout.log" })
	if !declared && (!versioned || !executed && (status == quality.StatusBlocked || status == quality.StatusNotRun)) {
		return
	}
	// 违规结论必须依赖实际读取的文件及目录，供审计收尾复核时降级。
	sources := append([]string{a.file("artifacts/report.json")}, a.treeSources["evidence/tool"]...)
	read := func(path string) ([]byte, error) {
		if !relativeArtifact(path) {
			return nil, errors.New("unsafe behavior evidence path")
		}
		sources = append(sources, a.file("evidence/"+path))
		raw, ok := a.bytes("evidence/"+path, true)
		if !ok {
			return nil, errors.New("behavior evidence unavailable")
		}
		return raw, nil
	}
	tree := func(path string) (map[string]string, error) {
		if !relativeArtifact(path) {
			return nil, errors.New("unsafe behavior tree path")
		}
		files, ok := a.tree("evidence/" + path)
		sources = append(sources, a.treeSources["evidence/"+path]...)
		if !ok {
			return nil, errors.New("behavior tree unavailable")
		}
		return files, nil
	}
	if err := ghcli.ValidateBehaviorEvidence(v, filepath.Join(a.ref.OriginalRoot, filepath.FromSlash(a.ref.Path), "evidence"), read, tree); err != nil {
		a.issue(audit.Violation, "behavior_evidence_mismatch", "evidence/behavior-observations.json", "", err.Error(), sources...)
	}
}
