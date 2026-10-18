package checks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"example.com/agent-harness/internal/execution"
	"example.com/agent-harness/internal/quality"
)

// SupportedSpecSHA256 将本检查集合绑定到冻结规范；支持新规范须显式修改验证器。
const SupportedSpecSHA256 = "330e439ab69a9c84dbb88343ef5ccc430eeec357bb3f54e01dcad83ce860e251"

// EvaluationRequest 接受已有 T1/T2 候选，两侧模块均需相邻的可信 SDK 布局。
// EvidenceDir 须在输入外且为空；缓存可选，默认位于 EvidenceDir/go-cache。
// 相对路径按当前目录解析，本接口不创建 Run。
type EvaluationRequest struct {
	BaselineDir  string
	CandidateDir string
	SpecPath     string
	EvidenceDir  string
	CacheDir     string
	Backend      execution.Executor // 为空时仅用于原有可信本地夹具检查。
}

// Evaluation 保存实际检查与证据；执行错误时 Verdict 为空，保留部分结果。
type Evaluation struct {
	SchemaVersion   int                `json:"schema_version"`
	VerifierVersion string             `json:"verifier_version"`
	MaterialsSHA256 string             `json:"materials_sha256"`
	Evidence        []quality.Evidence `json:"evidence"`
	Gaps            []quality.Gap      `json:"gaps"`
	SpecVersion     string             `json:"spec_version"`
	SpecSHA256      string             `json:"spec_sha256"`
	BaselineSHA256  string             `json:"baseline_sha256"`
	CandidateSHA256 string             `json:"candidate_sha256"`
	Checks          map[string]Check   `json:"checks"`
	Commands        []Command          `json:"commands"`
	Verdict         string             `json:"verdict"`
}

// Evaluate 在独立副本中运行基线、候选和可信探针，生成证据并聚合判定。
// 补丁应用、用例预期比较和最终报告持久化由调用方负责。
func Evaluate(ctx context.Context, req EvaluationRequest) (result Evaluation, err error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if spec, readErr := os.ReadFile(req.SpecPath); readErr == nil && digest(spec) == T3SpecSHA256 {
		return evaluateT3(ctx, req)
	}
	result.SchemaVersion = 2
	result.VerifierVersion = verifierVersion
	result.Checks = make(map[string]Check, len(checkNames))
	for _, name := range checkNames {
		result.Checks[name] = Check{Status: "not_run", Detail: "not executed"}
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

	// 输入快照独立采集，不使用用例提供的预期结果。
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
		// 即使被取消，也不能为验证过程中变化的输入发布质量判定。
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
			result.annotateChecks()
		}
	}()
	if err := prepareEvidence(req); err != nil {
		return result, err
	}
	if err := os.WriteFile(filepath.Join(req.EvidenceDir, "spec.md"), spec, 0644); err != nil {
		return result, err
	}
	policyJSON, err := json.Marshal(t1t2Policy())
	if err != nil {
		return result, err
	}
	probe, err := probes.ReadFile("probes/context_test.go.txt")
	if err != nil {
		return result, err
	}
	result.MaterialsSHA256 = digest(append(policyJSON, probe...))
	if err := result.saveEvidence(req.EvidenceDir, "quality-policy", "host_policy", "quality-policy.json", t1t2Policy()); err != nil {
		return result, err
	}
	if err := os.WriteFile(filepath.Join(req.EvidenceDir, "context-probe.go.txt"), probe, 0644); err != nil {
		return result, err
	}
	if err := result.addEvidence(req.EvidenceDir, "probe-material", "trusted_probe", "context-probe.go.txt"); err != nil {
		return result, err
	}
	if err := result.saveEvidence(req.EvidenceDir, "input-snapshots", "input_snapshots", "input-snapshots.json", inputs); err != nil {
		return result, err
	}
	baseline := filepath.Join(req.EvidenceDir, "baseline", "demo-orders")
	candidate := filepath.Join(req.EvidenceDir, "candidate", "demo-orders")
	// 候选 SDK 被修改时拒绝它；实际命令始终使用基线侧可信 SDK。
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
		if err == nil {
			err = result.addEvidence(req.EvidenceDir, label, "command_log", label+".log")
		}
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
		result.Checks["dependencies"] = Check{Status: "fail", Detail: "DEP-001: SDK fixture changed"}
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
	result.Checks["build"] = Check{Status: "pass", Detail: "all candidate packages build"}
	if build.ExitCode != 0 {
		result.Checks["build"] = Check{Status: "fail", Detail: "candidate build did not complete successfully"}
		for _, name := range []string{"baseline_tests", "test_preservation", "context_propagation"} {
			result.Checks[name] = Check{Status: "blocked", Detail: "candidate build prerequisite failed"}
		}
	} else if err := evaluateTests(ctx, &result, baselineNames, baselineEvents, candidate, req.EvidenceDir, run); err != nil {
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
	if err := result.decide(req.EvidenceDir); err != nil {
		return result, err
	}
	return result, nil
}

func evaluateTests(ctx context.Context, result *Evaluation, baselineNames map[string]bool, baselineEvents map[string]string, candidate, evidenceDir string, run func(string, string, ...string) ([]byte, Command, error)) error {
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
	result.Checks["baseline_tests"] = Check{Status: status, Detail: "candidate business test command; see business-tests.log"}
	names, err := testNames(candidate)
	if err != nil {
		return err
	}
	result.Checks["test_preservation"] = preservationCheck(baselineNames, baselineEvents, names, states)
	if err := result.saveEvidence(evidenceDir, "test-inventory", "test_inventory", "test-inventory.json", struct {
		BaselineNames   map[string]bool   `json:"baseline_names"`
		CandidateNames  map[string]bool   `json:"candidate_names"`
		BaselineEvents  map[string]string `json:"baseline_events"`
		CandidateEvents map[string]string `json:"candidate_events"`
	}{baselineNames, names, baselineEvents, states}); err != nil {
		return err
	}
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
	policyError := ""
	if err := contextPolicy(candidate); err != nil {
		policyError = err.Error()
		result.Checks["context_propagation"] = Check{Status: "fail", Detail: policyError}
	}
	if err := result.saveEvidence(evidenceDir, "context-policy", "structural_check", "context-policy.json", struct {
		Scope string `json:"scope"`
		Error string `json:"error,omitempty"`
	}{"limited context syntax check, not general data-flow analysis", policyError}); err != nil {
		return err
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

// 解析已有祖先的符号链接，避免输出别名写入输入；此检查不是文件系统沙箱。
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
