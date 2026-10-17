package repotools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"example.com/agent-harness/internal/agent"
)

func requireLocalChecks(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("local process group checks require macOS or Linux")
	}
}

func checkerWithScript(t *testing.T, script string) *Executor {
	t.Helper()
	requireLocalChecks(t)
	executor, err := New(Config{
		Workspace: t.TempDir(), MaxOutputBytes: 256,
		GoCheck: &GoCheckConfig{CacheDir: t.TempDir(), Timeout: 5 * time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "fake-go")
	writeFile(t, path, "#!/bin/sh\n"+script)
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	// 替换命令仅用于验证进程边界；生产配置不接受模型指定可执行文件。
	executor.goCheck.goBinary = path
	return executor
}

func TestLocalCheckArgumentsEnvironmentAndFailure(t *testing.T) {
	t.Setenv("HARNESS_TEST_SECRET", "must-not-leak")
	t.Setenv("GOFLAGS", "untrusted-flags")
	executor := checkerWithScript(t, `printf '%s\n' "$*" "$GOFLAGS" "$GOENV" "$GOPROXY" "$HARNESS_TEST_SECRET"
exit 1
`)
	for _, kind := range []string{"build", "test"} {
		result := execute(t, executor, "go_check", fmt.Sprintf(`{"kind":%q}`, kind))
		wantArgs := "build ./..."
		if kind == "test" {
			wantArgs = "test -count=1 -timeout=5s ./..."
		}
		if !result.IsError || !strings.Contains(result.Content, wantArgs) || !strings.Contains(result.Content, "-mod=readonly\noff\noff") || strings.Contains(result.Content, "must-not-leak") || strings.Contains(result.Content, "untrusted-flags") {
			t.Fatalf("bad command contract: %+v", result)
		}
	}
	for _, arguments := range []string{`{"kind":"test; touch injected"}`, `{"kind":"test","args":["-run=none"]}`} {
		if result := execute(t, executor, "go_check", arguments); !result.IsError {
			t.Fatal("accepted arbitrary command arguments")
		}
	}
	executor.goCheck.goBinary = filepath.Join(t.TempDir(), "missing-go")
	_, err := executor.Execute(context.Background(), agent.ToolCall{Name: "go_check", Arguments: `{"kind":"build"}`})
	if err == nil {
		t.Fatal("startup failure was treated as a recoverable build failure")
	}
}

func TestLocalCheckOutputLimitAndDeadline(t *testing.T) {
	for _, tc := range []struct {
		name     string
		bytes    int
		exitCode int
		status   string
	}{
		{"success", 500, 0, "go build passed\n"},
		{"failure", 500, 1, "go build exited with code 1\n"},
		{"prefix-exceeds-limit", 250, 0, "go build passed\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := fmt.Sprintf("printf '%%0%dd' 0\nexit %d\n", tc.bytes, tc.exitCode)
			executor := checkerWithScript(t, script)
			result := execute(t, executor, "go_check", `{"kind":"build"}`)
			if result.IsError != (tc.exitCode != 0) || len(result.Content) > 256 || strings.Count(result.Content, "[output truncated]") != 1 || !strings.HasPrefix(result.Content, tc.status) || !strings.HasSuffix(result.Content, "[output truncated]\n") {
				t.Fatalf("unbounded or misleading output: %+v", result)
			}
		})
	}
	executor := checkerWithScript(t, "sleep 60\n")
	executor.goCheck.timeout = 50 * time.Millisecond
	start := time.Now()
	_, err := executor.Execute(context.Background(), agent.ToolCall{Name: "go_check", Arguments: `{"kind":"test"}`})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
		t.Fatalf("deadline did not stop command: %v", err)
	}
}

