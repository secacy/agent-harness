package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/secacy/agent-harness/internal/deepseek"
)

func TestBatchProbeDoesNotGenerateOrInitializeExecution(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "probe-test-secret")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/models" || r.Header.Get("Authorization") != "Bearer probe-test-secret" {
			t.Errorf("unexpected probe request: %s %s", r.Method, r.URL)
		}
		io.WriteString(w, batchMetadata)
	}))
	defer server.Close()
	config := batchTestConfig(t)
	config.Model.Endpoint = server.URL + "/chat/completions"
	config.Budget.MaxTokens, config.Targets[0].Budget.MaxTokens = 1, 1
	config.Execution.TrustedLocal = false
	config.Execution.Image = "nonexistent-image-probe-must-not-inspect"
	path := writeBatchTestConfig(t, config)
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"batch", "--config", path, "--probe-model"}, &stdout, &stderr); code != 0 || requests.Load() != 1 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "reservation=2123") {
		t.Fatalf("probe exit=%d requests=%d stdout=%s stderr=%s", code, requests.Load(), &stdout, &stderr)
	}
	entries, err := os.ReadDir(config.Directory)
	if err != nil || len(entries) != 3 {
		t.Fatalf("probe must only publish three material files: %v %v", entries, err)
	}
	raw, err := os.ReadFile(filepath.Join(config.Directory, "models.json"))
	if err != nil || string(raw) != batchMetadata {
		t.Fatalf("source bytes changed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(config.Directory, "capabilities.json"))
	var capabilities deepseek.Capabilities
	hash := sha256.Sum256(raw)
	if err != nil || json.Unmarshal(data, &capabilities) != nil || capabilities.Profile.SourceSHA256 != hex.EncodeToString(hash[:]) {
		t.Fatalf("capabilities not bound to source: %s %v", data, err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(config.Directory, entry.Name()))
		if err != nil || bytes.Contains(data, []byte("probe-test-secret")) {
			t.Fatalf("probe credentials persisted: %s %v", entry.Name(), err)
		}
	}
	stdout.Reset()
	stderr.Reset()
	if code := run(context.Background(), []string{"batch", "--config", path, "--probe-model"}, &stdout, &stderr); code != 2 || requests.Load() != 1 {
		t.Fatalf("probe reused output or repeated HTTP: %d %s", code, &stderr)
	}
}

func TestBatchProbeRejectsConflictingModesAndInvalidCapabilities(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "probe-test-secret")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		io.WriteString(w, `{"object":"list","data":[{"id":"test-model"}]}`)
	}))
	defer server.Close()
	config := batchTestConfig(t)
	config.Model.Endpoint = server.URL + "/chat/completions"
	path := writeBatchTestConfig(t, config)
	for _, flags := range [][]string{{"--check", "--probe-model"}, {"--probe-model"}} {
		var stdout, stderr bytes.Buffer
		wantCode := 2
		if len(flags) == 1 {
			wantCode = 1
		}
		args := append([]string{"batch", "--config", path}, flags...)
		if code := run(context.Background(), args, &stdout, &stderr); code != wantCode {
			t.Fatalf("probe exit=%d stdout=%s stderr=%s", code, &stdout, &stderr)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("unexpected request count: %d", requests.Load())
	}
	if _, err := os.Stat(config.Directory); !os.IsNotExist(err) {
		t.Fatalf("failed probe published output: %v", err)
	}
}
