package evaluation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2"
)

// Evaluate 固定 Goddd 候选，在新的证据目录中执行参考检查并生成待复核模板。
// 返回的 Automatic 只代表自动检查；完整结论由独立填写的 Review 与 Assess 合成。
func Evaluate(ctx context.Context, req Request) (Report, error) {
	return evaluate(ctx, req, godddScenario())
}

// EvaluateTranslation 对同一候选执行两种必需构建配置，不接受调用方裁剪配置。
func EvaluateTranslation(ctx context.Context, req Request) (Report, error) {
	return evaluate(ctx, req, translationScenario())
}

// EvaluateWorkouts 验证取消回调、两个客户端和只读 common 模块。
func EvaluateWorkouts(ctx context.Context, req Request) (Report, error) {
	return evaluate(ctx, req, workoutsScenario())
}

func evaluate(ctx context.Context, req Request, scenario referenceScenario) (report Report, err error) {
	if req.TargetID == "" || req.RunID == "" || req.CandidateSHA256 == "" || req.Backend == nil {
		return report, errors.New("target, run, candidate digest and backend are required")
	}
	if req.Backend.Info().Kind != execution.KindDocker && !(req.TrustedLocal && req.Backend.Info().Kind == execution.KindLocal) {
		return report, errors.New("local reference execution requires explicit trust")
	}
	paths := []*string{&req.FixturesDir, &req.CandidateDir, &req.SpecPath, &req.EvidenceDir}
	for _, path := range paths {
		*path, err = gate.Canonical(*path)
		if err != nil {
			return report, err
		}
	}
	for i, a := range paths {
		for _, b := range paths[i+1:] {
			if gate.Overlap(*a, *b) {
				return report, errors.New("reference input and output paths overlap")
			}
		}
	}
	if err = ctx.Err(); err != nil {
		return report, err
	}
	if err = os.Mkdir(req.EvidenceDir, 0755); err != nil {
		return report, err
	}
	report = Report{SchemaVersion: 1, Version: scenario.version, TargetID: req.TargetID, RunID: req.RunID, CandidateSHA256: req.CandidateSHA256, Backend: req.Backend.Info(), StartedAt: time.Now().UTC(), Automatic: Inconclusive, Checks: map[string]Observation{}, Artifacts: map[string]string{}}
	for _, name := range automaticSectionsFor(scenario.version) {
		report.Checks[name] = Observation{Status: Inconclusive, Reason: "not executed"}
	}
	if scenario.module == "translation" {
		report.Configurations = map[string]ConfigurationReport{}
		for _, config := range scenario.configurations {
			report.Configurations[config.id] = newConfiguration(config)
		}
	}
	if scenario.module == "trainings" {
		report.LocalModules = map[string]ModuleReport{"common": {Checks: map[string]Observation{
			"build": {Status: Inconclusive, Reason: "not executed"}, "tests": {Status: Inconclusive, Reason: "not executed"},
		}}}
	}
	defer func() {
		if report.Configurations != nil {
			mergeConfigurations(&report)
		}
		if report.LocalModules != nil {
			mergeLocalModules(&report)
		}
		report.FinishedAt = time.Now().UTC()
		if err != nil {
			report.Error = err.Error()
		}
		if report.InputsUnchanged {
			report.Automatic = aggregate(report.Checks)
		} else {
			report.Automatic = Inconclusive
		}
		filename := filepath.Join(req.EvidenceDir, "reference.json")
		if saveErr := gate.WriteJSON(filename, report); saveErr != nil {
			report.Automatic = Inconclusive
			err = errors.Join(err, saveErr)
			return
		}
		data, readErr := os.ReadFile(filename)
		if readErr != nil {
			err = errors.Join(err, readErr)
			return
		}
		err = errors.Join(err, gate.WriteJSON(filepath.Join(req.EvidenceDir, "review-template.json"), reviewTemplate(report, gate.Digest(data))))
	}()
	data, err := scenario.data()
	if err != nil {
		return report, err
	}
	report.ReferenceSHA256 = gate.Digest(data["freeze.json"])
	report.ReferenceVersion = scenario.dataVersion
	tools, err := toolData()
	if err != nil {
		return report, err
	}
	report.ToolSHA256 = gate.TreeHash(tools)
	if err = gate.CopyFiles(filepath.Join(req.EvidenceDir, "materials/reference"), data); err != nil {
		return report, err
	}
	if err = gate.CopyFiles(filepath.Join(req.EvidenceDir, "materials/tools"), tools); err != nil {
		return report, err
	}
	fixtures, err := gate.ReadFiles(req.FixturesDir)
	if err != nil {
		return report, err
	}
	host, err := scenario.materials()
	if err != nil {
		return report, err
	}
	var release struct {
		Files map[string]string `json:"files"`
	}
	if err = json.Unmarshal(host["freeze.json"], &release); err != nil {
		return report, err
	}
	expected := map[string]string{}
	for name, hash := range release.Files {
		if rel, ok := strings.CutPrefix(name, "fixtures/"); ok {
			expected[rel] = hash
		}
	}
	if !maps.Equal(gate.Hashes(fixtures), expected) {
		return report, errors.New("fixture snapshot does not match reference release")
	}
	candidate, err := gate.ReadFiles(req.CandidateDir)
	if err != nil {
		return report, err
	}
	if gate.TreeHash(candidate) != req.CandidateSHA256 {
		return report, errors.New("candidate digest does not match request")
	}
	spec, err := os.ReadFile(req.SpecPath)
	if err != nil {
		return report, err
	}
	if gate.Digest(spec) != scenario.specSHA256 {
		return report, errors.New("unsupported reference specification")
	}
	report.SpecSHA256 = gate.Digest(spec)
	report.BaselineSHA256 = gate.TreeHash(gate.Module(fixtures, scenario.module))
	prod := production(candidate)
	report.ProductionSHA256 = gate.TreeHash(prod)
	// 原输入和执行副本均在运行后核对；任何漂移使整份参考失去有效绑定。
	snapshots := map[string]map[string][]byte{req.FixturesDir: fixtures, req.CandidateDir: candidate}
	defer func() {
		var changed error
		for root, before := range snapshots {
			after, e := gate.ReadFiles(root)
			if e != nil || gate.TreeHash(after) != gate.TreeHash(before) {
				changed = errors.Join(changed, fmt.Errorf("reference input changed: %s", root), e)
			}
		}
		for name, hash := range report.Artifacts {
			data, e := os.ReadFile(filepath.Join(req.EvidenceDir, name))
			if e != nil || gate.Digest(data) != hash {
				changed = errors.Join(changed, fmt.Errorf("reference evidence changed: %s", name), e)
			}
		}
		after, e := os.ReadFile(req.SpecPath)
		if e != nil || !bytes.Equal(after, spec) {
			changed = errors.Join(changed, errors.New("reference specification changed"), e)
		}
		report.InputsUnchanged = changed == nil
		err = errors.Join(err, changed)
	}()
	layout := filepath.Join(req.EvidenceDir, "candidate")
	trusted := filepath.Join(req.EvidenceDir, "production")
	baseline := filepath.Join(req.EvidenceDir, "baseline", scenario.module)
	for _, entry := range []struct {
		root  string
		files map[string][]byte
	}{
		{filepath.Join(layout, scenario.module), candidate}, {filepath.Join(trusted, scenario.module), prod}, {baseline, gate.Module(fixtures, scenario.module)},
	} {
		if err = gate.CopyFiles(entry.root, entry.files); err != nil {
			return report, err
		}
	}
	siblings := []string{"rpcx-v1", "rpcx-v2"}
	if scenario.module == "trainings" {
		siblings = append(siblings, "common")
	}
	for _, root := range []string{layout, trusted, filepath.Dir(baseline)} {
		for _, sdk := range siblings {
			if err = gate.CopyFiles(filepath.Join(root, sdk), gate.Module(fixtures, sdk)); err != nil {
				return report, err
			}
		}
	}
	if err = gate.Write(filepath.Join(trusted, scenario.module, scenario.probeFile), data[scenario.probeData]); err != nil {
		return report, err
	}
	if err = gate.Write(filepath.Join(req.EvidenceDir, "materials/spec.md"), spec); err != nil {
		return report, err
	}
	for _, root := range []string{layout, trusted, filepath.Dir(baseline), filepath.Join(req.EvidenceDir, "materials")} {
		snapshots[root], err = gate.ReadFiles(root)
		if err != nil {
			return report, err
		}
	}
	backend, err := l2.PrepareBackend(ctx, filepath.Join(req.EvidenceDir, "modules"), req.Backend, host)
	if err != nil {
		return report, err
	}
	run := func(id, dir string, args ...string) (execution.Result, error) {
		commandCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		start := time.Now()
		root := filepath.Dir(dir)
		mounts := []string{dir}
		for _, sdk := range siblings {
			path := filepath.Join(root, sdk)
			if _, e := os.Stat(path); e == nil && path != dir {
				mounts = append(mounts, path)
			}
		}
		result, runErr := backend.Run(commandCtx, execution.Request{Directory: dir, Args: args, ReadOnlyDirs: mounts, CacheDir: filepath.Join(req.EvidenceDir, "cache")})
		actualArgs := append([]string{"env", "GOMODCACHE=" + backend.Environment()["GOMODCACHE"]}, args...)
		command := Command{ID: id, Args: actualArgs, Directory: dir, ExitCode: result.ExitCode, Stdout: "logs/" + id + ".stdout.log", Stderr: "logs/" + id + ".stderr.log", ContainerID: result.ContainerID, DurationMS: time.Since(start).Milliseconds()}
		if result.OOMKilled || result.OutputLimit {
			runErr = errors.Join(runErr, errors.New("reference command exceeded resource limits"))
		}
		runErr = errors.Join(runErr, gate.Write(filepath.Join(req.EvidenceDir, command.Stdout), result.Stdout), gate.Write(filepath.Join(req.EvidenceDir, command.Stderr), result.Stderr))
		if runErr != nil {
			command.Error = runErr.Error()
		}
		report.Commands = append(report.Commands, command)
		for _, path := range []string{command.Stdout, command.Stderr} {
			contents, e := os.ReadFile(filepath.Join(req.EvidenceDir, path))
			if e == nil {
				report.Artifacts[path] = gate.Digest(contents)
			}
		}
		return result, runErr
	}
	version, err := run("go-version", baseline, "go", "version")
	if err != nil {
		return report, err
	}
	if version.ExitCode != 0 {
		return report, errors.New("Go environment could not be identified")
	}
	baseMod, err := run("baseline-mod", baseline, "go", "mod", "edit", "-json")
	if err != nil {
		return report, err
	}
	if baseMod.ExitCode != 0 {
		return report, errors.New("authenticated baseline go.mod could not be read")
	}
	module := filepath.Join(layout, scenario.module)
	mod, err := run("candidate-mod", module, "go", "mod", "edit", "-json")
	if err != nil {
		return report, err
	}
	report.Checks["dependencies"] = dependencyObservation(baseMod.Stdout, mod.Stdout, mod.ExitCode)
	pathsCheck := Observation{Status: Pass}
	for _, name := range scenario.protected {
		if !bytes.Equal(candidate[name], fixtures[scenario.module+"/"+name]) {
			pathsCheck.Status = Fail
			pathsCheck.Findings = append(pathsCheck.Findings, Finding{Rule: "PATH-001", Detail: "protected path changed: " + name})
		}
	}
	report.Checks["protected_paths"] = pathsCheck
	// 非法依赖不进入构建，防止候选 replace 把命令指向认证范围外。
	if report.Checks["dependencies"].Status != Pass {
		return report, nil
	}
	var inventory struct {
		Modules map[string]testInventory `json:"modules"`
	}
	if err = json.Unmarshal(host["tests.json"], &inventory); err != nil {
		return report, err
	}
	if scenario.module == "trainings" {
		record, moduleErr := checkCommon(run, filepath.Join(layout, "common"), gate.TreeHash(gate.Module(fixtures, "common")), inventory.Modules["common"])
		report.LocalModules["common"] = record
		if moduleErr != nil {
			return report, moduleErr
		}
		if aggregate(record.Checks) != Pass {
			return report, nil
		}
	}
	for _, config := range scenario.configurations {
		checks, configErr := checkConfiguration(run, scenario, config, baseline, module, filepath.Join(trusted, scenario.module), inventory.Modules[scenario.module])
		if report.Configurations != nil {
			report.Configurations[config.id] = checks
		} else {
			for _, name := range []string{"build", "candidate_tests", "behavior", "expiry"} {
				report.Checks[name] = checks.Checks[name]
			}
		}
		if configErr != nil {
			return report, configErr
		}
	}
	return report, nil
}

