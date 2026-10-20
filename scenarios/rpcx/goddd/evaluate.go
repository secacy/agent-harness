package goddd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/analysis"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2"
	"github.com/secacy/agent-harness/scenarios/rpcx/testintegrity"
)

type evaluation struct {
	result  checks.Evaluation
	req     checks.EvaluationRequest
	backend *ModuleBackend
}

func (e *evaluation) binding() quality.Binding {
	return quality.Binding{SpecSHA256: e.result.SpecSHA256, BaselineSHA256: e.result.BaselineSHA256, CandidateSHA256: e.result.CandidateSHA256}
}
func (e *evaluation) save(id, kind, path string, data []byte) error {
	if err := write(filepath.Join(e.req.EvidenceDir, path), data); err != nil {
		return err
	}
	e.result.Evidence = append(e.result.Evidence, quality.Evidence{ID: id, Kind: kind, Path: path, SHA256: digest(data), Binding: e.binding()})
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
	before := map[string]string{}
	for _, path := range mounts {
		files, err := readFiles(path)
		if err != nil {
			return execution.Result{}, err
		}
		before[path] = treeHash(files)
	}
	start := time.Now()
	result, err := e.backend.Run(ctx, execution.Request{Args: args, Directory: root, ReadOnlyDirs: mounts, CacheDir: e.req.CacheDir, MaxBytes: 4 << 20})
	actual := append([]string{"env", "GOMODCACHE=" + e.backend.Environment()["GOMODCACHE"]}, args...)
	e.result.Commands = append(e.result.Commands, checks.Command{Args: actual, Directory: root, Log: "logs/" + name + ".stdout.log", ExitCode: result.ExitCode, TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded), Cancelled: errors.Is(ctx.Err(), context.Canceled), DurationMS: time.Since(start).Milliseconds(), Backend: e.backend.Info(), ContainerID: result.ContainerID, OOMKilled: result.OOMKilled, OutputLimit: result.OutputLimit})
	err = errors.Join(err, e.save(name+"-stdout", "command_stdout", "logs/"+name+".stdout.log", result.Stdout), e.save(name+"-stderr", "command_stderr", "logs/"+name+".stderr.log", result.Stderr), e.saveJSON(name, "command", "commands/"+name+".json", e.result.Commands[len(e.result.Commands)-1]))
	if result.ExitCode < 0 || result.OOMKilled || result.OutputLimit {
		err = errors.Join(err, errors.New("command did not complete within execution limits"))
	}
	for path, hash := range before {
		files, readErr := readFiles(path)
		if readErr != nil || treeHash(files) != hash {
			err = errors.Join(err, readErr, fmt.Errorf("command input changed: %s", path))
		}
	}
	return result, err
}

