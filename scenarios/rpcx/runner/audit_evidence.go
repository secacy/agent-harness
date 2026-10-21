package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/secacy/agent-harness/internal/audit"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
)

func (a *runAudit) checkBaseline(b BaselineReport, required bool) {
	p := "preflight/baseline.json"
	if b.ProfileSHA256 != a.profile.hash() || b.BaselineSHA256 != a.profile.RepositorySHA256 || b.SpecSHA256 != a.profile.SpecSHA256 || b.BuildConfig != a.profile.BuildConfig {
		a.issue(audit.Violation, "baseline_binding_mismatch", p, "", "baseline check is not bound to expected inputs", a.file("inputs/task.json"))
	}
	if required && b.Status != "pass" {
		a.issue(audit.Violation, "baseline_prerequisite_failed", p, "status", "generation requires a passed baseline")
	}
	if b.Status != "pass" && b.Status != "running" && b.Status != "fail" && b.Status != "error" {
		a.issue(audit.Violation, "invalid_material", p, "status", "unsupported baseline status")
	}
	layout := a.trees["baseline"]
	sources := append([]string{a.file("inputs/task.json")}, a.treeSources["baseline"]...)
	if len(layout) > 0 {
		raw, _ := json.Marshal(layout)
		if digest(raw) != b.LayoutSHA256 {
			a.issue(audit.Violation, "baseline_binding_mismatch", p, "layout_sha256", "baseline layout differs from archived copies", sources...)
		}
	}
	names := map[string]bool{}
	for _, cmd := range b.Commands {
		if names[cmd.Name] {
			a.issue(audit.Violation, "baseline_command_mismatch", p, "commands", "duplicate baseline command name")
		}
		names[cmd.Name] = true
		for _, log := range []struct{ p, sha string }{{cmd.Stdout, cmd.StdoutSHA256}, {cmd.Stderr, cmd.StderrSHA256}} {
			if !relativeArtifact(log.p) || !strings.HasPrefix(log.p, "preflight/") {
				a.issue(audit.Violation, "unsafe_artifact", p, "commands", "baseline log escapes preflight")
				continue
			}
			// 启动时命令错误可能尚未生成日志；完成声明则必须有完整摘要。
			if log.sha != "" {
				a.pinnedFile(log.p, log.sha, b.Status == "pass", a.file(p))
			} else if b.Status == "pass" {
				a.issue(audit.Violation, "baseline_command_mismatch", p, "commands", "passed baseline command lacks log digest")
			}
		}
		if b.Status == "pass" && (cmd.ExitCode != 0 || cmd.Error != "") {
			a.issue(audit.Violation, "baseline_command_mismatch", p, "commands", "passed baseline has an unsuccessful command")
		}
	}
	if b.Status != "pass" {
		return
	}
	requireTests := func(tests map[string]string, required []string) {
		for _, id := range required {
			if tests[id] != "pass" {
				a.issue(audit.Violation, "baseline_inventory_mismatch", p, "tests", "passed baseline lacks a required passing test")
			}
		}
	}
	for _, config := range a.profile.configurations() {
		prefix := ""
		tests := b.Tests
		if len(a.profile.BuildConfigurations) > 0 {
			prefix = config.ID + "-"
			tests = b.ConfigurationTests[config.ID]
		}
		for _, name := range []string{prefix + "build", prefix + "test"} {
			if !names[name] {
				a.issue(audit.Violation, "baseline_command_mismatch", p, "commands", "passed baseline lacks required command "+name)
			}
		}
		requireTests(tests, a.profile.RequiredTests)
	}
	for module, tests := range a.profile.PrerequisiteTests {
		requireTests(b.ModuleTests[module], tests)
		for _, kind := range []string{"build", "test"} {
			if !names[module+"-"+kind] {
				a.issue(audit.Violation, "baseline_command_mismatch", p, "commands", "prerequisite module command missing")
			}
		}
	}
}

