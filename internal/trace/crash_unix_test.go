//go:build unix

package trace

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestTraceCrashHelper(t *testing.T) {
	path := os.Getenv("HARNESS_TRACE_CRASH_HELPER")
	if path == "" {
		return
	}
	r, err := New(path, "crash-run")
	if err != nil {
		t.Fatal(err)
	}
	ctx, _, err := Start(Context(context.Background(), r), "run", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, span, err := Start(ctx, "tool_call", map[string]string{"name": "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if err := span.End(map[string]string{"result": "confirmed"}, nil); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("HARNESS_TRACE_CRASH_TAIL") == "1" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.WriteString(`{"interrupted":`); err != nil {
			t.Fatal(err)
		}
		if err = f.Sync(); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
	fmt.Fprintln(os.Stdout, "trace-durable")
	// The parent kills this exact child after the ready signal; do not close the
	// recorder or synthesize the incomplete root's terminal record.
	time.Sleep(time.Hour)
}
func TestSIGKILLKeepsConfirmedSpansAndRecognizesPartialTail(t *testing.T) {
	for _, tail := range []string{"0", "1"} {
		t.Run(tail, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "trace.jsonl")
			cmd := exec.Command(os.Args[0], "-test.run=^TestTraceCrashHelper$")
			cmd.Env = append(os.Environ(), "HARNESS_TRACE_CRASH_HELPER="+path, "HARNESS_TRACE_CRASH_TAIL="+tail)
			pipe, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stderr = os.Stderr
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
			ready := make(chan string, 1)
			go func() { line, _ := bufio.NewReader(pipe).ReadString('\n'); ready <- line }()
			select {
			case line := <-ready:
				if line != "trace-durable\n" {
					t.Fatalf("unexpected helper state: %q", line)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("helper never confirmed durability")
			}
			if err = cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = cmd.Wait()
			log, err := Read(path)
			if err != nil {
				t.Fatal(err)
			}
			if len(log.Events) != 3 || log.Events[2].Kind != "tool_call" || log.Events[2].Phase != "end" || log.TruncatedTail != (tail == "1") {
				t.Fatalf("confirmed evidence lost: %+v", log)
			}
		})
	}
}
