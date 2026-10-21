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

	"github.com/secacy/agent-harness/internal/campaign"
)

func TestPrepareProcessHelper(t *testing.T) {
	root := os.Getenv("HARNESS_PREPARE_ROOT")
	if root == "" {
		t.Skip("subprocess helper")
	}
	record := load[campaign.Record](t, root, "campaign.json")
	request := PrepareRequest{Request: checkRequest(root, os.Getenv("HARNESS_PREPARE_OUTPUT"), record.PlanSHA256), ID: "recovery-one"}
	_, err := prepare(context.Background(), request, prepareHooks{after: func(point string) {
		if point != os.Getenv("HARNESS_PREPARE_POINT") {
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
		t.Fatal("checkpoint barrier unexpectedly released")
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Fatal("helper returned without stopping at its barrier")
}

func TestPrepareActualSIGKILLReentry(t *testing.T) {
	for _, point := range []string{"directory", "checkpoint", "head"} {
		t.Run(point, func(t *testing.T) {
			root, request := prepareFixture(t)
			parent := t.TempDir()
			if output := os.Getenv("HARNESS_RECOVERY_ARTIFACTS"); output != "" {
				if err := os.MkdirAll(output, 0700); err != nil {
					t.Fatal(err)
				}
				var err error
				parent, err = os.MkdirTemp(output, "prepare-"+point+"-")
				if err != nil {
					t.Fatal(err)
				}
				parent, _ = filepath.EvalSymlinks(parent)
				// 在固定新目录创建独立原始 Campaign，不能移动后改写历史身份。
				config := controlledConfig(filepath.Join(parent, "campaign"))
				record, _, err := campaign.Run(context.Background(), config)
				if err != nil {
					t.Fatal(err)
				}
				root = record.Directory
				request = PrepareRequest{Request: checkRequest(root, filepath.Join(parent, "retry-1"), record.PlanSHA256), ID: "recovery-one"}
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
			cmd := exec.Command(os.Args[0], "-test.run=^TestPrepareProcessHelper$")
			cmd.Env = append(os.Environ(), "HARNESS_PREPARE_ROOT="+root, "HARNESS_PREPARE_OUTPUT="+filepath.Join(parent, "before-kill"), "HARNESS_PREPARE_POINT="+point)
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
					t.Fatal(event)
				}
			case <-time.After(15 * time.Second):
				t.Fatal("prepare did not reach its persisted barrier")
			}
			_, payloadErr := os.Stat(filepath.Join(root, "recoveries/checkpoint-recovery-one.json"))
			_, headErr := os.Stat(filepath.Join(root, "recoveries/head.json"))
			if (point != "directory") != (payloadErr == nil) || (point == "head") != (headErr == nil) {
				t.Fatal("barrier files do not match checkpoint", point, payloadErr, headErr)
			}
			if err = cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			waited = true
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
				t.Fatal("expected real SIGKILL", err)
			}
			result, err := Prepare(context.Background(), request)
			if err != nil || result.CommitState != "Prepared" || !reflect.DeepEqual(before, originalTree(t, root)) {
				t.Fatal("prepare did not recover its own publication", result, err)
			}
			full := tree(t, root)
			again := request
			again.OutputDir = filepath.Join(parent, "retry-2")
			second, err := Prepare(context.Background(), again)
			if err != nil || second.Checkpoint != result.Checkpoint || !reflect.DeepEqual(full, tree(t, root)) {
				t.Fatal("reentry changed the committed checkpoint", err)
			}
			save(t, parent, "evidence.json", map[string]any{"point": point, "pid": cmd.Process.Pid, "signal": "SIGKILL", "commit_state": result.CommitState, "source_unchanged": true, "second_prepare_unchanged": true, "budget": result.State.Budget.Totals, "checkpoint": result.Checkpoint})
			t.Logf("%s: SIGKILL, same-ID reentry committed once; output=%s", point, parent)
		})
	}
}
