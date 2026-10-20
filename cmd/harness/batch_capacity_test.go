package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestBatchContextDriftStopsGenerationAndPreservesCapabilities(t *testing.T) {
	for _, mode := range []string{"check", "probe", "generate"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("DEEPSEEK_API_KEY", "capacity-test-secret")
			config := batchTestConfig(t)
			config.Model.ExpectedContextTokens = 1999
			// 不存在的镜像证明容量拒绝先于执行后端初始化。
			config.Execution.TrustedLocal = false
			config.Execution.Image = "harness-capacity-test:no-image"
			var gets, posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/models" {
					gets.Add(1)
					io.WriteString(w, batchMetadata)
					return
				}
				posts.Add(1)
				w.WriteHeader(http.StatusBadRequest)
			}))
			defer server.Close()
			config.Model.Endpoint = server.URL + "/chat/completions"
			args := []string{"batch", "--config", writeBatchTestConfig(t, config)}
			if mode == "check" {
				args = append(args, "--check")
			} else if mode == "probe" {
				args = append(args, "--probe-model")
			}
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), args, &stdout, &stderr)
			wantCode, wantGets := 0, int32(1)
			if mode == "generate" {
				wantCode = 2
				if !strings.Contains(stderr.String(), "expected 1999, discovered 2000") {
					t.Fatalf("missing drift reason: %s", &stderr)
				}
			} else if mode == "check" {
				wantGets = 0
			}
			if code != wantCode || gets.Load() != wantGets || posts.Load() != 0 {
				t.Fatalf("exit=%d GET=%d POST=%d stdout=%s stderr=%s", code, gets.Load(), posts.Load(), &stdout, &stderr)
			}
			if mode == "check" {
				if _, err := os.Stat(config.Directory); !os.IsNotExist(err) {
					t.Fatalf("offline check created output: %v", err)
				}
				return
			}
			for _, name := range []string{"models.json", "capabilities.json", "config.json"} {
				data, err := os.ReadFile(filepath.Join(config.Directory, name))
				if err != nil || len(data) == 0 {
					t.Fatalf("missing diagnostic %s: %v", name, err)
				}
				if name == "models.json" && string(data) != batchMetadata {
					t.Fatal("capability response was changed")
				}
			}
			for _, name := range []string{"campaign", "execution.json"} {
				if _, err := os.Stat(filepath.Join(config.Directory, name)); !os.IsNotExist(err) {
					t.Fatalf("drift initialized %s: %v", name, err)
				}
			}
		})
	}
}
