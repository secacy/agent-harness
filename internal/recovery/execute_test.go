//go:build darwin || linux

package recovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/task"
)

func executionFixture(t *testing.T, mutate func(*campaign.Config)) (string, ExecuteRequest) {
	t.Helper()
	config := controlledConfig(filepath.Join(t.TempDir(), "campaign"))
	config.Plan.Recovery = &campaign.RecoveryPolicy{MaxAttempts: 2, GenerationDeadline: time.Now().Add(time.Hour).UTC()}
	if mutate != nil {
		mutate(&config)
	}
	record, _, err := campaign.Run(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	request := checkRequest(record.Directory, filepath.Join(t.TempDir(), "prepare"), record.PlanSHA256)
	if _, err := Prepare(context.Background(), PrepareRequest{Request: request, ID: "retry-checkpoint"}); err != nil {
		t.Fatal(err)
	}
	request.OutputDir = filepath.Join(t.TempDir(), "activate")
	return record.Directory, ExecuteRequest{Request: request, Targets: []string{"orders"}, Backend: execution.Local{}, Execute: config.Execute, NewModel: config.NewModel, VerifyModel: func(context.Context, budget.Profile) error { return nil }}
}

func TestExecuteReentryPreservesOriginalsAndDoesNotRepeat(t *testing.T) {
	root, request := executionFixture(t, nil)
	before := originalTree(t, root)
	prepared := tree(t, filepath.Join(root, "recoveries"))
	first, err := Execute(context.Background(), request)
	if err != nil || first.Result == nil || first.Audit.Consistency != "pass" || first.Audit.RecoveryExecutionState != "terminal_with_failures" {
		t.Fatal(first.Audit, err)
	}
	if !reflect.DeepEqual(before, originalTree(t, root)) {
		t.Fatal("original campaign changed")
	}
	for p, hash := range prepared {
		if p != "." && tree(t, filepath.Join(root, "recoveries"))[p] != hash {
			t.Fatal("checkpoint changed", p)
		}
	}
	a := load[campaign.Activation](t, root, campaign.RecoveryExecutionPath+"/activation.json")
	if len(a.Receipts) != 1 || a.Receipts[0].Ordinal != 2 || first.Result.Budget.Targets["orders"].RunID != a.Receipts[0].RunID {
		t.Fatal(a, first.Result)
	}
	all := tree(t, root)
	request.OutputDir = filepath.Join(t.TempDir(), "again")
	request.Execute = func(context.Context, campaign.Target, campaign.Services) (campaign.Attempt, error) {
		t.Fatal("executed twice")
		return campaign.Attempt{}, nil
	}
	request.NewModel = func(task.Target) (budget.ProfiledModel, error) {
		t.Fatal("model factory called on replay")
		return nil, nil
	}
	request.VerifyModel = func(context.Context, budget.Profile) error {
		t.Fatal("model verification called on replay")
		return nil
	}
	second, err := Execute(context.Background(), request)
	if err != nil || !reflect.DeepEqual(first.Result, second.Result) || !reflect.DeepEqual(all, tree(t, root)) {
		t.Fatal("replay changed a committed result", err)
	}
}

func TestExecuteRefusesUnauthorizedSelectionsWithoutAllocation(t *testing.T) {
	for _, mode := range []string{"no-policy", "one-attempt", "verification-error", "unknown", "duplicate", "nil", "model-unverified", "profile-changed", "campaign-budget", "target-budget", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			root, request := executionFixture(t, func(c *campaign.Config) {
				switch mode {
				case "no-policy":
					c.Plan.Recovery = nil
				case "one-attempt":
					c.Plan.Recovery.MaxAttempts = 1
				case "campaign-budget":
					c.Plan.Budget.MaxTokens = 99
				case "target-budget":
					c.Plan.Targets[0].Budget.MaxTokens = 99
				}
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "verification-error":
				request.VerifyModel = func(context.Context, budget.Profile) error { return context.DeadlineExceeded }
			case "unknown":
				request.Targets = []string{"absent"}
			case "duplicate":
				request.Targets = []string{"orders", "orders"}
			case "nil":
				request.Targets = nil
			case "model-unverified":
				request.VerifyModel = nil
			case "profile-changed":
				request.NewModel = func(task.Target) (budget.ProfiledModel, error) { return controlledModel{}, nil }
			case "cancel":
				cancel()
			}
			before := tree(t, root)
			request.Execute = func(context.Context, campaign.Target, campaign.Services) (campaign.Attempt, error) {
				t.Fatal("unauthorized task executed")
				return campaign.Attempt{}, nil
			}
			if _, err := Execute(ctx, request); err == nil {
				t.Fatal("unauthorized recovery accepted")
			}
			if !reflect.DeepEqual(before, tree(t, root)) {
				t.Fatal("rejected activation changed the campaign")
			}
		})
	}
}