// 只使用 Go 自身对模块声明的解析；与运行时门禁的文本规则分别实现。
func dependencyObservation(baseline, candidate []byte, exit int) Observation {
	fail := func(detail string) Observation {
		return Observation{Status: Fail, Findings: []Finding{{Rule: "DEP-001", Detail: detail}}}
	}
	if exit != 0 {
		return fail("candidate go.mod is invalid")
	}
	var before, after map[string]any
	if json.Unmarshal(baseline, &before) != nil || json.Unmarshal(candidate, &after) != nil {
		return Observation{Status: Inconclusive, Reason: "invalid go mod edit JSON"}
	}
	require, ok := before["Require"].([]any)
	if !ok {
		return Observation{Status: Inconclusive, Reason: "baseline module requirements missing"}
	}
	for _, item := range require {
		entry, ok := item.(map[string]any)
		if !ok {
			return Observation{Status: Inconclusive, Reason: "invalid baseline requirement"}
		}
		if entry["Path"] == "example.com/rpcx" {
			entry["Path"] = "example.com/rpcx/v2"
			entry["Version"] = "v2.0.0"
		}
	}
	replacements, ok := before["Replace"].([]any)
	if !ok {
		return Observation{Status: Inconclusive, Reason: "baseline replacements missing"}
	}
	var kept []any
	for _, item := range replacements {
		entry, ok := item.(map[string]any)
		if !ok {
			return Observation{Status: Inconclusive, Reason: "invalid baseline replacement"}
		}
		old, ok := entry["Old"].(map[string]any)
		if !ok {
			return Observation{Status: Inconclusive, Reason: "invalid baseline replacement source"}
		}
		if old["Path"] != "example.com/rpcx" {
			kept = append(kept, item)
		}
	}
	before["Replace"] = kept
	for _, doc := range []map[string]any{before, after} {
		for _, key := range []string{"Require", "Replace"} {
			if entries, ok := doc[key].([]any); ok {
				slices.SortFunc(entries, func(a, b any) int { x, _ := json.Marshal(a); y, _ := json.Marshal(b); return bytes.Compare(x, y) })
			}
		}
	}
	x, _ := json.Marshal(before)
	y, _ := json.Marshal(after)
	if !bytes.Equal(x, y) {
		return fail("module declarations differ from prescribed v2 migration")
	}
	return Observation{Status: Pass}
}
