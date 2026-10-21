package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/secacy/agent-harness/internal/audit"
	"github.com/secacy/agent-harness/scenarios/rpcx/runner"
)

func runAudit(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("harness audit", flag.ContinueOnError)
	flags.SetOutput(stderr)
	campaign := flags.String("campaign", "", "stopped campaign directory")
	output := flags.String("output", "", "new audit report directory")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *campaign == "" || *output == "" {
		fmt.Fprintln(stderr, "--campaign and --output are required; no positional arguments are allowed")
		return 2
	}
	source, err := filepath.Abs(*campaign)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	destination, err := filepath.Abs(*output)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	report, err := audit.Inspect(ctx, audit.Request{CampaignDir: source, OutputDir: destination}, runner.InspectRun)
	if err != nil {
		fmt.Fprintln(stderr, err)
		if errors.Is(err, audit.ErrConfig) {
			return 2
		}
		if errors.Is(err, context.Canceled) {
			return 130
		}
		return 3
	}
	fmt.Fprintf(stdout, "Consistency: %s; execution_state: %s; violations: %d; gaps: %d; observations: %d\n", report.Consistency, report.ExecutionState, len(report.Violations), len(report.Gaps), len(report.Observations))
	if report.RecoveryExecutionState != "" {
		fmt.Fprintln(stdout, "Recovery execution state:", report.RecoveryExecutionState)
	}
	if report.ResourceState != "" {
		fmt.Fprintln(stdout, "Resource record state:", report.ResourceState)
	}
	fmt.Fprintln(stdout, "Report:", filepath.Join(destination, "audit.json"))
	if len(report.Violations) > 0 {
		return 1
	}
	if !report.ScanComplete || report.Consistency != "pass" {
		return 3
	}
	return 0
}
