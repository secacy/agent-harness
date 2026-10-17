package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"example.com/agent-harness/scenarios/rpcx/checks"
)

func main() {
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: scripts/verify-rpcx-t1-t2.sh")
		os.Exit(2)
	}
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	runDir, err := os.MkdirTemp(filepath.Join(root, "runs"), "rpcx-t1-t2-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	report, verifyErr := checks.Verify(root, runDir)
	if report != nil {
		if verifyErr != nil {
			report.Error = verifyErr.Error()
		}
		data, err := json.MarshalIndent(report, "", "  ")
		if err == nil {
			err = os.WriteFile(filepath.Join(runDir, "report.json"), append(data, '\n'), 0644)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Println("Report:", filepath.Join(runDir, "report.json"))
	if verifyErr != nil {
		fmt.Fprintln(os.Stderr, verifyErr)
		os.Exit(1)
	}
	matched := report.FixturesUnchanged
	for _, result := range report.Results {
		fmt.Printf("%-22s %-16s expected=%v\n", result.ID, result.Verdict, result.ExpectationMatched)
		matched = matched && result.ExpectationMatched
	}
	if !matched {
		fmt.Fprintln(os.Stderr, "verification did not match expectations, or fixtures changed")
		os.Exit(1)
	}
}
