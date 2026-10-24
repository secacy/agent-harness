package deepseek

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/repotools"
)

func TestFinalResponseBudgetedHTTP(t *testing.T) {
	for _, mode := range []string{"stop", "illegal-tool", "unknown-usage", "denied"} {
		t.Run(mode, func(t *testing.T) {
			workspace := t.TempDir()
			if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module example.com/demo\n"), 0600); err != nil {
				t.Fatal(err)
			}
			executor, err := repotools.New(repotools.Config{Workspace: workspace})
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			ledgerFile := filepath.Join(root, "budget", "budget.json")
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := int(requests.Add(1))
				var request map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				var messages []chatMessage
				json.Unmarshal(request["messages"], &messages)
				if n == 1 && len(request["tools"]) == 0 {
					t.Error("initial tools missing")
				}
				if n == 2 {
					if _, exists := request["tools"]; exists {
						t.Error("final tools field present")
					}
					if len(messages) != 5 || messages[3].Role != "tool" || messages[3].ToolCallID != "read" {
						t.Error("history lost", messages)
					}
				}
				raw, err := os.ReadFile(ledgerFile)
				var snap budget.Snapshot
				if err != nil || json.Unmarshal(raw, &snap) != nil || len(snap.Calls) != n || snap.Calls[n-1].State != budget.StateReserved {
					t.Error("request preceded reservation", err)
				}
				if n == 1 {
					fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"read","type":"function","function":{"name":"read_file","arguments":"{\"root\":\"workspace\",\"path\":\"go.mod\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25}}`)
					return
				}
				if mode == "illegal-tool" {
					fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"write","type":"function","function":{"name":"replace_text","arguments":"{\"path\":\"go.mod\",\"old\":\"demo\",\"new\":\"corrupt\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25}}`)
					return
				}
				usage := `,"usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25}`
				if mode == "unknown-usage" {
					usage = ""
				}
				fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]`+usage+`}`)
			}))
			defer server.Close()
			profile := controlledProfile(server.URL)
			quota := budget.Limits{MaxCalls: 2, MaxTokens: 220}
			if mode == "denied" {
				quota.MaxCalls = 1
			}
			ledger, err := budget.Create(budget.Config{Directory: filepath.Dir(ledgerFile), CampaignID: "test", Profile: profile, Limits: quota, Targets: map[string]budget.Limits{"orders": quota}})
			if err != nil {
				t.Fatal(err)
			}
			client, err := New(Config{APIKey: "test", Endpoint: server.URL, Model: profile.Model, MaxTokens: 10, BudgetProfile: &profile})
			if err != nil {
				t.Fatal(err)
			}
			model, err := budget.NewModel(ledger, "orders", "run-1", client)
			if err != nil {
				t.Fatal(err)
			}
			loop := agent.Loop{CompletionPolicy: agent.FinalResponseV1, Model: model, Executor: executor, Tools: executor.Specs(), Limits: agent.Limits{MaxModelCalls: 2, MaxToolCalls: 4}}
			result, runErr := loop.Run(context.Background(), initialMessages())
			if err := agent.ValidateCompletionResult(agent.FinalResponseV1, loop.Limits, result); err != nil {
				t.Fatal(err)
			}
			expected := agent.ExitCompleted
			if mode == "illegal-tool" {
				expected = agent.ExitFailed
			}
			if mode == "denied" {
				expected = agent.ExitLimitReached
			}
			if result.Reason != expected || result.ToolCalls != 1 || (runErr != nil) != (mode == "illegal-tool" || mode == "denied") {
				t.Fatal(result, runErr)
			}
			raw, _ := os.ReadFile(filepath.Join(workspace, "go.mod"))
			if string(raw) != "module example.com/demo\n" {
				t.Fatal("final tool executed")
			}
			attempt := result.FinalResponseAttempt
			input, _ := json.Marshal(struct {
				Messages []agent.Message
				Tools    []agent.ToolSpec
			}{Messages: result.Messages[:attempt.MessageCount]})
			sum := sha256.Sum256(input)
			want := hex.EncodeToString(sum[:])
			snap := ledger.Snapshot()
			if mode == "denied" {
				if requests.Load() != 1 || len(snap.Rejections) != 1 || snap.Rejections[0].InputSHA256 != want {
					t.Fatal("denied attempt sent or digest lost")
				}
			} else {
				if requests.Load() != 2 || snap.Calls[1].InputSHA256 != want {
					t.Fatal("admitted digest mismatch")
				}
			}
			if mode == "unknown-usage" && snap.Totals.UnknownTokens != 110 {
				t.Fatal("unknown reservation released")
			}
		})
	}
}
