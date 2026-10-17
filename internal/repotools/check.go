package repotools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"example.com/agent-harness/internal/agent"
)

// LocalGoConfig 显式启用可信仓库的宿主执行，不提供网络或文件系统沙箱。
type LocalGoConfig struct {
	// 缓存目录必须已存在，且不能与任何可见目录重叠。
	CacheDir string
	// 为零时每条命令最多运行两分钟。
	Timeout time.Duration
}

type localGoChecker struct {
	workspace string
	goBinary  string
	env       []string
	timeout   time.Duration
	maxOutput int
}

func (e *Executor) checkKind(raw string) (string, error) {
	if e.goCheck == nil {
		return "", errors.New("local Go checks are disabled")
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

func newLocalGoChecker(roots map[string]string, config LocalGoConfig, limit int) (*localGoChecker, error) {
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
	goBinary, err := exec.LookPath("go")
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"build", "modules", "gopath", "home", "tmp"} {
		if err := os.MkdirAll(filepath.Join(cache, name), 0700); err != nil {
			return nil, err
		}
	}
	// 仅传递运行所需环境，不继承宿主的凭证、代理和自定义 Go 参数。
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + filepath.Join(cache, "home"),
		"TMPDIR=" + filepath.Join(cache, "tmp"),
		"GOCACHE=" + filepath.Join(cache, "build"),
		"GOMODCACHE=" + filepath.Join(cache, "modules"),
		"GOPATH=" + filepath.Join(cache, "gopath"),
		"GOPROXY=off", "GOSUMDB=off", "GOWORK=off", "GOENV=off",
		"GOTOOLCHAIN=local", "GOFLAGS=-mod=readonly", "GOTELEMETRY=off", "CGO_ENABLED=0",
	}
	return &localGoChecker{workspace: roots[workspaceRoot], goBinary: goBinary, env: env, timeout: timeout, maxOutput: limit}, nil
}

func (c *localGoChecker) run(parent context.Context, kind string) (agent.ToolResult, error) {
	ctx, cancel := context.WithTimeout(parent, c.timeout)
	defer cancel()
	args := []string{checkBuild, "./..."}
	if kind == checkTest {
		args = []string{checkTest, "-count=1", "-timeout=" + c.timeout.String(), "./..."}
	}
	command := exec.CommandContext(ctx, c.goBinary, args...)
	command.Dir = c.workspace
	command.Env = c.env
	command.WaitDelay = time.Second
	if err := configureProcess(command); err != nil {
		return agent.ToolResult{}, err
	}
	output := newOutput(c.maxOutput)
	command.Stdout, command.Stderr = output, output
	err := command.Run()
	// 正常退出后也回收仍留在该进程组中的后代；主动脱离进程组的进程不在保证内。
	cleanupErr := terminateProcessGroup(command)
	if ctx.Err() != nil {
		return agent.ToolResult{Content: output.String()}, errors.Join(ctx.Err(), cleanupErr)
	}
	if cleanupErr != nil {
		return agent.ToolResult{Content: output.String()}, fmt.Errorf("clean up Go process group: %w", cleanupErr)
	}
	status := fmt.Sprintf("go %s passed\n", kind)
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return agent.ToolResult{Content: output.String()}, fmt.Errorf("run Go check: %w", err)
		}
		status = fmt.Sprintf("go %s exited with code %d\n", kind, exit.ExitCode())
	}
	// 状态放在输出之前，即使命令输出截断，模型也能看到退出码。
	return agent.ToolResult{Content: output.stringWithPrefix(status), IsError: err != nil}, nil
}
