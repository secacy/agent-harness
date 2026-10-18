package repotools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/execution"
)

// GoCheckConfig 显式启用 Go 检查，后端由宿主选择。
type GoCheckConfig struct {
	Backend      execution.Executor
	ReadOnlyDirs []string
	// 缓存目录必须已存在，且不能与任何可见目录重叠。
	CacheDir string
	// 为零时每条命令最多运行两分钟。
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

	return &goChecker{workspace: roots[workspaceRoot], goBinary: goBinary, backend: backend, mounts: mounts, cache: cache, timeout: timeout, maxOutput: limit}, nil
}

func (c *goChecker) run(parent context.Context, kind string) (agent.ToolResult, error) {
	ctx, cancel := context.WithTimeout(parent, c.timeout)
	defer cancel()
	args := []string{checkBuild, "./..."}
	if kind == checkTest {
		args = []string{checkTest, "-count=1", "-timeout=" + c.timeout.String(), "./..."}
	}
	if c.backend.Info().Kind == execution.KindDocker && kind == checkBuild {
		args = []string{checkBuild, "-o", "/tmp/", "./..."}
	}
	result, err := c.backend.Run(ctx, execution.Request{Args: append([]string{c.goBinary}, args...), Directory: c.workspace, ReadOnlyDirs: c.mounts, CacheDir: c.cache})
	output := newOutput(c.maxOutput)
	output.Write(result.Stdout)
	output.Write(result.Stderr)
	if err != nil {
		return agent.ToolResult{Content: output.String()}, fmt.Errorf("run Go check: %w", err)
	}
	status := fmt.Sprintf("go %s passed\n", kind)
	if result.ExitCode != 0 {
		status = fmt.Sprintf("go %s exited with code %d\n", kind, result.ExitCode)
	}
	return agent.ToolResult{Content: output.stringWithPrefix(status), IsError: result.ExitCode != 0}, nil
}
