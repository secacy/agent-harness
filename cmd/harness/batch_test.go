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

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/scenarios/rpcx/runner"
)

const batchMetadata = `{"object":"list","data":[{"id":"test-model","context_window":2000,"max_output_tokens":500,"input_modalities":["text"],"output_modalities":["text"]}]}`

func batchTestConfig(t *testing.T) batchConfig {
	t.Helper()
	input, err := runner.TargetFromScenario("orders", "../../scenarios/rpcx", "v0.1.0")
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	if root := os.Getenv("HARNESS_BATCH_ARTIFACTS"); root != "" {
		if err := os.MkdirAll(root, 0700); err != nil {
			t.Fatal(err)
		}
		parent, err = os.MkdirTemp(root, "batch-cli-")
		if err != nil {
			t.Fatal(err)
		}
	}
	return batchConfig{SchemaVersion: 1, ID: "test-batch", Directory: filepath.Join(parent, "batch"),
		Concurrency: 2, ExecutionConcurrency: 2, Budget: budget.Limits{MaxCalls: 3, MaxTokens: 10000},
		Model: batchModel{Name: "test-model", MaxTokens: 123}, Execution: batchExecution{TrustedLocal: true},
		Targets: []batchTarget{{Input: input, Budget: budget.Limits{MaxCalls: 2, MaxTokens: 10000}, MaxModelCalls: 2, MaxToolCalls: 10, Timeout: "5m"}}}
}

func writeBatchTestConfig(t *testing.T, config batchConfig) string {
	t.Helper()
	path := filepath.Join(filepath.Dir(config.Directory), "request.json")
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBatchCommandUsesDiscoveredProfileBeforeGeneration(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(map[bool]string{false: "all delivered", true: "local failure"}[mixed], func(t *testing.T) {
			const secret = "batch-test-secret-never-save"
			t.Setenv("DEEPSEEK_API_KEY", secret)
			config := batchTestConfig(t)
			if mixed {
				missing := config.Targets[0]
				missing.Input.ID = "missing"
				missing.Input.Repository.Directory = filepath.Join(filepath.Dir(config.Directory), "absent")
				config.Targets = append(config.Targets, missing)
			}
			var gets, posts atomic.Int32
			hash := sha256.Sum256([]byte(batchMetadata))
			wantHash := hex.EncodeToString(hash[:])
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+secret {
					t.Error("missing authentication")
				}
				if r.Method == http.MethodGet && r.URL.Path == "/models" {
					gets.Add(1)
					io.WriteString(w, batchMetadata)
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					w.WriteHeader(400)
					return
				}
				posts.Add(1)
				var request struct {
					Model     string                `json:"model"`
					MaxTokens int                   `json:"max_tokens"`
					Stream    bool                  `json:"stream"`
					Thinking  struct{ Type string } `json:"thinking"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Model != "test-model" || request.MaxTokens != 123 || request.Stream || request.Thinking.Type != "disabled" {
					t.Errorf("request/profile mismatch: %+v %v", request, err)
				}
				data, err := os.ReadFile(filepath.Join(config.Directory, "campaign/budget/budget.json"))
				var snapshot budget.Snapshot
				if err != nil || json.Unmarshal(data, &snapshot) != nil || snapshot.Totals.ReservedTokens != 2123 || snapshot.Profile.SourceSHA256 != wantHash || len(snapshot.Calls) != 1 || snapshot.Calls[0].State != budget.StateReserved {
					t.Errorf("generation without persisted reservation: %s %v", data, err)
				}
				io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Submitted unchanged."},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25}}`)
			}))
			defer server.Close()
			config.Model.Endpoint = server.URL + "/chat/completions"
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), []string{"batch", "--config", writeBatchTestConfig(t, config)}, &stdout, &stderr)
			wantCode := 0
			if mixed {
				wantCode = 1
			}
			if code != wantCode || gets.Load() != 1 || posts.Load() != 1 || !strings.Contains(stdout.String(), "Rejected: 1") || stderr.Len() != 0 {
				t.Fatalf("batch exit=%d gets=%d posts=%d stdout=%s stderr=%s", code, gets.Load(), posts.Load(), &stdout, &stderr)
			}
			data, err := os.ReadFile(filepath.Join(config.Directory, "models.json"))
			if err != nil || string(data) != batchMetadata {
				t.Fatalf("capability source not preserved: %s %v", data, err)
			}
			if err := filepath.WalkDir(config.Directory, func(path string, entry os.DirEntry, err error) error {
				if err != nil || entry.IsDir() {
					return err
				}
				data, err := os.ReadFile(path)
				if bytes.Contains(data, []byte(secret)) {
					t.Errorf("credential persisted: %s", path)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			var againOut, againErr bytes.Buffer
			if code := run(context.Background(), []string{"batch", "--config", writeBatchTestConfig(t, config)}, &againOut, &againErr); code != 2 || gets.Load() != 1 || posts.Load() != 1 {
				t.Fatalf("existing batch was reused: code=%d %s", code, &againErr)
			}
		})
	}
}