func (a *runAudit) checkValidation(v checks.Evaluation, completed, stale bool) {
	owner := "artifacts/report.json"
	if completed && v.Verdict != a.ref.Record.Verdict {
		a.issue(audit.Violation, "validation_binding_mismatch", owner, "validation.verdict", "saved verdict differs from run")
	}
	if v.SpecVersion != a.profile.SpecVersion || v.SpecSHA256 != a.profile.SpecSHA256 || v.BaselineSHA256 != a.profile.RepositorySHA256 || !isDigest(v.CandidateSHA256) {
		a.issue(audit.Violation, "validation_binding_mismatch", owner, "validation", "validation is not bound to expected inputs")
	}
	if !stale && a.ref.Record.CandidateSHA256 != "" && v.CandidateSHA256 != a.ref.Record.CandidateSHA256 {
		a.issue(audit.Violation, "validation_binding_mismatch", owner, "validation.candidate_sha256", "validation and run candidate differ")
	}
	binding := quality.Binding{SpecSHA256: v.SpecSHA256, BaselineSHA256: v.BaselineSHA256, CandidateSHA256: v.CandidateSHA256}
	ids := map[string]quality.Evidence{}
	evidencePaths := map[string]string{}
	for _, e := range v.Evidence {
		if e.ID == "" || ids[e.ID].ID != "" {
			a.issue(audit.Violation, "evidence_identity_mismatch", owner, "validation.evidence", "empty or duplicate evidence ID")
		}
		ids[e.ID] = e
		if e.Binding != binding {
			a.issue(audit.Violation, "evidence_binding_mismatch", owner, "validation.evidence", "evidence uses a different input/candidate binding")
		}
		if !relativeArtifact(e.Path) {
			a.issue(audit.Violation, "unsafe_artifact", owner, "validation.evidence.path", "evidence path escapes its run")
			continue
		}
		a.pinnedFile("evidence/"+e.Path, e.SHA256, true, a.file(owner))
		evidencePaths[e.Path] = e.ID
	}
	for name, check := range v.Checks {
		switch check.Status {
		case quality.StatusPass, quality.StatusFail, quality.StatusUnknown, quality.StatusBlocked, quality.StatusSkipped, quality.StatusNotRun:
		default:
			a.issue(audit.Gap, "unsupported_check_status", owner, "validation.checks."+name, "unknown saved check status")
		}
		refs := append([]string{}, check.EvidenceIDs...)
		for _, finding := range check.Findings {
			refs = append(refs, finding.EvidenceIDs...)
			for _, loc := range finding.Locations {
				if !relativeArtifact(loc.Path) || (loc.Tree != "baseline" && loc.Tree != "candidate") || loc.Line < 0 {
					a.issue(audit.Violation, "finding_location_mismatch", owner, "validation.checks."+name, "finding location is not a valid source location")
				}
			}
		}
		for _, id := range refs {
			if _, ok := ids[id]; !ok {
				a.issue(audit.Violation, "evidence_reference_missing", owner, "validation.checks."+name, "saved check references missing evidence ID "+id)
			}
		}
	}
	for n, command := range v.Commands {
		logPath := command.Log
		if filepath.IsAbs(logPath) {
			original := filepath.Join(a.ref.OriginalRoot, filepath.FromSlash(a.ref.Path), "evidence")
			relative, err := filepath.Rel(original, logPath)
			if err == nil {
				logPath = filepath.ToSlash(relative)
			}
		}
		if !relativeArtifact(logPath) || evidencePaths[logPath] == "" {
			a.issue(audit.Violation, "command_evidence_mismatch", owner, fmt.Sprintf("validation.commands[%d].log", n), "command log lacks a bound evidence entry")
		}
		if command.Directory != "" {
			original := filepath.Join(a.ref.OriginalRoot, filepath.FromSlash(a.ref.Path), "evidence")
			relative, err := filepath.Rel(original, command.Directory)
			if err != nil || !relativeArtifact(filepath.ToSlash(relative)) {
				a.issue(audit.Violation, "command_evidence_mismatch", owner, "validation.commands.directory", "command directory is outside the declared evidence root")
			}
		}
	}
	for _, e := range v.Evidence {
		if e.Kind == "command" && relativeArtifact(e.Path) {
			var saved checks.Command
			if a.read("evidence/"+e.Path, &saved, 0, true) {
				found := false
				for _, cmd := range v.Commands {
					if reflect.DeepEqual(saved, cmd) {
						found = true
						break
					}
				}
				if !found {
					a.issue(audit.Violation, "command_evidence_mismatch", "evidence/"+e.Path, "", "saved command is not in report command inventory", a.file(owner))
				}
			}
		}
	}
	a.checkMaterials(v)
	a.checkPolicy(v)
	if !stale {
		a.checkInputSnapshots(v, completed)
	}
}

