package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"example.com/agent-harness/internal/agent"
	"example.com/agent-harness/internal/deepseek"
	"example.com/agent-harness/internal/execution"
	"example.com/agent-harness/scenarios/rpcx/runner"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run 将参数、密钥和具体模型的组装留在入口，Runner 只接收统一接口。
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("harness", flag.ContinueOnError)
	flags.SetOutput(stderr)
	scenario := flags.String("scenario", "scenarios/rpcx", "scenario resource directory")
	specVersion := flags.String("spec-version", "v0.1.0", "frozen specification version: v0.1.0 or v0.2.0 (T3)")
	runs := flags.String("runs", "runs", "output parent directory")
	modelName := flags.String("model", os.Getenv("DEEPSEEK_MODEL"), "DeepSeek model name (or DEEPSEEK_MODEL)")
	endpoint := flags.String("endpoint", "", "full chat completions URL; empty uses the DeepSeek endpoint")
	maxTokens := flags.Int("max-tokens", 4096, "maximum output tokens per model call")
	modelCalls := flags.Int("max-model-calls", 20, "model call budget")
	toolCalls := flags.Int("max-tool-calls", 80, "tool call budget")
	timeout := flags.Duration("timeout", 5*time.Minute, "overall run timeout")
	image := flags.String("image", execution.DefaultImage, "pre-pulled Docker toolchain image")
	cpus := flags.Int("cpus", 2, "container CPU limit")
	memory := flags.Int64("memory-mib", 2048, "container memory limit in MiB")
	pids := flags.Int("pids", 128, "container process limit")
	trusted := flags.Bool("trusted-local", false, "allow Go checks on this host for a controlled, trusted experiment")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *timeout <= 0 || *modelCalls <= 0 || *toolCalls <= 0 || *maxTokens <= 0 {
		fmt.Fprintln(stderr, "positive budgets and no positional arguments are required")
		return 2
	}
	model, err := deepseek.New(deepseek.Config{
		APIKey: os.Getenv("DEEPSEEK_API_KEY"), Model: *modelName, Endpoint: *endpoint, MaxTokens: *maxTokens,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	var backend execution.Executor
	if *trusted {
		backend = execution.Local{}
	} else {
		if *memory < 32 || *memory > 1<<20 || *cpus < 1 || *pids < 8 {
			fmt.Fprintln(stderr, "invalid container resource limits")
			return 2
		}
		backend, err = execution.NewDocker(ctx, execution.DockerConfig{Image: *image, CPUs: *cpus, MemoryBytes: *memory * 1024 * 1024, Pids: *pids})
		if err != nil {
			fmt.Fprintln(stderr, err)
			if errors.Is(err, context.Canceled) {
				return 130
			}
			return 1
		}
	}
	record, err := runner.Run(ctx, runner.Config{
		ScenarioDir: *scenario, SpecVersion: *specVersion, RunsDir: *runs, Model: model, ModelName: *modelName,
		Limits:  agent.Limits{MaxModelCalls: *modelCalls, MaxToolCalls: *toolCalls},
		Timeout: *timeout, TrustedLocal: *trusted, Backend: backend,
	})
	if record.Directory != "" {
		fmt.Fprintln(stdout, "Run:", record.Directory)
		fmt.Fprintln(stdout, "Report:", filepath.Join(record.Directory, "artifacts/report.json"))
		fmt.Fprintln(stdout, "Review:", filepath.Join(record.Directory, "artifacts/review.md"))
		fmt.Fprintf(stdout, "Status: %s; verdict: %s\n", record.Status, record.Verdict)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		if errors.Is(err, runner.ErrInvalidConfig) {
			return 2
		}
		if errors.Is(err, context.Canceled) {
			return 130
		}
		return 1
	}
	return 0
}
