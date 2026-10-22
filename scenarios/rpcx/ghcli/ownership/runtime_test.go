package ownership

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRuntimeStudy 执行自建输入，原型不进入运行时门禁，也不执行真实候选。
func TestRuntimeStudy(t *testing.T) {
	output := os.Getenv("HARNESS_GHCLI_OWNERSHIP_OUTPUT")
	if output == "" {
		t.Skip("set HARNESS_GHCLI_OWNERSHIP_OUTPUT for native ownership witness checks")
	}
	output, err := filepath.Abs(output)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(output, 0755); err != nil {
		t.Fatal(err)
	}
	sdk, err := filepath.Abs("../../l2/data/rpcx-l2-ghcli-v1/fixtures/rpcx-v2")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(sdk, "client.go"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		after, err := os.ReadFile(filepath.Join(sdk, "client.go"))
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("SDK input changed")
		}
	})
	contract, err := os.ReadFile("testdata/contract_test.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	var results []map[string]any
	for _, tc := range []struct{ name, before, after, wantFailure string }{
		{"correct", "", "", ""},
		{"helper-equivalent", "cancel()\n        return response.Response, err", "drop(cancel)\n        return response.Response, err", ""},
		{"aliased-error-equivalent", "return response.Response, err\n    }", "savedErr:=err; return response.Response, savedErr\n    }", ""},
		{"error-leak", "cancel()\n        return response.Response, err", "return response.Response, err", "TestErrorRelease"},
		{"empty-leak", "cancel()\n        return response.Response, nil", "return response.Response, nil", "TestEmptyRelease"},
		{"early-cancel", "err = client.Call", "cancel()\n    err = client.Call", "TestBodyLifetime"},
		{"close-error-lost", "return body.ReadCloser.Close()", "_ = body.ReadCloser.Close(); return nil", "TestBodyLifetime"},
		{"RPC-error-lost", "return response.Response, err\n    }", "return response.Response, nil\n    }", "TestErrorRelease"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := fixture(t)
			if tc.before != "" {
				source = replace(t, source, tc.before, tc.after)
			}
			if strings.Contains(source, "drop(") {
				source += "\nfunc drop(c context.CancelFunc){c()}\n"
			}
			analysis := inspect(t, source)
			dir := filepath.Join(output, tc.name)
			if err := os.Mkdir(dir, 0755); err != nil {
				t.Fatal(err)
			}
			mod := fmt.Sprintf("module example.com/ownership-study\n\ngo 1.23.0\n\nrequire example.com/rpcx/v2 v2.0.0\nreplace example.com/rpcx/v2 => %q\n", sdk)
			for name, data := range map[string][]byte{"go.mod": []byte(mod), "boundary.go": []byte(source), "contract_test.go": contract} {
				if err := os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "test", "-json", "-count=1", "-timeout=20s", "./...")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GOPROXY=off", "GOSUMDB=off", "GOWORK=off", "GOTOOLCHAIN=local")
			data, runErr := cmd.CombinedOutput()
			if err := os.WriteFile(filepath.Join(dir, "test.jsonl"), data, 0644); err != nil {
				t.Fatal(err)
			}
			passed := map[string]bool{}
			failed := map[string]bool{}
			for _, line := range bytes.Split(data, []byte{'\n'}) {
				var event struct{ Action, Test string }
				if json.Unmarshal(line, &event) == nil && event.Test != "" {
					if event.Action == "fail" {
						failed[event.Test] = true
					}
					if event.Action == "pass" {
						passed[event.Test] = true
					}
				}
			}
			if ctx.Err() != nil {
				t.Fatal(ctx.Err())
			}
			if tc.wantFailure == "" {
				if runErr != nil || len(passed) != 4 || len(failed) != 0 {
					t.Fatalf("positive witness failed: %v\n%s", runErr, data)
				}
			} else if runErr == nil || !failed[tc.wantFailure] || analysis.Status == Proven {
				t.Fatalf("negative witness not established: %+v / %v / %v", analysis, failed, runErr)
			}
			results = append(results, map[string]any{"case": tc.name, "analysis": analysis, "expected_failure": tc.wantFailure, "passed": passed, "failed": failed, "input_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(source))), "sdk_sha256": fmt.Sprintf("%x", sha256.Sum256(before))})
		})
	}
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(output, "results.json"), append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
}
