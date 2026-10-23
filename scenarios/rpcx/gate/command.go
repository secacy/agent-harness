package gate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/quality"
)

// Command 保存命令的实际执行信息，JSON 字段兼容既有报告。
type Command struct {
	Args        []string       `json:"args"`
	Directory   string         `json:"directory"`
	Log         string         `json:"log"`
	ExitCode    int            `json:"exit_code"`
	TimedOut    bool           `json:"timed_out"`
	Cancelled   bool           `json:"cancelled,omitempty"`
	DurationMS  int64          `json:"duration_ms"`
	Backend     execution.Info `json:"backend"`
	ContainerID string         `json:"container_id,omitempty"`
	OOMKilled   bool           `json:"oom_killed,omitempty"`
	OutputLimit bool           `json:"output_limit,omitempty"`
}

// Recorder 由单次验证独占，向同一报告追加命令与绑定证据。
// 路径和执行器均由可信场景提供，不接收候选指定的保存位置。
type Recorder struct {
	Directory string
	Binding   quality.Binding
	Commands  *[]Command
	Evidence  *[]quality.Evidence
}

func (r Recorder) Save(id, kind, path string, data []byte) error {
	if err := Write(filepath.Join(r.Directory, path), data); err != nil {
		return err
	}
	*r.Evidence = append(*r.Evidence, quality.Evidence{ID: id, Kind: kind, Path: path, SHA256: Digest(data), Binding: r.Binding})
	return nil
}

func (r Recorder) SaveJSON(id, kind, path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return r.Save(id, kind, path, append(data, '\n'))
}

// Run 保留业务非零退出供上层归因；设施异常和证据失败返回错误。
// 即使父任务取消，也保存已返回的输出并复核输入，避免诊断随取消丢失。
func (r Recorder) Run(parent context.Context, name string, backend interface {
	execution.Executor
	Environment() map[string]string
}, req execution.Request) (execution.Result, error) {
	ctx, cancel := context.WithTimeout(parent, 90*time.Second)
	defer cancel()
	before := map[string]string{}
	for _, path := range req.ReadOnlyDirs {
		files, err := ReadFiles(path)
		if err != nil {
			return execution.Result{}, err
		}
		before[path] = TreeHash(files)
	}
	if err := ctx.Err(); err != nil {
		return execution.Result{}, err
	}
	start := time.Now()
	result, err := backend.Run(ctx, req)
	actual := append([]string{"env", "GOMODCACHE=" + backend.Environment()["GOMODCACHE"]}, req.Args...)
	command := Command{Args: actual, Directory: req.Directory, Log: "logs/" + name + ".stdout.log", ExitCode: result.ExitCode, TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded), Cancelled: errors.Is(ctx.Err(), context.Canceled), DurationMS: time.Since(start).Milliseconds(), Backend: backend.Info(), ContainerID: result.ContainerID, OOMKilled: result.OOMKilled, OutputLimit: result.OutputLimit}
	*r.Commands = append(*r.Commands, command)
	err = errors.Join(err, ctx.Err(), r.Save(name+"-stdout", "command_stdout", command.Log, result.Stdout), r.Save(name+"-stderr", "command_stderr", "logs/"+name+".stderr.log", result.Stderr), r.SaveJSON(name, "command", "commands/"+name+".json", command))
	if result.ExitCode < 0 || result.OOMKilled || result.OutputLimit {
		err = errors.Join(err, errors.New("command did not complete within execution limits"))
	}
	for _, path := range req.ReadOnlyDirs {
		files, readErr := ReadFiles(path)
		if readErr != nil || TreeHash(files) != before[path] {
			err = errors.Join(err, readErr, fmt.Errorf("command input changed: %s", path))
		}
	}
	return result, err
}
