package baseline

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/secacy/agent-harness/internal/execution"
)

// Command 保存实际命令、日志和执行结果；测试计数包含子测试。
type Command struct {
	Module       string           `json:"module"`
	BuildConfig  string           `json:"build_config,omitempty"`
	Args         []string         `json:"args"`
	Result       execution.Result `json:"result"`
	Stdout       string           `json:"stdout"`
	StdoutSHA256 string           `json:"stdout_sha256"`
	Stderr       string           `json:"stderr"`
	StderrSHA256 string           `json:"stderr_sha256"`
	PassedTests  int              `json:"passed_tests"`
	Error        string           `json:"error,omitempty"`
}

// Report 只说明冻结基线可复跑，不代表迁移验证或 Agent 效果。
type Report struct {
	SchemaVersion   int            `json:"schema_version"`
	DatasetVersion  string         `json:"dataset_version"`
	FreezeSHA256    string         `json:"freeze_sha256"`
	ToolSHA256      string         `json:"tool_sha256"`
	Backend         execution.Info `json:"backend"`
	Commands        []Command      `json:"commands"`
	InputsUnchanged bool           `json:"inputs_unchanged"`
	Accepted        bool           `json:"accepted"`
	Error           string         `json:"error,omitempty"`
}

// Verify 使用已有执行后端、全新工作副本和缓存复验一份固定开发数据。
func Verify(ctx context.Context, projectRoot, runRoot string, backend execution.Executor) (*Report, error) {
	return VerifyVersion(ctx, projectRoot, runRoot, Version, backend)
}

