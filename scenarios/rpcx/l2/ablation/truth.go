package ablation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/baseline"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
)

// TruthAssessment separates candidate correctness from delivery accuracy. It
// authenticates document structure and binding, not the human author's identity.
type TruthAssessment struct {
	Candidate       evaluation.Status `json:"candidate"`
	Delivery        evaluation.Status `json:"delivery"`
	ReferenceSHA256 string            `json:"reference_sha256"`
	ReviewSHA256    string            `json:"review_sha256"`
	TargetID        string            `json:"target_id"`
	RunID           string            `json:"run_id"`
	CandidateSHA256 string            `json:"candidate_sha256"`
}

// AssessTruth validates the original documents through the production reference
// consumer before aggregating only the two candidate review sections. The
// original review is never changed to manufacture a passing delivery item.
func AssessTruth(referenceJSON, reviewJSON []byte, formal bool) (TruthAssessment, error) {
	out := TruthAssessment{Candidate: evaluation.Inconclusive, Delivery: evaluation.Inconclusive}
	checked, err := evaluation.Assess(referenceJSON, reviewJSON)
	if err != nil {
		return out, err
	}
	var report evaluation.Report
	var review evaluation.Review
	if err = Decode(referenceJSON, &report); err != nil {
		return out, err
	}
	if err = Decode(reviewJSON, &review); err != nil {
		return out, err
	}
	if formal && review.Kind != "candidate_review" {
		return out, errors.New("development calibration cannot enter formal truth")
	}
	out.ReferenceSHA256, out.ReviewSHA256 = checked.ReportSHA256, checked.ReviewSHA256
	out.TargetID, out.RunID, out.CandidateSHA256 = report.TargetID, report.RunID, report.CandidateSHA256
	if !report.InputsUnchanged {
		return out, nil
	}
	out.Candidate = report.Automatic
	for _, section := range []string{"test_preservation", "change_coverage"} {
		status := review.Items[section].Status
		if status == evaluation.Fail {
			out.Candidate = evaluation.Fail
		} else if status == evaluation.Inconclusive && out.Candidate == evaluation.Pass {
			out.Candidate = evaluation.Inconclusive
		}
	}
	out.Delivery = review.Items["delivery_evidence"].Status
	return out, nil
}