// Evaluate 接受单个候选；预期清单与参考补丁不属于输入或门禁材料。
func Evaluate(ctx context.Context, req checks.EvaluationRequest) (out checks.Evaluation, err error) {
	e := &evaluation{req: req, result: checks.Evaluation{SchemaVersion: 3, VerifierVersion: VerifierVersion, SpecVersion: SpecVersion, SpecSHA256: SpecSHA256(), Checks: map[string]quality.Check{}, Evidence: []quality.Evidence{}, Gaps: []quality.Gap{}, Commands: []checks.Command{}}}
	defer func() {
		if err != nil {
			e.result.Verdict = ""
		}
		out = e.result
	}()
	if req.Backend == nil {
		return out, errors.New("Goddd verification requires an explicit backend")
	}
	for _, p := range []*string{&e.req.BaselineDir, &e.req.CandidateDir, &e.req.SpecPath, &e.req.EvidenceDir} {
		*p, err = canonical(*p)
		if err != nil {
			return out, err
		}
	}
	if e.req.CacheDir == "" {
		e.req.CacheDir = filepath.Join(e.req.EvidenceDir, "cache")
	}
	e.req.CacheDir, err = canonical(e.req.CacheDir)
	if err != nil {
		return out, err
	}
	if overlap(e.req.BaselineDir, e.req.CandidateDir) {
		return out, errors.New("baseline and candidate must not overlap")
	}
	spec, err := os.ReadFile(e.req.SpecPath)
	if err != nil || !bytes.Equal(spec, Spec()) || digest(spec) != SpecSHA256() {
		return out, errors.New("unsupported Goddd specification")
	}
	inputs := map[string]map[string][]byte{}
	for _, root := range []string{e.req.BaselineDir, e.req.CandidateDir} {
		for _, path := range []string{root, filepath.Join(filepath.Dir(root), "rpcx-v1"), filepath.Join(filepath.Dir(root), "rpcx-v2")} {
			if overlap(path, e.req.EvidenceDir) || overlap(path, e.req.CacheDir) {
				return out, errors.New("evidence/cache must not overlap module inputs")
			}
			inputs[path], err = readFiles(path)
			if err != nil {
				return out, err
			}
		}
	}
	if overlap(e.req.SpecPath, e.req.EvidenceDir) || overlap(e.req.SpecPath, e.req.CacheDir) {
		return out, errors.New("evidence/cache overlaps specification")
	}
	base, candidate := inputs[e.req.BaselineDir], inputs[e.req.CandidateDir]
	fixtures := map[string][]byte{}
	for _, entry := range []struct{ name, root string }{{"goddd", e.req.BaselineDir}, {"rpcx-v1", filepath.Join(filepath.Dir(e.req.BaselineDir), "rpcx-v1")}, {"rpcx-v2", filepath.Join(filepath.Dir(e.req.BaselineDir), "rpcx-v2")}} {
		for name, data := range inputs[entry.root] {
			fixtures[entry.name+"/"+name] = data
		}
	}
	if err := authenticateFixtures(fixtures); err != nil {
		return out, err
	}

	e.result.BaselineSHA256, e.result.CandidateSHA256 = treeHash(base), treeHash(candidate)
	completed := false
	defer func() {
		after, readErr := os.ReadFile(e.req.SpecPath)
		if readErr != nil || !bytes.Equal(after, spec) {
			err = errors.Join(err, errors.New("specification changed during verification"), readErr)
		}
		for name, before := range inputs {
			after, readErr := readFiles(name)
			if readErr != nil || treeHash(after) != treeHash(before) {
				err = errors.Join(err, errors.New("verification input changed: "+name), readErr)
			}
		}
		if e.backend != nil {
			err = errors.Join(err, e.backend.Check(context.WithoutCancel(ctx)))
		}
		if !completed && err == nil {
			err = errors.New("Goddd verification interrupted before completion")
		}
	}()
	if err := os.MkdirAll(e.req.EvidenceDir, 0755); err != nil {
		return out, err
	}
	entries, err := os.ReadDir(e.req.EvidenceDir)
	if err != nil || len(entries) != 0 {
		return out, errors.New("Goddd evidence directory must be empty")
	}
	if err := write(filepath.Join(e.req.EvidenceDir, "go.mod"), []byte("module example.com/goddd-evidence\n\ngo 1.23.0\n")); err != nil {
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
	e.result.MaterialsSHA256 = treeHash(material)
	if err := copyFiles(filepath.Join(e.req.EvidenceDir, "tool"), material); err != nil {
		return out, err
	}
	if err := e.save("specification", "specification", "spec.md", spec); err != nil {
		return out, err
	}
	if err := e.saveJSON("policy", "host_policy", "policy.json", policy()); err != nil {
		return out, err
	}
	snapshots := map[string]map[string]string{}
	for name, files := range inputs {
		snapshots[name] = hashes(files)
	}
	if err := e.saveJSON("input-snapshots", "input_snapshots", "input-snapshots.json", snapshots); err != nil {
		return out, err
	}
	host, err := HostMaterialsSHA256()
	if err != nil {
		return out, err
	}
	if err := e.saveJSON("host-materials", "host_execution_materials", "host-materials.json", map[string]string{"sha256": host, "baseline_data_version": "rpcx-l2-goddd-v1"}); err != nil {
		return out, err
	}
	for _, r := range policy().Requirements {
		e.result.Checks[r.ID] = check(r.ID, quality.StatusBlocked, "prerequisite checks have not completed", "specification", "input-snapshots", "host-materials")
	}
	e.backend, err = PrepareBackend(ctx, filepath.Join(e.req.EvidenceDir, "host-materials/rpcx-goddd-modules"), req.Backend)
	if err != nil {
		return out, err
	}
	for _, entry := range []struct {
		name  string
		files map[string][]byte
	}{{"baseline", base}, {"candidate", candidate}} {
		layout := filepath.Join(e.req.EvidenceDir, entry.name)
		if err := copyFiles(filepath.Join(layout, "goddd"), entry.files); err != nil {
			return out, err
		}
		for _, sdk := range []string{"rpcx-v1", "rpcx-v2"} {
			if err := copyFiles(filepath.Join(layout, sdk), module(fixtures, sdk)); err != nil {
				return out, err
			}
		}
	}
	baselineRoot := filepath.Join(e.req.EvidenceDir, "baseline/goddd")
	candidateRoot := filepath.Join(e.req.EvidenceDir, "candidate/goddd")
	expectedMod, _ := modEntries(migratedMod(base["go.mod"]))
	actualMod, modErr := modEntries(candidate["go.mod"])
	depStatus := quality.StatusPass
	depDetail := "v2 module and unchanged third-party dependency identities verified"
	if modErr != nil {
		depStatus = quality.StatusUnknown
		depDetail = "module syntax exceeds the supported declaration parser"
	} else if !reflect.DeepEqual(expectedMod, actualMod) {
		depStatus = quality.StatusFail
		depDetail = "module declarations differ from the permitted v2 dependency update"
	}
	for _, sdk := range []string{"rpcx-v1", "rpcx-v2"} {
		if treeHash(inputs[filepath.Join(filepath.Dir(e.req.CandidateDir), sdk)]) != treeHash(module(fixtures, sdk)) {
			depStatus = quality.StatusFail
			depDetail = "candidate SDK differs from authenticated host material"
		}
	}
	e.result.Checks["dependencies"] = check("dependencies", depStatus, depDetail, "input-snapshots", "host-materials")
	protected := quality.StatusPass
	for _, name := range []string{"LICENSE", "go.sum"} {
		if !bytes.Equal(base[name], candidate[name]) {
			protected = quality.StatusFail
		}
	}
	e.result.Checks["protected_paths"] = check("protected_paths", protected, "protected LICENSE/go.sum content: "+protected, "input-snapshots")
	adaptedTests, err := e.checkAdaptations(base, candidate)
	if err != nil {
		return out, err
	}
	required, err := RequiredTests()
	if err != nil {
		return out, err
	}
	for _, step := range []string{"build", "test"} {
		args := []string{"go", "build", "-o", "/dev/null", "./..."}
		if step == "test" {
			args = []string{"go", "test", "-json", "-count=1", "-timeout=45s", "./..."}
		}
		r, runErr := e.command(ctx, "baseline-"+step, baselineRoot, args)
		if runErr != nil {
			return out, runErr
		}
		if r.ExitCode != 0 {
			return out, fmt.Errorf("authenticated baseline %s failed", step)
		}
		if step == "test" {
			s, _ := observe(r.Stdout, required, r.ExitCode)
			if s != quality.StatusPass {
				return out, errors.New("authenticated baseline test evidence incomplete")
			}
		}
	}
	if depStatus == quality.StatusPass {
		build, runErr := e.command(ctx, "candidate-build", candidateRoot, []string{"go", "build", "-o", "/dev/null", "./..."})
		if runErr != nil {
			return out, runErr
		}
		s := quality.StatusPass
		if build.ExitCode != 0 {
			s = quality.StatusFail
		}
		e.result.Checks["build"] = check("build", s, "candidate build: "+s, "candidate-build", "candidate-build-stdout", "candidate-build-stderr")
		if build.ExitCode == 0 {
			tests, runErr := e.command(ctx, "candidate-tests", candidateRoot, []string{"go", "test", "-json", "-count=1", "-timeout=45s", "./..."})
			if runErr != nil {
				return out, runErr
			}
			s, _ := observe(tests.Stdout, required, tests.ExitCode)
			if tests.ExitCode != 0 {
				s = quality.StatusFail
			}
			e.result.Checks["baseline_tests"] = check("baseline_tests", s, "candidate original test execution: "+s, "candidate-tests", "candidate-tests-stdout", "candidate-tests-stderr")
			if err := e.analyze(ctx, baselineRoot, candidateRoot); err != nil {
				return out, err
			}
			if err := e.behavior(ctx, candidate, fixtures, adaptedTests, required); err != nil {
				return out, err
			}
		}
	}
	decision, err := quality.Decide(e.req.EvidenceDir, policy(), e.binding(), e.result.Checks, e.result.Evidence)
	if err != nil {
		return out, err
	}
	e.result.Verdict, e.result.Gaps = decision.Verdict, decision.Gaps
	completed = true
	return e.result, nil
}

func canonical(name string) (string, error) {
	if name == "" {
		return "", errors.New("verification paths are required")
	}
	absolute, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil || !os.IsNotExist(err) {
		return resolved, err
	}
	if filepath.Dir(absolute) == absolute {
		return "", err
	}
	parent, err := canonical(filepath.Dir(absolute))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(absolute)), nil
}
func overlap(a, b string) bool {
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err == nil && (rel == "." || filepath.IsLocal(rel)) {
			return true
		}
	}
	return false
}