// VerifyVersion 分别执行发布版本的全部必需配置，不将默认构建代替完整矩阵。
func VerifyVersion(ctx context.Context, projectRoot, runRoot, version string, backend execution.Executor) (report *Report, err error) {
	dataset, err := lookupDataset(version)
	if err != nil {
		return nil, err
	}

	if backend == nil {
		return nil, errors.New("execution backend is required")
	}
	projectRoot, err = filepath.Abs(projectRoot)
	if err != nil {
		return nil, err
	}
	runRoot, err = filepath.Abs(runRoot)
	if err != nil {
		return nil, err
	}
	projectRoot, err = filepath.EvalSymlinks(projectRoot)
	if err != nil {
		return nil, err
	}
	// 输出目录由调用方创建，拒绝复用或通过符号链接写进数据根。
	runRoot, err = filepath.EvalSymlinks(runRoot)
	if err != nil {
		return nil, err
	}
	dataRoot := filepath.Join(projectRoot, "scenarios", "rpcx", "l2", "data", dataset.Version)
	if within(runRoot, dataRoot) || within(dataRoot, runRoot) {
		return nil, errors.New("output and dataset directories must not overlap")
	}
	entries, err := os.ReadDir(runRoot)
	if err != nil || len(entries) != 0 {
		return nil, errors.New("output directory must exist and be empty")
	}
	report = &Report{SchemaVersion: 1, DatasetVersion: dataset.Version, Backend: backend.Info(), Commands: []Command{}}
	defer func() {
		if err != nil {
			report.Error = err.Error()
		}
		err = errors.Join(err, writeJSON(filepath.Join(runRoot, "report.json"), report))
	}()
	files, err := read(ctx, dataRoot)
	if err != nil {
		return report, err
	}
	if err := frozenVersion(files, dataset.Version); err != nil {
		return report, err
	}
	if err := provenance(files); err != nil {
		return report, err
	}
	report.FreezeSHA256 = digest(files["freeze.json"])
	for _, name := range []string{"freeze.json", "tests.json", "provenance.json", "dependencies/modules.json"} {
		if err := write(filepath.Join(runRoot, "inputs", name), files[name]); err != nil {
			return report, err
		}
	}
	if report.ToolSHA256, err = saveTools(ctx, projectRoot, runRoot); err != nil {
		return report, err
	}
	var inventory testInventory
	if err := decode(files["tests.json"], &inventory); err != nil {
		return report, err
	}
	if inventory.SchemaVersion != 1 || len(inventory.Modules) != 3 {
		return report, errors.New("unsupported test inventory")
	}
	workspace := filepath.Join(runRoot, "workspace")
	for name, data := range files {
		if strings.HasPrefix(name, "fixtures/") {
			if err := write(filepath.Join(workspace, strings.TrimPrefix(name, "fixtures/")), data); err != nil {
				return report, err
			}
		}
	}
	moduleCache := filepath.Join(runRoot, "modules")
	if err := unpack(files, moduleCache); err != nil {
		return report, err
	}
	mounted := []string{moduleCache}
	for _, name := range []string{dataset.Business, "rpcx-v1", "rpcx-v2"} {
		mounted = append(mounted, filepath.Join(workspace, name))
	}
	workspaceBefore, err := read(ctx, workspace)
	if err != nil {
		return report, err
	}
	cacheBefore, err := read(ctx, moduleCache)
	if err != nil {
		return report, err
	}
	completed := false
	// 所有退出路径都核对实际输入；取消不能使修改后的输入冒充原材料。
	defer func() {
		finalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		a, sourceErr := read(finalCtx, dataRoot)
		b, workErr := read(finalCtx, workspace)
		c, cacheErr := read(finalCtx, moduleCache)
		report.InputsUnchanged = sourceErr == nil && workErr == nil && cacheErr == nil && equalFiles(files, a) && equalFiles(workspaceBefore, b) && equalFiles(cacheBefore, c)
		if !report.InputsUnchanged {
			err = errors.Join(err, errors.New("baseline inputs changed during verification"), sourceErr, workErr, cacheErr)
		}
		if !completed && err == nil {
			err = errors.New("baseline verification interrupted before completion")
		}
		err = errors.Join(err, ctx.Err())
		report.Accepted = completed && err == nil && report.InputsUnchanged
	}()
	for _, module := range []string{"rpcx-v1", "rpcx-v2", dataset.Business} {
		configurations := []buildConfiguration{{ID: "default"}}
		if module == dataset.Business {
			configurations = dataset.Configurations
		}
		for _, config := range configurations {
			steps := []string{"version", "build", "test"}
			if config.ActiveFile != "" {
				steps = []string{"version", "list", "build", "test"}
			}
			for _, step := range steps {
				args := []string{"go", "version"}
				if step == "build" {
					args = []string{"go", "build", "-o", "/dev/null", "./..."}
				} else if step == "test" {
					args = []string{"go", "test", "-json", "-count=1", "./..."}
				}
				if step == "list" {
					args = []string{"go", "list", "-json", "./..."}
				}
				if len(config.Tags) > 0 && step != "version" {
					args = append([]string{args[0], args[1], "-tags=" + strings.Join(config.Tags, ",")}, args[2:]...)
				}

				cache := moduleCache
				if backend.Info().Kind == execution.KindDocker {
					cache = "/workspace/modules"
				}
				args = append([]string{"env", "GOMODCACHE=" + cache}, args...)
				commandCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
				result, runErr := backend.Run(commandCtx, execution.Request{
					Args: args, Directory: filepath.Join(workspace, module), ReadOnlyDirs: mounted,
					CacheDir: filepath.Join(runRoot, "cache", module, config.ID, step), MaxBytes: 4 << 20,
				})
				runErr = errors.Join(runErr, commandCtx.Err())
				cancel()
				entry := Command{Module: module, BuildConfig: config.ID, Args: args, Result: result,
					Stdout: filepath.Join("logs", module+"-"+config.ID+"-"+step+".stdout.log"), Stderr: filepath.Join("logs", module+"-"+config.ID+"-"+step+".stderr.log"),
					StdoutSHA256: digest(result.Stdout), StderrSHA256: digest(result.Stderr)}
				// 日志单独保存，避免报告内重复嵌入正文或二进制编码。
				entry.Result.Stdout, entry.Result.Stderr = nil, nil
				runErr = errors.Join(runErr, write(filepath.Join(runRoot, entry.Stdout), result.Stdout), write(filepath.Join(runRoot, entry.Stderr), result.Stderr))
				if result.ExitCode != 0 || result.OOMKilled || result.OutputLimit {
					runErr = errors.Join(runErr, fmt.Errorf("%s %s did not complete successfully: exit %d", module, step, result.ExitCode))
				}
				if runErr == nil && step == "test" {
					entry.PassedTests, runErr = testEvents(result.Stdout, inventory.Modules[module])
				}
				if runErr == nil && step == "list" {
					runErr = configurationFiles(result.Stdout, config)
				}

				if runErr != nil {
					entry.Error = runErr.Error()
				}
				report.Commands = append(report.Commands, entry)
				if runErr != nil {
					return report, runErr
				}
			}
		}
	}
	completed = true
	return report, nil
}

func equalFiles(a, b map[string][]byte) bool {
	return maps.EqualFunc(a, b, bytes.Equal)
}

func within(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func saveTools(ctx context.Context, projectRoot, runRoot string) (string, error) {
	names := []string{"go.mod"}
	for _, directory := range []string{"scenarios/rpcx/l2/baseline", "scenarios/rpcx/l2/cmd/verify-baseline", "internal/execution"} {
		entries, err := os.ReadDir(filepath.Join(projectRoot, directory))
		if err != nil {
			return "", err
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
				names = append(names, filepath.Join(directory, e.Name()))
			}
		}
	}
	hashes := map[string]string{}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		data, err := os.ReadFile(filepath.Join(projectRoot, name))
		if err != nil {
			return "", err
		}
		hashes[filepath.ToSlash(name)] = digest(data)
		if err := write(filepath.Join(runRoot, "tool", name), data); err != nil {
			return "", err
		}
	}
	if err := writeJSON(filepath.Join(runRoot, "tool", "manifest.json"), hashes); err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(runRoot, "tool", "manifest.json"))
	return digest(data), err
}
