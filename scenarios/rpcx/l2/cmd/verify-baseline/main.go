package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/baseline"
)

func main() {
	trusted := flag.Bool("trusted-local", false, "run trusted fixture code locally instead of Docker")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: verify-baseline [-trusted-local]")
		os.Exit(2)
	}
	if err := run(*trusted); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(trusted bool) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	var backend execution.Executor = execution.Local{}
	if !trusted {
		backend, err = execution.NewDocker(ctx, execution.DockerConfig{})
		if err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "runs"), 0755); err != nil {
		return err
	}
	directory, err := os.MkdirTemp(filepath.Join(root, "runs"), "rpcx-l2-baseline-")
	if err != nil {
		return err
	}
	report, err := baseline.Verify(ctx, root, directory, backend)
	fmt.Println("Report:", filepath.Join(directory, "report.json"))
	if report != nil {
		fmt.Printf("accepted=%v inputs_unchanged=%v\n", report.Accepted, report.InputsUnchanged)
	}
	return err
}
