package gate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/quality"
)

type recordingBackend func(context.Context, execution.Request) (execution.Result, error)

func (b recordingBackend) Run(c context.Context, r execution.Request) (execution.Result, error) {
	return b(c, r)
}
func (recordingBackend) Info() execution.Info { return execution.Info{Kind: execution.KindDocker} }
func (recordingBackend) Environment() map[string]string {
	return map[string]string{"GOMODCACHE": "/trusted/modules"}
}

func TestCommandEvidenceFailures(t *testing.T) {
	for _, mode := range []string{"success", "business-failure", "backend-error", "cancel", "deadline", "negative-exit", "oom", "output-limit", "write-failure", "partial-write", "drift", "missing-input", "missing-before", "cancel-before"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			input := filepath.Join(root, "input")
			output := filepath.Join(root, "evidence")
			if err := os.Mkdir(input, 0755); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(input, "main.go")
			if err := os.WriteFile(source, []byte("before"), 0644); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel-before" {
				cancel()
			}
			if mode == "deadline" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 2*time.Second)
				defer stop()
			}
			if mode == "missing-before" {
				if err := os.RemoveAll(input); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "write-failure" {
				if err := os.WriteFile(output, []byte("blocked"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "partial-write" {
				if err := os.MkdirAll(filepath.Join(output, "logs/probe.stdout.log"), 0755); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			sentinel := errors.New("backend failed")
			req := execution.Request{Args: []string{"go", "test", "-json", "./..."}, Directory: input, ReadOnlyDirs: []string{input}, CacheDir: filepath.Join(root, "cache"), MaxBytes: 4 << 20}
			backend := recordingBackend(func(c context.Context, actual execution.Request) (execution.Result, error) {
				calls++
				if !reflect.DeepEqual(actual, req) {
					t.Fatalf("request changed: %+v", actual)
				}
				result := execution.Result{ExitCode: 0, Stdout: []byte("stdout\n"), Stderr: []byte("stderr\n"), ContainerID: "container-id"}
				switch mode {
				case "business-failure":
					result.ExitCode = 1
				case "backend-error":
					return result, sentinel
				case "cancel":
					cancel()
				case "deadline":
					<-c.Done()
				case "negative-exit":
					result.ExitCode = -1
				case "oom":
					result.OOMKilled = true
				case "output-limit":
					result.OutputLimit = true
				case "drift":
					if err := os.WriteFile(source, []byte("after"), 0644); err != nil {
						t.Fatal(err)
					}
				case "missing-input":
					if err := os.RemoveAll(input); err != nil {
						t.Fatal(err)
					}
				}
				return result, nil
			})
			var commands []Command
			var evidence []quality.Evidence
			binding := quality.Binding{SpecSHA256: "spec", BaselineSHA256: "base", CandidateSHA256: "candidate"}
			recorder := Recorder{Directory: output, Binding: binding, Commands: &commands, Evidence: &evidence}
			result, err := recorder.Run(ctx, "probe", backend, req)
			wantErr := mode != "success" && mode != "business-failure"
			if (err != nil) != wantErr {
				t.Fatalf("error=%v", err)
			}
			if mode == "backend-error" && !errors.Is(err, sentinel) {
				t.Fatal("backend cause lost")
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost")
			}
			if mode == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("deadline lost")
			}
			if mode == "cancel-before" || mode == "missing-before" {
				if calls != 0 || len(commands) != 0 || len(evidence) != 0 {
					t.Fatal("pre-execution failure published command")
				}
				return
			}
			if calls != 1 || len(commands) != 1 {
				t.Fatal("missing command")
			}
			c := commands[0]
			if c.ContainerID != result.ContainerID || c.ExitCode != result.ExitCode || c.Backend != backend.Info() || c.Directory != input || c.OOMKilled != result.OOMKilled || c.OutputLimit != result.OutputLimit || c.Cancelled != (mode == "cancel") || c.TimedOut != (mode == "deadline") {
				t.Fatalf("command mismatch: %+v", c)
			}
			if !reflect.DeepEqual(c.Args, append([]string{"env", "GOMODCACHE=/trusted/modules"}, req.Args...)) {
				t.Fatal("actual arguments lost")
			}
			if mode == "write-failure" {
				if len(evidence) != 0 {
					t.Fatal("failed writes added evidence")
				}
				return
			}
			if mode == "partial-write" {
				if len(evidence) != 2 || evidence[0].ID != "probe-stderr" || evidence[1].ID != "probe" {
					t.Fatalf("partial evidence=%+v", evidence)
				}
				return
			}
			for path, want := range map[string][]byte{"logs/probe.stdout.log": result.Stdout, "logs/probe.stderr.log": result.Stderr} {
				data, readErr := os.ReadFile(filepath.Join(output, path))
				if readErr != nil || string(data) != string(want) {
					t.Fatalf("output mismatch: %s %v", path, readErr)
				}
			}
			if len(evidence) != 3 {
				t.Fatalf("evidence=%+v", evidence)
			}
			for _, e := range evidence {
				data, readErr := os.ReadFile(filepath.Join(output, e.Path))
				if readErr != nil || Digest(data) != e.SHA256 || e.Binding != binding {
					t.Fatalf("unbound evidence: %+v %v", e, readErr)
				}
			}
			data, readErr := os.ReadFile(filepath.Join(output, "commands/probe.json"))
			var saved Command
			if readErr != nil || json.Unmarshal(data, &saved) != nil || !reflect.DeepEqual(c, saved) {
				t.Fatal("saved command differs")
			}
		})
	}
}
