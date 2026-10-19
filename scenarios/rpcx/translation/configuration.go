package translation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/internal/task"
	"github.com/secacy/agent-harness/scenarios/rpcx/analysis"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

// merge 保留每配置的身份与发现；任何配置 fail 优先，缺证据不能被另一配置 pass 覆盖。
func (e *evaluation) merge(id, config string, value quality.Check) {
	value.Detail = config + ": " + value.Detail
	for i := range value.Findings {
		value.Findings[i].Detail = config + ": " + value.Findings[i].Detail
	}
	old := e.result.Checks[id]
	if !e.merged[id] {
		e.merged[id] = true
		e.result.Checks[id] = value
		return
	}
	rank := map[string]int{quality.StatusPass: 0, quality.StatusUnknown: 1, quality.StatusBlocked: 1, quality.StatusFail: 2}
	if rank[value.Status] > rank[old.Status] {
		old.Status = value.Status
	}
	old.Detail += "; " + value.Detail
	old.EvidenceIDs = append(old.EvidenceIDs, value.EvidenceIDs...)
	old.Findings = append(old.Findings, value.Findings...)
	e.result.Checks[id] = old
}

func (e *evaluation) configuration(ctx context.Context, config task.BuildConfiguration, baseline, candidate string, production, fixtures, tests map[string][]byte, required []string, canBuild bool) (map[string]quality.Check, error) {
	row := map[string]quality.Check{}
	command := func(name, root string, args []string) (int, []byte, error) {
		r, err := e.command(ctx, config.ID+"-"+name, root, config.Arguments(args))
		return r.ExitCode, r.Stdout, err
	}
	refs := func(name string) []string {
		id := config.ID + "-" + name
		return []string{id, id + "-stdout", id + "-stderr", "configurations"}
	}
	for _, id := range []string{"build_configurations", "build", "baseline_tests", "migration_completeness", "semantic_behavior"} {
		row[id] = check(id, quality.StatusBlocked, "required configuration stage has not completed", "configurations", "input-snapshots")
	}
	for _, step := range []string{"list", "build", "tests"} {
		args := []string{"go", "list", "-json", "./..."}
		if step == "build" {
			args = []string{"go", "build", "-o", "/dev/null", "./..."}
		}
		if step == "tests" {
			args = []string{"go", "test", "-json", "-count=1", "-timeout=45s", "./..."}
		}
		code, data, err := command("baseline-"+step, baseline, args)
		if err != nil {
			return row, err
		}
		if code != 0 {
			return row, errors.New("authenticated baseline failed in configuration " + config.ID)
		}
		if step == "list" {
			if err := ValidateConfiguration(data, config.ID); err != nil {
				return row, err
			}
		}
		if step == "tests" {
			status, _ := gate.Observe(data, required, code, nil, "")
			if status != quality.StatusPass {
				return row, errors.New("authenticated baseline test events incomplete: " + config.ID)
			}
		}
	}
	if !canBuild {
		return row, nil
	}
	code, data, err := command("candidate-list", candidate, []string{"go", "list", "-json", "./..."})
	if err != nil {
		return row, err
	}
	status := quality.StatusPass
	detail := "required source selection and generated mock observed"
	if code != 0 {
		status = quality.StatusUnknown
		detail = "package list did not complete"
	} else if err := ValidateConfiguration(data, config.ID); err != nil {
		status = quality.StatusFail
		detail = err.Error()
	}
	row["build_configurations"] = check("build_configurations", status, detail, refs("candidate-list")...)
	code, _, err = command("candidate-build", candidate, []string{"go", "build", "-o", "/dev/null", "./..."})
	if err != nil {
		return row, err
	}
	status = quality.StatusPass
	if code != 0 {
		status = quality.StatusFail
	}
	row["build"] = check("build", status, "candidate build: "+status, refs("candidate-build")...)
	if code != 0 {
		return row, nil
	}
	code, data, err = command("candidate-tests", candidate, []string{"go", "test", "-json", "-count=1", "-timeout=45s", "./..."})
	if err != nil {
		return row, err
	}
	status, _ = gate.Observe(data, required, code, nil, "")
	if code != 0 {
		status = quality.StatusFail
	}
	row["baseline_tests"] = check("baseline_tests", status, "candidate original test execution: "+status, refs("candidate-tests")...)
	row["migration_completeness"], err = e.analyze(ctx, config, baseline, candidate)
	if err != nil {
		return row, err
	}
	row["semantic_behavior"], err = e.behavior(ctx, config, production, fixtures, tests, required)
	return row, err
}

