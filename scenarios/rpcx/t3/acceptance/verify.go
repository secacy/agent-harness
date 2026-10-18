package acceptance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"example.com/agent-harness/internal/execution"
)

type verifier struct {
	config Config
	report *Report
	cache  string
}

// Verify 每次创建独立目录；反例的预期失败算匹配，执行故障始终返回错误。
func Verify(ctx context.Context, config Config) (report *Report, err error) {
	if config.Backend == nil || (config.Backend.Info().Kind == execution.KindLocal) != config.TrustedLocal {
		return nil, errors.New("explicit execution backend and matching trusted-local option are required")
	}
	if config.Quick && !config.Development {
		return nil, errors.New("quick checks require development mode")
	}
	config.ScenarioDir, err = filepath.EvalSymlinks(config.ScenarioDir)
	if err != nil {
		return nil, err
	}
	config.ScenarioDir, err = filepath.Abs(config.ScenarioDir)
	if err != nil {
		return nil, err
	}
	config.RunsDir, err = resolveOutput(config.RunsDir)
	if err != nil {
		return nil, err
	}
	if overlaps(config.ScenarioDir, config.RunsDir) || overlaps(config.RunsDir, config.ScenarioDir) {
		return nil, errors.New("output overlaps scenario inputs")
	}
	inputs, err := snapshot(config.ScenarioDir)
	if err != nil {
		return nil, err
	}
	var cases manifest
	if err := readJSON(filepath.Join(config.ScenarioDir, "cases/manifest.yaml"), &cases); err != nil {
		return nil, err
	}
	if cases.SchemaVersion != 1 || cases.Spec != "specs/v0.2.0.md" || len(cases.Cases) != 4 {
		return nil, errors.New("unsupported T3 manifest")
	}
	ids := map[string]bool{}
	for _, c := range cases.Cases {
		if !safeChild(c.ID) || ids[c.ID] || c.Patch != "patches/"+c.ID+".patch" {
			return nil, errors.New("invalid or duplicate case path")
		}
		ids[c.ID] = true
	}
	if err := os.MkdirAll(config.RunsDir, 0755); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(config.RunsDir, "rpcx-t3-")
	if err != nil {
		return nil, err
	}
	mode := "frozen"
	if config.Development {
		mode = "development"
	}
	report = &Report{SchemaVersion: 1, Directory: dir, StartedAt: time.Now().UTC(), Mode: mode, Backend: config.Backend.Info(), Inputs: inputs, InputsSHA256: treeDigest(inputs), SlowEnabled: !config.Quick}
	v := &verifier{config: config, report: report, cache: filepath.Join(config.RunsDir, "go-cache")}
	defer func() {
		after, snapshotErr := snapshot(config.ScenarioDir)
		report.InputsUnchanged = snapshotErr == nil && treeDigest(after) == report.InputsSHA256
		if !report.InputsUnchanged {
			err = errors.Join(err, errors.New("scenario inputs changed during acceptance"), snapshotErr)
		}
		report.FinishedAt = time.Now().UTC()
		report.Accepted = err == nil && report.Matched && report.InputsUnchanged && report.FreezeMatched && report.SlowEnabled && mode == "frozen"
		if err != nil {
			report.Error = err.Error()
		}
		err = errors.Join(err, writeJSON(filepath.Join(dir, "report.json"), report))
	}()
	var frozen map[string]string
	freezeErr := readJSON(filepath.Join(config.ScenarioDir, "freeze.json"), &frozen)
	actual := map[string]string{}
	for path, hash := range inputs {
		if path != "freeze.json" {
			actual[path] = hash
		}
	}
	report.FreezeMatched = freezeErr == nil && treeDigest(frozen) == treeDigest(actual)
	if !config.Development && !report.FreezeMatched {
		return report, errors.New("frozen scenario does not match freeze.json")
	}
	if err := copyTree(config.ScenarioDir, filepath.Join(dir, "inputs/_snapshot"), false); err != nil {
		return report, err
	}
	// 版本查询不读取凭证；代码及所有场景输入的内容摘要另存报告。
	revision, revisionExit, revisionErr := v.command(ctx, execution.Local{}, "source-revision", config.ScenarioDir, "git", "rev-parse", "HEAD")
	if revisionErr != nil || revisionExit != 0 {
		return report, errors.Join(revisionErr, errors.New("source revision lookup failed"))
	}
	report.SourceRevision = strings.TrimSpace(string(revision))
	preflight := filepath.Join(dir, "baseline")
	if err := copyTree(filepath.Join(dir, "inputs/_snapshot/fixtures"), preflight, false); err != nil {
		return report, err
	}
	data, versionExit, err := v.command(ctx, config.Backend, "go-version", filepath.Join(preflight, "rpcx-v1"), "go", "version")
	if err != nil || versionExit != 0 {
		return report, errors.Join(err, errors.New("Go version lookup failed"))
	}
	report.GoVersion = strings.TrimSpace(string(data))
	for _, module := range []string{"rpcx-v1", "rpcx-v2", "demo-orders"} {
		data, exit, err := v.command(ctx, config.Backend, "baseline-"+module, filepath.Join(preflight, module), "go", "test", "-json", "-count=1", "-timeout=30s", "./...")
		if err != nil {
			return report, err
		}
		required := businessTests()
		if module == "rpcx-v1" {
			required = []string{"example.com/rpcx/TestCall", "example.com/rpcx/TestUnknownAddress", "example.com/rpcx/TestDefaultDeadlineAndRelease"}
		}
		if module == "rpcx-v2" {
			required = []string{"example.com/rpcx/v2/TestCallContext", "example.com/rpcx/v2/TestCallAlreadyCancelled", "example.com/rpcx/v2/TestUnknownAddress", "example.com/rpcx/v2/TestNoImplicitDeadline"}
		}
		check, parseErr := testCheck(data, exit, required)
		if parseErr != nil || check.Status != "pass" || exit != 0 {
			return report, fmt.Errorf("baseline %s did not pass: %w", module, errors.Join(parseErr, errors.New(check.Status)))
		}
	}
	if !config.Quick {
		module := filepath.Join(preflight, "rpcx-v1")
		if err := install(filepath.Join(dir, "inputs/_snapshot/oracle/v1_expiry_test.go.txt"), filepath.Join(module, "reference_expiry_test.go")); err != nil {
			return report, err
		}
		data, exit, err := v.command(ctx, config.Backend, "v1-expiry", module, "go", "test", "-json", "-count=1", "-timeout=90s", "-run=^TestReferenceV1Expiry$", "./...")
		if err != nil {
			return report, err
		}
		check, err := testCheck(data, exit, []string{"example.com/rpcx/TestReferenceV1Expiry"})
		report.V1Expiry = &check
		if err != nil {
			return report, err
		}
		if check.Status != "pass" {
			return report, fmt.Errorf("v1 actual expiry: %s", check.Status)
		}
	}
	for _, c := range cases.Cases {
		report.Cases = append(report.Cases, CaseResult{ID: c.ID})
		if err := v.verifyCase(ctx, c, &report.Cases[len(report.Cases)-1]); err != nil {
			return report, err
		}
	}
	// 所有实际结果已产生，再与 manifest 中的预期比较。
	report.Matched = true
	for i, c := range cases.Cases {
		got := &report.Cases[i]
		wantRules := slices.Clone(c.Expected.Rules)
		slices.Sort(wantRules)
		got.ExpectationMatched = got.Build.Status == c.Expected.Build && got.Business.Status == c.Expected.Business && got.Reference.Status == c.Expected.Reference && slices.Equal(got.Reference.Rules, wantRules)
		for _, name := range c.Expected.RequiredFailures {
			got.ExpectationMatched = got.ExpectationMatched && got.Reference.Tests[name] == "fail"
		}
		if !config.Quick && got.Reference.Status == "pass" {
			got.ExpectationMatched = got.ExpectationMatched && got.Slow != nil && got.Slow.Status == "pass"
		}
		report.Matched = report.Matched && got.ExpectationMatched
	}
	return report, nil
}

