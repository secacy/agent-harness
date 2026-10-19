package evaluation

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2"
)

const TranslationVersion = "rpcx-l2-reference-translation-v1"
const translationPackage = "github.com/evrone/go-clean-template"
const translationSpecSHA256 = "d45937d461cf056a82aba50741a27ba3c991c73967047e0d4e723cc70feb5e5f"

// 场景只由宿主注册；请求不能改变必需配置或参考材料。
type referenceScenario struct {
	version, module, specSHA256, dataVersion            string
	data, materials                                     func() (map[string][]byte, error)
	protected                                           []string
	probeFile, probeData, probePackage, behaviorPattern string
	behaviorTests, expiryTests                          []string
	configurations                                      []buildConfiguration
}

type buildConfiguration struct{ id, tags, adapter, ignored string }

func godddScenario() referenceScenario {
	return referenceScenario{version: Version, module: "goddd", specSHA256: godddSpecSHA256, dataVersion: referenceDataVersion,
		data: referenceData, materials: l2.GodddMaterials, protected: []string{"LICENSE", "go.sum"},
		probeFile: "server/l2_reference_test.go", probeData: "routes_test.go.txt", probePackage: "./server", behaviorPattern: "^TestL2ReferenceRoutes$",
		behaviorTests: referenceTests(), expiryTests: []string{godddPackage + "/server/TestL2ReferenceExpiry"}, configurations: []buildConfiguration{{id: "default"}}}
}

func translationScenario() referenceScenario {
	return referenceScenario{version: TranslationVersion, module: "translation", specSHA256: translationSpecSHA256, dataVersion: translationDataVersion,
		data: translationData, materials: l2.TranslationMaterials, protected: []string{"LICENSE", "go.sum", "internal/usecase/mocks_repo_test.go"},
		probeFile: "internal/repo/webapi/l2_reference_test.go", probeData: "translation_test.go.txt", probePackage: "./internal/repo/webapi", behaviorPattern: "^TestL2ReferenceTranslation$",
		behaviorTests: translationReferenceTests(), expiryTests: []string{translationPackage + "/internal/repo/webapi/TestL2ReferenceExpiry"},
		configurations: []buildConfiguration{
			{id: "default", adapter: "translation_google.go", ignored: "translation_rpcx_alternate.go"},
			{id: "rpcx_alternate", tags: "rpcx_alternate", adapter: "translation_rpcx_alternate.go", ignored: "translation_google.go"},
		}}
}

func translationReferenceTests() []string {
	out := []string{translationPackage + "/internal/repo/webapi/TestL2ReferenceTranslation"}
	for _, mode := range []string{"default", "short-parent", "long-parent", "cancelled", "expired", "remote-error", "store-error", "parent-cancel", "repeat", "empty-response", "history", "history-error"} {
		out = append(out, translationPackage+"/internal/repo/webapi/TestL2ReferenceTranslation/"+mode)
	}
	return out
}

var configurationSections = []string{"selection", "build", "candidate_tests", "behavior", "expiry"}

func newConfiguration(config buildConfiguration) ConfigurationReport {
	out := ConfigurationReport{Tags: config.tags, SelectedFiles: map[string][]string{}, Checks: map[string]Observation{}}
	for _, name := range configurationSections {
		out.Checks[name] = Observation{Status: Inconclusive, Reason: "not executed"}
	}
	return out
}

type testInventory struct {
	Packages []string `json:"packages"`
	Empty    []string `json:"packages_without_tests"`
	Original []string `json:"original_tests"`
	Added    []string `json:"added_tests"`
}

type commandRunner func(string, string, ...string) (execution.Result, error)