// AuthenticateReference verifies the saved reference and its frozen inputs. The
// returned raw bytes are the exact document to bind to a review. A missing or
// damaged artifact is an evidence error, never a candidate correctness failure.
// This does not rerun commands or authenticate the claimed human review.
func AuthenticateReference(root string, expected Task, candidateSHA, targetID, runID string) (evaluation.Report, []byte, error) {
	var report evaluation.Report
	raw, err := referenceFile(root, "reference.json")
	if err != nil {
		return report, nil, err
	}
	if err = Decode(raw, &report); err != nil {
		return report, nil, err
	}
	versions := map[string]string{"goddd": evaluation.Version, "translation": evaluation.TranslationVersion, "workouts": evaluation.WorkoutsVersion, "ghcli": evaluation.GHCLIVersion}
	if report.SchemaVersion != 1 || versions[expected.ID] == "" || report.Version != versions[expected.ID] || report.TargetID != targetID || report.RunID != runID || !digestOK(candidateSHA) || report.CandidateSHA256 != candidateSHA || report.BaselineSHA256 != expected.Input.Repository.SHA256 || report.SpecSHA256 != expected.Input.Specification.SHA256 || report.ReferenceVersion != expected.ReferenceVersion || report.ReferenceSHA256 != expected.ReferenceSHA256 || report.ToolSHA256 != expected.ReferenceToolSHA256 {
		return report, nil, errors.New("reference does not match frozen task and candidate")
	}
	// A pending review only invokes the existing schema/configuration validator;
	// it is neither a persisted review nor a source of a passing truth label.
	pending := evaluation.Review{SchemaVersion: 1, Kind: "candidate_review", ReportSHA256: gate.Digest(raw), TargetID: targetID, RunID: runID, CandidateSHA256: candidateSHA, Items: map[string]evaluation.ReviewItem{}}
	for _, name := range []string{"test_preservation", "change_coverage", "delivery_evidence"} {
		pending.Items[name] = evaluation.ReviewItem{Status: evaluation.Inconclusive}
	}
	pendingRaw, _ := json.Marshal(pending)
	if _, err = evaluation.Assess(raw, pendingRaw); err != nil {
		return report, nil, err
	}
	// Frozen identities must correspond to the actually published reference,
	// rather than a self-consistent but unrelated material bundle.
	published, err := evaluation.PublishedIdentity(expected.ReferenceVersion)
	if err != nil || published.SpecSHA256 != report.SpecSHA256 || published.ReferenceSHA256 != report.ReferenceSHA256 || published.ToolSHA256 != report.ToolSHA256 {
		return report, nil, errors.New("reference differs from published materials")
	}
	tools, err := referenceTree(root, "materials/tools")
	if err != nil || gate.TreeHash(tools) != report.ToolSHA256 {
		return report, nil, errors.New("reference tool material mismatch")
	}
	spec, err := referenceFile(root, "materials/spec.md")
	if err != nil || gate.Digest(spec) != report.SpecSHA256 {
		return report, nil, errors.New("reference specification mismatch")
	}
	data, err := referenceTree(root, "materials/reference")
	if err != nil {
		return report, nil, err
	}
	var freeze struct {
		SchemaVersion int               `json:"schema_version"`
		Version       string            `json:"version"`
		SpecSHA256    string            `json:"spec_sha256"`
		Files         map[string]string `json:"files"`
	}
	if gate.Digest(data["freeze.json"]) != report.ReferenceSHA256 || Decode(data["freeze.json"], &freeze) != nil || freeze.SchemaVersion != 1 || freeze.Version != report.ReferenceVersion || freeze.SpecSHA256 != report.SpecSHA256 {
		return report, nil, errors.New("reference data identity mismatch")
	}
	hashes := gate.Hashes(data)
	delete(hashes, "freeze.json")
	if !maps.Equal(hashes, freeze.Files) {
		return report, nil, errors.New("reference data membership or bytes changed")
	}
	if err = authenticateLayouts(root, expected, report, data); err != nil {
		return report, nil, err
	}
	for name, hash := range report.Artifacts {
		if !digestOK(hash) {
			return report, nil, errors.New("invalid reference artifact digest")
		}
		b, e := referenceFile(root, name)
		if e != nil || gate.Digest(b) != hash {
			return report, nil, fmt.Errorf("reference artifact mismatch: %s", name)
		}
	}
	if len(report.Commands) == 0 {
		return report, nil, errors.New("reference command evidence missing")
	}
	logs := map[string]string{}
	seen := map[string]bool{}
	for _, command := range report.Commands {
		if !identifier.MatchString(command.ID) || seen[command.ID] {
			return report, nil, errors.New("invalid or duplicated reference command")
		}
		seen[command.ID] = true
		for _, name := range []string{command.Stdout, command.Stderr} {
			if !strings.HasPrefix(name, "logs/") || !digestOK(report.Artifacts[name]) || logs[name] != "" {
				return report, nil, errors.New("command log identity missing or duplicated")
			}
			logs[name] = report.Artifacts[name]
		}
	}
	if err = authenticateCommandCoverage(expected.ID, report, seen); err != nil {
		return report, nil, err
	}
	actualLogs, err := referenceTree(root, "logs")
	if err != nil {
		return report, nil, err
	}
	actual := map[string]string{}
	for name, b := range actualLogs {
		actual["logs/"+name] = gate.Digest(b)
	}
	if !maps.Equal(logs, actual) {
		return report, nil, errors.New("reference command/log membership mismatch")
	}
	if expected.ID == "ghcli" {
		if err = authenticateReferenceHost(root, report); err != nil {
			return report, nil, err
		}
	}
	return report, raw, nil
}