func (v *verifier) verifyCase(ctx context.Context, c scenarioCase, result *CaseResult) error {
	candidateRoot := filepath.Join(v.report.Directory, c.ID, "workspace")
	if err := copyTree(filepath.Join(v.report.Directory, "inputs/_snapshot/fixtures"), candidateRoot, false); err != nil {
		return err
	}
	candidate := filepath.Join(candidateRoot, "demo-orders")
	patch := filepath.Join(v.report.Directory, "inputs/_snapshot/cases", c.Patch)
	if err := v.apply(ctx, c.ID, candidate, patch); err != nil {
		return err
	}
	before, err := snapshot(candidateRoot)
	if err != nil {
		return err
	}
	baseline, err := snapshot(filepath.Join(v.report.Directory, "inputs/_snapshot/fixtures"))
	if err != nil {
		return err
	}
	for path, hash := range before {
		if !strings.HasPrefix(path, "demo-orders/") || strings.HasPrefix(path, "demo-orders/internal/pb/") {
			if baseline[path] != hash {
				return fmt.Errorf("patch changed protected input: %s", path)
			}
		}
	}
	for path, hash := range baseline {
		if !strings.HasPrefix(path, "demo-orders/") || strings.HasPrefix(path, "demo-orders/internal/pb/") {
			if before[path] != hash {
				return fmt.Errorf("patch removed protected input: %s", path)
			}
		}
	}
	candidateFiles, err := snapshot(candidate)
	if err != nil {
		return err
	}
	result.CandidateSHA256 = treeDigest(candidateFiles)
	result.ProductionSHA256 = treeDigest(production(candidateFiles))
	replayRoot := filepath.Join(v.report.Directory, c.ID, "replay")
	if err := copyTree(filepath.Join(v.report.Directory, "inputs/_snapshot/fixtures"), replayRoot, false); err != nil {
		return err
	}
	if err := v.apply(ctx, c.ID+"-replay", filepath.Join(replayRoot, "demo-orders"), patch); err != nil {
		return err
	}
	replay, err := snapshot(replayRoot)
	if err != nil {
		return err
	}
	result.PatchReplayed = treeDigest(before) == treeDigest(replay)
	if !result.PatchReplayed {
		return errors.New("patch replay differs")
	}
	out := "/tmp/"
	if v.config.TrustedLocal {
		out = filepath.Join(v.report.Directory, c.ID, "build")
		if err := os.MkdirAll(out, 0755); err != nil {
			return err
		}
	}
	_, exit, err := v.command(ctx, v.config.Backend, c.ID+"-build", candidate, "go", "build", "-o", out+string(filepath.Separator), "./...")
	if err != nil {
		return err
	}
	result.Build.Status = "pass"
	if exit != 0 {
		result.Build.Status = "fail"
		return fmt.Errorf("candidate build failed: %s", c.ID)
	}
	data, exit, err := v.command(ctx, v.config.Backend, c.ID+"-business", candidate, "go", "test", "-json", "-count=1", "-timeout=30s", "./...")
	if err != nil {
		return err
	}
	result.Business, err = testCheck(data, exit, businessTests())
	if err != nil {
		return err
	}
	if result.Business.Status != "pass" || exit != 0 {
		return fmt.Errorf("candidate business tests did not pass: %s", c.ID)
	}
	referenceRoot := filepath.Join(v.report.Directory, c.ID, "reference")
	if err := copyTree(candidateRoot, referenceRoot, true); err != nil {
		return err
	}
	reference := filepath.Join(referenceRoot, "demo-orders")
	referenceFiles, err := snapshot(reference)
	if err != nil {
		return err
	}
	result.ReferenceProductionSHA256 = treeDigest(referenceFiles)
	if result.ReferenceProductionSHA256 != result.ProductionSHA256 {
		return errors.New("reference production differs from candidate")
	}
	if err := install(filepath.Join(v.report.Directory, "inputs/_snapshot/oracle/deadline_test.go.txt"), filepath.Join(reference, "cmd/server/reference_deadline_test.go")); err != nil {
		return err
	}
	if err := install(filepath.Join(v.report.Directory, "inputs/_snapshot/oracle/expiry_test.go.txt"), filepath.Join(reference, "cmd/server/reference_expiry_test.go")); err != nil {
		return err
	}
	trustedBefore, err := snapshot(referenceRoot)
	if err != nil {
		return err
	}
	data, exit, err = v.command(ctx, v.config.Backend, c.ID+"-reference", reference, "go", "test", "-json", "-count=1", "-timeout=30s", "-run=^TestReference(Boundary|Request|ServiceError)$", "./cmd/server")
	if err != nil {
		return err
	}
	result.Reference, err = testCheck(data, exit, referenceTests())
	if err != nil {
		return err
	}
	// 慢检查只运行快速参考检查全通过的候选，不从 case 名称读取正确性。
	if !v.config.Quick && result.Reference.Status == "pass" {
		data, exit, err = v.command(ctx, v.config.Backend, c.ID+"-expiry", reference, "go", "test", "-json", "-count=1", "-timeout=90s", "-parallel=2", "-run=^TestReferenceExpiry$", "./cmd/server")
		if err != nil {
			return err
		}
		check, parseErr := testCheck(data, exit, []string{"example.com/demo-orders/cmd/server/TestReferenceExpiry/Place", "example.com/demo-orders/cmd/server/TestReferenceExpiry/Cancel"})
		result.Slow = &check
		if parseErr != nil {
			return parseErr
		}
	}
	trustedAfter, err := snapshot(referenceRoot)
	if err != nil {
		return err
	}
	after, err := snapshot(candidateRoot)
	if err != nil {
		return err
	}
	result.InputsUnchanged = treeDigest(before) == treeDigest(after) && treeDigest(trustedBefore) == treeDigest(trustedAfter)
	if !result.InputsUnchanged {
		return errors.New("candidate or reference inputs changed")
	}
	return nil
}

