package ghcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/secacy/agent-harness/scenarios/rpcx/analysis"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2"
	"github.com/secacy/agent-harness/scenarios/rpcx/testintegrity"
)

type evaluation struct {
	result     checks.Evaluation
	req        checks.EvaluationRequest
	backend    *l2.ModuleBackend
	merged     map[string]bool
	extraMount string
}

func (e *evaluation) binding() quality.Binding {
	return quality.Binding{SpecSHA256: e.result.SpecSHA256, BaselineSHA256: e.result.BaselineSHA256, CandidateSHA256: e.result.CandidateSHA256}
}
func (e *evaluation) save(id, kind, path string, data []byte) error {
	if err := gate.Write(filepath.Join(e.req.EvidenceDir, path), data); err != nil {
		return err
	}
	e.result.Evidence = append(e.result.Evidence, quality.Evidence{ID: id, Kind: kind, Path: path, SHA256: gate.Digest(data), Binding: e.binding()})
	return nil
}
func (e *evaluation) saveJSON(id, kind, path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return e.save(id, kind, path, append(data, '\n'))
}
func (e *evaluation) command(parent context.Context, name, root string, args []string) (execution.Result, error) {
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	mounts := []string{root, filepath.Join(filepath.Dir(root), "rpcx-v1"), filepath.Join(filepath.Dir(root), "rpcx-v2")}
	if e.extraMount != "" {
		mounts = append(mounts, e.extraMount)
	}
	before := map[string]string{}
	for _, path := range mounts {
		files, err := gate.ReadFiles(path)
		if err != nil {
			return execution.Result{}, err
		}
		before[path] = gate.TreeHash(files)
	}
	start := time.Now()
	result, err := e.backend.Run(ctx, execution.Request{Args: args, Directory: root, ReadOnlyDirs: mounts, CacheDir: e.req.CacheDir, MaxBytes: 4 << 20})
	actual := append([]string{"env", "GOMODCACHE=" + e.backend.Environment()["GOMODCACHE"]}, args...)
	e.result.Commands = append(e.result.Commands, checks.Command{Args: actual, Directory: root, Log: "logs/" + name + ".stdout.log", ExitCode: result.ExitCode, TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded), Cancelled: errors.Is(ctx.Err(), context.Canceled), DurationMS: time.Since(start).Milliseconds(), Backend: e.backend.Info(), ContainerID: result.ContainerID, OOMKilled: result.OOMKilled, OutputLimit: result.OutputLimit})
	err = errors.Join(err, e.save(name+"-stdout", "command_stdout", "logs/"+name+".stdout.log", result.Stdout), e.save(name+"-stderr", "command_stderr", "logs/"+name+".stderr.log", result.Stderr), e.saveJSON(name, "command", "commands/"+name+".json", e.result.Commands[len(e.result.Commands)-1]))
	err = errors.Join(err, ctx.Err())
	if result.ExitCode < 0 || result.OOMKilled || result.OutputLimit {
		err = errors.Join(err, errors.New("command did not complete within execution limits"))
	}
	for path, hash := range before {
		files, readErr := gate.ReadFiles(path)
		if readErr != nil || gate.TreeHash(files) != hash {
			err = errors.Join(err, readErr, fmt.Errorf("command input changed: %s", path))
		}
	}
	return result, err
}

