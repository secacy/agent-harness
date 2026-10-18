package l1

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"example.com/agent-harness/internal/execution"
	"example.com/agent-harness/scenarios/rpcx/checks"
)

//go:embed types.go files.go events.go reference.go verify.go cmd/verify/main.go
var tool embed.FS

// Verify 先产生两套独立观测，再比较 manifest；执行错误不作为预期失败匹配。
func Verify(ctx context.Context, config Config) (report *Report, err error) {
	if config.Backend == nil || (config.Backend.Info().Kind == execution.KindLocal) != config.TrustedLocal {
		return nil, errors.New("explicit backend and matching trusted-local option are required")
	}
	if (config.Quick || len(config.Cases) != 0) && !config.Development {
		return nil, errors.New("quick or subset selection requires development mode")
	}
	paths := []*string{&config.Directory, &config.T3Directory, &config.RunsDir}
	for _, path := range paths {
		*path, err = resolve(*path)
		if err != nil {
			return nil, err
		}
	}
	if overlaps(config.Directory, config.T3Directory) || overlaps(config.RunsDir, config.Directory) || overlaps(config.RunsDir, config.T3Directory) {
		return nil, errors.New("L1 data, T3 inputs and output directories must not overlap")
	}
	data, err := dataset(ctx, config.Directory)
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err := decode(data["manifest.json"], &manifest); err != nil {
		return nil, err
	}
	if err := validateManifest(manifest, data); err != nil {
		return nil, err
	}
	t3, err := readTree(ctx, config.T3Directory)
	if err != nil {
		return nil, err
	}
	var frozenT3 map[string]string
	if err := decode(t3["freeze.json"], &frozenT3); err != nil {
		return nil, err
	}
	actual := hashes(t3)
	delete(actual, "freeze.json")
	if !reflect.DeepEqual(actual, frozenT3) || digest(t3["specs/v0.2.0.md"]) != manifest.SpecSHA256 {
		return nil, errors.New("T3 inputs differ from frozen scene or specification")
	}
	freeze, err := os.ReadFile(filepath.Join(config.Directory, "freeze.json"))
	if err != nil {
		return nil, err
	}
	var locked map[string]string
	if err := decode(freeze, &locked); err != nil {
		return nil, err
	}
	frozen := reflect.DeepEqual(hashes(data), locked)
	if !frozen && !config.Development {
		return nil, errors.New("L1 dataset does not match freeze.json")
	}
	selection := map[string]bool{}
	for _, id := range config.Cases {
		if selection[id] {
			return nil, errors.New("duplicate L1 selection")
		}
		selection[id] = true
	}
	for id := range selection {
		found := false
		for _, c := range manifest.Cases {
			found = found || c.ID == id
		}
		if !found {
			return nil, errors.New("unknown L1 selection: " + id)
		}
	}
	if err := os.MkdirAll(config.RunsDir, 0755); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(config.RunsDir, "rpcx-l1-")
	if err != nil {
		return nil, err
	}
	mode := "frozen"
	if config.Development {
		mode = "development"
	}
	toolFiles := map[string][]byte{}
	for _, name := range []string{"types.go", "files.go", "events.go", "reference.go", "verify.go", "cmd/verify/main.go"} {
		source, _ := tool.ReadFile(name)
		toolFiles[name+".txt"] = source
	}
	report = &Report{SchemaVersion: 1, Directory: dir, Mode: mode, Backend: config.Backend.Info(), StartedAt: time.Now().UTC(),
		DataSHA256: treeHash(data), ToolSHA256: treeHash(toolFiles), T3SHA256: treeHash(t3), FreezeMatched: frozen, FullSelection: len(selection) == 0,
		SlowEnabled: !config.Quick, Counts: map[string]int{}, Results: []Result{}}
	defer func() {
		after, e1 := dataset(context.WithoutCancel(ctx), config.Directory)
		t3After, e2 := readTree(context.WithoutCancel(ctx), config.T3Directory)
		freezeAfter, e3 := os.ReadFile(filepath.Join(config.Directory, "freeze.json"))
		report.InputsUnchanged = e1 == nil && e2 == nil && e3 == nil && treeHash(after) == report.DataSHA256 && treeHash(t3After) == report.T3SHA256 && string(freezeAfter) == string(freeze)
		if !report.InputsUnchanged {
			err = errors.Join(err, errors.New("L1 inputs changed during verification"), e1, e2, e3)
		}
		report.FinishedAt = time.Now().UTC()
		report.Accepted = err == nil && report.Matched && report.InputsUnchanged && report.FreezeMatched && report.FullSelection && report.SlowEnabled && !config.Development
		if err != nil {
			report.Error = err.Error()
		}
		err = errors.Join(err, writeJSON(filepath.Join(dir, "report.json"), report))
	}()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/l1-evidence\n\ngo 1.23.0\n"), 0644); err != nil {
		return report, err
	}
	for path, files := range map[string]map[string][]byte{"inputs/l1": data, "inputs/t3": t3, "materials": toolFiles} {
		if err := writeTree(filepath.Join(dir, path), files); err != nil {
			return report, err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "inputs/l1/freeze.json"), freeze, 0644); err != nil {
		return report, err
	}
	baseFiles, sdk, oracle := map[string][]byte{}, map[string][]byte{}, map[string][]byte{}
	for path, source := range t3 {
		if rel, ok := strings.CutPrefix(path, "fixtures/"); ok {
			baseFiles[rel] = source
			if !strings.HasPrefix(rel, "demo-orders/") {
				sdk[rel] = source
			}
		}
	}
	for name := range sdk {
		if strings.HasSuffix(name, "_test.go") {
			delete(sdk, name)
		}
	}
	oracle["reference_deadline_test.go"] = t3["oracle/deadline_test.go.txt"]
	oracle["reference_expiry_test.go"] = t3["oracle/expiry_test.go.txt"]
	oracle["reference_alternate_test.go"] = data["oracle/alternate_test.go.txt"]
	baseline := filepath.Join(dir, "baseline")
	if err := writeTree(baseline, baseFiles); err != nil {
		return report, err
	}
	cache := filepath.Join(config.RunsDir, "go-cache")
	backend := config.Backend
	if config.TrustedLocal {
		backend = cachedLocal{cache: cache}
	}
	for _, c := range manifest.Cases {
		if len(selection) != 0 && !selection[c.ID] {
			continue
		}
		report.Results = append(report.Results, Result{ID: c.ID, PatchSHA256: c.PatchSHA256, Mismatches: []string{}, Commands: []Command{}})
		r := &report.Results[len(report.Results)-1]
		root := filepath.Join(dir, c.ID)
		workspace := filepath.Join(root, "workspace")
		if err := writeTree(workspace, baseFiles); err != nil {
			return report, err
		}
		patch := filepath.Join(dir, "inputs/l1", c.Patch)
		for i, args := range [][]string{{"git", "apply", "--check", patch}, {"git", "apply", patch}} {
			output, meta, err := runCommand(ctx, execution.Local{}, cache, root, fmt.Sprintf("patch-%d", i), workspace, args...)
			r.Commands = append(r.Commands, meta)
			if err != nil || output.ExitCode != 0 {
				return report, errors.Join(err, errors.New("L1 patch application failed: "+c.ID))
			}
		}
		candidate, err := readTree(ctx, filepath.Join(workspace, "demo-orders"))
		if err != nil {
			return report, err
		}
		whole, err := readTree(ctx, workspace)
		if err != nil {
			return report, err
		}
		r.CandidateSHA256, r.ProductionSHA256 = treeHash(candidate), treeHash(production(candidate))
		replay := filepath.Join(root, "replay")
		if err := writeTree(replay, baseFiles); err != nil {
			return report, err
		}
		replayed, meta, err := runCommand(ctx, execution.Local{}, cache, root, "patch-replay", replay, "git", "apply", patch)
		r.Commands = append(r.Commands, meta)
		if err != nil || replayed.ExitCode != 0 {
			return report, errors.Join(err, errors.New("L1 patch replay failed"))
		}
		replayFiles, err := readTree(ctx, replay)
		if err != nil {
			return report, err
		}
		r.PatchReplayed = treeHash(replayFiles) == treeHash(whole)
		if !r.PatchReplayed {
			return report, errors.New("L1 replay differs from evaluated candidate")
		}
		// 两条支路只共享只读候选；目录、测试文本、日志与结论互不消费。
		// 每个 case 最多两条命令支路，减少两份真实到期检查串行等待的成本。
		caseCtx, cancel := context.WithCancel(ctx)
		var gateErr, referenceErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			r.Gate, gateErr = checks.Evaluate(caseCtx, checks.EvaluationRequest{BaselineDir: filepath.Join(baseline, "demo-orders"), CandidateDir: filepath.Join(workspace, "demo-orders"), SpecPath: filepath.Join(dir, "inputs/t3/specs/v0.2.0.md"), EvidenceDir: filepath.Join(root, "gate"), CacheDir: cache, Backend: backend})
			if gateErr != nil {
				cancel()
			}
		}()
		go func() {
			defer wg.Done()
			referenceErr = verifyReference(caseCtx, backend, cache, root, candidate, sdk, oracle, config.Quick, r)
			if referenceErr != nil {
				cancel()
			}
		}()
		wg.Wait()
		cancel()
		if err := writeJSON(filepath.Join(root, "gate.json"), r.Gate); err != nil {
			return report, err
		}
		if err := errors.Join(gateErr, referenceErr); err != nil {
			return report, fmt.Errorf("case %s: %w", c.ID, err)
		}
		if r.Gate.CandidateSHA256 != r.CandidateSHA256 || r.Gate.SpecSHA256 != manifest.SpecSHA256 {
			return report, errors.New("L1 gate input binding mismatch")
		}
		baseModule := map[string][]byte{}
		for path, source := range baseFiles {
			if rel, ok := strings.CutPrefix(path, "demo-orders/"); ok {
				baseModule[rel] = source
			}
		}
		if r.Gate.BaselineSHA256 != treeHash(baseModule) {
			return report, errors.New("L1 gate baseline binding mismatch")
		}
		for _, evidence := range r.Gate.Evidence {
			if !filepath.IsLocal(evidence.Path) || evidence.Binding.SpecSHA256 != manifest.SpecSHA256 || evidence.Binding.CandidateSHA256 != r.CandidateSHA256 || evidence.Binding.BaselineSHA256 != r.Gate.BaselineSHA256 {
				return report, errors.New("L1 gate evidence binding mismatch")
			}
			source, err := os.ReadFile(filepath.Join(root, "gate", evidence.Path))
			if err != nil || digest(source) != evidence.SHA256 {
				return report, errors.Join(err, errors.New("L1 gate evidence file hash mismatch"))
			}
		}
		after, err := readTree(ctx, workspace)
		if err != nil || treeHash(after) != treeHash(whole) {
			return report, errors.Join(err, errors.New("L1 candidate changed during verification"))
		}
	}
	// 预期只能在两套实际结果均产生后用于比较。
	report.Matched = true
	for i := range report.Results {
		r := &report.Results[i]
		for _, c := range manifest.Cases {
			if c.ID == r.ID {
				r.Mismatches = compare(c.Expected, *r, config.Quick)
			}
		}
		report.Matched = report.Matched && len(r.Mismatches) == 0
		report.Counts[r.Gate.Verdict]++
		report.Counts["expected_mismatches"] += len(r.Mismatches)
		report.Counts["reference_fast_"+r.Reference.Fast.Status]++
		if r.Gate.Verdict == "ReadyForReview" && (r.Reference.Fast.Status == "fail" || r.Reference.Slow.Status == "fail") {
			report.Counts["known_behavior_false_release"]++
		}
	}
	report.Artifacts, err = artifactHashes(dir)
	if err != nil {
		return report, err
	}
	return report, nil
}

