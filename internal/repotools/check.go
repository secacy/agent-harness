package repotools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/task"
)

// GoCheckConfig 显式启用 Go 检查，后端由宿主选择。
type GoCheckConfig struct {
	// 为空时检查默认配置，非空时检查宿主给定的完整集合。
	BuildConfigurations []task.BuildConfiguration
	Backend             execution.Executor
	ReadOnlyDirs        []string
	// 缓存目录必须已存在，且不能与任何可见目录重叠。
	CacheDir string
	// 为零时一次工具检查最多运行两分钟，所有配置共用此期限。
	Timeout time.Duration
}

type goChecker struct {
	workspace string
	goBinary  string
	backend   execution.Executor
	mounts    []string
	cache     string
	timeout   time.Duration
	maxOutput int
	builds    []task.BuildConfiguration
}

func (e *Executor) checkKind(raw string) (string, error) {
	if e.goCheck == nil {
		return "", errors.New("Go checks are disabled")
	}
	var args struct {
		Kind string `json:"kind"`
	}
	if err := decode(raw, &args, "kind"); err != nil {
		return "", err
	}
	if args.Kind != checkBuild && args.Kind != checkTest {
		return "", errors.New("kind must be build or test")
	}
	return args.Kind, nil
}

func newGoChecker(roots map[string]string, config GoCheckConfig, limit int) (*goChecker, error) {
	if err := task.ValidateBuildConfigurations(config.BuildConfigurations); err != nil {
		return nil, err
	}
	cache, err := canonicalDir(config.CacheDir)
	if err != nil {
		return nil, fmt.Errorf("Go cache: %w", err)
	}
	for name, root := range roots {
		if overlaps(cache, root) {
			return nil, fmt.Errorf("Go cache overlaps root %s", name)
		}
	}
	timeout := config.Timeout
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	if timeout < 0 {
		return nil, errors.New("Go timeout must be positive")
	}
	backend := config.Backend
	if backend == nil {
		backend = execution.Local{}
	}
	goBinary := "go"
	if backend.Info().Kind == execution.KindLocal {
		goBinary, err = exec.LookPath("go")
		if err != nil {
			return nil, err
		}
	}
	mounts := []string{roots[workspaceRoot]}
	for _, path := range config.ReadOnlyDirs {
		root, err := canonicalDir(path)
		if err != nil {
			return nil, err
		}
		visible := false
		for name, value := range roots {
			if name != workspaceRoot && value == root {
				visible = true
			}
		}
		if !visible {
			return nil, errors.New("Go mount must be an advertised read-only root")
		}
		mounts = append(mounts, root)
	}

	builds := make([]task.BuildConfiguration, len(config.BuildConfigurations))
	for i, c := range config.BuildConfigurations {
		builds[i] = task.BuildConfiguration{ID: c.ID, Tags: append([]string{}, c.Tags...)}
	}
	if len(builds) == 0 {
		builds = []task.BuildConfiguration{{ID: "default"}}
	}
	return &goChecker{builds: builds, workspace: roots[workspaceRoot], goBinary: goBinary, backend: backend, mounts: mounts, cache: cache, timeout: timeout, maxOutput: limit}, nil
}

func (c *goChecker) run(parent context.Context, kind string) (agent.ToolResult, error) {
	ctx, cancel := context.WithTimeout(parent, c.timeout)
	defer cancel()
	args := []string{checkBuild, "-o", "/dev/null", "./..."}
	if kind == checkTest {
		args = []string{checkTest, "-count=1", "-timeout=" + c.timeout.String(), "./..."}
	}

	output := newOutput(c.maxOutput)
	failed := false
	exitCode := 0
	for _, config := range c.builds {
		requestArgs := config.Arguments(append([]string{c.goBinary}, args...))
		result, err := c.backend.Run(ctx, execution.Request{Args: requestArgs, Directory: c.workspace, ReadOnlyDirs: c.mounts, CacheDir: c.cache})
		if len(c.builds) > 1 {
			fmt.Fprintf(output, "configuration %s\n", config.ID)
		}
		output.Write(result.Stdout)
		output.Write(result.Stderr)
		if err != nil {
			return agent.ToolResult{Content: output.String()}, fmt.Errorf("run Go check (%s): %w", config.ID, err)
		}
		if ctx.Err() != nil {
			return agent.ToolResult{Content: output.String()}, ctx.Err()
		}
		if result.ExitCode != 0 {
			failed = true
			exitCode = result.ExitCode
			if len(c.builds) > 1 {
				fmt.Fprintf(output, "go %s exited with code %d\n", kind, result.ExitCode)
			}
		}
	}
	status := fmt.Sprintf("go %s passed\n", kind)
	if failed {
		status = fmt.Sprintf("go %s failed in required configuration\n", kind)
		if len(c.builds) == 1 {
			status = fmt.Sprintf("go %s exited with code %d\n", kind, exitCode)
		}
	}
	return agent.ToolResult{Content: output.stringWithPrefix(status), IsError: failed}, nil
}