// Evaluate 接受单个候选；预期清单与参考补丁不属于输入或门禁材料。
func Evaluate(ctx context.Context, req checks.EvaluationRequest) (out checks.Evaluation, err error) {
	e := &evaluation{req: req, merged: map[string]bool{}, result: checks.Evaluation{SchemaVersion: 3, VerifierVersion: VerifierVersion, SpecVersion: SpecVersion, SpecSHA256: SpecSHA256(), Checks: map[string]quality.Check{}, Evidence: []quality.Evidence{}, Gaps: []quality.Gap{}, Commands: []checks.Command{}}}
	defer func() {
		if err != nil {
			e.result.Verdict = ""
		}
		out = e.result
	}()
	if req.Backend == nil || req.Backend.Info().Kind != execution.KindDocker {
		return out, errors.New("GHCLI verification requires an explicit backend")
	}
	for _, p := range []*string{&e.req.BaselineDir, &e.req.CandidateDir, &e.req.SpecPath, &e.req.EvidenceDir} {
		*p, err = gate.Canonical(*p)
		if err != nil {
			return out, err
		}
	}
	if e.req.CacheDir == "" {
		e.req.CacheDir = filepath.Join(e.req.EvidenceDir, "cache")
	}
	e.req.CacheDir, err = gate.Canonical(e.req.CacheDir)
	if err != nil {
		return out, err
	}
	if gate.Overlap(e.req.BaselineDir, e.req.CandidateDir) {
		return out, errors.New("baseline and candidate must not overlap")
	}
	specInfo, statErr := os.Lstat(e.req.SpecPath)
	if statErr != nil || !specInfo.Mode().IsRegular() || specInfo.Size() > 1<<20 {
		return out, errors.New("specification must be a bounded regular file")
	}
	spec, err := os.ReadFile(e.req.SpecPath)
	if err != nil || !bytes.Equal(spec, Spec()) || gate.Digest(spec) != SpecSHA256() {
		return out, errors.New("unsupported GHCLI specification")
	}
	inputs := map[string]map[string][]byte{}
	for _, root := range []string{e.req.BaselineDir, e.req.CandidateDir} {
		for _, path := range []string{root, filepath.Join(filepath.Dir(root), "rpcx-v1"), filepath.Join(filepath.Dir(root), "rpcx-v2")} {
			if gate.Overlap(path, e.req.EvidenceDir) || gate.Overlap(path, e.req.CacheDir) {
				return out, errors.New("evidence/cache must not overlap module inputs")
			}
			inputs[path], err = gate.ReadFiles(path)
			if err != nil {
				return out, err
			}
		}
	}
	if gate.Overlap(e.req.SpecPath, e.req.EvidenceDir) || gate.Overlap(e.req.SpecPath, e.req.CacheDir) {
		return out, errors.New("evidence/cache overlaps specification")
	}
	base, candidate := inputs[e.req.BaselineDir], inputs[e.req.CandidateDir]
	fixtures := map[string][]byte{}
	for _, entry := range []struct{ name, root string }{{"ghcli", e.req.BaselineDir}, {"rpcx-v1", filepath.Join(filepath.Dir(e.req.BaselineDir), "rpcx-v1")}, {"rpcx-v2", filepath.Join(filepath.Dir(e.req.BaselineDir), "rpcx-v2")}} {
		for name, data := range inputs[entry.root] {
			fixtures[entry.name+"/"+name] = data
		}
	}
	if err := authenticate(fixtures); err != nil {
		return out, err
	}

	e.result.BaselineSHA256, e.result.CandidateSHA256 = gate.TreeHash(base), gate.TreeHash(candidate)
	completed := false
	defer func() {
		after, readErr := os.ReadFile(e.req.SpecPath)
		if readErr != nil || !bytes.Equal(after, spec) {
			err = errors.Join(err, errors.New("specification changed during verification"), readErr)
		}
		for name, before := range inputs {
			after, readErr := gate.ReadFiles(name)
			if readErr != nil || gate.TreeHash(after) != gate.TreeHash(before) {
				err = errors.Join(err, errors.New("verification input changed: "+name), readErr)
			}
		}
		if e.backend != nil {
			err = errors.Join(err, e.backend.Check(context.WithoutCancel(ctx)))
		}
		err = errors.Join(err, ctx.Err())
		if !completed && err == nil {
			err = errors.New("GHCLI verification interrupted before completion")
		}
	}()
	if err := os.MkdirAll(e.req.EvidenceDir, 0755); err != nil {
		return out, err
	}
	entries, err := os.ReadDir(e.req.EvidenceDir)
	if err != nil || len(entries) != 0 {
		return out, errors.New("GHCLI evidence directory must be empty")
	}
	if err := gate.Write(filepath.Join(e.req.EvidenceDir, "go.mod"), []byte("module example.com/ghcli-evidence\n\ngo 1.23.0\n")); err != nil {
		return out, err
	}
	material, err := materialFiles()
	if err != nil {
		return out, err
	}
	shared, err := testintegrity.Materials()
	if err != nil {
		return out, err
	}
	for name, data := range shared {
		material["testintegrity/"+name] = data
	}
	common, err := gate.Materials()
	if err != nil {
		return out, err
	}
	for name, data := range common {
		material["gate/"+name] = data
	}
	hostImplementation, err := l2.ExecutionMaterials()
	if err != nil {
		return out, err
	}
	for name, data := range hostImplementation {
		material["l2/"+name] = data
	}
	e.result.MaterialsSHA256 = gate.TreeHash(material)
	if err := gate.CopyFiles(filepath.Join(e.req.EvidenceDir, "tool"), material); err != nil {
		return out, err
	}
	if err := e.save("specification", "specification", "spec.md", spec); err != nil {
		return out, err
	}
	if err := e.saveJSON("policy", "host_policy", "policy.json", policy()); err != nil {
		return out, err
	}
	ruleMap, err := ruleMapping()
	if err != nil {
		return out, err
	}
	if err := e.saveJSON("rule-map", "host_rule_mapping", "rule-map.json", ruleMap); err != nil {
		return out, err
	}
	snapshots := map[string]map[string]string{}
	for name, files := range inputs {
		snapshots[name] = gate.Hashes(files)
	}
	if err := e.saveJSON("input-snapshots", "input_snapshots", "input-snapshots.json", snapshots); err != nil {
		return out, err
	}
	host, err := HostMaterialsSHA256()
	if err != nil {
		return out, err
	}
	if err := e.saveJSON("host-materials", "host_execution_materials", "host-materials.json", map[string]string{"sha256": host, "baseline_data_version": "rpcx-l2-ghcli-v1"}); err != nil {
		return out, err
	}
	hostFiles, err := l2.GHCLIMaterials()
	if err != nil {
		return out, err
	}
	for name, data := range hostFiles {
		if strings.HasPrefix(name, "dependencies/") {
			if err = e.save("host-"+strings.ReplaceAll(strings.TrimPrefix(name, "dependencies/"), ".", "-"), "host_dependency_bundle", "host-dependencies/"+strings.TrimPrefix(name, "dependencies/"), data); err != nil {
				return out, err
			}
		}
	}
	for _, r := range policy().Requirements {
		e.result.Checks[r.ID] = check(r.ID, quality.StatusBlocked, "prerequisite checks have not completed", "specification", "input-snapshots", "host-materials")
	}
	e.backend, err = PrepareBackend(ctx, filepath.Join(e.req.EvidenceDir, "host-materials/rpcx-ghcli-modules"), req.Backend)
	if err != nil {
		return out, err
	}
	for _, entry := range []struct {
		name  string
		files map[string][]byte
	}{{"baseline", base}, {"candidate", candidate}} {
		layout := filepath.Join(e.req.EvidenceDir, entry.name)
		if err := gate.CopyFiles(filepath.Join(layout, "ghcli"), entry.files); err != nil {
			return out, err
		}
		for _, sdk := range []string{"rpcx-v1", "rpcx-v2"} {
			if err := gate.CopyFiles(filepath.Join(layout, sdk), gate.Module(fixtures, sdk)); err != nil {
				return out, err
			}
		}
	}
	baselineRoot := filepath.Join(e.req.EvidenceDir, "baseline/ghcli")
	candidateRoot := filepath.Join(e.req.EvidenceDir, "candidate/ghcli")
	depStatus, modErr := dependencyStatus(base["go.mod"], candidate["go.mod"])
	if modErr != nil {
		return out, fmt.Errorf("parse authenticated migration module: %w", modErr)
	}
	for _, sdk := range []string{"rpcx-v1", "rpcx-v2"} {
		if gate.TreeHash(inputs[filepath.Join(filepath.Dir(e.req.CandidateDir), sdk)]) != gate.TreeHash(gate.Module(fixtures, sdk)) {
			depStatus = quality.StatusFail
		}
	}
	e.result.Checks["dependencies"] = check("dependencies", depStatus, "authenticated SDK and permitted dependency update: "+depStatus, "input-snapshots", "host-materials")
	protected := quality.StatusPass
	for name, before := range base {
		if name != "go.mod" && name != boundaryFile && !bytes.Equal(before, candidate[name]) {
			protected = quality.StatusFail
		}
	}
	e.result.Checks["protected_paths"] = check("protected_paths", protected, "protected original files: "+protected, "input-snapshots")
	integrity, err := testintegrity.CompareAdapted(gate.TestFiles(base), gate.TestFiles(base), gate.TestFiles(candidate), testintegrity.Plan{Version: "rpcx-ghcli-test-structure-v1", SpecSHA256: SpecSHA256(), MaterialSHA256: e.result.MaterialsSHA256, BaselineFiles: gate.Hashes(gate.TestFiles(base))})
	if err != nil {
		return out, err
	}
	if err = e.saveJSON("test-integrity", "test_structure", "test-integrity.json", integrity); err != nil {
		return out, err
	}
	testStatus := map[string]string{"preserved": quality.StatusPass, "violated": quality.StatusFail, "unknown": quality.StatusUnknown}[integrity.Status]
	e.result.Checks["test_preservation"] = check("test_preservation", testStatus, "original test preservation: "+integrity.Status, "test-integrity", "input-snapshots")
	required, err := RequiredTests()
	if err != nil {
		return out, err
	}
	for _, side := range []struct{ name, root string }{{"baseline", baselineRoot}, {"candidate", candidateRoot}} {
		b, runErr := e.command(ctx, side.name+"-build", side.root, []string{"go", "build", "-o", "/dev/null", "./..."})
		if runErr != nil {
			return out, runErr
		}
		if side.name == "baseline" && b.ExitCode != 0 {
			return out, errors.New("authenticated baseline build failed")
		}
		if side.name == "candidate" {
			s := quality.StatusPass
			if b.ExitCode != 0 {
				s = quality.StatusFail
			}
			e.result.Checks["build"] = check("build", s, "candidate build: "+s, "candidate-build", "candidate-build-stdout", "candidate-build-stderr")
		}
		t, runErr := e.command(ctx, side.name+"-test", side.root, []string{"go", "test", "-json", "-count=1", "-timeout=45s", "./..."})
		if runErr != nil {
			return out, runErr
		}
		s, _ := gate.Observe(t.Stdout, required, t.ExitCode, nil, "")
		if side.name == "baseline" && s != quality.StatusPass {
			return out, errors.New("authenticated baseline test events incomplete")
		}
		if side.name == "candidate" {
			e.result.Checks["baseline_tests"] = check("baseline_tests", s, "original candidate test events: "+s, "candidate-test", "candidate-test-stdout", "candidate-test-stderr")
		}
	}
	if e.result.Checks["build"].Status == quality.StatusPass && depStatus == quality.StatusPass {
		typed, typedErr := e.inspect(ctx, candidateRoot, candidate, fixtures)
		if typedErr != nil {
			return out, typedErr
		}
		if err = e.saveJSON("typed-analysis", "typed_boundary_analysis", "typed-analysis.json", typed); err != nil {
			return out, err
		}
		status := quality.StatusUnknown
		if typed.Status == "complete" && len(typed.Calls) == 1 && boundaryCovered(typed) {
			status = typed.Calls[0].Status
			if typed.Calls[0].Function != "invokeHTTP" || typed.Calls[0].Operation == nil || *typed.Calls[0].Operation != "http.do" {
				status = quality.StatusFail
			}
		}
		e.result.Checks["migration_completeness"] = check("migration_completeness", status, "typed bounded call proof: "+status, "typed-analysis", "boundary-proof", "typed-command", "typed-command-stdout", "typed-command-stderr")
		e.result.Checks["request_scope"] = scope(base, candidate)
		if err = e.behavior(ctx, candidate, fixtures); err != nil {
			return out, err
		}
	} else {
		if err = e.saveJSON("typed-analysis", "typed_boundary_analysis", "typed-analysis.json", map[string]string{"status": "not_run"}); err != nil {
			return out, err
		}
		e.result.Checks["request_scope"] = scope(base, candidate)
	}
	decision, err := quality.Decide(e.req.EvidenceDir, policy(), e.binding(), e.result.Checks, e.result.Evidence)
	if err != nil {
		return out, err
	}
	e.result.Verdict, e.result.Gaps = decision.Verdict, decision.Gaps
	completed = true
	return e.result, nil
}

