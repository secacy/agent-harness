// Package repotools 为单个候选仓库提供文件操作和可选的本地 Go 检查。
// 文件访问限制不构成进程沙箱；启用本地检查时只能运行可信仓库。
package repotools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"example.com/agent-harness/internal/agent"
)

const (
	// 工具名称是对外协议，描述与分发共用同一组常量。
	toolListFiles   = "list_files"
	toolReadFile    = "read_file"
	toolSearch      = "search"
	toolReplaceText = "replace_text"
	toolGoCheck     = "go_check"

	workspaceRoot = "workspace"
	checkBuild    = "build"
	checkTest     = "test"

	defaultOutputBytes = 16 * 1024
	maxFileBytes       = 1024 * 1024
	maxEntries         = 10000
)

// Config 由宿主提供，不能由模型通过工具参数改变。
type Config struct {
	Workspace      string
	ReadOnlyRoots  map[string]string
	ProtectedPaths []string
	MaxOutputBytes int
	// 为空时不公布或执行 Go 检查；启用意味着允许在宿主执行可信仓库代码。
	LocalGo *LocalGoConfig
}

// Executor 持有一次仓库任务的访问范围，应配合循环串行使用。
// 运行期间目录不应被其他进程并发替换；当前不防御文件系统竞态攻击。
type Executor struct {
	roots     map[string]string
	protected []string
	maxOutput int
	goCheck   *localGoChecker
}

var _ agent.ToolExecutor = (*Executor)(nil)

func New(config Config) (*Executor, error) {
	roots, protected, err := preparePaths(config)
	if err != nil {
		return nil, err
	}
	limit := config.MaxOutputBytes
	if limit == 0 {
		limit = defaultOutputBytes
	}
	if limit < 256 || limit > maxFileBytes {
		return nil, errors.New("output limit must be between 256 bytes and 1 MiB")
	}
	executor := &Executor{roots: roots, protected: protected, maxOutput: limit}
	if config.LocalGo != nil {
		executor.goCheck, err = newLocalGoChecker(roots, *config.LocalGo, limit)
		if err != nil {
			return nil, err
		}
	}
	return executor, nil
}

// Specs 返回当前可用的工具及参数约束，每次返回独立副本。
func (e *Executor) Specs() []agent.ToolSpec {
	root := map[string]any{"type": "string", "enum": sortedRoots(e.roots)}
	text := map[string]any{"type": "string"}
	positive := map[string]any{"type": "integer", "minimum": 1}
	specs := []agent.ToolSpec{
		toolSpec(toolListFiles, "List direct children of a directory. Use an advertised root alias and a relative path.",
			[]string{"root", "path"}, map[string]any{"root": root, "path": text}),
		toolSpec(toolReadFile, "Read a text file. Optional start_line and end_line use inclusive, one-based line numbers.",
			[]string{"root", "path"}, map[string]any{"root": root, "path": text, "start_line": positive, "end_line": positive}),
		toolSpec(toolSearch, "Search a directory recursively for literal text. Return relative paths, line numbers and matching lines.",
			[]string{"root", "path", "text"}, map[string]any{"root": root, "path": text, "text": text}),
		toolSpec(toolReplaceText, "Replace text in an existing workspace file. The old text must be nonempty and match exactly once.",
			[]string{"path", "old", "new"}, map[string]any{"path": text, "old": text, "new": text}),
	}
	if e.goCheck != nil {
		specs = append(specs, toolSpec(toolGoCheck, "Run a fixed offline Go build or test in the workspace. Success does not imply independent validation has passed.",
			[]string{"kind"}, map[string]any{"kind": map[string]any{"type": "string", "enum": []string{checkBuild, checkTest}}}))
	}
	return specs
}

func toolSpec(name, description string, required []string, properties map[string]any) agent.ToolSpec {
	parameters, _ := json.Marshal(map[string]any{
		"type": "object", "properties": properties, "required": required, "additionalProperties": false,
	})
	return agent.ToolSpec{Name: name, Description: description, Parameters: parameters}
}

func (e *Executor) Execute(ctx context.Context, call agent.ToolCall) (agent.ToolResult, error) {
	if err := ctx.Err(); err != nil {
		return agent.ToolResult{}, err
	}
	output := newOutput(e.maxOutput)
	var err error
	switch call.Name {
	case toolListFiles:
		var args pathArgs
		if err = decode(call.Arguments, &args, "root", "path"); err == nil {
			err = e.list(ctx, args, output)
		}
	case toolReadFile:
		var args readArgs
		if err = decode(call.Arguments, &args, "root", "path"); err == nil {
			err = e.read(ctx, args, output)
		}
	case toolSearch:
		var args searchArgs
		if err = decode(call.Arguments, &args, "root", "path", "text"); err == nil {
			err = e.search(ctx, args, output)
		}
	case toolReplaceText:
		var args replaceArgs
		if err = decode(call.Arguments, &args, "path", "old", "new"); err == nil {
			err = e.replace(ctx, args, output)
		}
	case toolGoCheck:
		var kind string
		kind, err = e.checkKind(call.Arguments)
		if err == nil {
			return e.goCheck.run(ctx, kind)
		}
	default:
		err = fmt.Errorf("unknown tool %q", call.Name)
	}
	if ctx.Err() != nil {
		return agent.ToolResult{Content: output.String()}, ctx.Err()
	}
	if err != nil {
		// 文件或参数错误可由模型修正；检查进程的启动错误由运行器单独上报。
		message := newOutput(e.maxOutput)
		fmt.Fprint(message, err)
		return agent.ToolResult{Content: message.String(), IsError: true}, nil
	}
	return agent.ToolResult{Content: output.String()}, nil
}

// decode 同时校验结构、必填字段与空值，避免缺失参数被零值悄悄接收。
func decode(raw string, target any, required ...string) error {
	if len(raw) > 3*maxFileBytes {
		return errors.New("arguments exceed size limit")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil || fields == nil {
		return errors.New("arguments must be a JSON object")
	}
	for name, value := range fields {
		if string(value) == "null" {
			return fmt.Errorf("argument %s cannot be null", name)
		}
	}
	for _, name := range required {
		if _, ok := fields[name]; !ok {
			return fmt.Errorf("missing argument %s", name)
		}
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}
