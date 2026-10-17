package deepseek

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"example.com/agent-harness/internal/agent"
	"example.com/agent-harness/internal/repotools"
)

func newTestClient(t *testing.T, endpoint string) *Client {
	t.Helper()
	client, err := New(Config{APIKey: "test-key", Model: "test-model", Endpoint: endpoint, MaxTokens: 123})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func initialMessages() []agent.Message {
	return []agent.Message{{Role: agent.RoleUser, Content: "Read go.mod"}}
}

func TestClientLoopWithRealFileTool(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module example.com/demo\n"), 0644); err != nil {
		t.Fatal(err)
	}
	executor, err := repotools.New(repotools.Config{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step := requests.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
		}
		var request chatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if request.Model != "test-model" || request.MaxTokens != 123 || request.Stream || request.Thinking.Type != "disabled" || len(request.Tools) != 4 {
			t.Errorf("unexpected completion settings: %+v", request)
		}
		w.Header().Set("Content-Type", "application/json")
		switch step {
		case 1:
			if len(request.Messages) != 1 {
				t.Errorf("unexpected initial history: %+v", request.Messages)
			}
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"bad","type":"function","function":{"name":"read_file","arguments":"not JSON"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"prompt_cache_hit_tokens":6,"prompt_cache_miss_tokens":4}}`)
		case 2:
			if len(request.Messages) != 3 || request.Messages[1].ToolCalls[0].Function.Arguments != "not JSON" || request.Messages[2].ToolCallID != "bad" || !strings.HasPrefix(request.Messages[2].Content, "Tool error: ") {
				t.Errorf("tool error was not fed back: %+v", request.Messages)
			}
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"read-1","type":"function","function":{"name":"read_file","arguments":"{\"root\":\"workspace\",\"path\":\"go.mod\"}"}}]},"finish_reason":"tool_calls"}]}`)
		case 3:
			if len(request.Messages) != 5 || request.Messages[4].Role != "tool" || request.Messages[4].ToolCallID != "read-1" || request.Messages[4].Content != "module example.com/demo\n" {
				t.Errorf("file contents were not fed back: %+v", request.Messages)
			}
			io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":3,"total_tokens":23}}`)
		default:
			t.Error("unexpected extra request")
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server.URL+"/chat/completions")
	loop := agent.Loop{Model: client, Executor: executor, Tools: executor.Specs(), Limits: agent.Limits{MaxModelCalls: 3, MaxToolCalls: 2}}
	initial := initialMessages()
	result, err := loop.Run(context.Background(), initial)
	if err != nil || result.Reason != agent.ExitCompleted || result.ModelCalls != 3 || result.ToolCalls != 2 || requests.Load() != 3 {
		t.Fatalf("loop failed: %+v %v", result, err)
	}
	if len(result.Usage) != 3 || result.Usage[0] == nil || *result.Usage[0].CacheHitTokens != 6 || *result.Usage[0].CacheMissTokens != 4 || result.Usage[1] != nil || result.Usage[2] == nil || result.Usage[2].TotalTokens != 23 {
		t.Fatalf("usage lost or invented: %+v", result.Usage)
	}
	if len(initial) != 1 || initial[0].Content != "Read go.mod" {
		t.Fatal("client mutated initial messages")
	}
}

func TestClientRejectsHTTPAndMalformedResponses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", 401, `{"error":{"message":"invalid test-key"}}`},
		{"rate-limit", 429, `{"error":{"message":"slow down"}}`},
		{"server-error", 500, "internal server error"},
		{"malformed-json", 200, "{"},
		{"multiple-json-values", 200, `{} {}`},
		{"empty-choices", 200, `{"choices":[]}`},
		{"oversized", 200, strings.Repeat(" ", maxResponseBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer server.Close()
			_, err := newTestClient(t, server.URL).Complete(context.Background(), initialMessages(), nil)
			if err == nil || requests.Load() != 1 || strings.Contains(err.Error(), "test-key") {
				t.Fatalf("failure was hidden, retried or leaked credentials: %v requests=%d", err, requests.Load())
			}
			if tc.status != http.StatusOK {
				var apiError *HTTPError
				if !errors.As(err, &apiError) || apiError.StatusCode != tc.status {
					t.Fatalf("HTTP status lost: %v", err)
				}
			}
		})
	}
}