func (e *evaluation) analyze(ctx context.Context, baselineRoot, candidateRoot string) error {
	var reports []analysis.Report
	for _, entry := range []struct{ name, root string }{{"baseline", baselineRoot}, {"candidate", candidateRoot}} {
		dir := filepath.Join(e.req.EvidenceDir, "analysis", entry.name)
		r, err := analysis.Inspect(ctx, analysis.Request{ModuleDir: entry.root, SDKDirs: []string{filepath.Join(filepath.Dir(entry.root), "rpcx-v1"), filepath.Join(filepath.Dir(entry.root), "rpcx-v2")}, EvidenceDir: dir, CacheDir: e.req.CacheDir, Backend: e.backend})
		if err != nil {
			return err
		}
		reports = append(reports, r.Report)
		data, err := os.ReadFile(filepath.Join(dir, "analysis.json"))
		if err != nil {
			return err
		}
		if err := e.save("analysis-"+entry.name, "typed_analysis", "analysis/"+entry.name+".json", data); err != nil {
			return err
		}
	}
	status, detail := quality.StatusUnknown, "typed package loading did not establish complete coverage"
	if reports[0].Status == "complete" && reports[1].Status == "complete" {
		comparison, err := analysis.Compare(reports[0], reports[1])
		if err != nil {
			return err
		}
		if err := e.saveJSON("analysis-comparison", "call_correspondence", "analysis/comparison.json", comparison); err != nil {
			return err
		}
		status = quality.StatusPass
		if len(comparison.Matches) != 1 || len(comparison.Changes) != 0 || len(comparison.Gaps) != 0 {
			status = quality.StatusUnknown
		}
		for _, call := range reports[1].Calls {
			if call.Status == "violation" || call.Status == "legacy" {
				status = quality.StatusFail
			}
			if call.Status == "unknown" && status != quality.StatusFail {
				status = quality.StatusUnknown
			}
		}
		detail = "typed RPC correspondence and deadline boundary: " + status
	}
	e.result.Checks["migration_completeness"] = check("migration_completeness", status, detail, "analysis-baseline", "analysis-candidate", "input-snapshots", "host-materials")
	return nil
}

