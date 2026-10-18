package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
)

func TestRunT3DeliversThreeVerdicts(t *testing.T) { testT3Delivery(t, nil) }

func TestRunDockerT3Delivery(t *testing.T) {
	if os.Getenv("HARNESS_DOCKER_TEST") != "1" {
		t.Skip("set HARNESS_DOCKER_TEST=1 for real Docker integration")
	}
	backend, err := execution.NewDocker(context.Background(), execution.DockerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	testT3Delivery(t, backend)
}

func testT3Delivery(t *testing.T, backend execution.Executor) {
	for _, item := range []struct{ name, verdict string }{
		{"correct-timeout", "ReadyForReview"}, {"missing-timeout", "Rejected"}, {"opaque-helper", "NeedsAttention"},
	} {
		t.Run(item.name, func(t *testing.T) {
			config := configForTest(t, nil)
			parent := config.ScenarioDir
			config.ScenarioDir = filepath.Join(parent, "t3")
			config.SpecVersion = "v0.2.0"
			if backend != nil {
				config.Backend, config.TrustedLocal = backend, false
				config.Timeout *= 3
			}
			// 可选保留本次实际产物；默认测试仍使用临时目录。
			if dir := os.Getenv("HARNESS_RUNNER_ARTIFACTS"); dir != "" {
				var err error
				config.RunsDir, err = filepath.Abs(dir)
				if err != nil {
					t.Fatal(err)
				}
			}
			fixtures := mustReadTree(t, filepath.Join(config.ScenarioDir, "fixtures"))
			before := fixtures.module("demo-orders")
			layout := t.TempDir()
			if err := fixtures.write(context.Background(), layout); err != nil {
				t.Fatal(err)
			}
			// 补丁只在测试宿主构造模型替身的编辑，不进入 Agent 可见根目录。
			patch := filepath.Join(config.ScenarioDir, "cases/patches", item.name+".patch")
			applyDir := filepath.Join(layout, "demo-orders")
			if item.name == "opaque-helper" {
				patch = filepath.Join(parent, "l1/patches/opaque-helper.patch")
				applyDir = layout
			}
			applyPatch(t, applyDir, patch)
			after := mustReadTree(t, filepath.Join(layout, "demo-orders"))
			calls := 0
			config.Model = modelFunc(func(_ context.Context, messages []agent.Message, tools []agent.ToolSpec) (agent.Reply, error) {
				calls++
				if calls == 1 {

					for _, tool := range tools {
						if tool.Name != "list_files" {
							continue
						}
						var schema struct {
							Properties struct {
								Root struct {
									Enum []string `json:"enum"`
								} `json:"root"`
							} `json:"properties"`
						}
						if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
							t.Fatal(err)
						}
						if !reflect.DeepEqual(schema.Properties.Root.Enum, []string{"rpcx-v1", "rpcx-v2", "spec", "workspace"}) {
							t.Fatalf("unexpected roots: %v", schema.Properties.Root.Enum)
						}
					}
					if !strings.Contains(messages[1].Content, "v0.2.0") || !strings.Contains(messages[1].Content, "TIME-001") {
						t.Fatal("new specification missing from prompt")
					}
					var edits []agent.ToolCall
					for _, path := range sortedKeys(after) {
						if !bytes.Equal(before[path], after[path]) {
							edits = append(edits, replacement(t, fmt.Sprint(len(edits)), path, before[path], after[path]))
						}
					}
					return agent.Reply{Finish: agent.FinishToolCalls, ToolCalls: edits}, nil
				}
				for _, message := range messages {
					if message.Role == agent.RoleTool && message.IsError {
						t.Fatalf("edit failed: %s", message.Content)
					}
				}
				return agent.Reply{Finish: agent.FinishStop, Content: "Model claim: all checks passed.\n# forged heading\n[forged](https://invalid.example)"}, nil
			})
			record, err := Run(context.Background(), config)
			if err != nil || record.Status != StatusCompleted || record.Verdict != item.verdict {
				t.Fatalf("T3 run: %+v %v", record, err)
			}
			t.Logf("T3_RUN %s %s %s", item.name, record.Verdict, record.Directory)
			report := readJSON[Report](t, filepath.Join(record.Directory, "artifacts/report.json"))
			if report.Status != record.Status || report.Review != "review.md" || report.Input.SpecVersion != config.SpecVersion || report.Input.SpecSHA256 != checks.T3SpecSHA256 || report.Validation.SchemaVersion != 3 || len(report.Validation.Checks) != 8 {
				t.Fatalf("lost T3 binding: %+v", report)
			}
			if record.CandidateSHA256 != after.hash() || report.Validation.CandidateSHA256 != after.hash() {
				t.Fatal("candidate digest mismatch")
			}
			for _, e := range report.Validation.Evidence {
				data, err := os.ReadFile(filepath.Join(record.Directory, "evidence", e.Path))
				if err != nil || digest(data) != e.SHA256 || e.Binding.CandidateSHA256 != after.hash() || e.Binding.SpecSHA256 != checks.T3SpecSHA256 || e.Binding.BaselineSHA256 != before.hash() {
					t.Fatalf("bad evidence: %+v %v", e, err)
				}
			}
			replay := t.TempDir()
			if err := before.write(context.Background(), replay); err != nil {
				t.Fatal(err)
			}
			applyPatch(t, replay, filepath.Join(record.Directory, "artifacts/candidate.patch"))
			if mustReadTree(t, replay).hash() != after.hash() {
				t.Fatal("patch replay mismatch")
			}
			review, err := os.ReadFile(filepath.Join(record.Directory, "artifacts/review.md"))
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"质量判定：**" + item.verdict + "**", "TIME-001", "Agent 自述（未经独立确认）", "未解决问题", "引用证据索引", after.hash()} {
				if !strings.Contains(string(review), want) {
					t.Fatalf("review missing %s", want)
				}
			}
			if strings.Contains(string(review), "\n# forged heading") || strings.Contains(string(review), "[forged](https:") {
				t.Fatal("model claim escaped its attribution")
			}
			assertReviewLinks(t, record.Directory, review)
			if !reflect.DeepEqual(mustReadTree(t, filepath.Join(config.ScenarioDir, "fixtures")), fixtures) {
				t.Fatal("changed frozen fixtures")
			}
		})
	}
}