func authenticateLayouts(root string, expected Task, report evaluation.Report, data map[string][]byte) error {
	module := map[string]string{"goddd": "goddd", "translation": "translation", "workouts": "trainings", "ghcli": "ghcli"}[expected.ID]
	saved := map[string]map[string][]byte{}
	for _, layout := range []string{"baseline", "candidate", "production"} {
		files, err := referenceTree(root, layout)
		if err != nil {
			return err
		}
		saved[layout] = files
		for name, pin := range expected.Input.Dependencies {
			if gate.TreeHash(gate.Module(files, name)) != pin.SHA256 {
				return fmt.Errorf("reference %s dependency changed: %s", layout, name)
			}
		}
		for name := range files {
			first, _, _ := strings.Cut(name, "/")
			if first != module {
				if _, ok := expected.Input.Dependencies[first]; !ok {
					return errors.New("unexpected reference layout member")
				}
			}
		}
	}
	base := gate.Module(saved["baseline"], module)
	candidate := gate.Module(saved["candidate"], module)
	if gate.TreeHash(base) != expected.Input.Repository.SHA256 || gate.TreeHash(candidate) != report.CandidateSHA256 {
		return errors.New("reference baseline or candidate tree mismatch")
	}
	prod := map[string][]byte{}
	for name, b := range candidate {
		if !strings.HasSuffix(name, "_test.go") {
			prod[name] = b
		}
	}
	if gate.TreeHash(prod) != report.ProductionSHA256 {
		return errors.New("reference production identity mismatch")
	}
	switch expected.ID {
	case "goddd":
		prod["server/l2_reference_test.go"] = data["routes_test.go.txt"]
	case "translation":
		prod["internal/repo/webapi/l2_reference_test.go"] = data["translation_test.go.txt"]
	case "workouts":
		prod["adapters/l2_reference_test.go"] = data["workouts_test.go.txt"]
	case "ghcli":
		prod["pkg/cmd/release/delete/reference_test.go"] = data["probes_test.go.txt"]
		prod["pkg/cmd/release/delete/reference_command_expiry_test.go"] = data["command_expiry_test.go.txt"]
		prod["pkg/cmd/release/delete/reference_response_return_test.go"] = data["response_return_test.go.txt"]
		original, err := referenceTree(root, "original")
		if err != nil {
			return err
		}
		want := map[string][]byte{}
		for name, b := range candidate {
			if !strings.HasSuffix(name, "_test.go") {
				want[name] = b
			}
		}
		for name, b := range base {
			if strings.HasSuffix(name, "_test.go") {
				want[name] = b
			}
		}
		if !maps.Equal(gate.Hashes(want), gate.Hashes(gate.Module(original, module))) {
			return errors.New("reference original-test copy mismatch")
		}
		for name, pin := range expected.Input.Dependencies {
			if gate.TreeHash(gate.Module(original, name)) != pin.SHA256 {
				return errors.New("reference original dependency mismatch")
			}
		}
		for name := range original {
			first, _, _ := strings.Cut(name, "/")
			if first != module {
				if _, ok := expected.Input.Dependencies[first]; !ok {
					return errors.New("unexpected original reference layout member")
				}
			}
		}
	}
	if !maps.Equal(gate.Hashes(prod), gate.Hashes(gate.Module(saved["production"], module))) {
		return errors.New("reference production/probe copy mismatch")
	}
	return nil
}

func referenceFile(root, name string) ([]byte, error) {
	if !filepath.IsLocal(name) || filepath.ToSlash(filepath.Clean(name)) != name {
		return nil, errors.New("reference path is not local and canonical")
	}
	at := root
	parts := append([]string{""}, strings.Split(name, "/")...)
	for i, part := range parts {
		if part != "" {
			at = filepath.Join(at, part)
		}
		info, err := os.Lstat(at)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("reference paths cannot contain symlinks")
		}
		if i == len(parts)-1 {
			if !info.Mode().IsRegular() || info.Size() > 16<<20 {
				return nil, errors.New("invalid reference file")
			}
		} else if !info.IsDir() {
			return nil, errors.New("reference path parent is not a directory")
		}
	}
	return os.ReadFile(at)
}