func (e *evaluation) analyze(ctx context.Context, config task.BuildConfiguration, baseline, candidate string) (quality.Check, error) {
	var reports []analysis.Report
	var ids []string
	for _, entry := range []struct{ name, root string }{{"baseline", baseline}, {"candidate", candidate}} {
		path := "analysis/" + config.ID + "/" + entry.name
		dir := filepath.Join(e.req.EvidenceDir, path)
		r, err := analysis.Inspect(ctx, analysis.Request{ModuleDir: entry.root, SDKDirs: []string{filepath.Join(filepath.Dir(entry.root), "rpcx-v1"), filepath.Join(filepath.Dir(entry.root), "rpcx-v2")}, EvidenceDir: dir, CacheDir: e.req.CacheDir, Tags: config.Tags, Backend: e.backend})
		if err != nil {
			return quality.Check{}, err
		}
		reports = append(reports, r.Report)
		for _, name := range []string{"analysis.json", "execution.json", "inputs.json"} {
			data, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				return quality.Check{}, err
			}
			id := "analysis-" + config.ID + "-" + entry.name + "-" + strings.TrimSuffix(name, ".json")
			if err = e.save(id, "typed_analysis", path+"-"+name, data); err != nil {
				return quality.Check{}, err
			}
			ids = append(ids, id)
		}
	}
	status, detail := quality.StatusUnknown, "typed package loading did not establish coverage"
	if reports[0].Status == "complete" && reports[1].Status == "complete" {
		comparison, err := analysis.Compare(reports[0], reports[1])
		if err != nil {
			return quality.Check{}, err
		}
		id := "comparison-" + config.ID
		if err = e.saveJSON(id, "call_correspondence", "analysis/"+config.ID+"/comparison.json", comparison); err != nil {
			return quality.Check{}, err
		}
		ids = append(ids, id)
		status = quality.StatusPass
		if !configuredCoverage(config.ID, comparison) {
			status = quality.StatusUnknown
		}
		for _, call := range reports[1].Calls {
			if call.Status == "legacy" || call.Status == "violation" {
				status = quality.StatusFail
			}
			if call.Status == "unknown" && status != quality.StatusFail {
				status = quality.StatusUnknown
			}
		}
		detail = "typed RPC correspondence/deadline boundary: " + status
	}
	return check("migration_completeness", status, detail, ids...), nil
}

// 只承认另一必需配置负责的已知互斥文件；所有配置的行仍须共同通过。
// 额外非活动文件、间接调用或遗漏对应关系都不能借此消除缺口。
func configuredCoverage(id string, c analysis.Comparison) bool {
	active, inactive := adapters[0], adapters[1]
	if id == "rpcx_alternate" {
		active, inactive = inactive, active
	} else if id != "default" {
		return false
	}
	if len(c.Matches) != 1 || len(c.Changes) != 0 || len(c.Gaps) != 2 {
		return false
	}
	match := c.Matches[0]
	if match.Baseline.File != active || match.Candidate.File != active || match.Operation != "translate" {
		return false
	}
	trees := map[string]bool{}
	for _, gap := range c.Gaps {
		if gap.Reason != "inactive_build_file" || gap.File != inactive || gap.Tree != "baseline" && gap.Tree != "candidate" || trees[gap.Tree] {
			return false
		}
		trees[gap.Tree] = true
	}
	return trees["baseline"] && trees["candidate"]
}