func (v *verifier) command(parent context.Context, backend execution.Executor, label, dir string, args ...string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	mounts := []string{dir}
	if filepath.Base(dir) == "demo-orders" {
		for _, name := range []string{"rpcx-v1", "rpcx-v2"} {
			mounts = append(mounts, filepath.Join(filepath.Dir(dir), name))
		}
	}
	started := time.Now()
	result, err := backend.Run(ctx, execution.Request{Args: args, Directory: dir, ReadOnlyDirs: mounts, CacheDir: v.cache})
	meta := Command{Label: label, Args: args, Directory: dir, Stdout: label + ".stdout.log", Stderr: label + ".stderr.log", ExitCode: result.ExitCode, DurationMS: time.Since(started).Milliseconds(), ContainerID: result.ContainerID}
	err = errors.Join(err, os.WriteFile(filepath.Join(v.report.Directory, meta.Stdout), result.Stdout, 0644), os.WriteFile(filepath.Join(v.report.Directory, meta.Stderr), result.Stderr, 0644))
	if err != nil {
		meta.Error = err.Error()
	}
	v.report.Commands = append(v.report.Commands, meta)
	return result.Stdout, result.ExitCode, err
}

func (v *verifier) apply(ctx context.Context, label, candidate, patch string) error {
	for i, args := range [][]string{{"git", "apply", "--check", patch}, {"git", "apply", patch}} {
		_, exit, err := v.command(ctx, execution.Local{}, fmt.Sprintf("%s-patch-%d", label, i), candidate, args...)
		if err != nil {
			return err
		}
		if exit != 0 {
			return fmt.Errorf("patch application failed: %s", label)
		}
	}
	return nil
}

// 输出路径解析现有祖先，避免符号链接使运行目录落入只读场景。
func resolveOutput(path string) (string, error) {
	if path == "" {
		return "", errors.New("output directory is required")
	}
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
	resolved, err = resolveOutput(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(abs)), nil
}
