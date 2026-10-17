package checks

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"example.com/agent-harness/internal/execution"
)

// SupportedSpecSHA256 binds these checks to the frozen v0.1.0 specification.
// Supporting a different specification requires an explicit verifier change.
const SupportedSpecSHA256 = "330e439ab69a9c84dbb88343ef5ccc430eeec357bb3f54e01dcad83ce860e251"

// EvaluationRequest describes an already prepared rpcx T1/T2 candidate.
// BaselineDir and CandidateDir are demo-orders modules with adjacent rpcx-v1
// and rpcx-v2 directories. EvidenceDir must be new or empty and outside inputs.
// CacheDir is optional; its default is EvidenceDir/go-cache. No Run is created.
// Relative paths are resolved against the caller's current working directory.
type EvaluationRequest struct {
	BaselineDir  string
	CandidateDir string
	SpecPath     string
	EvidenceDir  string
	CacheDir     string
	Backend      execution.Executor // 为空时仅用于原有可信本地夹具检查。
}

// Evaluation contains actual evidence, never expected case outcomes.
// A non-nil error leaves Verdict empty; completed checks remain available.
type Evaluation struct {
	SpecVersion     string           `json:"spec_version"`
	SpecSHA256      string           `json:"spec_sha256"`
	BaselineSHA256  string           `json:"baseline_sha256"`
	CandidateSHA256 string           `json:"candidate_sha256"`
	Checks          map[string]Check `json:"checks"`
	Commands        []Command        `json:"commands"`
	Verdict         string           `json:"verdict"`
}

