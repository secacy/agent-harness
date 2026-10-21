package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/round"
)

func protocolBatch(t *testing.T, config batchConfig) (batchConfig, round.Enrollment) {
	t.Helper()
	loaded, plan, err := loadBatchConfig(writeBatchTestConfig(t, config))
	if err != nil {
		t.Fatal(err)
	}
	// 只声明本机模拟服务的已知响应，绝不认证真实供应商上界。
	plan.Profile = budget.Profile{Provider: "deepseek", Endpoint: config.Model.Endpoint, Model: config.Model.Name,
		ContextTokens: 2000, MaxOutputTokens: int64(config.Model.MaxTokens), SourceSHA256: gate.Digest([]byte(batchMetadata)), VerifiedAt: time.Now().UTC()}
	hash, err := round.PlanSettingsSHA256(plan)
	if err != nil {
		t.Fatal(err)
	}
	var enrollment round.Enrollment
	if err := round.Decode(loaded.enrollmentData, &enrollment); err != nil {
		t.Fatal(err)
	}
	enrollment.SchemaVersion, enrollment.Kind = 2, round.Formal
	enrollment.Protocol = &round.Protocol{ID: "mock-protocol-v1", Split: round.DevelopmentSplit, AttemptsPerTarget: 1,
		PlanSettingsSHA256: hash, Execution: execution.Info{Kind: execution.KindLocal}, Reviewers: map[string]string{}, Materials: map[string]round.Artifact{}}
	for _, target := range enrollment.Targets {
		enrollment.Protocol.Reviewers[target.ID] = "synthetic-test-only"
	}
	for _, name := range []string{"protocol", "dataset", "implementation", "environment"} {
		raw := []byte("HOST-ONLY-PROTOCOL-MATERIAL mechanism-only " + name)
		path := filepath.Join(filepath.Dir(config.Directory), "material-"+name)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		enrollment.Protocol.Materials[name] = round.Artifact{Path: path, SHA256: gate.Digest(raw)}
	}
	raw, err := json.MarshalIndent(enrollment, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(loaded.Enrollment.Path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	loaded.Enrollment.SHA256 = gate.Digest(raw)
	return loaded, enrollment
}

func TestBatchProtocolPublishedBeforeGenerationAndConsumedOffline(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "mock-protocol-key")
	config := enrolledBatch(t, false)
	var enrollment round.Enrollment
	var gets, posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			gets.Add(1)
			// 配置加载后原文件漂移，发布的仍须是预先固定的副本。
			if err := os.WriteFile(enrollment.Protocol.Materials["protocol"].Path, []byte("changed original"), 0600); err != nil {
				t.Error(err)
			}
			io.WriteString(w, batchMetadata)
			return
		}
		posts.Add(1)
		for name, pin := range enrollment.Protocol.Materials {
			raw, err := os.ReadFile(filepath.Join(config.Directory, "protocol-materials", name))
			if err != nil || gate.Digest(raw) != pin.SHA256 {
				t.Errorf("generation preceded frozen material publication: %s %v", name, err)
			}
		}
		for _, name := range []string{"enrollment.json", "round-manifest.json", "round-request.json"} {
			if _, err := os.Stat(filepath.Join(config.Directory, name)); err != nil {
				t.Errorf("generation preceded protocol publication: %s %v", name, err)
			}
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || bytes.Contains(body, []byte("HOST-ONLY-PROTOCOL-MATERIAL")) || bytes.Contains(body, []byte("synthetic-test-only")) {
			t.Error("host protocol or reviewer metadata reached model messages", err)
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Submitted unchanged."},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25}}`)
	}))
	defer server.Close()
	config.Model.Endpoint = server.URL + "/chat/completions"
	config, enrollment = protocolBatch(t, config)
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"batch", "--config", writeBatchTestConfig(t, config)}, &stdout, &stderr); code != 0 || gets.Load() != 1 || posts.Load() != 1 {
		t.Fatalf("protocol batch failed: exit=%d gets=%d posts=%d stdout=%s stderr=%s", code, gets.Load(), posts.Load(), &stdout, &stderr)
	}
	var request round.Request
	raw, err := os.ReadFile(filepath.Join(config.Directory, "round-request.json"))
	if err != nil || round.Decode(raw, &request) != nil {
		t.Fatalf("missing round request: %v", err)
	}
	out, err := round.Summarize(context.Background(), request)
	if err != nil || !out.Valid || out.Kind != round.Formal || out.Protocol.Split != round.DevelopmentSplit || out.Counts.Targets != 1 || out.Counts.Verdicts["Rejected"] != 1 || out.Metrics.WrongRelease != nil || out.Cost.Totals.Calls != 1 || out.Cost.Totals.KnownTokens != 25 {
		t.Fatalf("published protocol cannot be consumed offline: %+v %v", out, err)
	}
	// 新汇总仍保留原拒绝判定；此处没有真实供应商或实际人员复核。
}