func compare(want Expected, got Result, quick bool) []string {
	diffs := []string{}
	if want.Verdict != got.Gate.Verdict {
		diffs = append(diffs, "gate_verdict")
	}
	for id, status := range want.Checks {
		if got.Gate.Checks[id].Status != status {
			diffs = append(diffs, "check:"+id)
		}
	}
	for _, reason := range want.Reasons {
		found := false
		for _, f := range got.Gate.Checks[reason.Check].Findings {
			found = found || f.Code == reason.Code && slices.Contains(f.RuleIDs, reason.Rule)
		}
		if !found {
			diffs = append(diffs, "finding:"+reason.Check+":"+reason.Code+":"+reason.Rule)
		}
	}
	if got.Reference.Build != want.ReferenceBuild {
		diffs = append(diffs, "reference_build")
	}
	if got.Reference.Fast.Status != want.ReferenceFast {
		diffs = append(diffs, "reference_fast")
	}
	for _, rule := range want.ReferenceRules {
		if !slices.Contains(got.Reference.Fast.Rules, rule) {
			diffs = append(diffs, "reference_rule:"+rule)
		}
	}
	if !quick && got.Reference.Fast.Status == "pass" && got.Reference.Slow.Status != "pass" {
		diffs = append(diffs, "reference_actual_expiry")
	}
	slices.Sort(diffs)
	return diffs
}