// Evaluate validates one prepared candidate without reading a manifest, applying
// patches or comparing expectations. It runs trusted baseline tests itself and
// executes all commands and probes in verification copies under EvidenceDir.
// It does not persist a final report: that belongs to its caller.
func Evaluate(ctx context.Context, req EvaluationRequest) (result Evaluation, err error) {
	result.Checks = make(map[string]Check, len(checkNames))
	for _, name := range checkNames {
		result.Checks[name] = Check{"not_run", "not executed"}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if req, err = normalizeRequest(req); err != nil {
		return result, err
	}
	spec, err := os.ReadFile(req.SpecPath)
	if err != nil {
		return result, fmt.Errorf("read specification: %w", err)
	}
	result.SpecSHA256 = digest(spec)
	if result.SpecSHA256 != SupportedSpecSHA256 {
		return result, fmt.Errorf("unsupported rpcx specification: SHA-256 %s", result.SpecSHA256)
	}
	result.SpecVersion = "v0.1.0"

	// These are input snapshots, not executable expectations supplied by a case.
	inputs := map[string]map[string]string{}
	for _, module := range []string{req.BaselineDir, req.CandidateDir} {
		for _, path := range []string{module, filepath.Join(filepath.Dir(module), "rpcx-v1"), filepath.Join(filepath.Dir(module), "rpcx-v2")} {
			files, err := snapshotContext(ctx, path)
			if err != nil {
				return result, fmt.Errorf("snapshot %s: %w", path, err)
			}
			inputs[path] = files
		}
	}
	result.BaselineSHA256 = treeDigest(inputs[req.BaselineDir])
	result.CandidateSHA256 = treeDigest(inputs[req.CandidateDir])
	defer func() {
		// Even on cancellation, do not publish evidence for mutated input code.
		afterSpec, specErr := os.ReadFile(req.SpecPath)
		if specErr != nil || digest(afterSpec) != result.SpecSHA256 {
			err = errors.Join(err, fmt.Errorf("specification changed during verification"))
		}
		for path, before := range inputs {
			after, checkErr := snapshot(path)
			if checkErr != nil || treeDigest(before) != treeDigest(after) {
				err = errors.Join(err, fmt.Errorf("input changed during verification: %s", path))
			}
		}
		if err != nil {
			result.Verdict = ""
		}
	}()
	if err := prepareEvidence(req); err != nil {
		return result, err
	}
	if err := os.WriteFile(filepath.Join(req.EvidenceDir, "spec.md"), spec, 0644); err != nil {
		return result, err
	}
	baseline := filepath.Join(req.EvidenceDir, "baseline", "demo-orders")
	candidate := filepath.Join(req.EvidenceDir, "candidate", "demo-orders")
	// A faulty SDK copy is rejected, but commands use trusted SDKs so the
	// candidate cannot change its own acceptance behavior through the backend.
	sdkChanged := false
	for _, sdk := range []string{"rpcx-v1", "rpcx-v2"} {
		trusted := filepath.Join(filepath.Dir(req.BaselineDir), sdk)
		provided := filepath.Join(filepath.Dir(req.CandidateDir), sdk)
		if treeDigest(inputs[trusted]) != treeDigest(inputs[provided]) {
			sdkChanged = true
		}
	}
	for _, pair := range [][2]string{{req.BaselineDir, baseline}, {req.CandidateDir, candidate}} {
		if err := copyLayout(ctx, pair[0], pair[1], filepath.Dir(req.BaselineDir)); err != nil {
			return result, err
		}
	}
	run := func(dir, label string, args ...string) ([]byte, Command, error) {
		meta, data, err := commandOn(ctx, req.Backend, req.CacheDir, dir, filepath.Join(req.EvidenceDir, label+".log"), args...)
		result.Commands = append(result.Commands, meta)
		return data, meta, err
	}
	data, baselineRun, err := run(baseline, "baseline-tests", "go", "test", "-json", "-count=1", "-timeout=30s", "./...")
	if err != nil {
		return result, fmt.Errorf("baseline tests: %w", err)
	}
	if baselineRun.ExitCode != 0 {
		return result, fmt.Errorf("baseline tests failed: see %s", baselineRun.Log)
	}
	baselineEvents, err := testEvents(data)
	if err != nil {
		return result, fmt.Errorf("baseline test output: %w", err)
	}
	baselineNames, err := testNames(baseline)
	if err != nil {
		return result, err
	}
	if len(baselineNames) == 0 {
		return result, fmt.Errorf("trusted baseline has no tests")
	}
	for name := range baselineNames {
		if baselineEvents[name] != "pass" {
			return result, fmt.Errorf("baseline test not passed: %s", name)
		}
	}
	result.Checks["protected_paths"] = protectedCheck(baseline, candidate)
	dep, _, err := dependencyCheck(candidate, run)
	if err != nil {
		return result, err
	}
	result.Checks["dependencies"] = dep
	if sdkChanged {
		result.Checks["dependencies"] = Check{"fail", "DEP-001: SDK fixture changed"}
	}
	buildDir := filepath.Join(req.EvidenceDir, "build")
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return result, err
	}
	outputDir := buildDir
	if req.Backend.Info().Kind == execution.KindDocker {
		outputDir = "/tmp/"
	}
	_, build, err := run(candidate, "build", "go", "build", "-o", outputDir, "./...")
	if err != nil {
		return result, err
	}
	result.Checks["build"] = Check{"pass", "all candidate packages build"}
	if build.ExitCode != 0 {
		result.Checks["build"] = Check{"fail", "candidate build did not complete successfully"}
		for _, name := range []string{"baseline_tests", "test_preservation", "context_propagation"} {
			result.Checks[name] = Check{"blocked", "candidate build prerequisite failed"}
		}
	} else if err := evaluateTests(ctx, &result, baselineNames, baselineEvents, candidate, run); err != nil {
		return result, err
	}
	for _, module := range []string{baseline, candidate} {
		want := result.BaselineSHA256
		if module == candidate {
			want = result.CandidateSHA256
		}
		files, err := snapshotContext(ctx, module)
		if err != nil {
			return result, err
		}
		if treeDigest(files) != want {
			return result, fmt.Errorf("verification copy changed during checks; evidence is stale: %s", module)
		}
		for _, sdk := range []string{"rpcx-v1", "rpcx-v2"} {
			files, err := snapshotContext(ctx, filepath.Join(filepath.Dir(module), sdk))
			if err != nil {
				return result, err
			}
			if treeDigest(files) != treeDigest(inputs[filepath.Join(filepath.Dir(req.BaselineDir), sdk)]) {
				return result, fmt.Errorf("SDK verification copy changed during checks: %s", sdk)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	result.Verdict = verdict(result.Checks)
	return result, nil
}

func evaluateTests(ctx context.Context, result *Evaluation, baselineNames map[string]bool, baselineEvents map[string]string, candidate string, run func(string, string, ...string) ([]byte, Command, error)) error {
	data, tests, err := run(candidate, "business-tests", "go", "test", "-json", "-count=1", "-timeout=30s", "./...")
	if err != nil {
		return err
	}
	states, err := testEvents(data)
	if err != nil {
		return fmt.Errorf("business test output: %w", err)
	}
	status := "pass"
	if tests.ExitCode != 0 {
		status = "fail"
	}
	result.Checks["baseline_tests"] = Check{status, "candidate business test command; see business-tests.log"}
	names, err := testNames(candidate)
	if err != nil {
		return err
	}
	result.Checks["test_preservation"] = preservationCheck(baselineNames, baselineEvents, names, states)
	probe, err := probes.ReadFile("probes/context_test.go.txt")
	if err != nil {
		return err
	}
	probePath := filepath.Join(candidate, "cmd/server/harness_context_test.go")
	if _, err := os.Lstat(probePath); !os.IsNotExist(err) {
		return fmt.Errorf("candidate occupies trusted probe path")
	}
	if err := os.WriteFile(probePath, probe, 0644); err != nil {
		return err
	}
	data, probeRun, runErr := run(candidate, "context-probes", "go", "test", "-json", "-count=1", "-timeout=30s", "-run", "^TestHarnessRequestContext$", "./cmd/server")
	removeErr := os.Remove(probePath)
	if err := errors.Join(runErr, removeErr); err != nil {
		return err
	}
	result.Checks["context_propagation"] = probeCheck(data, probeRun)
	if err := contextPolicy(candidate); err != nil {
		result.Checks["context_propagation"] = Check{"fail", err.Error()}
	}
	return ctx.Err()
}

func copyLayout(ctx context.Context, source, dest, trustedSDKDir string) error {
	if err := copyTreeContext(ctx, source, dest); err != nil {
		return err
	}
	for _, sdk := range []string{"rpcx-v1", "rpcx-v2"} {
		if err := copyTreeContext(ctx, filepath.Join(trustedSDKDir, sdk), filepath.Join(filepath.Dir(dest), sdk)); err != nil {
			return err
		}
	}
	return nil
}

func normalizeRequest(req EvaluationRequest) (EvaluationRequest, error) {
	if req.Backend == nil {
		req.Backend = execution.Local{}
	}
	for _, path := range []*string{&req.BaselineDir, &req.CandidateDir, &req.SpecPath, &req.EvidenceDir} {
		if *path == "" {
			return req, fmt.Errorf("baseline, candidate, specification and evidence paths are required")
		}
		abs, err := canonicalPath(*path)
		if err != nil {
			return req, err
		}
		*path = abs
	}
	if req.CacheDir == "" {
		req.CacheDir = filepath.Join(req.EvidenceDir, "go-cache")
	}
	var err error
	req.CacheDir, err = canonicalPath(req.CacheDir)
	if err != nil {
		return req, err
	}
	inputs := []string{req.BaselineDir, req.CandidateDir, req.SpecPath}
	for _, module := range []string{req.BaselineDir, req.CandidateDir} {
		for _, sdk := range []string{"rpcx-v1", "rpcx-v2"} {
			path, err := canonicalPath(filepath.Join(filepath.Dir(module), sdk))
			if err != nil {
				return req, err
			}
			inputs = append(inputs, path)
		}
	}
	for _, output := range []string{req.EvidenceDir, req.CacheDir} {
		for _, input := range inputs {
			if within(output, input) || within(input, output) {
				return req, fmt.Errorf("output overlaps input: %s and %s", output, input)
			}
		}
	}
	return req, nil
}

func within(path, parent string) bool {
	rel, err := filepath.Rel(parent, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Resolve existing symlink ancestors too, so an output alias cannot write into
// an input. This is a local API guard, not an adversarial filesystem sandbox.
func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(abs)
	if parent == abs {
		return "", err
	}
	resolved, err = canonicalPath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(abs)), nil
}

func prepareEvidence(req EvaluationRequest) error {
	if err := os.MkdirAll(req.EvidenceDir, 0755); err != nil {
		return err
	}
	entries, err := os.ReadDir(req.EvidenceDir)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("evidence directory must be empty: %s", req.EvidenceDir)
	}
	return os.MkdirAll(req.CacheDir, 0755)
}
