package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/secacy/agent-harness/scenarios/rpcx/runner"
)

func runReplay(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("harness replay", flag.ContinueOnError)
	flags.SetOutput(stderr)
	directory := flags.String("run", "", "recorded run directory (read only)")
	ledger := flags.String("ledger", "", "explicit archived budget.json; otherwise locate ancestor campaign ledger")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *directory == "" {
		fmt.Fprintln(stderr, "--run is required; no positional arguments")
		return 2
	}
	result, err := runner.Replay(ctx, *directory, *ledger)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

type runDirectories []string

func (r *runDirectories) String() string { return fmt.Sprint([]string(*r)) }
func (r *runDirectories) Set(v string) error {
	if v == "" {
		return errors.New("empty run directory")
	}
	*r = append(*r, v)
	return nil
}
func runFailures(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("harness failures", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var directories runDirectories
	flags.Var(&directories, "run", "run directory; repeat for each member of the fixed sample")
	asJSON := flags.Bool("json", false, "emit machine-readable counts and per-run causes")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || len(directories) == 0 {
		fmt.Fprintln(stderr, "at least one --run is required; no positional arguments")
		return 2
	}
	result, err := runner.SummarizeFailures(ctx, directories)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *asJSON {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "Runs: %d; non-success terminal outcomes: %d; incomplete: %d\n", result.Runs, result.Failures, result.Incomplete)
	out := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(out, "CAUSE\tCOUNT\t% RUNS\t% FAILURES")
	for _, c := range result.Counts {
		fraction := "N/A"
		if c.FractionOfFailures != nil {
			fraction = fmt.Sprintf("%.2f%%", 100**c.FractionOfFailures)
		}
		fmt.Fprintf(out, "%s\t%d\t%.2f%%\t%s\n", c.Category, c.Count, 100*c.FractionOfRuns, fraction)
	}
	if err := out.Flush(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