func TestLocalCheckCancellationStopsProcessGroup(t *testing.T) {
	// 子进程尝试延迟写入标记；取消后不应继续执行。
	executor := checkerWithScript(t, `cd "${0%/*}"
(sleep 0.5; printf leaked > leaked) &
printf ready > ready
wait
`)
	markerDir := filepath.Dir(executor.goCheck.goBinary)
	executor.goCheck.timeout = 10 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := executor.Execute(ctx, agent.ToolCall{Name: "go_check", Arguments: `{"kind":"test"}`})
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(markerDir, "ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not start")
		}
		select {
		case err := <-done:
			t.Fatalf("command exited before readiness: %v", err)
		default:
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("wrong cancellation error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("process group did not terminate")
	}
	time.Sleep(600 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(markerDir, "leaked")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("descendant survived cancellation")
	}
}

type scriptedModel func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error)

func (m scriptedModel) Complete(ctx context.Context, messages []agent.Message, specs []agent.ToolSpec) (agent.Reply, error) {
	return m(ctx, messages, specs)
}

func TestLoopRepairsRealFixtureUsingToolFeedback(t *testing.T) {
	requireLocalChecks(t)
	fixtures, err := filepath.Abs("../../scenarios/rpcx/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	layout := t.TempDir()
	for _, name := range []string{"demo-orders", "rpcx-v1", "rpcx-v2"} {
		copyFixture(t, filepath.Join(fixtures, name), filepath.Join(layout, name))
	}
	workspace := filepath.Join(layout, "demo-orders")
	cache := t.TempDir()
	executor, err := New(Config{
		Workspace: workspace, ProtectedPaths: []string{"internal/pb"},
		ReadOnlyRoots: map[string]string{"rpcx-v1": filepath.Join(layout, "rpcx-v1"), "rpcx-v2": filepath.Join(layout, "rpcx-v2")},
		GoCheck:       &GoCheckConfig{CacheDir: cache},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "internal/inventory/client.go"
	steps := []struct {
		name string
		args map[string]string
	}{
		{"read_file", map[string]string{"root": "rpcx-v2", "path": "client.go"}},
		{"replace_text", map[string]string{"path": path, "old": "package inventory", "new": "package ???"}},
		{"go_check", map[string]string{"kind": "build"}},
		{"replace_text", map[string]string{"path": path, "old": "package ???", "new": "package inventory"}},
		{"go_check", map[string]string{"kind": "test"}},
	}
	step := 0
	model := scriptedModel(func(_ context.Context, messages []agent.Message, specs []agent.ToolSpec) (agent.Reply, error) {
		if len(specs) != 5 {
			t.Fatal("real tools were not advertised")
		}
		if step > 0 {
			last := messages[len(messages)-1]
			if last.Role != agent.RoleTool || last.IsError != (step == 3) {
				t.Fatalf("real tool feedback lost: %+v", last)
			}
		}
		if step == len(steps) {
			return agent.Reply{Finish: agent.FinishStop}, nil
		}
		next := steps[step]
		step++
		args, _ := json.Marshal(next.args)
		return agent.Reply{Finish: agent.FinishToolCalls, ToolCalls: []agent.ToolCall{{ID: fmt.Sprint(step), Name: next.name, Arguments: string(args)}}}, nil
	})
	loop := agent.Loop{Model: model, Executor: executor, Tools: executor.Specs(), Limits: agent.Limits{MaxModelCalls: 6, MaxToolCalls: 5}}
	result, err := loop.Run(context.Background(), []agent.Message{{Role: agent.RoleUser, Content: "验证真实工具的错误反馈与修复链路"}})
	if err != nil || result.Reason != agent.ExitCompleted || result.ToolCalls != 5 || result.ModelCalls != 6 {
		t.Fatalf("loop failed: %+v %v", result, err)
	}
	if readFile(t, filepath.Join(workspace, path)) != readFile(t, filepath.Join(fixtures, "demo-orders", path)) {
		t.Fatal("repair did not restore fixture")
	}
	for _, name := range []string{"rpcx-v1", "rpcx-v2"} {
		if readFile(t, filepath.Join(layout, name, "client.go")) != readFile(t, filepath.Join(fixtures, name, "client.go")) {
			t.Fatal("read-only SDK changed")
		}
	}
}

func copyFixture(t *testing.T, source, destination string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		writeFile(t, target, readFile(t, path))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