func TestBatchCheckIsOfflineAndRejectsInvalidConfiguration(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	cases := map[string]func(*batchConfig){
		"valid":            func(*batchConfig) {},
		"duplicate target": func(c *batchConfig) { c.Targets = append(c.Targets, c.Targets[0]) },
		"unknown profile":  func(c *batchConfig) { c.Targets[0].Input.ProfileID = "unknown" },
		"modified pin":     func(c *batchConfig) { c.Targets[0].Input.Repository.SHA256 = strings.Repeat("e", 64) },
		"zero budget":      func(c *batchConfig) { c.Budget.MaxTokens = 0 },
		"zero concurrency": func(c *batchConfig) { c.Concurrency = 0 },
		"bad timeout":      func(c *batchConfig) { c.Targets[0].Timeout = "0s" },
		"bad endpoint":     func(c *batchConfig) { c.Model.Endpoint = "http://example.com/chat/completions" },
		"input overlap": func(c *batchConfig) {
			c.Directory = filepath.Join(c.Targets[0].Input.Repository.Directory, "batch-output")
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			config := batchTestConfig(t)
			path := writeBatchTestConfig(t, config)
			change(&config)
			data, _ := json.Marshal(config)
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), []string{"batch", "--config", path, "--check"}, &stdout, &stderr)
			if name == "valid" {
				if code != 0 || !strings.Contains(stdout.String(), "capabilities not queried") {
					t.Fatalf("offline check: %d %s %s", code, &stdout, &stderr)
				}
			} else if code != 2 {
				t.Fatalf("invalid config accepted: %d %s %s", code, &stdout, &stderr)
			}
			if _, err := os.Stat(config.Directory); !os.IsNotExist(err) {
				t.Fatalf("check created output: %v", err)
			}
		})
	}
}

func TestBatchConfigurationResolvesPathsAndRejectsUnknownFields(t *testing.T) {
	config := batchTestConfig(t)
	path := writeBatchTestConfig(t, config)
	base := filepath.Dir(path)
	config.Directory = "batch"
	input := &config.Targets[0].Input
	input.Repository.Directory, _ = filepath.Rel(base, input.Repository.Directory)
	input.Specification.Path, _ = filepath.Rel(base, input.Specification.Path)
	for name, dependency := range input.Dependencies {
		dependency.Directory, _ = filepath.Rel(base, dependency.Directory)
		input.Dependencies[name] = dependency
	}
	data, _ := json.Marshal(config)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	resolved, plan, err := loadBatchConfig(path)
	physicalBase, pathErr := filepath.EvalSymlinks(base)
	if err != nil || pathErr != nil || resolved.Directory != filepath.Join(physicalBase, "batch") || !filepath.IsAbs(plan.Targets[0].Input.Repository.Directory) {
		t.Fatalf("relative configuration: %+v %v", resolved, err)
	}
	for _, bad := range [][]byte{
		bytes.Replace(data, []byte(`"schema_version":1`), []byte(`"api_key":"forbidden","schema_version":1`), 1),
		append(append([]byte{}, data...), []byte(` {"extra":true}`)...),
		bytes.Replace(data, []byte(`"max_tokens":123`), []byte(`"context_tokens":1,"max_tokens":123`), 1),
	} {
		if err := os.WriteFile(path, bad, 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := loadBatchConfig(path); err == nil {
			t.Fatal("unknown fields or trailing JSON accepted")
		}
	}
}

func TestBatchCapabilityFailureNeverGenerates(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "batch-test-key")
	for _, mode := range []string{"missing bounds", "budget too small", "cancelled", "publication failed"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int32
			config := batchTestConfig(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet {
					t.Error("capability failure sent generation request")
				}
				if mode == "publication failed" {
					// 模拟检查后输出位置被占用；不得覆盖原目录或继续调用模型。
					if err := os.Mkdir(config.Directory, 0700); err != nil {
						t.Error(err)
					}
				}
				if mode == "missing bounds" {
					io.WriteString(w, `{"object":"list","data":[{"id":"test-model"}]}`)
				} else {
					io.WriteString(w, batchMetadata)
				}
			}))
			defer server.Close()
			config.Model.Endpoint = server.URL + "/chat/completions"
			if mode == "budget too small" {
				config.Budget.MaxTokens = 1
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wantCode, wantRequests := 1, int32(1)
			if mode == "cancelled" {
				cancel()
				wantCode, wantRequests = 130, 0
			} else if mode == "budget too small" {
				wantCode = 2
			}
			var stdout, stderr bytes.Buffer
			if code := run(ctx, []string{"batch", "--config", writeBatchTestConfig(t, config)}, &stdout, &stderr); code != wantCode || requests.Load() != wantRequests {
				t.Fatalf("exit=%d requests=%d stdout=%s stderr=%s", code, requests.Load(), &stdout, &stderr)
			}
			if mode == "publication failed" {
				entries, err := os.ReadDir(config.Directory)
				if err != nil || len(entries) != 0 {
					t.Fatalf("occupied output modified: %v %v", entries, err)
				}
			} else if _, err := os.Stat(config.Directory); !os.IsNotExist(err) {
				t.Fatalf("failed preflight created output: %v", err)
			}
		})
	}
}
