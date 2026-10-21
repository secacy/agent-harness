package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/audit"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/task"
)

type runAudit struct {
	view        *audit.View
	ref         audit.RunRef
	profile     profile
	findings    []audit.Finding
	trees       map[string]map[string]string
	treeSources map[string][]string
}

// InspectRun 只核对归档材料的结构与绑定，不执行候选或重新判定质量。
func InspectRun(ctx context.Context, view *audit.View, ref audit.RunRef) (audit.RunInspection, error) {
	a := &runAudit{view: view, ref: ref, trees: map[string]map[string]string{}, treeSources: map[string][]string{}}
	p, err := lookupProfile(ref.Target.ProfileID)
	if err != nil {
		a.issue(audit.Gap, "unsupported_profile", "run.json", "", err.Error())
		return audit.RunInspection{Findings: a.findings}, nil
	}
	a.profile = p
	if ref.Target.BuildConfig != p.BuildConfig || ref.Target.Repository.SHA256 != p.RepositorySHA256 || ref.Target.Specification.SHA256 != p.SpecSHA256 || len(ref.Target.Dependencies) != len(p.Dependencies) {
		a.issue(audit.Violation, "profile_binding_mismatch", "run.json", "", "target does not match the published profile", "plan.json")
	}
	for name, sha := range p.Dependencies {
		if ref.Target.Dependencies[name].SHA256 != sha {
			a.issue(audit.Violation, "profile_binding_mismatch", "run.json", "", "dependency does not match the profile", "plan.json")
		}
	}
	completed := ref.Record.Status == string(StatusCompleted)
	prepared := ref.Record.Phase != string(PhasePreparing)
	var input Input
	haveInput := a.read("inputs/task.json", &input, 0, prepared || completed)
	profileBytes, haveProfile := a.bytes("inputs/profile.json", prepared || completed)
	if haveProfile && digest(profileBytes) != p.hash() {
		a.issue(audit.Violation, "profile_digest_mismatch", "inputs/profile.json", "", "archived profile differs from the published profile")
	}
	if haveInput {
		a.checkInput(input, prepared)
	}
	if prepared {
		a.checkPrepared(input, haveInput)
	}
	requireBaseline := ref.Record.Phase == string(PhaseGenerating) || ref.Record.Phase == string(PhaseValidating) || ref.Record.Phase == string(PhasePublishing) || completed
	var baseline BaselineReport
	haveBaseline := a.read("preflight/baseline.json", &baseline, 1, requireBaseline)
	if haveBaseline {
		a.checkBaseline(baseline, requireBaseline)
	}
	var report Report
	reportBytes, haveReport := a.bytes("artifacts/report.json", completed)
	if haveReport && a.reportFormat(reportBytes) && a.decode("artifacts/report.json", reportBytes, &report, 0) {
		stale := (ref.Record.Status == string(StatusFailed) || ref.Record.Status == string(StatusCancelled)) && (report.Status == StatusCompleted || report.Status == StatusFailed || report.Status == StatusCancelled) && string(report.Status) != ref.Record.Status
		if stale {
			a.issue(audit.Gap, "stale_report", "artifacts/report.json", "status", "completed report is not an accepted verdict for this failed run")
		}
		if report.RunID != ref.Record.ID {
			a.issue(audit.Violation, "report_binding_mismatch", "artifacts/report.json", "run_id", "report belongs to another run")
		}
		if ref.Record.Status == "Running" {
			a.issue(audit.Observation, "publication_pending", "artifacts/report.json", "status", "report exists before run terminal publication")
		} else if string(report.Status) != ref.Record.Status && !stale {
			a.issue(audit.Violation, "report_binding_mismatch", "artifacts/report.json", "status", "report and run terminal statuses differ")
		}
		if haveInput && !reflect.DeepEqual(input, report.Input) {
			severity := audit.Gap
			if completed {
				severity = audit.Violation
			}
			a.issue(severity, "task_report_mismatch", "artifacts/report.json", "input", "report and saved task input differ", a.file("inputs/task.json"))
		}
		if completed && (report.CandidateSHA256 != ref.Record.CandidateSHA256 || report.PatchSHA256 != ref.Record.PatchSHA256) {
			a.issue(audit.Violation, "report_binding_mismatch", "artifacts/report.json", "candidate_sha256/patch_sha256", "completed report pins differ from run")
		}
		if haveBaseline && report.Baseline != nil && !reflect.DeepEqual(baseline, *report.Baseline) {
			severity := audit.Gap
			if completed {
				severity = audit.Violation
			}
			a.issue(severity, "baseline_report_mismatch", "artifacts/report.json", "baseline_check", "published baseline snapshots differ", a.file("preflight/baseline.json"))
		}
		if completed && (report.Baseline == nil || report.Validation == nil || report.AgentRecord != "agent.json" || report.Review != "review.md") {
			a.issue(audit.Violation, "terminal_claim_incomplete", "artifacts/report.json", "", "completed delivery lacks required report sections")
		}
		if report.Review != "" {
			if report.Review != "review.md" {
				a.issue(audit.Violation, "unsafe_artifact", "artifacts/report.json", "review", "unexpected review path")
			} else {
				a.bytes("artifacts/review.md", completed)
			}
		}
		if report.AgentRecord != "" {
			if report.AgentRecord != "agent.json" {
				a.issue(audit.Violation, "unsafe_artifact", "artifacts/report.json", "agent_record", "unexpected agent path")
			} else {
				if completed {
					var record agent.Result
					if a.read("artifacts/agent.json", &record, 0, true) && (record.Reason != agent.ExitCompleted || ref.Record.AgentExitReason != string(agent.ExitCompleted) || record.ModelCalls < 0 || record.ToolCalls < 0) {
						a.issue(audit.Violation, "agent_record_mismatch", "artifacts/agent.json", "Reason", "completed run lacks a matching submitted agent result")
					}
				} else {
					a.bytes("artifacts/agent.json", false)
				}
			}
		}
		if report.Validation != nil {
			a.checkValidation(*report.Validation, completed, stale)
		}
	}
	if ref.Record.PatchSHA256 != "" {
		a.pinnedFile("artifacts/candidate.patch", ref.Record.PatchSHA256, true)
	}
	if ref.Record.CandidateSHA256 != "" {
		dir := "workspace/" + p.RepositoryName
		a.pinnedTree(dir, ref.Record.CandidateSHA256)
	}
	if completed && (!haveReport || !haveBaseline) {
		a.issue(audit.Violation, "terminal_claim_incomplete", "run.json", "", "completed run lacks its report or baseline evidence")
	}
	complete := true
	for _, f := range a.findings {
		if f.Severity == audit.Gap {
			complete = false
		}
	}
	return audit.RunInspection{Complete: complete, Findings: a.findings}, ctx.Err()
}

