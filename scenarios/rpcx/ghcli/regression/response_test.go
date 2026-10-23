package regression_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

const boundaryPath = "pkg/cmd/release/delete/outbound.go"

type responseManifest struct {
	Version string
	Origin  struct {
		CandidateSHA256 string `json:"candidate_sha256"`
	}
	BaselineSHA256 string            `json:"baseline_sha256"`
	BoundarySHA256 string            `json:"boundary_sha256"`
	SpecSHA256     string            `json:"spec_sha256"`
	SDKs           map[string]string `json:"sdks"`
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func replaceOnce(t *testing.T, data []byte, before, after string) []byte {
	t.Helper()
	if bytes.Count(data, []byte(before)) != 1 {
		t.Fatal("fixture edit must identify exactly one occurrence")
	}
	return bytes.Replace(data, []byte(before), []byte(after), 1)
}

// TestResponseReturnContract 执行固定开发输入，不接受任意候选，也不更新运行时判定。
func TestResponseReturnContract(t *testing.T) {
	dataRoot, err := gate.Canonical("testdata/response-v1")
	if err != nil {
		t.Fatal(err)
	}
	fixtures, err := gate.Canonical("../../l2/data/rpcx-l2-ghcli-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	var manifest responseManifest
	if err := json.Unmarshal(readFile(t, filepath.Join(dataRoot, "manifest.json")), &manifest); err != nil || manifest.Version != "ghcli-response-return-v1" {
		t.Fatalf("unsupported regression manifest: %v", err)
	}
	baseline, err := gate.ReadFiles(filepath.Join(fixtures, "ghcli"))
	if err != nil || gate.TreeHash(baseline) != manifest.BaselineSHA256 {
		t.Fatalf("baseline identity changed: %v", err)
	}
	saved := readFile(t, filepath.Join(dataRoot, "outbound.go.txt"))
	if gate.Digest(saved) != manifest.BoundarySHA256 || gate.Digest(readFile(t, "../specs/v0.6.0.md")) != manifest.SpecSHA256 {
		t.Fatal("saved boundary or specification identity changed")
	}
	// 重建完整模块身份，避免只凭一个来源说明就把片段当成原模型候选。
	candidate := maps.Clone(baseline)
	candidate[boundaryPath] = saved
	candidate["go.mod"] = replaceOnce(t, candidate["go.mod"], "example.com/rpcx v1.0.0", "example.com/rpcx/v2 v2.0.0")
	candidate["go.mod"] = replaceOnce(t, candidate["go.mod"], "replace example.com/rpcx => ../rpcx-v1", "replace example.com/rpcx/v2 => ../rpcx-v2")
	if gate.TreeHash(candidate) != manifest.Origin.CandidateSHA256 {
		t.Fatal("reconstructed candidate differs from the original model output")
	}
	output := t.TempDir()
	if requested := os.Getenv("HARNESS_GHCLI_RESPONSE_OUTPUT"); requested != "" {
		output, err = gate.Canonical(requested)
		if err != nil || gate.Overlap(output, dataRoot) || gate.Overlap(output, fixtures) {
			t.Fatalf("regression output overlaps input or is invalid: %v", err)
		}
		if err := os.Mkdir(output, 0755); err != nil {
			t.Fatal(err)
		}
	}
	for name, hash := range manifest.SDKs {
		files, err := gate.ReadFiles(filepath.Join(fixtures, name))
		if err != nil || gate.TreeHash(files) != hash {
			t.Fatalf("SDK identity changed: %s %v", name, err)
		}
		if err := gate.CopyFiles(filepath.Join(output, name), files); err != nil {
			t.Fatal(err)
		}
	}
	const reset = "\tif err != nil {\n\t\tresponse.Response = nil\n\t}\n"
	preserved := replaceOnce(t, saved, reset, "")
	discarded := replaceOnce(t, preserved, "return response.Response, err", "return response.Response, nil")
	contract := readFile(t, filepath.Join(dataRoot, "contract_test.go.txt"))
	var results []map[string]any
	for _, tc := range []struct {
		name, sdk, version string
		source             []byte
		failed             []string
	}{
		{"baseline-v1", "rpcx", "v1.0.0", baseline[boundaryPath], nil},
		{"saved-v2", "rpcx/v2", "v2.0.0", saved, []string{"TestResponseRedirectDeclined"}},
		{"preserve-response-v2", "rpcx/v2", "v2.0.0", preserved, nil},
		{"discard-error-v2", "rpcx/v2", "v2.0.0", discarded, []string{"TestResponseRedirectDeclined", "TestResponseTransportError"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(output, tc.name)
			sdk := "rpcx-v2"
			if tc.sdk == "rpcx" {
				sdk = "rpcx-v1"
			}
			mod := fmt.Sprintf("module response.invalid/regression\n\ngo 1.23.0\n\nrequire example.com/%s %s\nreplace example.com/%s => ../%s\n", tc.sdk, tc.version, tc.sdk, sdk)
			if err := gate.CopyFiles(dir, map[string][]byte{"go.mod": []byte(mod), "outbound.go": tc.source, "contract_test.go": contract}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "test", "-json", "-count=1", "-timeout=10s", ".")
			cmd.Dir, cmd.Env = dir, responseEnvironment()
			cmd.WaitDelay = time.Second
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			stdout, runErr := cmd.Output()
			if err := gate.Write(filepath.Join(dir, "test.jsonl"), stdout); err != nil {
				t.Fatal(err)
			}
			if err := gate.Write(filepath.Join(dir, "test.stderr.log"), stderr.Bytes()); err != nil {
				t.Fatal(err)
			}
			if ctx.Err() != nil {
				t.Fatal(ctx.Err())
			}
			failed, err := responseFailures(stdout, runErr)
			if err != nil || !slices.Equal(failed, tc.failed) {
				t.Fatalf("unexpected contract result: failed=%v want=%v error=%v process=%v\n%s\n%s", failed, tc.failed, err, runErr, stdout, stderr.Bytes())
			}
			results = append(results, map[string]any{"case": tc.name, "source_sha256": gate.Digest(tc.source), "failed_tests": failed, "passed_tests": 4 - len(failed)})
		})
	}
	if t.Failed() {
		return
	}
	if err := gate.WriteJSON(filepath.Join(output, "results.json"), results); err != nil {
		t.Fatal(err)
	}
	t.Logf("four isolated response contracts verified: %s", output)
}

// 子进程仅继承运行 Go 所需的本机设置，不继承模型凭证或联网代理。
func responseEnvironment() []string {
	var env []string
	for _, name := range []string{"PATH", "HOME", "TMPDIR", "TMP", "TEMP", "SYSTEMROOT", "GOCACHE", "GOMODCACHE"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	return append(env, "GOPROXY=off", "GOSUMDB=off", "GOWORK=off", "GOTOOLCHAIN=local", "GOENV=off", "GOFLAGS=-mod=readonly", "CGO_ENABLED=0", "GOTELEMETRY=off")
}

// 预期失败也必须来自完整测试断言，不能用编译错误、panic 或缺少事件冒充。
func responseFailures(data []byte, processErr error) ([]string, error) {
	required := []string{"TestResponseSuccess", "TestResponseTransportError", "TestResponseRedirectDeclined", "TestResponseUseLastResponse"}
	runs, ends := map[string]int{}, map[string]int{}
	var failed []string
	packageEnds := 0
	packageStatus := ""
	d := json.NewDecoder(bytes.NewReader(data))
	for {
		var event struct{ Action, Test, Output string }
		if err := d.Decode(&event); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		if strings.Contains(event.Output, "panic:") {
			return nil, fmt.Errorf("contract process panicked")
		}
		if event.Test == "" {
			if event.Action == "pass" || event.Action == "fail" || event.Action == "skip" {
				packageEnds++
				packageStatus = event.Action
			}
			continue
		}
		if !slices.Contains(required, event.Test) {
			return nil, fmt.Errorf("unexpected contract test: %s", event.Test)
		}
		switch event.Action {
		case "run":
			runs[event.Test]++
		case "pass", "fail":
			ends[event.Test]++
			if event.Action == "fail" {
				failed = append(failed, event.Test)
			}
		case "skip":
			return nil, fmt.Errorf("contract test skipped: %s", event.Test)
		}
	}
	for _, name := range required {
		if runs[name] != 1 || ends[name] != 1 {
			return nil, fmt.Errorf("incomplete contract events: %s", name)
		}
	}
	wantStatus := "pass"
	if len(failed) > 0 {
		wantStatus = "fail"
	}
	if packageEnds != 1 || (processErr == nil) != (len(failed) == 0) || packageStatus != wantStatus {
		return nil, fmt.Errorf("contract exit and package events disagree")
	}
	slices.Sort(failed)
	return failed, nil
}