func TestClientDoesNotFollowRedirects(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	_, err := newTestClient(t, source.URL).Complete(context.Background(), initialMessages(), nil)
	var apiError *HTTPError
	if !errors.As(err, &apiError) || apiError.StatusCode != 307 || redirected.Load() != 0 {
		t.Fatalf("redirect was followed: %v requests=%d", err, redirected.Load())
	}
}

func TestClientCancellationAndTimeout(t *testing.T) {
	for _, mode := range []string{"cancelled-before-request", "cancelled-during-request", "cancelled-during-body", "HTTP-timeout"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				if mode == "cancelled-during-body" {
					fmt.Fprint(w, "{")
					w.(http.Flusher).Flush()
				}
				close(started)
				<-r.Context().Done()
			}))
			defer server.Close()
			client := newTestClient(t, server.URL)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled-before-request" {
				cancel()
			}
			if mode == "HTTP-timeout" {
				client.httpClient.Timeout = 50 * time.Millisecond
			}
			done := make(chan error, 1)
			go func() {
				_, err := client.Complete(ctx, initialMessages(), nil)
				done <- err
			}()
			if mode == "cancelled-during-request" || mode == "cancelled-during-body" {
				select {
				case <-started:
					cancel()
				case <-time.After(2 * time.Second):
					t.Fatal("request did not start")
				}
			}
			select {
			case err := <-done:
				want := context.Canceled
				if mode == "HTTP-timeout" {
					want = context.DeadlineExceeded
				}
				if !errors.Is(err, want) {
					t.Fatalf("context error lost: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("client ignored cancellation or timeout")
			}
		})
	}
}

func TestClientConfiguration(t *testing.T) {
	for _, config := range []Config{
		{Model: "model"}, {APIKey: "key"},
		{APIKey: "key", Model: "model", MaxTokens: -1},
		{APIKey: "key\nheader", Model: "model"},
		{APIKey: "key", Model: "model", Endpoint: "/relative"},
		{APIKey: "key", Model: "model", Endpoint: "ftp://example.com"},
		{APIKey: "key", Model: "model", HTTPClient: &http.Client{Timeout: -1}},
	} {
		if _, err := New(config); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
	provided := &http.Client{}
	client, err := New(Config{APIKey: "key", Model: "model", HTTPClient: provided})
	if err != nil || client.endpoint != defaultEndpoint || client.maxTokens != defaultMaxTokens || client.httpClient.Timeout <= 0 || provided.Timeout != 0 || provided.CheckRedirect != nil {
		t.Fatalf("defaults missing or caller client modified: %+v %v", client, err)
	}
}

func TestClientLengthReplyStopsLoop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"partial","tool_calls":[{"function":{"arguments":"{"}}]},"finish_reason":"length"}],"usage":{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}}`)
	}))
	defer server.Close()
	executor, err := repotools.New(repotools.Config{Workspace: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	loop := agent.Loop{Model: newTestClient(t, server.URL), Executor: executor, Tools: executor.Specs(), Limits: agent.Limits{MaxModelCalls: 1, MaxToolCalls: 1}}
	result, err := loop.Run(context.Background(), initialMessages())
	if !errors.Is(err, agent.ErrOutputTruncated) || result.Reason != agent.ExitFailed || result.ToolCalls != 0 || result.Messages[1].Content != "partial" || len(result.Usage) != 1 || result.Usage[0].TotalTokens != 7 {
		t.Fatalf("truncated reply was lost or executed: %+v %v", result, err)
	}
}
