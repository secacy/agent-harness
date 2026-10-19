package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/secacy/agent-harness/scenarios/rpcx/l2/round"
)

func main() {
	config := flag.String("config", "", "absolute path to round request JSON")
	flag.Parse()
	if *config == "" {
		fmt.Fprintln(os.Stderr, "-config is required")
		os.Exit(2)
	}
	raw, err := os.ReadFile(*config)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var request round.Request
	if err = round.Decode(raw, &request); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	report, err := round.Summarize(ctx, request)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("round=%s kind=%s valid=%t targets=%d released=%d unknown_releases=%d\n", report.ID, report.Kind, report.Valid, report.Counts.Targets, report.Counts.Released, report.Counts.UnknownReleases)
}
