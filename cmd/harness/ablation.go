package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/secacy/agent-harness/scenarios/rpcx/l2/ablation"
)

// The P1 entry is deliberately distinct from production batch/audit contracts.
// These preparation/report modes never construct a provider or read its key.
func runAblation(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "Usage: harness ablation prepare --output <new-dir> [--project <root>]\n       harness ablation report --experiment <root> --output <new-dir>")
		return 2
	}
	mode := args[0]
	if mode != "prepare" && mode != "report" {
		fmt.Fprintln(stderr, "unsupported ablation mode; supported: prepare, report")
		return 2
	}
	flags := flag.NewFlagSet("harness ablation "+mode, flag.ContinueOnError)
	flags.SetOutput(stderr)
	project := flags.String("project", ".", "project root for public task inputs")
	root := flags.String("experiment", "", "saved P1 experiment root")
	output := flags.String("output", "", "new output directory")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *output == "" || (mode == "report" && *root == "") || (mode == "prepare" && *root != "") {
		fmt.Fprintln(stderr, "explicit output and mode-appropriate inputs are required")
		return 2
	}
	var err error
	if mode == "prepare" {
		err = ablation.PrepareDraft(ctx, *project, *output)
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