func (a *runAudit) file(relative string) string { return a.ref.Path + "/" + relative }
func (a *runAudit) issue(severity audit.Severity, code, relative, field, reason string, sources ...string) {
	p := a.file(relative)
	sources = append(sources, p, a.file("run.json"))
	a.findings = append(a.findings, audit.Finding{Severity: severity, Code: code, Path: p, Field: field, Reason: reason, Sources: sources})
}
func (a *runAudit) bytes(p string, required bool) ([]byte, bool) {
	data, err := a.view.ReadFile(a.file(p))
	if errors.Is(err, os.ErrNotExist) {
		severity := audit.Observation
		if required {
			severity = audit.Violation
		}
		a.issue(severity, "material_not_published", p, "", "material has not been published")
		if required {
			a.issue(audit.Gap, "material_checks_blocked", p, "", "required material is unavailable for further checks")
		}
		return nil, false
	}
	if err != nil {
		severity, code := audit.Gap, "material_unreadable"
		if errors.Is(err, audit.ErrUnsafePath) {
			severity, code = audit.Violation, "unsafe_artifact"
		}
		a.issue(severity, code, p, "", err.Error())
		return nil, false
	}
	return data, true
}
func (a *runAudit) decode(p string, data []byte, out any, version int) bool {
	if err := audit.DecodeJSON(data, out, version); err != nil {
		severity := audit.Violation
		code := "invalid_material"
		if audit.UnsupportedFormat(err) {
			severity, code = audit.Gap, "unsupported_format"
		}
		a.issue(severity, code, p, "", err.Error())
		a.issue(audit.Gap, "material_checks_blocked", p, "", "dependent checks require a supported record")
		return false
	}
	return true
}
func (a *runAudit) read(p string, out any, version int, required bool) bool {
	data, ok := a.bytes(p, required)
	return ok && a.decode(p, data, out, version)
}

