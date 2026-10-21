//go:build darwin || linux

package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/execution"
)

func TestExecutionProcessHelper(t *testing.T) {
	root := os.Getenv("HARNESS_EXECUTION_ROOT")
	if root == "" {
		t.Skip("subprocess helper")
	}
	record := load[campaign.Record](t, root, "campaign.json")
	config := controlledConfig(root)
	request := ExecuteRequest{Request: checkRequest(root, os.Getenv("HARNESS_EXECUTION_OUTPUT"), record.PlanSHA256), Targets: []string{"orders"}, Backend: execution.Local{}, Execute: config.Execute, NewModel: config.NewModel, VerifyModel: func(context.Context, budget.Profile) error { return nil }}
	_, err := execute(context.Background(), request, func(point string) {
		if point != os.Getenv("HARNESS_EXECUTION_POINT") {
			return
		}
		pipe := os.NewFile(3, "barrier")
		if err := json.NewEncoder(pipe).Encode(struct {
			PID   int
			Point string
		}{os.Getpid(), point}); err != nil {
			t.Fatal(err)
		}
		_ = pipe.Close()
		var one [1]byte
		_, _ = os.Stdin.Read(one[:])
		t.Fatal("execution barrier unexpectedly released")
	})
	t.Fatal("helper returned without its barrier", err)
}

func TestExecutionActualSIGKILLDoesNotRepeatAllocation(t *testing.T) {
	for _, point := range []string{"activation", "ledger", "started", "result"} {
		t.Run(point, func(t *testing.T) {
			parent := t.TempDir()
			if output := os.Getenv("HARNESS_RECOVERY_ARTIFACTS"); output != "" {
				if err := os.MkdirAll(output, 0700); err != nil {
					t.Fatal(err)
				}
				var err error
				parent, err = os.MkdirTemp(output, "execution-"+point+"-")
				if err != nil {
					t.Fatal(err)
				}
			}
			parent, _ = filepath.EvalSymlinks(parent)
			config := controlledConfig(filepath.Join(parent, "campaign"))
			config.Plan.Recovery = &campaign.RecoveryPolicy{MaxAttempts: 2, GenerationDeadline: time.Now().Add(time.Hour).UTC()}
			record, _, err := campaign.Run(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			root := record.Directory
			request := checkRequest(root, filepath.Join(parent, "prepare"), record.PlanSHA256)
			if _, err = Prepare(context.Background(), PrepareRequest{Request: request, ID: "recovery-one"}); err != nil {
				t.Fatal(err)
			}
			before := originalTree(t, root)
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			log, err := os.Create(filepath.Join(parent, "child.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			cmd := exec.Command(os.Args[0], "-test.run=^TestExecutionProcessHelper$")
			cmd.Env = append(os.Environ(), "HARNESS_EXECUTION_ROOT="+root, "HARNESS_EXECUTION_OUTPUT="+filepath.Join(parent, "before-kill"), "HARNESS_EXECUTION_POINT="+point)
			cmd.ExtraFiles = []*os.File{w}
			cmd.Stdout, cmd.Stderr = log, log
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			_ = w.Close()
			waited := false
			defer func() {
				if !waited {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()
			ready := make(chan struct {
				PID   int
				Point string
			}, 1)
			go func() {
				var event struct {
					PID   int
					Point string
				}
				if json.NewDecoder(r).Decode(&event) == nil {
					ready <- event
				}
			}()
			select {
			case event := <-ready:
				if event.PID != cmd.Process.Pid || event.Point != point {
					t.Fatal("owned process mismatch", event)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("execution did not reach its persisted barrier")
			}
			for name, shouldExist := range map[string]bool{"activation.json": true, "budget/budget.json": point != "activation", "started.json": point == "started" || point == "result", "result.json": point == "result"} {
				_, statErr := os.Stat(filepath.Join(root, campaign.RecoveryExecutionPath, name))
				if (statErr == nil) != shouldExist {
					t.Fatal("persisted prefix mismatch", point, name, statErr)
				}
			}
			if err = cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			waited = true
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
				t.Fatal("expected actual SIGKILL", err)
			}
			full := tree(t, root)
			for _, round := range []string{"reenter-1", "reenter-2"} {
				request.OutputDir = filepath.Join(parent, round)
				out, err := Execute(context.Background(), ExecuteRequest{Request: request, Targets: []string{"orders"}})
				if point == "result" {
					if err != nil || out.Result == nil {
						t.Fatal(out, err)
					}
				} else if !errors.Is(err, ErrInterrupted) {
					t.Fatal("allocated execution was not blocked", err)
				}
				if !reflect.DeepEqual(full, tree(t, root)) || !reflect.DeepEqual(before, originalTree(t, root)) {
					t.Fatal("reentry rewrote originals or repeated execution")
				}
			}
			save(t, parent, "evidence.json", map[string]any{"point": point, "pid": cmd.Process.Pid, "signal": "SIGKILL", "source_unchanged": true, "reentry_unchanged": true, "generation_requests": 0, "scope": "controlled execution allocation; no repository command or provider call"})
			t.Log("execution SIGKILL evidence:", parent)
		})
	}
}
