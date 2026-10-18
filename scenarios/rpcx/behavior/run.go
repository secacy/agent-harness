package behavior

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/testintegrity"
)

//go:embed probe_test.go.txt run.go files.go events.go types.go
var materials embed.FS

// Run 保存输入快照，以同一生产字节执行两阶段检查。错误不返回可消费的报告，原始日志保留。
func Run(ctx context.Context, req Request) (report Report, err error) {
	if req.Backend == nil {
		return report, errors.New("behavior backend is required")
	}
	paths := []string{req.BaselineDir, req.CandidateDir, req.SDKDir, req.EvidenceDir}
	for i, path := range paths {
		paths[i], err = resolve(path)
		if err != nil {
			return Report{}, err
		}
		for _, previous := range paths[:i] {
			if overlaps(paths[i], previous) {
				return Report{}, errors.New("behavior input and evidence directories must not overlap")
			}
		}
	}
	sources := make([]map[string][]byte, 3)
	for i := range sources {
		info, statErr := os.Stat(paths[i])
		if statErr != nil || !info.IsDir() {
			return Report{}, errors.New("behavior inputs must be directories")
		}
		sources[i], err = readTree(ctx, paths[i])
		if err != nil {
			return Report{}, err
		}
	}
	adapted, err := testintegrity.AdaptBaseline(selectTests(sources[0], true))
	if err != nil {
		return Report{}, err
	}
	adapter, err := testintegrity.Compare(selectTests(sources[0], true), adapted)
	if err != nil {
		return Report{}, err
	}
	sdk := selectTests(sources[2], false)
	if len(sdk) != 2 || digest(sdk["client.go"]) != "597e0281f91f4ce39d5fc9a9ba82052d87dcd4cdff9eb31561f949ecada1f40d" || digest(sdk["go.mod"]) != "b5c281563f355cf0cd376479fef44323561958bf6f012e67159b3714fb42582f" {
		return Report{}, errors.New("unsupported trusted rpcx v2 production files")
	}
	evidence := paths[3]
	if err = os.MkdirAll(evidence, 0755); err != nil {
		return Report{}, err
	}
	entries, err := os.ReadDir(evidence)
	if err != nil || len(entries) != 0 {
		return Report{}, errors.New("behavior evidence directory must be empty")
	}
	defer func() {
		for i, source := range sources {
			after, checkErr := readTree(context.WithoutCancel(ctx), paths[i])
			if checkErr != nil || bind(after).SHA256 != bind(source).SHA256 {
				err = errors.Join(err, errors.New("behavior source changed during verification"), checkErr)
			}
		}
		if err != nil {
			report = Report{}
			if removeErr := os.Remove(filepath.Join(evidence, "report.json")); !errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, removeErr)
			}
		}
	}()
	inputs := map[string]Binding{}
	for i, name := range []string{"baseline", "candidate", "rpcx-v2"} {
		inputs[name] = bind(sources[i])
		if err = writeTree(filepath.Join(evidence, "inputs", name), sources[i]); err != nil {
			return Report{}, err
		}
	}
	tool := map[string][]byte{}
	for _, name := range []string{"probe_test.go.txt", "run.go", "files.go", "events.go", "types.go"} {
		tool[name], err = materials.ReadFile(name)
		if err != nil {
			return Report{}, err
		}
	}
	tool["adaptation.json"], err = json.MarshalIndent(adapter, "", "  ")
	if err != nil {
		return Report{}, err
	}
	if err = writeTree(filepath.Join(evidence, "materials"), tool); err != nil {
		return Report{}, err
	}
	production := selectTests(sources[1], false)
	assembly := map[string][]byte{}
	for path, data := range production {
		assembly["demo-orders/"+path] = data
	}
	for path, data := range adapted {
		assembly["demo-orders/"+path] = data
	}
	probePath := "demo-orders/cmd/server/harness_behavior_test.go"
	// 原有测试一律剥离，宿主探针不覆盖任何候选生产文件。
	if _, exists := assembly[probePath]; exists {
		return Report{}, errors.New("trusted probe path collision")
	}
	assembly[probePath] = tool["probe_test.go.txt"]
	for path, data := range sdk {
		assembly["rpcx-v2/"+path] = data
	}
	root := filepath.Join(evidence, "trusted")
	if err = writeTree(root, assembly); err != nil {
		return Report{}, err
	}
	assemblyBinding := bind(assembly)
	report = Report{SchemaVersion: 1, Version: Version, SpecSHA256: adapter.SpecSHA256, Material: bind(tool), Inputs: inputs,
		Production: bind(production), Assembly: assemblyBinding, Backend: req.Backend.Info(), Tests: []Observation{}, Gaps: []string{}}
	// 每个阶段前后核验装配字节；候选生产代码和原始输入也不能在阶段间被替换。
	for _, slow := range []bool{false, true} {
		if slow && aggregate(report.Tests, report.Gaps) != "pass" {
			for id, rules := range requiredTests(true) {
				report.Tests = append(report.Tests, Observation{id, "not_run", rules})
			}
			report.Gaps = append(report.Gaps, "expiry_not_run:fast_checks_not_passed")
			break
		}
		phase, args := "fast", []string{"go", "test", "-json", "-count=1", "-timeout=20s", "-skip=^TestHarnessExpiry$", "./..."}
		if slow {
			phase, args = "expiry", []string{"go", "test", "-json", "-count=1", "-timeout=50s", "-parallel=2", "-run=^TestHarnessExpiry$", "./cmd/server"}
		}
		before, checkErr := readTree(ctx, root)
		if checkErr != nil || bind(before).SHA256 != assemblyBinding.SHA256 {
			return Report{}, errors.Join(errors.New("trusted assembly changed before execution"), checkErr)
		}
		for i, source := range sources {
			current, checkErr := readTree(ctx, paths[i])
			if checkErr != nil || bind(current).SHA256 != bind(source).SHA256 {
				return Report{}, errors.Join(errors.New("behavior source changed before execution"), checkErr)
			}
		}
		command, runErr := req.Backend.Run(ctx, execution.Request{Args: args, Directory: filepath.Join(root, "demo-orders"),
			ReadOnlyDirs: []string{filepath.Join(root, "demo-orders"), filepath.Join(root, "rpcx-v2")}, CacheDir: filepath.Join(evidence, "go-cache")})
		for name, data := range map[string][]byte{phase + ".jsonl": command.Stdout, phase + ".stderr.log": command.Stderr} {
			runErr = errors.Join(runErr, os.WriteFile(filepath.Join(evidence, name), data, 0644))
		}
		runErr = errors.Join(runErr, writeJSON(filepath.Join(evidence, phase+".execution.json"), struct {
			Backend     execution.Info `json:"backend"`
			Args        []string       `json:"args"`
			ExitCode    int            `json:"exit_code"`
			ContainerID string         `json:"container_id,omitempty"`
			OOMKilled   bool           `json:"oom_killed"`
			OutputLimit bool           `json:"output_limit"`
		}{req.Backend.Info(), args, command.ExitCode, command.ContainerID, command.OOMKilled, command.OutputLimit}))
		after, checkErr := readTree(context.WithoutCancel(ctx), root)
		if checkErr != nil || bind(after).SHA256 != assemblyBinding.SHA256 {
			runErr = errors.Join(runErr, errors.New("trusted assembly changed during execution"), checkErr)
		}
		if command.OutputLimit || command.OOMKilled {
			runErr = errors.Join(runErr, errors.New("behavior execution resource limit exceeded"))
		}
		if runErr != nil {
			return Report{}, fmt.Errorf("%s behavior execution: %w", phase, runErr)
		}
		tests, gaps := observe(command, slow)
		report.Tests, report.Gaps = append(report.Tests, tests...), append(report.Gaps, gaps...)
	}
	// 文件摘要同时绑定候选完整输入与剥离测试后的生产输入，不能只绑定补丁或测试日志。
	sort.Slice(report.Tests, func(i, j int) bool { return report.Tests[i].ID < report.Tests[j].ID })
	report.Status = aggregate(report.Tests, report.Gaps)
	if err = writeJSON(filepath.Join(evidence, "report.json"), report); err != nil {
		return Report{}, err
	}
	return report, nil
}