func TestBatchProtocolDriftStopsBeforeGeneration(t *testing.T) {
	for _, mode := range []string{"settings", "capabilities", "material", "execution"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("DEEPSEEK_API_KEY", "mock-protocol-key")
			config := enrolledBatch(t, false)
			var gets, posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					gets.Add(1)
					raw := batchMetadata
					if mode == "capabilities" {
						// 相同字段值，响应字节改变也不冒充旧能力材料。
						raw += "\n"
					}
					io.WriteString(w, raw)
					return
				}
				posts.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()
			config.Model.Endpoint = server.URL + "/chat/completions"
			config, enrollment := protocolBatch(t, config)
			wantGets, wantExit := int32(1), 1
			if mode == "settings" {
				config.Budget.MaxCalls++
			} else if mode == "execution" {
				enrollment.Protocol.Execution = execution.Info{Kind: execution.KindDocker, Image: "sha256:" + strings.Repeat("a", 64), CPUs: 2, MemoryBytes: 2 << 30, Pids: 128}
				raw, _ := json.Marshal(enrollment)
				if err := os.WriteFile(config.Enrollment.Path, raw, 0600); err != nil {
					t.Fatal(err)
				}
				config.Enrollment.SHA256 = gate.Digest(raw)
			} else if mode == "material" {
				wantGets, wantExit = 0, 2
				if err := os.WriteFile(enrollment.Protocol.Materials["dataset"].Path, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr bytes.Buffer
			if code := run(context.Background(), []string{"batch", "--config", writeBatchTestConfig(t, config)}, &stdout, &stderr); code != wantExit || gets.Load() != wantGets || posts.Load() != 0 {
				t.Fatalf("drift reached generation: exit=%d gets=%d posts=%d stderr=%s", code, gets.Load(), posts.Load(), &stderr)
			}
			if _, err := os.Stat(filepath.Join(config.Directory, "round-request.json")); !os.IsNotExist(err) {
				t.Fatal("failed protocol published a round request")
			}
		})
	}
}

func TestBatchProtocolMaterialPublicationFailure(t *testing.T) {
	config := enrolledBatch(t, false)
	config.Model.Endpoint = "https://example.invalid/chat/completions"
	config, _ = protocolBatch(t, config)
	config, plan, err := loadBatchConfig(writeBatchTestConfig(t, config))
	if err != nil {
		t.Fatal(err)
	}
	plan.Profile = budget.Profile{Provider: "deepseek", Endpoint: config.Model.Endpoint, Model: config.Model.Name,
		ContextTokens: 2000, MaxOutputTokens: int64(config.Model.MaxTokens), SourceSHA256: gate.Digest([]byte(batchMetadata)), VerifiedAt: time.Now().UTC()}
	if err := os.MkdirAll(filepath.Join(config.Directory, "campaign"), 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(plan)
	path := filepath.Join(config.Directory, "campaign/plan.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	// 阻断新材料目录的发布，不能留下可运行的后续汇总请求。
	if err := os.WriteFile(filepath.Join(config.Directory, "protocol-materials"), []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareBatchEnrollment(config, execution.Local{})(context.Background(), campaign.Artifact{Path: path, SHA256: gate.Digest(raw)}); err == nil || !strings.Contains(err.Error(), "file exists") {
		t.Fatalf("publication failure accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(config.Directory, "round-request.json")); !os.IsNotExist(err) {
		t.Fatal("failed material publication produced a request")
	}
}
