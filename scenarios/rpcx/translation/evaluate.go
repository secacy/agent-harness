package translation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/analysis"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2"
	"github.com/secacy/agent-harness/scenarios/rpcx/testintegrity"
)

type evaluation struct {
	boundedScope bool
	analyses     map[string]ScopeAnalysis
	result       checks.Evaluation
	req          checks.EvaluationRequest
	backend      *l2.ModuleBackend
	merged       map[string]bool
}

func (e *evaluation) binding() quality.Binding {
	return quality.Binding{SpecSHA256: e.result.SpecSHA256, BaselineSHA256: e.result.BaselineSHA256, CandidateSHA256: e.result.CandidateSHA256}
}
func (e *evaluation) recorder() gate.Recorder {
	return gate.Recorder{Directory: e.req.EvidenceDir, Binding: e.binding(), Commands: &e.result.Commands, Evidence: &e.result.Evidence}
}
func (e *evaluation) save(id, kind, path string, data []byte) error {
	return e.recorder().Save(id, kind, path, data)
}
func (e *evaluation) saveJSON(id, kind, path string, value any) error {
	return e.recorder().SaveJSON(id, kind, path, value)
}
func (e *evaluation) command(ctx context.Context, name, root string, args []string) (execution.Result, error) {
	mounts := []string{root, filepath.Join(filepath.Dir(root), "rpcx-v1"), filepath.Join(filepath.Dir(root), "rpcx-v2")}
	return e.recorder().Run(ctx, name, e.backend, execution.Request{Args: args, Directory: root, ReadOnlyDirs: mounts, CacheDir: e.req.CacheDir, MaxBytes: 4 << 20})
}

// Evaluate 接受单个候选；预期清单与参考补丁不属于输入或门禁材料。
func Evaluate(ctx context.Context, req checks.EvaluationRequest) (checks.Evaluation, error) {
	return evaluate(ctx, req, false)
}

// EvaluateScope 由新宿主 Profile 选择，增加有限范围证明而保留原规则入口。
func EvaluateScope(ctx context.Context, req checks.EvaluationRequest) (checks.Evaluation, error) {
	return evaluate(ctx, req, true)
}

