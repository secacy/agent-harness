package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCommandRunsAdapterLoopAndIndependentValidation(t *testing.T) { testCommandRun(t, true, false) }

func TestCommandRunsDockerByDefault(t *testing.T) {
	if os.Getenv("HARNESS_DOCKER_TEST") != "1" {
		t.Skip("set HARNESS_DOCKER_TEST=1 for real Docker integration")
	}
	testCommandRun(t, false, false)
}

func TestCommandSelectsT3(t *testing.T) { testCommandRun(t, true, true) }

func testCommandRun(t *testing.T, trusted, t3 bool) {
	const secret = "test-key-must-not-be-persisted"
	t.Setenv("DEEPSEEK_API_KEY", secret)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+secret {
			t.Error("invalid API request")
		}
		var request struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Model != "test-model" || len(request.Messages) != 3 || !strings.Contains(request.Messages[1].Content, "CTX-002") || !strings.Contains(request.Messages[2].Content, "Harness budget") {
			t.Errorf("unexpected request: %+v %v", request, err)
		}
		if t3 && !strings.Contains(request.Messages[1].Content, "TIME-001") {
			t.Error("T3 specification did not reach adapter")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Candidate submitted unchanged."},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25}}`))
	}))
	defer server.Close()
	scenario, err := filepath.Abs("../../scenarios/rpcx")
	if err != nil {
		t.Fatal(err)
	}
	runs := filepath.Join(t.TempDir(), "runs")
	var stdout, stderr bytes.Buffer
	if t3 {
		scenario = filepath.Join(scenario, "t3")
	}
	args := []string{"--model", "test-model", "--endpoint", server.URL, "--scenario", scenario, "--runs", runs}
	if t3 {
		args = append(args, "--spec-version", "v0.2.0")
	}
	if trusted {
		args = append(args, "--trusted-local")
	}
	code := run(context.Background(), args, &stdout, &stderr)
	if code != 0 || requests.Load() != 1 || !strings.Contains(stdout.String(), "Status: Completed; verdict: Rejected") || stderr.Len() != 0 {
		t.Fatalf("command: exit=%d, requests=%d, stdout=%s, stderr=%s", code, requests.Load(), &stdout, &stderr)
	}
	// 验证客户端密钥不会因配置序列化进入运行记录或审核材料。
	if err := filepath.WalkDir(runs, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && (strings.HasSuffix(path, ".json") || strings.HasSuffix(path, ".md")) {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if t3 && filepath.Base(path) == "report.json" && filepath.Base(filepath.Dir(path)) == "artifacts" && !bytes.Contains(data, []byte(`"spec_version": "v0.2.0"`)) {
				t.Error("T3 specification not recorded")
			}
			if bytes.Contains(data, []byte(secret)) {
				t.Errorf("API key leaked into %s", path)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCommandRejectsInvalidConfigurationWithoutRunning(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("DEEPSEEK_MODEL", "")
	for _, args := range [][]string{
		{}, {"--trusted-local"}, {"--trusted-local", "--model", "test-model"},
		{"--trusted-local", "--timeout", "0s"}, {"--trusted-local", "--max-model-calls", "0"},
		{"--trusted-local", "unexpected"}, {"--unknown"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), args, &stdout, &stderr); code != 2 || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Fatalf("args=%v: code=%d stdout=%s stderr=%s", args, code, &stdout, &stderr)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"--help"}, &stdout, &stderr); code != 0 || !strings.Contains(stderr.String(), "trusted-local") {
		t.Fatalf("help: code=%d, %s", code, &stderr)
	}
}
