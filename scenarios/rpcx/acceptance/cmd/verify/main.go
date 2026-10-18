package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/acceptance"
)

func main() {
	development := flag.Bool("development", false, "allow unfrozen development inputs; never marks acceptance complete")
	quick := flag.Bool("quick", false, "omit actual expiry checks (requires development mode)")
	trusted := flag.Bool("trusted-local", false, "execute controlled fixture code on this host")
	scenario := flag.String("scenario", "scenarios/rpcx/t3", "T3 scenario directory")
	runs := flag.String("runs", "runs", "output parent directory")
	flag.Parse()
	if flag.NArg() != 0 || (*quick && !*development) {
		fmt.Fprintln(os.Stderr, "quick checks require development mode and no positional arguments")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	var backend execution.Executor = execution.Local{}
	if !*trusted {
		docker, err := execution.NewDocker(ctx, execution.DockerConfig{})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		backend = docker
	}
	report, err := acceptance.Verify(ctx, acceptance.Config{ScenarioDir: *scenario, RunsDir: *runs, Backend: backend, TrustedLocal: *trusted, Development: *development, Quick: *quick})
	if report != nil {
		fmt.Println("Report:", report.Directory+"/report.json")
		for _, result := range report.Cases {
			fmt.Printf("%-26s business=%s reference=%s expected=%v\n", result.ID, result.Business.Status, result.Reference.Status, result.ExpectationMatched)
		}
		fmt.Printf("Accepted: %v; mode: %s; actual expiry checks: %v\n", report.Accepted, report.Mode, report.SlowEnabled)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if !report.Matched || (!*development && !report.Accepted) {
		fmt.Fprintln(os.Stderr, "acceptance did not match expectations")
		os.Exit(1)
	}
}
