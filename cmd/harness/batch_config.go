package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/deepseek"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/task"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/round"
	"github.com/secacy/agent-harness/scenarios/rpcx/runner"
)

type batchConfig struct {
	SchemaVersion        int             `json:"schema_version"`
	ID                   string          `json:"id"`
	Directory            string          `json:"directory"`
	Concurrency          int             `json:"concurrency"`
	ExecutionConcurrency int             `json:"execution_concurrency"`
	Budget               budget.Limits   `json:"budget"`
	Model                batchModel      `json:"model"`
	Execution            batchExecution  `json:"execution"`
	Targets              []batchTarget   `json:"targets"`
	Enrollment           *round.Artifact `json:"enrollment,omitempty"`
	enrollmentData       []byte
}

type batchModel struct {
	Name      string `json:"name"`
	Endpoint  string `json:"endpoint,omitempty"`
	MaxTokens int    `json:"max_tokens"`
}

type batchExecution struct {
	TrustedLocal bool   `json:"trusted_local"`
	Image        string `json:"image,omitempty"`
	CPUs         int    `json:"cpus,omitempty"`
	MemoryMiB    int64  `json:"memory_mib,omitempty"`
	Pids         int    `json:"pids,omitempty"`
}

type batchTarget struct {
	Input         task.Target   `json:"input"`
	Budget        budget.Limits `json:"budget"`
	MaxModelCalls int           `json:"max_model_calls"`
	MaxToolCalls  int           `json:"max_tool_calls"`
	Timeout       string        `json:"timeout"`
}

var batchID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)

func loadBatchConfig(path string) (batchConfig, campaign.Plan, error) {
	var config batchConfig
	var plan campaign.Plan
	file, err := os.Open(path)
	if err != nil {
		return config, plan, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	if err := errors.Join(readErr, file.Close()); err != nil {
		return config, plan, err
	}
	if len(data) > 1024*1024 {
		return config, plan, errors.New("batch configuration exceeds 1 MiB limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, plan, fmt.Errorf("decode batch configuration: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return config, plan, errors.New("exactly one batch configuration is required")
	}
	if config.SchemaVersion != 1 || !batchID.MatchString(config.ID) || config.Directory == "" || config.Concurrency < 1 || config.ExecutionConcurrency < 1 ||
		config.Budget.MaxCalls < 1 || config.Budget.MaxTokens < 1 || len(config.Targets) == 0 || config.Model.MaxTokens < 1 {
		return config, plan, errors.New("schema version 1, valid ID, directory, positive limits and targets are required")
	}
	// 只校验请求配置；占位鉴权不读取环境，也不会发起 HTTP。
	if _, err := deepseek.New(deepseek.Config{APIKey: "configuration-check", Model: config.Model.Name, Endpoint: config.Model.Endpoint, MaxTokens: config.Model.MaxTokens}); err != nil {
		return config, plan, err
	}
	if _, err := deepseek.ModelsEndpoint(config.Model.Endpoint); err != nil {
		return config, plan, err
	}
	e := &config.Execution
	if e.Image == "" {
		e.Image = execution.DefaultImage
	}
	if e.CPUs == 0 {
		e.CPUs = 2
	}
	if e.MemoryMiB == 0 {
		e.MemoryMiB = 2048
	}
	if e.Pids == 0 {
		e.Pids = 128
	}
	if e.CPUs < 1 || e.MemoryMiB < 32 || e.MemoryMiB > 1<<20 || e.Pids < 8 || strings.HasPrefix(e.Image, "-") {
		return config, plan, errors.New("invalid container resource limits")
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return config, plan, err
	}
	resolve := func(value string) (string, error) {
		if !filepath.IsAbs(value) {
			value = filepath.Join(base, value)
		}
		return batchCanonical(value)
	}
	config.Directory, err = resolve(config.Directory)
	if err != nil {
		return config, plan, err
	}
	if _, err := os.Lstat(config.Directory); !errors.Is(err, os.ErrNotExist) {
		return config, plan, errors.New("batch output directory must not exist")
	}
	if info, err := os.Stat(filepath.Dir(config.Directory)); err != nil || !info.IsDir() {
		return config, plan, errors.New("batch output parent must exist")
	}
	plan = campaign.Plan{ID: config.ID, Concurrency: config.Concurrency, ExecutionConcurrency: config.ExecutionConcurrency, Budget: config.Budget}
	ids := map[string]bool{}
	for i := range config.Targets {
		target := &config.Targets[i]
		if ids[target.Input.ID] || target.Budget.MaxCalls < 1 || target.Budget.MaxTokens < 1 || target.MaxModelCalls < 1 || target.MaxToolCalls < 1 {
			return config, plan, errors.New("unique targets and positive target limits are required")
		}
		ids[target.Input.ID] = true
		timeout, err := parseBatchTimeout(target.Timeout)
		if err != nil {
			return config, plan, err
		}
		t := &target.Input
		if t.Repository.Directory == "" || t.Specification.Path == "" {
			return config, plan, errors.New("repository and specification paths are required")
		}
		paths := []*string{&t.Repository.Directory, &t.Specification.Path}
		for name, dependency := range t.Dependencies {
			if dependency.Directory == "" {
				return config, plan, errors.New("dependency paths are required")
			}
			dependency.Directory, err = resolve(dependency.Directory)
			if err != nil {
				return config, plan, err
			}
			t.Dependencies[name] = dependency
		}
		for _, p := range paths {
			*p, err = resolve(*p)
			if err != nil {
				return config, plan, err
			}
		}
		if err := runner.ValidateTarget(*t, config.Directory); err != nil {
			return config, plan, err
		}
		plan.Targets = append(plan.Targets, campaign.Target{Input: *t, Budget: target.Budget,
			Limits: agent.Limits{MaxModelCalls: target.MaxModelCalls, MaxToolCalls: target.MaxToolCalls}, Timeout: timeout})
	}
	if config.Enrollment != nil {
		if config.Enrollment.Path == "" || config.Enrollment.SHA256 == "" {
			return config, plan, errors.New("enrollment path and SHA-256 pin are required")
		}
		config.Enrollment.Path, err = resolve(config.Enrollment.Path)
		if err != nil {
			return config, plan, err
		}
		config.enrollmentData, err = readBatchEnrollment(config, plan.Targets)
		if err != nil {
			return config, plan, err
		}
	}
	return config, plan, nil
}

// batchCanonical 解析现存祖先，防止输出路径经符号链接落入输入目录。
func batchCanonical(path string) (string, error) {
	path = filepath.Clean(path)
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, os.ErrNotExist) || filepath.Dir(path) == path {
		return "", err
	}
	parent, err := batchCanonical(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(path)), nil
}

// parseBatchTimeout 保留人可读的配置单位，Campaign 仍使用 time.Duration。
func parseBatchTimeout(value string) (time.Duration, error) {
	timeout, err := time.ParseDuration(value)
	if err != nil || timeout <= 0 {
		return 0, errors.New("positive target timeout is required")
	}
	return timeout, nil
}