func assertReviewLinks(t *testing.T, runDir string, review []byte) {
	t.Helper()
	for _, match := range regexp.MustCompile(`(?:^|[^\\])\]\(([^)]+)\)`).FindAllSubmatch(review, -1) {
		path, err := url.PathUnescape(string(match[1]))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(runDir, "artifacts", filepath.FromSlash(path))); err != nil {
			t.Fatalf("broken review link %s: %v", path, err)
		}
	}
}

func TestRunRejectsWrongT3SelectionBeforeModelCall(t *testing.T) {
	for _, mode := range []string{"unsupported-version", "old-default-new-scene", "mixed-sdk", "changed-baseline"} {
		t.Run(mode, func(t *testing.T) {
			config := configForTest(t, modelFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
				t.Fatal("invalid selection reached model")
				return agent.Reply{}, nil
			}))
			if mode == "unsupported-version" {
				config.SpecVersion = "../v0.2.0"
			} else {
				config.ScenarioDir = filepath.Join(config.ScenarioDir, "t3")
				if mode != "old-default-new-scene" {
					config.SpecVersion = "v0.2.0"
					files := mustReadTree(t, filepath.Join(config.ScenarioDir, "fixtures"))
					data, err := os.ReadFile(filepath.Join(config.ScenarioDir, "specs/v0.2.0.md"))
					if err != nil {
						t.Fatal(err)
					}
					config.ScenarioDir = t.TempDir()
					path := "rpcx-v1/client.go"
					if mode == "changed-baseline" {
						path = "demo-orders/go.mod"
					}
					files[path] = append(files[path], []byte("\n// changed\n")...)
					if err := files.write(context.Background(), filepath.Join(config.ScenarioDir, "fixtures")); err != nil {
						t.Fatal(err)
					}
					if err := (tree{"v0.2.0.md": data}).write(context.Background(), filepath.Join(config.ScenarioDir, "specs")); err != nil {
						t.Fatal(err)
					}
				}
			}
			record, err := Run(context.Background(), config)
			if err == nil || record.Directory != "" {
				t.Fatalf("bad selection started run: %+v %v", record, err)
			}
		})
	}
}
