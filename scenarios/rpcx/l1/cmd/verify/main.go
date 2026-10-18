package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/l1"
)

func main() {
	var config l1.Config
	var selected string
	flag.StringVar(&config.Directory, "data", "scenarios/rpcx/l1", "L1 dataset directory")
	flag.StringVar(&config.T3Directory, "t3", "scenarios/rpcx/t3", "frozen T3 scene")
	flag.StringVar(&config.RunsDir, "runs", "runs", "artifact parent directory")
	flag.BoolVar(&config.TrustedLocal, "trusted-local", false, "execute trusted fixtures locally")
	flag.BoolVar(&config.Development, "development", false, "development run, never accepted as formal")
	flag.BoolVar(&config.Quick, "quick", false, "skip reference real expiry; requires development")
	flag.StringVar(&selected, "cases", "", "comma-separated development subset")
	flag.Parse()
	if selected != "" {
		config.Cases = strings.Split(selected, ",")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if config.TrustedLocal {
		config.Backend = execution.Local{}
	} else {
		backend, err := execution.NewDocker(ctx, execution.DockerConfig{})
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		config.Backend = backend
	}
	report, err := l1.Verify(ctx, config)
	if report != nil {
		fmt.Println(report.Directory)
		fmt.Printf("matched=%t accepted=%t cases=%d\n", report.Matched, report.Accepted, len(report.Results))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if report == nil || !report.Matched {
		os.Exit(1)
	}
}