// inspect 使用标准加载器提供的类型身份；工具和模块仅以各自只读根挂载。
func (e *evaluation) inspect(ctx context.Context, root string, candidate, fixtures map[string][]byte) (analysis.Report, error) {
	toolRoot := filepath.Join(e.req.EvidenceDir, "typed-tool")
	for _, name := range []string{"main.go", "rules.go", "source.go"} {
		data, err := materials.ReadFile("worker/" + name)
		if err != nil {
			return analysis.Report{}, err
		}
		if err = gate.Write(filepath.Join(toolRoot, name), data); err != nil {
			return analysis.Report{}, err
		}
	}
	backendPath := func(path string) string { return "/workspace/" + filepath.Base(path) }
	input := map[string][]byte{}
	for name, data := range candidate {
		input["ghcli/"+name] = data
	}
	for _, sdk := range []string{"rpcx-v1", "rpcx-v2"} {
		for name, data := range gate.Module(fixtures, sdk) {
			input[sdk+"/"+name] = data
		}
	}
	args := []string{"go", "run", backendPath(toolRoot) + "/main.go", backendPath(toolRoot) + "/rules.go", backendPath(toolRoot) + "/source.go", "-input", gate.TreeHash(input), "-tool", e.result.MaterialsSHA256}
	for _, p := range []string{root, filepath.Join(filepath.Dir(root), "rpcx-v1"), filepath.Join(filepath.Dir(root), "rpcx-v2")} {
		args = append(args, backendPath(p))
	}
	e.extraMount = toolRoot
	defer func() { e.extraMount = "" }()
	result, err := e.command(ctx, "typed-command", root, args)
	if err != nil {
		return analysis.Report{}, err
	}
	if result.ExitCode != 0 {
		return analysis.Report{}, errors.New("typed analysis worker failed")
	}
	envelope, err := decodeSourceEnvelope(result.Stdout, candidate, gate.TreeHash(input), e.result.MaterialsSHA256)
	if err != nil {
		return analysis.Report{}, err
	}
	if err = e.saveJSON("boundary-proof", "typed_request_parent_proof", "boundary-proof.json", envelope); err != nil {
		return analysis.Report{}, err
	}
	return envelope.Report, nil
}

// boundaryCovered 不把原封不动的受保护支持包动态导入当作新 RPC 边界缺口。
func boundaryCovered(r analysis.Report) bool {
	for _, g := range r.Gaps {
		if g.File == boundaryFile {
			return false
		}
	}
	return true
}