func (a *runAudit) pinnedFile(p, sha string, required bool, sources ...string) {
	data, ok := a.bytes(p, required)
	if ok && (!isDigest(sha) || digest(data) != sha) {
		a.issue(audit.Violation, "material_digest_mismatch", p, "", "file differs from its archived digest", sources...)
	}
}
func isDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func relativeArtifact(p string) bool {
	return p != "" && p != "." && !path.IsAbs(p) && p != ".." && !strings.HasPrefix(p, "../") && path.Clean(p) == p && !strings.Contains(p, "\\")
}

func (a *runAudit) checkInput(input Input, prepared bool) {
	if input.Target == nil || !sameAuditInput(*input.Target, a.ref.Target) || input.RepositoryName != a.profile.RepositoryName || input.ProfileSHA256 != a.profile.hash() || input.SpecVersion != a.profile.SpecVersion {
		a.issue(audit.Violation, "task_binding_mismatch", "inputs/task.json", "", "task identity differs from plan/profile", "plan.json", a.file("inputs/profile.json"))
	}
	if input.Target != nil && sameAuditInput(*input.Target, a.ref.Target) && !reflect.DeepEqual(*input.Target, a.ref.Target) {
		a.issue(audit.Observation, "source_location_normalized", "inputs/task.json", "target", "source locations differ while fixed input identities agree", "plan.json")
	}

	if prepared && (input.SpecSHA256 != a.profile.SpecSHA256 || input.BaselineSHA256 != a.profile.RepositorySHA256) {
		a.issue(audit.Violation, "task_binding_mismatch", "inputs/task.json", "spec_sha256/baseline_sha256", "prepared input lacks its expected pins")
	}
	if input.Budget != nil {
		b := input.Budget
		var ledger budget.Snapshot
		budgetPath, version := a.ref.BudgetPath, 2
		if budgetPath == "" {
			budgetPath, version = "budget/budget.json", 1
		}
		data, err := a.view.ReadFile(budgetPath)
		if err == nil && audit.DecodeJSON(data, &ledger, version) == nil {
			profile := ledger.Profile
			profile.VerifiedAt = profile.VerifiedAt.UTC()
			raw, _ := json.Marshal(profile)
			if b.CampaignID != a.ref.CampaignID || b.TargetID != a.ref.Target.ID || b.RunID != a.ref.Record.ID || b.LedgerPath != filepath.Join(a.ref.OriginalRoot, filepath.FromSlash(budgetPath)) || b.ProfileSHA256 != digest(raw) || input.ModelName != profile.Model || ledger.Targets[a.ref.Target.ID].RunID != a.ref.Record.ID {
				a.issue(audit.Violation, "budget_reference_mismatch", "inputs/task.json", "budget", "task budget reference differs from campaign ledger", budgetPath)
			}
		} else {
			a.issue(audit.Gap, "budget_reference_unverified", "inputs/task.json", "budget", "ledger is unavailable or unsupported", budgetPath)
		}
	} else if a.ref.Record.Status == string(StatusCompleted) || a.ref.Record.Phase == string(PhaseValidating) || a.ref.Record.Phase == string(PhasePublishing) {
		a.issue(audit.Violation, "budget_reference_mismatch", "inputs/task.json", "budget", "submitted run lacks its budget reference")
	}
}

func (a *runAudit) checkPrepared(input Input, haveInput bool) {
	a.pinnedFile("inputs/spec/spec.md", a.profile.SpecSHA256, true)
	layout, layoutOK := a.tree("baseline")
	for _, name := range append([]string{a.profile.RepositoryName}, sortedKeys(a.profile.Dependencies)...) {
		expected := a.profile.RepositorySHA256
		if name != a.profile.RepositoryName {
			expected = a.profile.Dependencies[name]
		}
		base := "baseline/" + name
		a.pinnedTree(base, expected)
		if name != a.profile.RepositoryName {
			a.pinnedTree("workspace/"+name, expected)
		}
	}
	if haveInput && layoutOK && !reflect.DeepEqual(input.Files, layout) {
		sources := a.treeSources["baseline"]
		a.issue(audit.Violation, "input_manifest_mismatch", "inputs/task.json", "files", "input file inventory differs from baseline copies", sources...)
	}
}