func referenceTree(root, name string) (map[string][]byte, error) {
	if !filepath.IsLocal(name) || filepath.ToSlash(filepath.Clean(name)) != name {
		return nil, errors.New("invalid reference tree path")
	}
	// Validate every directory component, including the tree root itself.
	at := root
	for _, part := range append([]string{""}, strings.Split(name, "/")...) {
		if part != "" {
			at = filepath.Join(at, part)
		}
		info, err := os.Lstat(at)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("reference tree contains non-directory or symlink")
		}
	}
	files := map[string][]byte{}
	total := 0
	err := filepath.WalkDir(at, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		b, err := referenceFile(root, filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		total += len(b)
		if len(files) >= 10000 || total > 64<<20 {
			return errors.New("reference tree exceeds read limits")
		}
		rel, err = filepath.Rel(at, path)
		if err == nil {
			files[filepath.ToSlash(rel)] = b
		}
		return err
	})
	return files, err
}

// Required commands follow the frozen evaluator's actual stopping points. In
// particular, deleting both a command and its log/index entries cannot turn a
// successful check into an evidence-free pass.
func authenticateCommandCoverage(task string, report evaluation.Report, seen map[string]bool) error {
	require := func(names ...string) error {
		for _, name := range names {
			if !seen[name] {
				return fmt.Errorf("required reference command missing: %s", name)
			}
		}
		return nil
	}
	if err := require("go-version", "baseline-mod", "candidate-mod"); err != nil {
		return err
	}
	configs := map[string]map[string]evaluation.Observation{"": report.Checks}
	if task == "translation" {
		configs = map[string]map[string]evaluation.Observation{}
		for id, config := range report.Configurations {
			configs[id+"-"] = config.Checks
		}
	}
	for prefix, checks := range configs {
		if task == "translation" && checks["selection"].Status != evaluation.Inconclusive {
			if err := require(prefix+"baseline-selection", prefix+"candidate-selection"); err != nil {
				return err
			}
		}
		if checks["build"].Status != evaluation.Inconclusive {
			if err := require(prefix+"baseline-build", prefix+"candidate-build"); err != nil {
				return err
			}
		}
		if checks["candidate_tests"].Status != evaluation.Inconclusive {
			if err := require(prefix + "candidate-tests"); err != nil {
				return err
			}
			if task != "goddd" {
				if err := require(prefix + "baseline-tests"); err != nil {
					return err
				}
			}
			if task == "ghcli" {
				if err := require("original-tests"); err != nil {
					return err
				}
			}
		}
		if checks["behavior"].Status != evaluation.Inconclusive {
			if err := require(prefix + "behavior"); err != nil {
				return err
			}
		}
		if checks["expiry"].Status != evaluation.Inconclusive {
			names := []string{prefix + "expiry"}
			if task == "workouts" {
				names = []string{"expiry-users", "expiry-trainer"}
			}
			if task == "ghcli" {
				names = []string{"expiry-TestReferenceActualDefaultExpiry", "expiry-TestReferenceCommandDefaultExpiry"}
			}
			if err := require(names...); err != nil {
				return err
			}
		}
	}
	if task == "workouts" {
		for section, observation := range report.LocalModules["common"].Checks {
			if observation.Status != evaluation.Inconclusive {
				if err := require("common-" + section); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func authenticateReferenceHost(root string, report evaluation.Report) error {
	host, e := l2.GHCLIMaterials()
	if e != nil {
		return e
	}
	hash, e := l2.HostMaterialsSHA256Version(host, baseline.GHCLIVersion)
	if e != nil || report.HostMaterialsSHA256 != hash {
		return errors.New("reference host identity mismatch")
	}
	required := map[string]bool{}
	for name, b := range host {
		leaf, ok := strings.CutPrefix(name, "dependencies/")
		if !ok {
			continue
		}
		artifact := "modules.bundle/" + leaf
		if leaf == "modules.json" {
			artifact = "modules.json"
		}
		required[artifact] = true
		saved, e := referenceFile(root, artifact)
		if e != nil || report.Artifacts[artifact] != gate.Digest(b) || gate.Digest(saved) != gate.Digest(b) {
			return fmt.Errorf("required reference host artifact missing or changed: %s", artifact)
		}
	}
	for name := range report.Artifacts {
		if strings.HasPrefix(name, "modules.bundle/") && !required[name] {
			return errors.New("unexpected reference host artifact")
		}
	}
	return nil
}
