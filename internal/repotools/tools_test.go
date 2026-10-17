package repotools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"example.com/agent-harness/internal/agent"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func newTestExecutor(t *testing.T) (*Executor, string, string) {
	t.Helper()
	workspace, sdk := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(workspace, "client.go"), "package demo\n\nvar name = \"old\"\n")
	writeFile(t, filepath.Join(workspace, "internal/pb/generated.go"), "protected\n")
	writeFile(t, filepath.Join(workspace, ".git/config"), "secret\n")
	writeFile(t, filepath.Join(sdk, "client.go"), "package rpcx\n")
	executor, err := New(Config{Workspace: workspace, ReadOnlyRoots: map[string]string{"sdk": sdk}, ProtectedPaths: []string{"internal/pb/"}})
	if err != nil {
		t.Fatal(err)
	}
	return executor, workspace, sdk
}

func execute(t *testing.T, executor *Executor, name, arguments string) agent.ToolResult {
	t.Helper()
	result, err := executor.Execute(context.Background(), agent.ToolCall{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestFileToolsAndSchemas(t *testing.T) {
	executor, workspace, sdk := newTestExecutor(t)
	for _, tc := range []struct{ name, arguments, want string }{
		{"list_files", `{"root":"workspace","path":"."}`, "client.go\ninternal/\n"},
		{"read_file", `{"root":"workspace","path":"client.go","start_line":3,"end_line":3}`, "var name = \"old\"\n"},
		{"read_file", `{"root":"sdk","path":"client.go"}`, "package rpcx\n"},
		{"search", `{"root":"workspace","path":".","text":"old"}`, "client.go:3:var name = \"old\"\n"},
	} {
		result := execute(t, executor, tc.name, tc.arguments)
		if result.IsError || result.Content != tc.want {
			t.Fatalf("%s: %+v", tc.name, result)
		}
	}
	result := execute(t, executor, "replace_text", `{"path":"client.go","old":"\"old\"","new":"\"new\""}`)
	if result.IsError || !strings.Contains(readFile(t, filepath.Join(workspace, "client.go")), `"new"`) {
		t.Fatalf("replacement failed: %+v", result)
	}
	if got := readFile(t, filepath.Join(sdk, "client.go")); got != "package rpcx\n" {
		t.Fatal("SDK changed")
	}
	for _, spec := range executor.Specs() {
		if !json.Valid(spec.Parameters) || spec.Name == "go_check" {
			t.Fatalf("invalid advertised tool: %+v", spec)
		}
		spec.Parameters[0] = '!'
	}
	if !json.Valid(executor.Specs()[0].Parameters) {
		t.Fatal("caller mutated stored schema")
	}
}

func TestDeniedPathsAndArgumentsDoNotModifyFiles(t *testing.T) {
	executor, workspace, sdk := newTestExecutor(t)
	if err := os.Symlink(sdk, filepath.Join(workspace, "linked-sdk")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(workspace, "internal/pb/generated.go"), filepath.Join(workspace, "linked.go")); err != nil {
		t.Fatal(err)
	}
	before := readFile(t, filepath.Join(workspace, "client.go"))
	for _, tc := range []struct{ name, arguments string }{
		{"read_file", `{"root":"workspace","path":"../oracle/answer"}`},
		{"read_file", `{"root":"workspace","path":"/etc/passwd"}`},
		{"read_file", `{"root":"workspace","path":".git/config"}`},
		{"read_file", `{"root":"workspace","path":"internal/../client.go"}`},
		{"read_file", `{"root":"workspace","path":"linked-sdk/client.go"}`},
		{"read_file", `{"root":"oracle","path":"answer"}`},
		{"read_file", `{"root":"workspace","path":"client.go","start_line":0}`},
		{"read_file", `{"root":"workspace","path":"client.go","start_line":null}`},
		{"read_file", `{"root":"workspace","path":"client.go","start_line":3,"end_line":2}`},
		{"read_file", `{"root":"workspace","path":"client.go","unexpected":true}`},
		{"replace_text", `{"path":"internal/pb/generated.go","old":"protected","new":"changed"}`},
		{"replace_text", `{"path":"linked.go","old":"protected","new":"changed"}`},
		{"replace_text", `{"root":"sdk","path":"client.go","old":"rpcx","new":"changed"}`},
		{"replace_text", `{"path":"client.go","old":"missing","new":"changed"}`},
		{"replace_text", `{"path":"client.go","old":"","new":"changed"}`},
		{"replace_text", `{"path":"client.go","old":"old"}`},
		{"replace_text", `{"path":"client.go","old":"old","new":null}`},
		{"replace_text", `{"path":"new.go","old":"anything","new":"changed"}`},
		{"search", `{"root":"workspace","path":".","text":""}`},
		{"go_check", `{"kind":"test"}`},
		{"shell", `{"command":"rm -rf ."}`},
		{"read_file", `{}`},
		{"read_file", `null`},
		{"read_file", `{} {}`},
	} {
		t.Run(tc.name+"/"+tc.arguments, func(t *testing.T) {
			if result := execute(t, executor, tc.name, tc.arguments); !result.IsError {
				t.Fatalf("accepted forbidden request: %+v", result)
			}
		})
	}
	if readFile(t, filepath.Join(workspace, "client.go")) != before || readFile(t, filepath.Join(workspace, "internal/pb/generated.go")) != "protected\n" || readFile(t, filepath.Join(sdk, "client.go")) != "package rpcx\n" {
		t.Fatal("rejected operation modified files")
	}
	result := execute(t, executor, "search", `{"root":"workspace","path":".","text":"secret"}`)
	if result.IsError || strings.Contains(result.Content, "secret") {
		t.Fatalf("search exposed Git metadata: %+v", result)
	}
}

func TestReplacementRequiresUniqueMatchAndPreservesMode(t *testing.T) {
	executor, workspace, _ := newTestExecutor(t)
	path := filepath.Join(workspace, "client.go")
	for _, content := range []string{"old old", "aaa"} {
		writeFile(t, path, content)
		old := "old"
		if content == "aaa" {
			old = "aa"
		}
		raw, _ := json.Marshal(map[string]string{"path": "client.go", "old": old, "new": "changed"})
		if result := execute(t, executor, "replace_text", string(raw)); !result.IsError || readFile(t, path) != content {
			t.Fatalf("ambiguous replacement accepted: %+v", result)
		}
	}
	writeFile(t, path, "old")
	if err := os.Chmod(path, 0755); err != nil {
		t.Fatal(err)
	}
	// 用硬链接验证替换只发布新文件，不改写与其他路径共享的原始文件。
	alias := filepath.Join(workspace, "internal/pb/alias.go")
	if err := os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	result := execute(t, executor, "replace_text", `{"path":"client.go","old":"old","new":""}`)
	info, err := os.Stat(path)
	if result.IsError || err != nil || info.Mode().Perm() != 0755 || readFile(t, path) != "" || readFile(t, alias) != "old" {
		t.Fatalf("replacement lost mode or changed alias: %+v %v", result, err)
	}
}

func TestOutputAndInputLimits(t *testing.T) {
	executor, workspace, _ := newTestExecutor(t)
	executor.maxOutput = 256
	writeFile(t, filepath.Join(workspace, "large.txt"), strings.Repeat("中文", 500))
	for _, tool := range []struct{ name, args string }{
		{"read_file", `{"root":"workspace","path":"large.txt"}`},
		{"search", `{"root":"workspace","path":".","text":"中文"}`},
	} {
		result := execute(t, executor, tool.name, tool.args)
		if result.IsError || len(result.Content) > 256 || !utf8.ValidString(result.Content) || !strings.Contains(result.Content, "[output truncated]") {
			t.Fatalf("bad truncation: %+v", result)
		}
	}
	writeFile(t, filepath.Join(workspace, "large.txt"), strings.Repeat("x", maxFileBytes+1))
	writeFile(t, filepath.Join(workspace, "binary.txt"), "x\x00y")
	for _, name := range []string{"large.txt", "binary.txt"} {
		raw, _ := json.Marshal(map[string]string{"root": "workspace", "path": name})
		if result := execute(t, executor, "read_file", string(raw)); !result.IsError {
			t.Fatal("invalid file accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := executor.Execute(ctx, agent.ToolCall{Name: "replace_text", Arguments: `{"path":"client.go","old":"old","new":"new"}`})
	if !errors.Is(err, context.Canceled) || !strings.Contains(readFile(t, filepath.Join(workspace, "client.go")), "old") {
		t.Fatal("cancelled edit proceeded")
	}
}

func TestConfigurationRejectsOverlappingRoots(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	for _, config := range []Config{
		{Workspace: root, ReadOnlyRoots: map[string]string{"sdk": child}},
		{Workspace: child, ReadOnlyRoots: map[string]string{"sdk": root}},
		{Workspace: root, ReadOnlyRoots: map[string]string{"workspace": child}},
		{Workspace: root, ProtectedPaths: []string{"../pb"}},
		{Workspace: root, MaxOutputBytes: -1},
		{Workspace: root, LocalGo: &LocalGoConfig{CacheDir: child}},
	} {
		if _, err := New(config); err == nil {
			t.Fatalf("invalid configuration accepted: %+v", config)
		}
	}
}
