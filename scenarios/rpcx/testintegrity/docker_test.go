package testintegrity

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/execution"
)

func TestDockerShowsTheLimitsOfNameOnlyPreservation(t *testing.T) {
	if os.Getenv("HARNESS_DOCKER_TEST") != "1" {
		t.Skip("set HARNESS_DOCKER_TEST=1 to run real Docker integration")
	}
	backend, err := execution.NewDocker(context.Background(), execution.DockerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	baseline := readTests(t, filepath.Join("..", "t3", "fixtures", "demo-orders"))
	names := func(files map[string][]byte) []string {
		_, functions := parseSources(files)
		result := []string{}
		for _, id := range keys(functions) {
			if len(functions[id]) == 1 && ordinaryTest(functions[id][0]) {
				result = append(result, id)
			}
		}
		return result
	}
	originalNames := names(baseline)
	if len(originalNames) != 10 {
		t.Fatal("fixture no longer contains the ten required original tests")
	}
	for _, sample := range []struct{ name, status string }{
		{"correct-patch", "preserved"}, {"host-adapted", "preserved"},
		{"deleted-assertion", "violated"}, {"helper-rewrite", "unknown"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			module := fixture(t, "correct-timeout")
			candidate := readTests(t, module)
			if sample.name == "host-adapted" {
				candidate, err = AdaptBaseline(baseline)
				if err != nil {
					t.Fatal(err)
				}
			} else if sample.name != "correct-patch" {
				mutateCase(t, candidate, sample.name)
			}
			r := check(t, baseline, candidate)
			if r.Status != sample.status {
				t.Fatalf("unexpected structural result: %+v", r)
			}
			for path, data := range candidate {
				if err := os.WriteFile(filepath.Join(module, path), data, 0644); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			result, err := backend.Run(ctx, execution.Request{
				Directory: module, ReadOnlyDirs: []string{module, filepath.Join(filepath.Dir(module), "rpcx-v1"), filepath.Join(filepath.Dir(module), "rpcx-v2")},
				Args: []string{"go", "test", "-json", "-count=1", "-timeout=30s", "./..."},
			})
			if err != nil || result.ExitCode != 0 || result.ContainerID == "" {
				t.Fatalf("business tests failed: %+v %v", result, err)
			}
			events := map[string]string{}
			decoder := json.NewDecoder(bytes.NewReader(result.Stdout))
			for {
				var event struct{ Package, Test, Action string }
				if err := decoder.Decode(&event); err == io.EOF {
					break
				} else if err != nil {
					t.Fatal(err)
				}
				if event.Action == "pass" || event.Action == "fail" || event.Action == "skip" {
					events[event.Package+"/"+event.Test] = event.Action
				}
			}
			for _, id := range originalNames {
				dot := strings.LastIndex(id, ".")
				pkg := "example.com/demo-orders/" + id[:dot]
				if events[pkg+"/"+id[dot+1:]] != "pass" || events[pkg+"/"] != "pass" {
					t.Fatalf("original test did not execute successfully: %s", id)
				}
			}
			if !reflect.DeepEqual(names(candidate), originalNames) || reflect.DeepEqual(baseline, candidate) {
				t.Fatal("comparison alternatives no longer have the intended controls")
			}
			t.Logf("BUSINESS_TEST_EVENTS %s", result.Stdout)
			meta, _ := json.Marshal(struct {
				Backend        execution.Info `json:"backend"`
				ContainerID    string         `json:"container_id"`
				OriginalPassed int            `json:"original_passed"`
				UnchangedBytes bool           `json:"unchanged_bytes"`
				SameNames      bool           `json:"same_names"`
				Structure      string         `json:"structure"`
				InputSHA256    string         `json:"test_input_sha256"`
			}{backend.Info(), result.ContainerID, 10, false, true, r.Status, r.Candidate.SHA256})
			t.Logf("TEST_STRATEGY %s", meta)
			if err := exec.Command("docker", "inspect", result.ContainerID).Run(); err == nil {
				t.Fatal("test container was not removed")
			}
		})
	}
}
