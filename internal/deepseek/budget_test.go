package deepseek

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
)

func controlledProfile(endpoint string) budget.Profile {
	return budget.Profile{Provider: "deepseek", Endpoint: endpoint, Model: "controlled-model", ContextTokens: 100, MaxOutputTokens: 10, SourceSHA256: strings.Repeat("f", 64), VerifiedAt: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)}
}

func TestBudgetedHTTPCallAndUsageFailures(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       budget.CallState
		wantErr    bool
	}{
		{"known", `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25}}`, 200, budget.StateSettled, false},
		{"missing-field", `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"total_tokens":25}}`, 200, budget.StateUnknown, false},
		{"protocol-error-with-usage", `{"choices":[],"usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25}}`, 200, budget.StateSettled, true},
		{"bad-json", `{"choices":`, 200, budget.StateUnknown, true},
		{"invalid-usage", `{"choices":[{"message":{"role":"assistant"},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":1}}`, 200, budget.StateInvalid, true},
		{"server-error", `{"error":{"message":"unavailable"}}`, 503, budget.StateUnknown, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			var ledger *budget.Ledger
			ledgerFile := filepath.Join(t.TempDir(), "budget", "budget.json")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				data, err := os.ReadFile(ledgerFile)
				if err != nil {
					t.Error(err)
				}
				var snap budget.Snapshot
				if err = json.Unmarshal(data, &snap); err != nil || len(snap.Calls) != 1 || snap.Calls[0].State != budget.StateReserved {
					t.Errorf("HTTP before reservation: %s %v", data, err)
				}
				var request chatRequest
				if err = json.NewDecoder(r.Body).Decode(&request); err != nil || request.MaxTokens != 10 || request.Thinking.Type != "disabled" || request.Model != "controlled-model" {
					t.Errorf("request configuration: %+v %v", request, err)
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			profile := controlledProfile(server.URL)
			var err error
			ledger, err = budget.Create(budget.Config{Directory: filepath.Dir(ledgerFile), CampaignID: "http-test", Profile: profile, Limits: budget.Limits{MaxCalls: 1, MaxTokens: 110}, Targets: map[string]budget.Limits{"orders": {MaxCalls: 1, MaxTokens: 110}}})
			if err != nil {
				t.Fatal(err)
			}
			client, err := New(Config{APIKey: "controlled-test-key", Model: profile.Model, Endpoint: server.URL, MaxTokens: 10, BudgetProfile: &profile})
			if err != nil {
				t.Fatal(err)
			}
			model, err := budget.NewModel(ledger, "orders", "run-1", client)
			if err != nil {
				t.Fatal(err)
			}
			_, err = model.Complete(context.Background(), []agent.Message{{Role: agent.RoleUser, Content: "hello"}}, nil)
			if (err != nil) != tc.wantErr || requests.Load() != 1 {
				t.Fatalf("error=%v requests=%d", err, requests.Load())
			}
			if got := ledger.Snapshot().Calls[0].State; got != tc.want {
				t.Fatalf("state=%s want=%s", got, tc.want)
			}
			_, err = model.Complete(context.Background(), []agent.Message{{Role: agent.RoleUser, Content: "again"}}, nil)
			if requests.Load() != 1 || err == nil {
				t.Fatalf("second HTTP request bypassed budget: %d %v", requests.Load(), err)
			}
		})
	}
}

func TestBudgetProfileMustMatchActualRequest(t *testing.T) {
	profile := controlledProfile("http://localhost:1")
	for _, change := range []func(*budget.Profile){func(p *budget.Profile) { p.Provider = "other" }, func(p *budget.Profile) { p.Model = "other" }, func(p *budget.Profile) { p.Endpoint = "http://localhost:2" }, func(p *budget.Profile) { p.MaxOutputTokens = 11 }} {
		p := profile
		change(&p)
		if _, err := New(Config{APIKey: "test", Model: profile.Model, Endpoint: profile.Endpoint, MaxTokens: 10, BudgetProfile: &p}); err == nil {
			t.Fatal("accepted mismatched model profile")
		}
	}
	client, err := New(Config{APIKey: "test", Model: profile.Model})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.BudgetProfile(); err == nil {
		t.Fatal("guessed model bound without host evidence")
	}
}

func TestBudgetedRedirectDoesNotReplayOrRelease(t *testing.T) {
	var redirected atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	p := controlledProfile(source.URL)
	client, err := New(Config{APIKey: "test", Model: p.Model, Endpoint: p.Endpoint, MaxTokens: 10, BudgetProfile: &p})
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := budget.Create(budget.Config{Directory: filepath.Join(t.TempDir(), "budget"), CampaignID: "redirect", Profile: p, Limits: budget.Limits{MaxCalls: 1, MaxTokens: 110}, Targets: map[string]budget.Limits{"orders": {MaxCalls: 1, MaxTokens: 110}}})
	if err != nil {
		t.Fatal(err)
	}
	model, err := budget.NewModel(ledger, "orders", "run-1", client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = model.Complete(context.Background(), []agent.Message{{Role: agent.RoleUser, Content: "hello"}}, nil); err == nil {
		t.Fatal("redirect was accepted")
	}
	if redirected.Load() != 0 || ledger.Snapshot().Totals.UnknownTokens != 110 {
		t.Fatal("redirect replayed or released unknown charge")
	}
}

type budgetCustomTransport struct{}

func (budgetCustomTransport) RoundTrip(*http.Request) (*http.Response, error) {
	panic("custom transport must not run")
}

func TestBudgetProfileCopiedAndCustomTransportRejected(t *testing.T) {
	profile := controlledProfile("http://localhost:1")
	config := Config{APIKey: "test", Model: profile.Model, Endpoint: profile.Endpoint, MaxTokens: 10, BudgetProfile: &profile}
	config.HTTPClient = &http.Client{Transport: budgetCustomTransport{}}
	if _, err := New(config); err == nil {
		t.Fatal("accepted unaccounted custom transport")
	}
	config.HTTPClient = nil
	client, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	profile.Model = "mutated-by-caller"
	got, err := client.BudgetProfile()
	if err != nil || got.Model == profile.Model {
		t.Fatalf("profile aliases caller: %+v %v", got, err)
	}
	got.Model = "mutated-copy"
	again, _ := client.BudgetProfile()
	if again.Model == got.Model {
		t.Fatal("profile accessor leaked mutable state")
	}
}