func evaluate(ctx context.Context, req checks.EvaluationRequest, bounded bool) (out checks.Evaluation, err error) {
	e := &evaluation{boundedScope: bounded, analyses: map[string]ScopeAnalysis{}, req: req, merged: map[string]bool{}, result: checks.Evaluation{SchemaVersion: 3, VerifierVersion: VerifierVersion, SpecVersion: SpecVersion, SpecSHA256: SpecSHA256(), Checks: map[string]quality.Check{}, Evidence: []quality.Evidence{}, Gaps: []quality.Gap{}, Commands: []checks.Command{}}}
	if bounded {
		e.result.VerifierVersion = ScopeVerifierVersion
	}
	defer func() {
		if err != nil {
			e.result.Verdict = ""
		}
		out = e.result
	}()
	if req.Backend == nil {
		return out, errors.New("Translation verification requires an explicit backend")
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
	spec, err := os.ReadFile(e.req.SpecPath)
	if err != nil || !bytes.Equal(spec, Spec()) || gate.Digest(spec) != SpecSHA256() {
		return out, errors.New("unsupported Translation specification")
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
	for _, entry := range []struct{ name, root string }{{"translation", e.req.BaselineDir}, {"rpcx-v1", filepath.Join(filepath.Dir(e.req.BaselineDir), "rpcx-v1")}, {"rpcx-v2", filepath.Join(filepath.Dir(e.req.BaselineDir), "rpcx-v2")}} {
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
			err = errors.New("Translation verification interrupted before completion")
		}
	}()
	if err := os.MkdirAll(e.req.EvidenceDir, 0755); err != nil {
		return out, err
	}
	entries, err := os.ReadDir(e.req.EvidenceDir)
	if err != nil || len(entries) != 0 {
		return out, errors.New("Translation evidence directory must be empty")
	}
	if err := gate.Write(filepath.Join(e.req.EvidenceDir, "go.mod"), []byte("module example.com/translation-evidence\n\ngo 1.23.0\n")); err != nil {
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
	configJSON, _ := json.Marshal(Configurations())
	material["configurations.json"] = configJSON
	if bounded {
		proofMaterials, err := analysis.ScopeMaterials()
		if err != nil {
			return out, err
		}
		for name, data := range proofMaterials {
			material["analysis-worker/"+name] = data
		}
	}
	e.result.MaterialsSHA256 = gate.TreeHash(material)
	if err := gate.CopyFiles(filepath.Join(e.req.EvidenceDir, "tool"), material); err != nil {
		return out, err
	}
	if err := e.save("specification", "specification", "spec.md", spec); err != nil {
		return out, err
	}
	if err := e.saveJSON("configurations", "required_build_configurations", "configurations.json", Configurations()); err != nil {
		return out, err
	}
	if err := e.saveJSON("policy", "host_policy", "policy.json", policy()); err != nil {
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
	if err := e.saveJSON("host-materials", "host_execution_materials", "host-materials.json", map[string]string{"sha256": host, "baseline_data_version": "rpcx-l2-translation-v1"}); err != nil {
		return out, err
	}
	for _, r := range policy().Requirements {
		e.result.Checks[r.ID] = check(r.ID, quality.StatusBlocked, "prerequisite checks have not completed", "specification", "input-snapshots", "host-materials")
	}
	e.backend, err = PrepareBackend(ctx, filepath.Join(e.req.EvidenceDir, "host-materials/rpcx-translation-modules"), req.Backend)
	if err != nil {
		return out, err
	}
	for _, entry := range []struct {
		name  string
		files map[string][]byte
	}{{"baseline", base}, {"candidate", candidate}} {
		layout := filepath.Join(e.req.EvidenceDir, entry.name)
		if err := gate.CopyFiles(filepath.Join(layout, "translation"), entry.files); err != nil {
			return out, err
		}
		for _, sdk := range []string{"rpcx-v1", "rpcx-v2"} {
			if err := gate.CopyFiles(filepath.Join(layout, sdk), gate.Module(fixtures, sdk)); err != nil {
				return out, err
			}
		}
	}
	baselineRoot := filepath.Join(e.req.EvidenceDir, "baseline/translation")
	candidateRoot := filepath.Join(e.req.EvidenceDir, "candidate/translation")
	expectedMod, _ := gate.ModEntries(gate.MigratedMod(base["go.mod"]))
	actualMod, modErr := gate.ModEntries(candidate["go.mod"])
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
		if gate.TreeHash(inputs[filepath.Join(filepath.Dir(e.req.CandidateDir), sdk)]) != gate.TreeHash(gate.Module(fixtures, sdk)) {
			depStatus = quality.StatusFail
			depDetail = "candidate SDK differs from authenticated host material"
		}
	}
	e.result.Checks["dependencies"] = check("dependencies", depStatus, depDetail, "input-snapshots", "host-materials")
	protected := quality.StatusPass
	for _, name := range []string{"LICENSE", "go.sum", "internal/usecase/mocks_repo_test.go"} {
		if !bytes.Equal(base[name], candidate[name]) {
			protected = quality.StatusFail
		}
	}
	e.result.Checks["protected_paths"] = check("protected_paths", protected, "protected license/checksums/generated mock: "+protected, "input-snapshots")
	integrity, adaptedTests, err := CompareTests(base, candidate, e.result.MaterialsSHA256)
	if err != nil {
		return out, err
	}
	if err := e.saveJSON("test-integrity", "test_structure", "test-integrity.json", integrity); err != nil {
		return out, err
	}
	if err := gate.CopyFiles(filepath.Join(e.req.EvidenceDir, "adapted-tests"), adaptedTests); err != nil {
		return out, err
	}
	status := map[string]string{"preserved": quality.StatusPass, "violated": quality.StatusFail, "unknown": quality.StatusUnknown}[integrity.Status]
	e.result.Checks["test_preservation"] = check("test_preservation", status, "original test structure: "+integrity.Status, "test-integrity", "input-snapshots")
	if !bounded {
		e.result.Checks["request_scope"] = scope(base, candidate)
		if err := e.saveJSON("scope", "production_scope", "scope.json", e.result.Checks["request_scope"]); err != nil {
			return out, err
		}
	}
	required, err := RequiredTests()
	if err != nil {
		return out, err
	}

	for _, config := range Configurations() {
		row, runErr := e.configuration(ctx, config, baselineRoot, candidateRoot, candidate, fixtures, adaptedTests, required, depStatus == quality.StatusPass)
		if runErr != nil {
			return out, runErr
		}
		if err := e.saveJSON("configuration-"+config.ID, "configuration_checks", "configurations/"+config.ID+".json", row); err != nil {
			return out, err
		}
		for id, value := range row {
			e.merge(id, config.ID, value)
		}
	}
	if bounded {
		proof, err := ProveScope(base, candidate, fixtures, e.analyses)
		if err != nil {
			return out, err
		}
		if err = e.saveJSON("scope-proof", "bounded_structure", "scope-proof.json", proof); err != nil {
			return out, err
		}
		e.result.Checks["request_scope"] = scopeCheck(proof)
		if err = e.saveJSON("scope", "production_scope", "scope.json", e.result.Checks["request_scope"]); err != nil {
			return out, err
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
