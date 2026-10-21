package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/deepseek"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/internal/task"
	"github.com/secacy/agent-harness/scenarios/rpcx/runner"
)

func runBatch(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("harness batch", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "batch JSON configuration file")
	check := flags.Bool("check", false, "check local configuration without credentials, network or execution")
	probe := flags.Bool("probe-model", false, "query and save model capabilities without generation or repository execution")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *path == "" || flags.NArg() != 0 || (*check && *probe) {
		fmt.Fprintln(stderr, "a config file, no positional arguments and mutually exclusive check/probe modes are required")
		return 2
	}
	config, plan, err := loadBatchConfig(*path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if *check {
		fmt.Fprintf(stdout, "Configuration valid: %d targets; model capabilities not queried.\n", len(plan.Targets))
		return 0
	}
	key := os.Getenv("DEEPSEEK_API_KEY")
	clientConfig := deepseek.Config{APIKey: key, Model: config.Model.Name, Endpoint: config.Model.Endpoint, MaxTokens: config.Model.MaxTokens}
	client, err := deepseek.New(clientConfig)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	// 每批重新查询；旧响应不能被当成本次仍然有效的上界。
	capabilities, err := client.Discover(ctx)
	if err != nil {
		return batchError(err, stderr)
	}
	plan.Profile = capabilities.Profile
	if *probe {
		if err := saveBatchMaterials(config, capabilities, nil); err != nil {
			return batchError(err, stderr)
		}
		fmt.Fprintln(stdout, "Capabilities:", config.Directory)
		fmt.Fprintf(stdout, "Model: %s; context=%d; output=%d; reservation=%d tokens per request\n",
			plan.Profile.Model, plan.Profile.ContextTokens, plan.Profile.MaxOutputTokens, plan.Profile.ContextTokens+plan.Profile.MaxOutputTokens)
		return 0
	}
	// 只比较服务声明，协议中的期望值不能替代实际查询得到的预算上界。
	if expected := config.Model.ExpectedContextTokens; expected > 0 && plan.Profile.ContextTokens != expected {
		if err := saveBatchMaterials(config, capabilities, nil); err != nil {
			return batchError(err, stderr)
		}
		fmt.Fprintf(stderr, "model context tokens changed: expected %d, discovered %d; capabilities saved to %s\n", expected, plan.Profile.ContextTokens, config.Directory)
		return 2
	}
	q := plan.Profile.ContextTokens + plan.Profile.MaxOutputTokens
	if plan.Budget.MaxTokens < q {
		fmt.Fprintf(stderr, "campaign token budget %d cannot reserve one model request (%d tokens required)\n", plan.Budget.MaxTokens, q)
		return 2
	}
	for _, target := range plan.Targets {
		if target.Budget.MaxTokens < q {
			fmt.Fprintf(stderr, "target %s token budget %d cannot reserve one model request (%d tokens required)\n", target.Input.ID, target.Budget.MaxTokens, q)
			return 2
		}
	}
	var backend execution.Executor = execution.Local{}
	if !config.Execution.TrustedLocal {
		backend, err = execution.NewDocker(ctx, execution.DockerConfig{Image: config.Execution.Image, CPUs: config.Execution.CPUs,
			MemoryBytes: config.Execution.MemoryMiB * 1024 * 1024, Pids: config.Execution.Pids})
		if err != nil {
			return batchError(err, stderr)
		}
		policy, policyErr := backend.(*execution.Docker).ResourcePolicy(ctx)
		if policyErr != nil {
			return batchError(policyErr, stderr)
		}
		plan.Resources = &policy
	}
	if err := saveBatchMaterials(config, capabilities, backend); err != nil {
		return batchError(err, stderr)
	}
	fmt.Fprintln(stdout, "Batch:", config.Directory)
	clientConfig.BudgetProfile = &plan.Profile
	record, summary, err := campaign.Run(ctx, campaign.Config{
		Directory: filepath.Join(config.Directory, "campaign"), Plan: plan, Backend: backend,
		Execute:  runner.CampaignExecutor(config.Execution.TrustedLocal),
		NewModel: func(task.Target) (budget.ProfiledModel, error) { return deepseek.New(clientConfig) },
		Prepare:  prepareBatchEnrollment(config),
	})
	if record.Directory != "" {
		fmt.Fprintln(stdout, "Summary:", filepath.Join(record.Directory, "summary.md"))
		fmt.Fprintf(stdout, "Status: %s; completed=%d failed=%d cancelled=%d skipped=%d\n", record.Status,
			summary.Counts.Completed, summary.Counts.Failed, summary.Counts.Cancelled, summary.Counts.Skipped)
		// 固定顺序便于人读与命令对照；这里仅展示原门禁计数。
		for _, verdict := range []string{quality.ReadyForReview, quality.Rejected, quality.NeedsAttention} {
			fmt.Fprintf(stdout, "%s: %d\n", verdict, summary.Counts.Verdicts[verdict])
		}
	}
	if err != nil {
		return batchError(err, stderr)
	}
	if summary.Counts.Failed+summary.Counts.Cancelled+summary.Counts.Skipped > 0 {
		return 1
	}
	return 0
}

// saveBatchMaterials 在任何生成调用前保存材料；探测不初始化执行后端。
func saveBatchMaterials(config batchConfig, capabilities deepseek.Capabilities, backend execution.Executor) error {
	if err := os.Mkdir(config.Directory, 0700); err != nil {
		return err
	}
	if err := syncBatchDirectory(filepath.Dir(config.Directory)); err != nil {
		return err
	}
	if err := writeBatchFile(filepath.Join(config.Directory, "models.json"), capabilities.Response); err != nil {
		return err
	}
	type material struct {
		name  string
		value any
	}
	materials := []material{{"config.json", config}, {"capabilities.json", capabilities}}
	if backend != nil {
		materials = append(materials, material{"execution.json", backend.Info()})
	}
	for _, material := range materials {
		if err := writeBatchJSON(filepath.Join(config.Directory, material.name), material.value); err != nil {
			return err
		}
	}
	return nil
}

func writeBatchJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeBatchFile(path, append(data, '\n'))
}

func batchError(err error, stderr io.Writer) int {
	fmt.Fprintln(stderr, err)
	if errors.Is(err, context.Canceled) {
		return 130
	}
	if errors.Is(err, campaign.ErrConfig) {
		return 2
	}
	return 1
}

func writeBatchFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	err = errors.Join(writeErr, file.Sync(), file.Close())
	if err != nil {
		return err
	}
	return syncBatchDirectory(filepath.Dir(path))
}

func syncBatchDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