func checkConfiguration(run commandRunner, scenario referenceScenario, config buildConfiguration, baseline, module, production string, tests testInventory) (ConfigurationReport, error) {
	out := newConfiguration(config)
	command := func(id, dir, verb string, args ...string) (execution.Result, error) {
		prefix := []string{"go", verb}
		if config.tags != "" {
			prefix = append(prefix, "-tags="+config.tags)
		}
		if scenario.module == "translation" {
			id = config.id + "-" + id
		}
		return run(id, dir, append(prefix, args...)...)
	}
	if scenario.module == "translation" {
		for _, entry := range []struct{ id, dir string }{{"baseline", baseline}, {"candidate", module}} {
			result, err := command(entry.id+"-selection", entry.dir, "list", "-json", "-test", "./...")
			if err != nil {
				return out, err
			}
			observation, files := translationSelection(result.Stdout, result.ExitCode, config)
			if entry.id == "baseline" {
				if observation.Status != Pass {
					return out, errors.New("authenticated baseline configuration could not be confirmed")
				}
			} else {
				out.Checks["selection"], out.SelectedFiles = observation, files
			}
		}
	} else {
		out.Checks["selection"] = Observation{Status: Pass}
	}
	result, err := command("baseline-build", baseline, "build", "-o", "/dev/null", "./...")
	if err != nil {
		return out, err
	}
	if result.ExitCode != 0 {
		return out, errors.New("authenticated baseline failed reference build")
	}
	result, err = command("candidate-build", module, "build", "-o", "/dev/null", "./...")
	if err != nil {
		return out, err
	}
	out.Checks["build"] = Observation{Status: Pass}
	if result.ExitCode != 0 {
		out.Checks["build"] = Observation{Status: Fail, Findings: []Finding{{Rule: "BUILD-001", Detail: "candidate failed " + config.id + " configuration build"}}}
		return out, nil
	}
	if out.Checks["selection"].Status != Pass {
		return out, nil
	}
	required := append(append([]string{}, tests.Original...), tests.Added...)
	if scenario.module == "translation" {
		result, err = command("baseline-tests", baseline, "test", "-json", "-count=1", "-timeout=45s", "./...")
		if err != nil {
			return out, err
		}
		if observe(result.Stdout, result.ExitCode, required, tests.Packages, tests.Empty, false).Status != Pass {
			return out, errors.New("authenticated baseline tests could not be confirmed")
		}
	}
	result, err = command("candidate-tests", module, "test", "-json", "-count=1", "-timeout=45s", "./...")
	if err != nil {
		return out, err
	}
	out.Checks["candidate_tests"] = observe(result.Stdout, result.ExitCode, required, tests.Packages, tests.Empty, false)
	for _, step := range []struct {
		id, pattern string
		required    []string
	}{
		{"behavior", scenario.behaviorPattern, scenario.behaviorTests},
		{"expiry", "^TestL2ReferenceExpiry$", scenario.expiryTests},
	} {
		result, err = command(step.id, production, "test", "-json", "-count=1", "-timeout=40s", "-run="+step.pattern, scenario.probePackage)
		if err != nil {
			return out, err
		}
		// 测试身份含完整包名；不从候选的模块声明推导参考身份。
		pkg := strings.Split(step.required[0], "/Test")[0]
		out.Checks[step.id] = observe(result.Stdout, result.ExitCode, step.required, []string{pkg}, nil, true)
		if out.Checks[step.id].Status != Pass {
			break
		}
	}
	return out, nil
}

// translationSelection 根据 Go 实际选中文件检查互斥实现及生成 mock，不复用门禁配置解析。
func translationSelection(data []byte, exit int, config buildConfiguration) (Observation, map[string][]string) {
	out := Observation{Status: Inconclusive, Reason: "configuration selection unavailable"}
	files := map[string][]string{}
	if exit != 0 {
		return out, files
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	adapter, mock := false, false
	for {
		var pkg struct {
			ImportPath                                         string
			GoFiles, IgnoredGoFiles, TestGoFiles, XTestGoFiles []string
		}
		err := decoder.Decode(&pkg)
		if err == io.EOF {
			break
		}
		if err != nil {
			out.Reason = "invalid Go list JSON"
			return out, files
		}
		switch pkg.ImportPath {
		case translationPackage + "/internal/repo/webapi":
			if _, exists := files["webapi/go"]; exists {
				out.Reason = "duplicate adapter package"
				return out, files
			}
			files["webapi/go"], files["webapi/ignored"] = pkg.GoFiles, pkg.IgnoredGoFiles
			adapter = slices.Contains(pkg.GoFiles, config.adapter) && !slices.Contains(pkg.GoFiles, config.ignored) && slices.Contains(pkg.IgnoredGoFiles, config.ignored)
		case translationPackage + "/internal/usecase":
			if _, exists := files["usecase/tests"]; exists {
				out.Reason = "duplicate usecase package"
				return out, files
			}
			files["usecase/tests"] = append(append([]string{}, pkg.TestGoFiles...), pkg.XTestGoFiles...)
			mock = slices.Contains(files["usecase/tests"], "mocks_repo_test.go")
		}
	}
	if files["webapi/go"] == nil || files["usecase/tests"] == nil {
		return out, files
	}
	out = Observation{Status: Pass}
	if !adapter {
		out.Status = Fail
		out.Findings = append(out.Findings, Finding{Rule: "BUILD-001", Detail: "required mutually exclusive adapter selection changed"})
	}
	if !mock {
		out.Status = Fail
		out.Findings = append(out.Findings, Finding{Rule: "PATH-001", Detail: "protected generated mock no longer participates in original tests"})
	}
	return out, files
}

func mergedConfigurationChecks(configs map[string]ConfigurationReport) map[string]Observation {
	out := map[string]Observation{}
	for _, name := range []string{"build", "candidate_tests", "behavior", "expiry"} {
		checks := map[string]Observation{}
		var findings []Finding
		// 固定顺序使结论与定位不依赖 map 遍历。
		for _, id := range []string{"default", "rpcx_alternate"} {
			config := configs[id]
			sections := []string{name}
			if name == "build" {
				sections = append(sections, "selection")
			}
			for _, section := range sections {
				check := config.Checks[section]
				checks[id+"/"+section] = check
				for _, finding := range check.Findings {
					finding.Detail = id + ": " + finding.Detail
					findings = append(findings, finding)
				}
			}
		}
		out[name] = Observation{Status: aggregate(checks), Findings: findings}
		if out[name].Status == Inconclusive {
			out[name] = Observation{Status: Inconclusive, Reason: "required configuration evidence incomplete", Findings: findings}
		}
	}
	return out
}

func mergeConfigurations(report *Report) {
	for name, check := range mergedConfigurationChecks(report.Configurations) {
		report.Checks[name] = check
	}
}
