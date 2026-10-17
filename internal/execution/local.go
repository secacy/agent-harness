package execution

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// Local 只用于可信代码，进程组回收不约束主动脱离该组的后代。
type Local struct{}

func (Local) Info() Info { return Info{Kind: KindLocal} }

func (Local) Run(ctx context.Context, req Request) (Result, error) {
	result := Result{ExitCode: -1}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if len(req.Args) == 0 || req.Directory == "" || req.CacheDir == "" {
		return result, errors.New("command, directory and cache are required")
	}
	for _, dir := range []string{"home", "tmp", "build", "modules", "gopath"} {
		if err := os.MkdirAll(filepath.Join(req.CacheDir, dir), 0700); err != nil {
			return result, err
		}
	}
	commandCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout, stderr := outputs(req.MaxBytes, cancel)
	command := exec.CommandContext(commandCtx, req.Args[0], req.Args[1:]...)
	command.Dir, command.Env = req.Directory, goEnv(req.CacheDir)
	command.Stdout, command.Stderr, command.WaitDelay = stdout, stderr, time.Second
	if err := configureProcess(command); err != nil {
		return result, err
	}
	err := command.Run()
	cleanupErr := terminateProcessGroup(command)
	result.Stdout, result.Stderr = stdout.bytes(), stderr.bytes()
	result.OutputLimit = stdout.exceeded() || stderr.exceeded()
	if command.ProcessState != nil {
		result.ExitCode = command.ProcessState.ExitCode()
	}
	if result.OutputLimit {
		return result, errors.Join(ErrOutputLimit, ctx.Err(), cleanupErr)
	}
	if ctx.Err() != nil {
		return result, errors.Join(ctx.Err(), cleanupErr)
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		err = nil
	}
	return result, errors.Join(err, cleanupErr)
}

func goEnv(cache string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(cache, "home"),
		"TMPDIR=" + filepath.Join(cache, "tmp"), "GOCACHE=" + filepath.Join(cache, "build"),
		"GOMODCACHE=" + filepath.Join(cache, "modules"), "GOPATH=" + filepath.Join(cache, "gopath"),
		"GOPROXY=off", "GOSUMDB=off", "GOWORK=off", "GOENV=off",
		"GOTOOLCHAIN=local", "GOFLAGS=-mod=readonly", "GOTELEMETRY=off", "CGO_ENABLED=0",
	}
}

// 超出保留上限即取消命令，仍接收管道余量直到进程退出。
type capture struct {
	mu     sync.Mutex
	data   []byte
	limit  int
	over   bool
	cancel context.CancelFunc
}

func outputs(limit int, cancel context.CancelFunc) (*capture, *capture) {
	if limit <= 0 {
		limit = 4 * 1024 * 1024
	}
	return &capture{limit: limit, cancel: cancel}, &capture{limit: limit, cancel: cancel}
}

func (c *capture) Write(data []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := min(len(data), c.limit-len(c.data))
	c.data = append(c.data, data[:n]...)
	if n < len(data) {
		c.over = true
		c.cancel()
	}
	return len(data), nil
}

func (c *capture) bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.data...)
}

func (c *capture) exceeded() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.over
}
