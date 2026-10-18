package checks

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"example.com/agent-harness/internal/quality"
	"example.com/agent-harness/scenarios/rpcx/analysis"
	"example.com/agent-harness/scenarios/rpcx/behavior"
	"example.com/agent-harness/scenarios/rpcx/testintegrity"
)

//go:embed t3-policy.json t3_evaluate.go t3_quality.go t3_scope.go evaluate.go verify.go evidence.go
var t3Materials embed.FS

func evaluateT3(ctx context.Context, req EvaluationRequest) (result Evaluation, err error) {
	result = Evaluation{SchemaVersion: 3, VerifierVersion: "rpcx-t3-evidence-v1", SpecVersion: "v0.2.0", SpecSHA256: T3SpecSHA256,
		Checks: map[string]Check{}, Evidence: []quality.Evidence{}, Gaps: []quality.Gap{}}
	for _, rule := range t3Policy().Requirements {
		result.Checks[rule.ID] = t3Check(rule.ID, "not_run", "not executed")
	}
	if req.Backend == nil {
		return result, errors.New("T3 evaluation requires an explicit execution backend")
	}
	if req, err = normalizeRequest(req); err != nil {
		return result, err
	}
	spec, err := os.ReadFile(req.SpecPath)
	if err != nil || digest(spec) != T3SpecSHA256 {
		return result, errors.New("unsupported T3 specification")
	}
	var pinned map[string]json.RawMessage
	data, _ := t3Materials.ReadFile("t3-policy.json")
	if err := json.Unmarshal(data, &pinned); err != nil {
		return result, err
	}
	inputs := map[string]map[string]string{}
	for _, module := range []string{req.BaselineDir, req.CandidateDir} {
		for _, path := range []string{module, filepath.Join(filepath.Dir(module), "rpcx-v1"), filepath.Join(filepath.Dir(module), "rpcx-v2")} {
			if err := limitT3Tree(ctx, path); err != nil {
				return result, err
			}
			inputs[path], err = snapshotContext(ctx, path)
			if err != nil {
				return result, err
			}
		}
	}
	for _, name := range []string{"baseline", "rpcx-v1", "rpcx-v2"} {
		var expected map[string]string
		if err := json.Unmarshal(pinned[name], &expected); err != nil {
			return result, err
		}
		path := req.BaselineDir
		if name != "baseline" {
			path = filepath.Join(filepath.Dir(path), name)
		}
		if !reflect.DeepEqual(inputs[path], expected) {
			return result, fmt.Errorf("unsupported trusted T3 input: %s", name)
		}
	}
	result.BaselineSHA256, result.CandidateSHA256 = treeDigest(inputs[req.BaselineDir]), treeDigest(inputs[req.CandidateDir])
	defer func() {
		after, checkErr := os.ReadFile(req.SpecPath)
		if checkErr != nil || digest(after) != T3SpecSHA256 {
			err = errors.Join(err, errors.New("T3 specification changed during evaluation"), checkErr)
		}
		for path, before := range inputs {
			after, checkErr := snapshot(path)
			if checkErr != nil || treeDigest(after) != treeDigest(before) {
				err = errors.Join(err, errors.New("T3 input changed during evaluation"), checkErr)
			}
		}
		if err != nil {
			result.Verdict = ""
		}
	}()
	if err := prepareEvidence(req); err != nil {
		return result, err
	}
	// 证据中的工具源码不应成为根模块 go test ./... 的额外输入。
	if err := os.WriteFile(filepath.Join(req.EvidenceDir, "go.mod"), []byte("module example.com/harness-evidence\n\ngo 1.23.0\n"), 0644); err != nil {
		return result, err
	}
	if err := os.WriteFile(filepath.Join(req.EvidenceDir, "spec.md"), spec, 0644); err != nil {
		return result, err
	}
	if err := result.addEvidence(req.EvidenceDir, "specification", "specification", "spec.md"); err != nil {
		return result, err
	}
	materialHashes := map[string]string{}
	if err := os.MkdirAll(filepath.Join(req.EvidenceDir, "verifier-materials"), 0755); err != nil {
		return result, err
	}
	for _, name := range []string{"t3-policy.json", "t3_evaluate.go", "t3_quality.go", "t3_scope.go", "evaluate.go", "verify.go", "evidence.go"} {
		data, _ := t3Materials.ReadFile(name)
		path := "verifier-materials/" + name + ".txt"
		if err := os.WriteFile(filepath.Join(req.EvidenceDir, path), data, 0644); err != nil {
			return result, err
		}
		materialHashes[name] = digest(data)
		if err := result.addEvidence(req.EvidenceDir, "material/"+name, "verifier_material", path); err != nil {
			return result, err
		}
	}
	result.MaterialsSHA256 = treeDigest(materialHashes)
	if err := result.saveEvidence(req.EvidenceDir, "quality-policy", "host_policy", "quality-policy.json", t3Policy()); err != nil {
		return result, err
	}
	if err := result.saveEvidence(req.EvidenceDir, "input-snapshots", "input_snapshots", "input-snapshots.json", inputs); err != nil {
		return result, err
	}
	baseline := filepath.Join(req.EvidenceDir, "baseline", "demo-orders")
	candidate := filepath.Join(req.EvidenceDir, "candidate", "demo-orders")
	for _, pair := range [][2]string{{req.BaselineDir, baseline}, {req.CandidateDir, candidate}} {
		if err := copyLayout(ctx, pair[0], pair[1], filepath.Dir(req.BaselineDir)); err != nil {
			return result, err
		}
	}
	// 副本也按完整字节核验；测试阶段不允许改变生产或测试文件。
	copies := map[string]map[string]string{}
	for _, module := range []string{baseline, candidate} {
		for _, path := range []string{module, filepath.Join(filepath.Dir(module), "rpcx-v1"), filepath.Join(filepath.Dir(module), "rpcx-v2")} {
			files, e := snapshotContext(ctx, path)
			if e != nil {
				return result, e
			}
			copies[path] = files
			if path != module && treeDigest(files) != treeDigest(inputs[filepath.Join(filepath.Dir(req.BaselineDir), filepath.Base(path))]) {
				return result, errors.New("T3 copied SDK does not match trusted input")
			}
		}
	}
	if treeDigest(copies[baseline]) != result.BaselineSHA256 || treeDigest(copies[candidate]) != result.CandidateSHA256 {
		return result, errors.New("T3 copy does not match input")
	}
	defer func() {
		for path, before := range copies {
			after, e := snapshot(path)
			if e != nil || treeDigest(before) != treeDigest(after) {
				err = errors.Join(err, errors.New("T3 verification copy changed"), e)
			}
		}
		if err != nil {
			result.Verdict = ""
		}
	}()
	run := func(dir, label string, args ...string) ([]byte, Command, error) {
		meta, data, err := commandOn(ctx, req.Backend, req.CacheDir, dir, filepath.Join(req.EvidenceDir, label+".log"), args...)
		result.Commands = append(result.Commands, meta)
		if err == nil && (meta.OOMKilled || meta.OutputLimit || meta.TimedOut || meta.Cancelled) {
			err = errors.New("T3 execution resource or time limit exceeded")
		}
		if err == nil {
			err = result.addEvidence(req.EvidenceDir, label, "command_log", label+".log")
		}
		return data, meta, err
	}
	names, err := testNames(baseline)
	if err != nil || len(names) != 10 {
		return result, errors.Join(errors.New("unsupported T3 test inventory"), err)
	}
	data, baseRun, err := run(baseline, "baseline-tests", "go", "test", "-json", "-count=1", "-timeout=30s", "./...")
	if err != nil {
		return result, err
	}
	if baseRun.ExitCode != 0 || candidateTestCheck(data, baseRun, names).Status != "pass" {
		return result, errors.New("trusted T3 baseline tests did not complete successfully")
	}
	result.Checks["protected_paths"] = protectedCheck(baseline, candidate)
	completeT3Check(&result, "protected_paths", "input-snapshots")
	dep, _, err := dependencyCheck(candidate, run)
	if err != nil {
		return result, err
	}
	for _, sdk := range []string{"rpcx-v1", "rpcx-v2"} {
		if treeDigest(inputs[filepath.Join(filepath.Dir(req.BaselineDir), sdk)]) != treeDigest(inputs[filepath.Join(filepath.Dir(req.CandidateDir), sdk)]) {
			dep = Check{Status: "fail", Detail: "DEP-001: candidate SDK files changed"}
		}
	}
	result.Checks["dependencies"] = dep
	completeT3Check(&result, "dependencies", "dependencies", "input-snapshots")
	baseFiles, err := readT3Files(ctx, baseline)
	if err != nil {
		return result, err
	}
	candidateFiles, err := readT3Files(ctx, candidate)
	if err != nil {
		return result, err
	}
	structure, err := testintegrity.Compare(onlyT3Tests(baseFiles), onlyT3Tests(candidateFiles))
	if err != nil {
		return result, err
	}
	if structure.SpecSHA256 != result.SpecSHA256 || structure.Baseline.SHA256 != treeDigest(testHashes(inputs[req.BaselineDir])) || structure.Candidate.SHA256 != treeDigest(testHashes(inputs[req.CandidateDir])) {
		return result, errors.New("T3 test structure input binding mismatch")
	}
	if err := result.saveEvidence(req.EvidenceDir, "test-structure", "test_structure", "test-structure.json", structure); err != nil {
		return result, err
	}
	adapted, err := testintegrity.AdaptBaseline(onlyT3Tests(baseFiles))
	if err != nil {
		return result, err
	}
	if err := writeT3Files(filepath.Join(req.EvidenceDir, "adapted-tests"), adapted); err != nil {
		return result, err
	}
	result.Checks["test_structure"] = structureCheck(structure)
	result.Checks["request_scope"] = requestScope(baseFiles, candidateFiles)
	if err := result.saveEvidence(req.EvidenceDir, "request-scope", "production_scope", "request-scope.json", result.Checks["request_scope"]); err != nil {
		return result, err
	}
	if dep.Status == "pass" {
		out := filepath.Join(req.EvidenceDir, "build")
		if err := os.MkdirAll(out, 0755); err != nil {
			return result, err
		}
		if req.Backend.Info().Kind == "docker" {
			out = "/tmp/"
		}
		_, build, err := run(candidate, "build", "go", "build", "-o", out, "./...")
		if err != nil {
			return result, err
		}
		status := "pass"
		if build.ExitCode != 0 {
			status = "fail"
		}
		result.Checks["build"] = t3Check("build", status, "candidate production build: "+status, "build")
		if status == "pass" {
			data, tests, err := run(candidate, "candidate-tests", "go", "test", "-json", "-count=1", "-timeout=30s", "./...")
			if err != nil {
				return result, err
			}
			result.Checks["candidate_tests"] = candidateTestCheck(data, tests, names)
			var records []analysis.Report
			for _, side := range []struct{ name, dir string }{{"baseline", baseline}, {"candidate", candidate}} {
				label := "analysis-" + side.name
				r, err := analysis.Inspect(ctx, analysis.Request{ModuleDir: side.dir, SDKDirs: []string{filepath.Join(filepath.Dir(side.dir), "rpcx-v1"), filepath.Join(filepath.Dir(side.dir), "rpcx-v2")},
					EvidenceDir: filepath.Join(req.EvidenceDir, label), CacheDir: req.CacheDir, Backend: req.Backend})
				if err != nil {
					return result, err
				}
				if r.Report.InputSHA256 != analysisInput(copies, side.dir) {
					return result, errors.New("T3 analysis input binding mismatch")
				}
				records = append(records, r.Report)
				if err := result.addT3Tree(req.EvidenceDir, label); err != nil {
					return result, err
				}
			}
			check, comparison, err := analysisCheck(records[0], records[1])
			if err != nil {
				return result, err
			}
			result.Checks["migration_analysis"] = check
			if comparison != nil {
				if err := result.saveEvidence(req.EvidenceDir, "analysis-comparison", "rpc_correspondence", "analysis-comparison.json", comparison); err != nil {
					return result, err
				}
				for _, gap := range comparison.Gaps {
					result.Gaps = append(result.Gaps, quality.Gap{CheckID: "migration_analysis", Code: gap.Reason, Detail: gap.Tree + ":" + gap.File})
				}
				for _, change := range comparison.Changes {
					result.Gaps = append(result.Gaps, quality.Gap{CheckID: "migration_analysis", Code: change.Reason, Detail: "RPC correspondence requires review"})
				}
			}
			// 已有充分违规时不再为同一候选支付真实到期成本，仍显式保留缺项。
			if !t3Failed(result.Checks) {
				r, err := behavior.Run(ctx, behavior.Request{BaselineDir: baseline, CandidateDir: candidate, SDKDir: filepath.Join(filepath.Dir(baseline), "rpcx-v2"), EvidenceDir: filepath.Join(req.EvidenceDir, "behavior"), Backend: req.Backend})
				if err != nil {
					return result, err
				}
				if r.SpecSHA256 != result.SpecSHA256 || r.Inputs["baseline"].SHA256 != result.BaselineSHA256 || r.Inputs["candidate"].SHA256 != result.CandidateSHA256 || r.Inputs["rpcx-v2"].SHA256 != treeDigest(copies[filepath.Join(filepath.Dir(baseline), "rpcx-v2")]) {
					return result, errors.New("T3 behavior input binding mismatch")
				}
				for path, hash := range structure.Expected.Files {
					if r.Assembly.Files["demo-orders/"+path] != hash {
						return result, errors.New("T3 adapted test binding mismatch")
					}
				}
				if err := result.addT3Tree(req.EvidenceDir, "behavior"); err != nil {
					return result, err
				}
				result.Checks["trusted_behavior"] = behaviorCheck(r)
			}
		}
	}
	for _, rule := range t3Policy().Requirements {
		if result.Checks[rule.ID].Status == "not_run" {
			result.Checks[rule.ID] = t3Check(rule.ID, "blocked", "prerequisite failed or a demonstrated violation stopped further execution", "input-snapshots")
		}
	}
	if err := result.addT3Tree(req.EvidenceDir, "adapted-tests"); err != nil {
		return result, err
	}
	for _, label := range []string{"baseline", "candidate"} {
		if err := result.addT3Tree(req.EvidenceDir, label); err != nil {
			return result, err
		}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	decision, err := quality.Decide(req.EvidenceDir, t3Policy(), result.binding(), result.Checks, result.Evidence)
	if err != nil {
		return result, err
	}
	result.Verdict, result.Gaps = decision.Verdict, append(result.Gaps, decision.Gaps...)
	return result, nil
}

func completeT3Check(result *Evaluation, id string, refs ...string) {
	c := result.Checks[id]
	base := t3Check(id, c.Status, c.Detail, refs...)
	if len(c.Findings) != 0 {
		base.Findings = c.Findings
		for i := range base.Findings {
			base.Findings[i].EvidenceIDs = refs
		}
	}
	result.Checks[id] = base
}

func t3Failed(checks map[string]Check) bool {
	for _, c := range checks {
		if c.Status == "fail" {
			return true
		}
	}
	return false
}

func readT3Files(ctx context.Context, root string) (map[string][]byte, error) {
	hashes, err := snapshotContext(ctx, root)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for path := range hashes {
		files[path], err = os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

func onlyT3Tests(files map[string][]byte) map[string][]byte {
	tests := map[string][]byte{}
	for path, data := range files {
		if strings.HasSuffix(path, "_test.go") {
			tests[path] = data
		}
	}
	return tests
}

func testHashes(files map[string]string) map[string]string {
	tests := map[string]string{}
	for path, hash := range files {
		if strings.HasSuffix(path, "_test.go") {
			tests[path] = hash
		}
	}
	return tests
}

func writeT3Files(root string, files map[string][]byte) error {
	for path, data := range files {
		dest := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, data, 0644); err != nil {
			return err
		}
	}
	return nil
}

func (result *Evaluation) addT3Tree(root, prefix string) error {
	return filepath.WalkDir(filepath.Join(root, prefix), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "go-cache" {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("T3 evidence must contain regular files")
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		return result.addEvidence(root, rel, "producer_material", rel)
	})
}

func limitT3Tree(ctx context.Context, root string) error {
	count, total := 0, int64(0)
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err = errors.Join(err, ctx.Err()); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return errors.New("T3 inputs must be regular files of at most 1 MiB")
		}
		count++
		total += info.Size()
		if count > 10000 || total > 16<<20 {
			return errors.New("T3 input limit exceeded")
		}
		return nil
	})
}

func analysisInput(copies map[string]map[string]string, module string) string {
	files := map[string]string{}
	for _, root := range []string{module, filepath.Join(filepath.Dir(module), "rpcx-v1"), filepath.Join(filepath.Dir(module), "rpcx-v2")} {
		for path, hash := range copies[root] {
			files[filepath.Base(root)+"/"+path] = hash
		}
	}
	return treeDigest(files)
}