func (e *evaluation) behavior(ctx context.Context, config task.BuildConfiguration, candidate, fixtures, tests map[string][]byte, required []string) (quality.Check, error) {
	root := filepath.Join(e.req.EvidenceDir, "trusted", config.ID, "translation")
	production := map[string][]byte{}
	for name, data := range candidate {
		if !strings.HasSuffix(name, "_test.go") {
			production[name] = data
		}
	}
	for _, entry := range []struct {
		root  string
		files map[string][]byte
	}{{root, production}, {root, tests}, {filepath.Join(filepath.Dir(root), "rpcx-v1"), gate.Module(fixtures, "rpcx-v1")}, {filepath.Join(filepath.Dir(root), "rpcx-v2"), gate.Module(fixtures, "rpcx-v2")}} {
		if err := gate.CopyFiles(entry.root, entry.files); err != nil {
			return quality.Check{}, err
		}
	}
	probe, err := materials.ReadFile("probes/boundary_test.go.txt")
	if err != nil {
		return quality.Check{}, err
	}
	if err = gate.Write(filepath.Join(root, "internal/repo/webapi/harness_boundary_test.go"), probe); err != nil {
		return quality.Check{}, err
	}
	id := "probe-" + config.ID
	if err = e.save(id, "trusted_probe", "probes/"+config.ID+".go.txt", probe); err != nil {
		return quality.Check{}, err
	}
	ids := []string{id}
	status := quality.StatusPass
	var findings []quality.Finding
	names := map[string]bool{}
	for _, test := range required {
		at := strings.Index(test, "/Test")
		name, _, _ := strings.Cut(test[at+1:], "/")
		names[name] = true
	}
	steps := []struct {
		name, pattern, pkg string
		tests              []string
		attributed         bool
	}{
		{"trusted-tests", "^(" + strings.Join(gate.SortedKeys(names), "|") + ")$", "./...", required, false},
		{"behavior-quick", "^TestHarnessTranslationBoundary$", "./internal/repo/webapi", probeTests(), true},
		{"behavior-expiry", "^TestHarnessTranslationExpiry$", "./internal/repo/webapi", []string{probePrefix + "TestHarnessTranslationExpiry"}, true},
	}
	for _, step := range steps {
		name := config.ID + "-" + step.name
		r, err := e.command(ctx, name, root, config.Arguments([]string{"go", "test", "-json", "-count=1", "-timeout=45s", "-run=" + step.pattern, step.pkg}))
		if err != nil {
			return quality.Check{}, err
		}
		observed, violations := gate.Observe(r.Stdout, step.tests, r.ExitCode, rules("semantic_behavior"), "TRANSLATION_FINDING ")
		if step.attributed && observed == quality.StatusFail && len(violations) == 0 {
			observed = quality.StatusUnknown
		}
		findings = append(findings, violations...)
		if observed == quality.StatusFail || status == quality.StatusPass {
			status = observed
		}
		ids = append(ids, name, name+"-stdout", name+"-stderr")
		// 原适配测试已明确失败时仍运行独立探针，避免把业务失败冒称期限证据。
		if observed != quality.StatusPass && !(step.name == "trusted-tests" && observed == quality.StatusFail) {
			break
		}
	}
	result := check("semantic_behavior", status, "trusted request/deadline/business observations: "+status, ids...)
	for i := range findings {
		findings[i].EvidenceIDs = ids
	}
	result.Findings = append(result.Findings, findings...)
	return result, nil
}

const probePrefix = "github.com/evrone/go-clean-template/internal/repo/webapi/"

func probeModes() []string {
	return []string{"default", "short-parent", "long-parent", "cancelled", "expired", "cancel-in-backend", "remote-error", "store-error", "repeat"}
}
func probeTests() []string {
	out := []string{probePrefix + "TestHarnessTranslationBoundary"}
	for _, mode := range probeModes() {
		out = append(out, probePrefix+"TestHarnessTranslationBoundary/"+mode)
	}
	return out
}