func (a *runAudit) checkMaterials(v checks.Evaluation) {
	switch a.profile.ID {
	case ProfileT1T2:
		policy, ok := a.bytes("evidence/quality-policy.json", true)
		probe, probeOK := a.bytes("evidence/context-probe.go.txt", true)
		if ok && probeOK {
			var compact bytes.Buffer
			if json.Compact(&compact, policy) != nil {
				a.issue(audit.Violation, "invalid_material", "evidence/quality-policy.json", "", "policy is not JSON")
				return
			}
			data := append(compact.Bytes(), probe...)
			if digest(data) != v.MaterialsSHA256 {
				a.issue(audit.Violation, "verifier_material_mismatch", "artifacts/report.json", "validation.materials_sha256", "archived verifier material digest differs", a.file("evidence/quality-policy.json"), a.file("evidence/context-probe.go.txt"))
			}
		}
	case ProfileT3:
		files, ok := a.tree("evidence/verifier-materials")
		if ok {
			hashes := map[string]string{}
			for p, sha := range files {
				hashes[strings.TrimSuffix(p, ".txt")] = sha
			}
			raw, _ := json.Marshal(hashes)
			if digest(raw) != v.MaterialsSHA256 {
				a.issue(audit.Violation, "verifier_material_mismatch", "artifacts/report.json", "validation.materials_sha256", "archived verifier materials differ", a.treeSources["evidence/verifier-materials"]...)
			}
		}
	default:
		a.pinnedTree("evidence/tool", v.MaterialsSHA256, a.file("artifacts/report.json"))
	}
	if a.profile.HostMaterialsSHA256 != "" {
		base := "host-materials/rpcx-" + a.profile.RepositoryName + "-modules"
		metadata, haveMetadata := a.bytes(base+".json", true)
		archive, haveArchive := a.bytes(base+".tar.gz", true)
		if haveMetadata && haveArchive {
			raw, _ := json.Marshal(map[string]string{"modules.json": digest(metadata), "modules.tar.gz": digest(archive)})
			if digest(raw) != a.profile.HostMaterialsSHA256 {
				a.issue(audit.Violation, "host_material_mismatch", base+".json", "", "archived module material differs from profile", a.file(base+".tar.gz"))
			}
		}
	}
}

func (a *runAudit) checkInputSnapshots(v checks.Evaluation, completed bool) {
	var snapshots map[string]map[string]string
	if !a.read("evidence/input-snapshots.json", &snapshots, 0, completed) {
		return
	}
	expected := map[string]bool{}
	original := filepath.Join(a.ref.OriginalRoot, filepath.FromSlash(a.ref.Path))
	names := append([]string{a.profile.RepositoryName}, sortedKeys(a.profile.Dependencies)...)
	for _, prefix := range []string{"baseline", "workspace"} {
		for _, name := range names {
			dir := prefix + "/" + name
			files, ok := a.tree(dir)
			if !ok {
				continue
			}
			key := filepath.Join(original, filepath.FromSlash(dir))
			expected[key] = true
			if !reflect.DeepEqual(snapshots[key], files) {
				a.issue(audit.Violation, "evidence_snapshot_mismatch", "evidence/input-snapshots.json", "", "saved input snapshot differs from its archived source", a.treeSources[dir]...)
			}
		}
	}
	for key := range snapshots {
		if !expected[key] {
			a.issue(audit.Violation, "evidence_snapshot_mismatch", "evidence/input-snapshots.json", "", "snapshot has an unexpected source identity")
		}
	}
	for _, entry := range []struct{ dir, sha string }{{"evidence/baseline/" + a.profile.RepositoryName, v.BaselineSHA256}, {"evidence/candidate/" + a.profile.RepositoryName, v.CandidateSHA256}} {
		a.pinnedTree(entry.dir, entry.sha, a.file("artifacts/report.json"))
	}
}

func (a *runAudit) checkPolicy(v checks.Evaluation) {
	name := "evidence/policy.json"
	if a.profile.ID == ProfileT1T2 || a.profile.ID == ProfileT3 {
		name = "evidence/quality-policy.json"
	}
	var policy quality.Policy
	if !a.read(name, &policy, 0, true) {
		return
	}
	if policy.SpecSHA256 != v.SpecSHA256 || len(policy.Requirements) == 0 {
		a.issue(audit.Violation, "policy_binding_mismatch", name, "", "archived policy has no requirements or uses another specification", a.file("artifacts/report.json"))
	}
	seen := map[string]bool{}
	for _, req := range policy.Requirements {
		if req.ID == "" || seen[req.ID] {
			a.issue(audit.Violation, "policy_binding_mismatch", name, "Requirements", "empty or duplicate archived requirement")
		}
		seen[req.ID] = true
		check, ok := v.Checks[req.ID]
		if !ok || !reflect.DeepEqual(check.RuleIDs, req.RuleIDs) {
			a.issue(audit.Violation, "check_inventory_mismatch", "artifacts/report.json", "validation.checks", "check or rule references differ from the archived policy", a.file(name))
		}
	}
}