func (a *runAudit) tree(dir string) (map[string]string, bool) {
	if files, ok := a.trees[dir]; ok {
		return files, files != nil
	}
	files := map[string]string{}
	sources := []string{}
	size := 0
	directories := 0
	var walk func(string, int) error
	walk = func(p string, depth int) error {
		if depth > 128 || directories >= 10000 {
			return errors.New("tree directory limit exceeded")
		}
		directories++
		sources = append(sources, a.file(p))
		entries, err := a.view.ReadDir(a.file(p))
		if err != nil {
			return err
		}
		for _, e := range entries {
			child := p + "/" + e.Name()
			if e.Name() == ".git" {
				return fmt.Errorf("%w: Git metadata is not an archived input", audit.ErrUnsafePath)
			}
			if e.IsDir() {
				if err := walk(child, depth+1); err != nil {
					return err
				}
				continue
			}
			sources = append(sources, a.file(child))
			data, err := a.view.ReadFile(a.file(child))
			if err != nil {
				return err
			}
			size += len(data)
			if len(data) > 1<<20 || len(files) >= 10000 || size > 16<<20 {
				return errors.New("tree exceeds file or byte limits")
			}
			files[strings.TrimPrefix(child, dir+"/")] = digest(data)
		}
		return nil
	}
	err := walk(dir, 0)
	a.treeSources[dir] = sources
	if err != nil {
		a.trees[dir] = nil
		severity, code := audit.Gap, "tree_unverified"
		if errors.Is(err, os.ErrNotExist) {
			severity, code = audit.Violation, "material_not_published"
		}
		if errors.Is(err, audit.ErrUnsafePath) {
			severity, code = audit.Violation, "unsafe_artifact"
		}
		a.issue(severity, code, dir, "", err.Error(), sources...)
		if severity == audit.Violation {
			a.issue(audit.Gap, "tree_checks_blocked", dir, "", "tree contents could not be inspected", sources...)
		}
		return nil, false
	}
	a.trees[dir] = files
	return files, true
}
func (a *runAudit) pinnedTree(dir, sha string, dependencies ...string) {
	files, ok := a.tree(dir)
	if !ok {
		return
	}
	data, _ := json.Marshal(files)
	if !isDigest(sha) || digest(data) != sha {
		a.issue(audit.Violation, "tree_digest_mismatch", dir, "", "tree differs from its archived pin", append(a.treeSources[dir], dependencies...)...)
	}
}

// 先检查嵌入版本，再按旧类型读取；未知版本不能触发旧字段缺失的伪违规。
func (a *runAudit) reportFormat(data []byte) bool {
	var object map[string]json.RawMessage
	if !a.decode("artifacts/report.json", data, &object, 0) {
		return false
	}
	raw := object["validation"]
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields["schema_version"] == nil || string(fields["schema_version"]) == "null" || fields["verifier_version"] == nil {
		a.issue(audit.Violation, "invalid_material", "artifacts/report.json", "validation", "validation requires schema_version and verifier_version")
		a.issue(audit.Gap, "material_checks_blocked", "artifacts/report.json", "validation", "validation format could not be selected")
		return false
	}
	var header struct {
		SchemaVersion   int    `json:"schema_version"`
		VerifierVersion string `json:"verifier_version"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		a.issue(audit.Violation, "invalid_material", "artifacts/report.json", "validation", err.Error())
		return false
	}
	version := 3
	if a.profile.ID == ProfileT1T2 {
		version = 2
	}
	if header.SchemaVersion != version || header.VerifierVersion != a.profile.Verifier {
		a.issue(audit.Gap, "unsupported_verifier", "artifacts/report.json", "validation", "unsupported archived validation schema/verifier combination")
		return false
	}
	return true
}

// 来源位置属于历史元数据；内容身份由固定摘要认证，不解析旧机器的符号链接。
func sameAuditInput(actual, expected task.Target) bool {
	if actual.Validate() != nil {
		return false
	}
	actual.Repository.Directory = expected.Repository.Directory
	actual.Specification.Path = expected.Specification.Path
	dependencies := make(map[string]task.Snapshot, len(actual.Dependencies))
	for name, s := range actual.Dependencies {
		s.Directory = expected.Dependencies[name].Directory
		dependencies[name] = s
	}
	actual.Dependencies = dependencies
	return reflect.DeepEqual(actual, expected)
}