func (e *evaluation) behavior(ctx context.Context, candidate, fixtures, tests map[string][]byte, required []string) error {
	root := filepath.Join(e.req.EvidenceDir, "trusted/goddd")
	production := map[string][]byte{}
	for name, data := range candidate {
		if !strings.HasSuffix(name, "_test.go") {
			production[name] = data
		}
	}
	if err := copyFiles(root, production); err != nil {
		return err
	}
	if err := copyFiles(root, tests); err != nil {
		return err
	}
	for _, sdk := range []string{"rpcx-v1", "rpcx-v2"} {
		if err := copyFiles(filepath.Join(filepath.Dir(root), sdk), module(fixtures, sdk)); err != nil {
			return err
		}
	}
	probe, err := materials.ReadFile("probes/boundary_test.go.txt")
	if err != nil {
		return err
	}
	if err := write(filepath.Join(root, "server/harness_boundary_test.go"), probe); err != nil {
		return err
	}
	if err := e.save("behavior-probe", "trusted_probe", "probe.go.txt", probe); err != nil {
		return err
	}
	names := map[string]bool{}
	for _, id := range required {
		if at := strings.Index(id, "/Test"); at >= 0 {
			name, _, _ := strings.Cut(id[at+1:], "/")
			names[name] = true
		}
	}
	pattern := "^(" + strings.Join(sortedKeys(names), "|") + ")$"
	testsRun, err := e.command(ctx, "trusted-tests", root, []string{"go", "test", "-json", "-count=1", "-timeout=45s", "-run=" + pattern, "./..."})
	if err != nil {
		return err
	}
	status, _ := observe(testsRun.Stdout, required, testsRun.ExitCode)
	ids := []string{"trusted-tests", "trusted-tests-stdout", "trusted-tests-stderr", "behavior-probe"}
	findings := []quality.Finding{}
	if status == quality.StatusPass {
		const prefix = "github.com/marcusolsson/goddd/server/"
		wanted := []string{prefix + "TestHarnessGodddBoundary"}
		for _, mode := range []string{"default", "short-parent", "long-parent", "cancelled", "expired", "cancel-in-backend", "remote-error", "repeat", "unknown-cargo", "empty-response"} {
			wanted = append(wanted, prefix+"TestHarnessGodddBoundary/"+mode)
		}
		quick, err := e.command(ctx, "behavior-quick", root, []string{"go", "test", "-json", "-count=1", "-timeout=45s", "-run=^TestHarnessGodddBoundary$", "./server"})
		if err != nil {
			return err
		}
		status, findings = observe(quick.Stdout, wanted, quick.ExitCode)
		if status == quality.StatusFail && len(findings) == 0 {
			status = quality.StatusUnknown
		}
		ids = append(ids, "behavior-quick", "behavior-quick-stdout", "behavior-quick-stderr")
		if status == quality.StatusPass {
			slow, err := e.command(ctx, "behavior-expiry", root, []string{"go", "test", "-json", "-count=1", "-timeout=45s", "-run=^TestHarnessGodddExpiry$", "./server"})
			if err != nil {
				return err
			}
			status, findings = observe(slow.Stdout, []string{prefix + "TestHarnessGodddExpiry"}, slow.ExitCode)
			if status == quality.StatusFail && len(findings) == 0 {
				status = quality.StatusUnknown
			}
			ids = append(ids, "behavior-expiry", "behavior-expiry-stdout", "behavior-expiry-stderr")
		}
	}
	result := check("semantic_behavior", status, "trusted business/request/deadline observations: "+status, ids...)
	for i := range findings {
		findings[i].EvidenceIDs = ids
	}
	result.Findings = append(result.Findings, findings...)
	e.result.Checks["semantic_behavior"] = result
	return nil
}