func TestExecuteAdoptionOnlyDoesNotNeedModelOrRetryPermission(t *testing.T) {
	root, request := executionFixture(t, func(c *campaign.Config) { c.Plan.Recovery = nil })
	request.Targets = []string{}
	request.Execute, request.NewModel, request.VerifyModel, request.Backend = nil, nil, nil, nil
	out, err := Execute(context.Background(), request)
	if err != nil || out.Result == nil || out.Result.Budget.Totals.Calls != 0 || out.Audit.Consistency != "pass" {
		t.Fatal(out, err)
	}
	if _, err := os.Stat(filepath.Join(root, campaign.RecoveryExecutionPath, "budget")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("adoption opened a model ledger", err)
	}
}

func TestExecuteIncompleteAllocationCannotBeRepeated(t *testing.T) {
	for _, point := range []string{"activation", "ledger", "started"} {
		t.Run(point, func(t *testing.T) {
			root, request := executionFixture(t, nil)
			func() {
				defer func() {
					if recover() != point {
						t.Fatal("allocation did not stop at the requested boundary")
					}
				}()
				_, _ = execute(context.Background(), request, func(p string) {
					if p == point {
						panic(point)
					}
				})
			}()
			before := tree(t, root)
			request.OutputDir = filepath.Join(t.TempDir(), "again")
			request.Execute = func(context.Context, campaign.Target, campaign.Services) (campaign.Attempt, error) {
				t.Fatal("incomplete allocation executed again")
				return campaign.Attempt{}, nil
			}
			out, err := Execute(context.Background(), request)
			if !errors.Is(err, ErrInterrupted) || out.Audit.Consistency != "pass" || !reflect.DeepEqual(before, tree(t, root)) {
				t.Fatal(out.Audit, err)
			}
		})
	}
}

func TestRecoveryExecutionTamperingBlocksReentry(t *testing.T) {
	for _, mode := range []string{"receipt", "result", "invalid-failure-status", "budget", "short-calls", "extra", "no-start"} {
		t.Run(mode, func(t *testing.T) {
			root, request := executionFixture(t, nil)
			if _, err := Execute(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			dir := campaign.RecoveryExecutionPath
			switch mode {
			case "receipt":
				a := load[campaign.Activation](t, root, dir+"/activation.json")
				a.Receipts[0].Ordinal++
				save(t, root, dir+"/activation.json", a)
			case "result":
				r := load[campaign.RecoveryResult](t, root, dir+"/result.json")
				r.Targets[0].Attempt.Status = campaign.Completed
				save(t, root, dir+"/result.json", r)
			case "invalid-failure-status":
				r := load[campaign.RecoveryResult](t, root, dir+"/result.json")
				r.Targets[0].Attempt.Status = "Unrecognized"
				save(t, root, dir+"/result.json", r)
			case "budget":
				b := load[budget.Snapshot](t, root, dir+"/budget/budget.json")
				b.Limits.MaxTokens++
				save(t, root, dir+"/budget/budget.json", b)
			case "short-calls":
				b := load[budget.Snapshot](t, root, dir+"/budget/budget.json")
				b.Calls = nil
				save(t, root, dir+"/budget/budget.json", b)
			case "extra":
				if err := os.WriteFile(filepath.Join(root, dir, "unexpected"), []byte("x"), 0600); err != nil {
					t.Fatal(err)
				}
			case "no-start":
				if err := os.Remove(filepath.Join(root, dir, "started.json")); err != nil {
					t.Fatal(err)
				}
			}
			before := tree(t, root)
			request.OutputDir = filepath.Join(t.TempDir(), "tampered")
			out, err := Execute(context.Background(), request)
			if !errors.Is(err, ErrBlocked) || out.Result != nil || !reflect.DeepEqual(before, tree(t, root)) {
				t.Fatal("tampered execution accepted or repaired", out, err)
			}
		})
	}
}
