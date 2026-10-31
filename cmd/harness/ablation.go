package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/secacy/agent-harness/internal/deepseek"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/ablation"
	"github.com/secacy/agent-harness/scenarios/rpcx/runner"
)

// The P1 entry is deliberately distinct from production batch/audit contracts.
// Preparation/report do not read a key; probing only requests model metadata.
func runAblation(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "Usage: harness ablation <prepare|probe|freeze|run|reference|report> [flags]\nUse a mode followed by --help for its inputs.")
		return 2
	}
	mode := args[0]
	if mode == "freeze" || mode == "run" || mode == "reference" {
		return runAblationLaunch(ctx, mode, args[1:], stdout, stderr)
	}
	if mode != "prepare" && mode != "report" && mode != "probe" {
		fmt.Fprintln(stderr, "unsupported ablation mode; supported: prepare, probe, report")
		return 2
	}
	flags := flag.NewFlagSet("harness ablation "+mode, flag.ContinueOnError)
	flags.SetOutput(stderr)
	project := flags.String("project", ".", "project root for public task inputs")
	root := flags.String("experiment", "", "saved P1 experiment root")
	output := flags.String("output", "", "new output directory")
	draft := flags.String("draft", "", "draft protocol for metadata probing (no generation)")
	price := flags.String("price", "", "optional frozen price basis JSON")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *output == "" || (mode == "report" && *root == "") || (mode == "prepare" && *root != "") || (mode == "probe" && *draft == "") {
		fmt.Fprintln(stderr, "explicit output and mode-appropriate inputs are required")
		return 2
	}
	var err error
	if mode == "prepare" {
		err = ablation.PrepareDraft(ctx, *project, *output)
	} else if mode == "probe" {
		err = probeAblation(ctx, *draft, *price, *output)
	} else {
		var summary ablation.Summary
		summary, err = ablation.Collect(ctx, *root)
		if err == nil {
			err = ablation.WriteReport(*output, summary)
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "P1", mode, *output)
	if mode == "prepare" {
		fmt.Fprintln(stdout, "Draft only: model/engine identity, source freeze, spending authorization and human review remain required.")
	}
	return 0
}

func probeAblation(ctx context.Context, draft, pricePath, output string) error {
	data, err := os.ReadFile(draft)
	if err != nil {
		return err
	}
	var p ablation.Protocol
	if err = ablation.Decode(data, &p); err != nil {
		return err
	}
	if p.Model.Provider != "deepseek" || p.Model.MaxOutputTokens <= 0 {
		return errors.New("probe requires a DeepSeek draft and output limit")
	}
	if pricePath != "" {
		data, err = os.ReadFile(pricePath)
		if err != nil {
			return err
		}
		var price ablation.Price
		if err = ablation.Decode(data, &price); err != nil {
			return err
		}
		if err = price.Validate(); err != nil {
			return err
		}
		p.Price = &price
	}
	output, err = gate.Canonical(output)
	if err != nil {
		return err
	}
	for _, t := range p.Tasks {
		if err = runner.ValidateTarget(t.Input, output); err != nil {
			return err
		}
	}
	if _, err = os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		return errors.New("probe output must not exist")
	}
	// This client is used only for authenticated GET /models, never Complete.
	client, err := deepseek.New(deepseek.Config{APIKey: os.Getenv("DEEPSEEK_API_KEY"), Model: p.Model.Model, Endpoint: p.Model.Endpoint, MaxTokens: int(p.Model.MaxOutputTokens)})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	backend, err := execution.NewDocker(ctx, execution.DockerConfig{Image: execution.DefaultImage, CPUs: p.Execution.CPUs, MemoryBytes: p.Execution.MemoryBytes, Pids: p.Execution.Pids})
	if err != nil {
		return err
	}
	resources, err := backend.ResourcePolicy(ctx)
	if err != nil {
		return err
	}
	capabilities, err := client.Discover(ctx)
	if err != nil {
		return err
	}
	p.Model, p.Execution, p.Resources = capabilities.Profile, backend.Info(), resources
	if err = p.Validate(); err != nil {
		return err
	}
	if err = os.Mkdir(output, 0700); err != nil {
		return err
	}
	if err = gate.Write(filepath.Join(output, "models.json"), capabilities.Response); err != nil {
		return err
	}
	if err = gate.WriteJSON(filepath.Join(output, "capabilities.json"), capabilities); err != nil {
		return err
	}
	return gate.WriteJSON(filepath.Join(output, "protocol.probed.json"), p)
}