func validateManifest(m Manifest, files map[string][]byte) error {
	if m.SchemaVersion != 1 || m.Version != "v0.1.0" || m.SpecSHA256 != checks.T3SpecSHA256 || len(m.Cases) != 19 {
		return errors.New("unsupported L1 manifest")
	}
	seen := map[string]bool{}
	for _, c := range m.Cases {
		if c.ID == "" || strings.ContainsAny(c.ID, "/\\.") || seen[c.ID] || c.Patch != "patches/"+c.ID+".patch" || c.Review != "reviews.md#"+c.ID || digest(files[c.Patch]) != c.PatchSHA256 {
			return errors.New("invalid L1 case identity or patch binding")
		}
		seen[c.ID] = true
		if c.Expected.Verdict != "ReadyForReview" && c.Expected.Verdict != "Rejected" && c.Expected.Verdict != "NeedsAttention" {
			return errors.New("invalid L1 expected verdict")
		}
		if len(c.Expected.Checks) != 8 {
			return errors.New("L1 case must declare all required checks")
		}
		for _, id := range []string{"dependencies", "protected_paths", "build", "candidate_tests", "test_structure", "request_scope", "migration_analysis", "trusted_behavior"} {
			if !slices.Contains([]string{"pass", "fail", "unknown", "blocked"}, c.Expected.Checks[id]) {
				return errors.New("invalid L1 expected check")
			}
		}
		if c.Expected.Verdict != "ReadyForReview" && len(c.Expected.Reasons) == 0 {
			return errors.New("L1 non-ready case requires a clause finding")
		}
		if !slices.Contains([]string{"pass", "fail"}, c.Expected.ReferenceBuild) || !slices.Contains([]string{"pass", "fail", "blocked"}, c.Expected.ReferenceFast) {
			return errors.New("invalid L1 reference expectation")
		}
		if c.Expected.ReferenceFast == "fail" && len(c.Expected.ReferenceRules) == 0 {
			return errors.New("L1 reference failure requires a clause")
		}
	}
	return nil
}

func artifactHashes(root string) (map[string]string, error) {
	hashes := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
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
			return errors.New("L1 evidence must be regular files")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		hashes[filepath.ToSlash(rel)] = digest(data)
		return nil
	})
	return hashes, err
}

// cachedLocal 仅用于显式可信夹具，编译缓存可以并发使用，不共享测试进程状态。
type cachedLocal struct{ cache string }

func (cachedLocal) Info() execution.Info { return execution.Local{}.Info() }
func (b cachedLocal) Run(ctx context.Context, req execution.Request) (execution.Result, error) {
	req.CacheDir = b.cache
	return execution.Local{}.Run(ctx, req)
}
