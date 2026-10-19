package deepseek

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const textMetadata = `{"id":"test-model","context_window":2000,"max_output_tokens":500,"input_modalities":["text","image"],"output_modalities":["text"]}`

func TestDiscoverBindsExactResponseAndRequest(t *testing.T) {
	body := "{\n\"object\":\"list\",\"data\":[" + textMetadata + "]}\n"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("unexpected metadata request: %s %s", r.Method, r.URL)
		}
		io.WriteString(w, body)
	}))
	defer server.Close()
	client := newTestClient(t, server.URL+"/v1/chat/completions")
	before := time.Now()
	got, err := client.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(body))
	if calls.Load() != 1 || string(got.Response) != body || got.Profile.SourceSHA256 != hex.EncodeToString(hash[:]) ||
		got.Profile.ContextTokens != 2000 || got.Profile.MaxOutputTokens != 123 || got.ModelOutputLimit != 500 || got.Profile.Endpoint != client.endpoint ||
		got.Profile.Model != "test-model" || got.Profile.VerifiedAt.Before(before) || got.URL != server.URL+"/v1/models" {
		t.Fatalf("capabilities not bound to response: %+v", got)
	}
}

func TestDiscoverFailsClosed(t *testing.T) {
	cases := map[string]string{
		"missing model":      `{"object":"list","data":[]}`,
		"wrong object":       `{"data":[` + textMetadata + `]}`,
		"duplicate model":    `{"object":"list","data":[` + textMetadata + `,` + textMetadata + `]}`,
		"missing context":    strings.ReplaceAll(textMetadata, `"context_window":2000,`, ""),
		"missing output":     strings.ReplaceAll(textMetadata, `"max_output_tokens":500,`, ""),
		"missing modalities": strings.ReplaceAll(textMetadata, `"input_modalities":["text","image"],`, ""),
		"not text output":    strings.ReplaceAll(textMetadata, `"output_modalities":["text"]`, `"output_modalities":["audio"]`),
		"negative bound":     strings.ReplaceAll(textMetadata, `:2000`, `:-1`),
		"output too small":   strings.ReplaceAll(textMetadata, `:500`, `:100`),
		"inconsistent bound": strings.ReplaceAll(textMetadata, `:500`, `:3000`),
		"overflow":           strings.ReplaceAll(textMetadata, `:2000`, `:9223372036854775807`),
		"wrong field type":   strings.ReplaceAll(textMetadata, `:2000`, `:"2000"`),
		"not JSON":           "invalid",
		"secret echo":        `{"object":"list","secret":"test-key","data":[` + textMetadata + `]}`,
		"oversize":           strings.Repeat(" ", maxResponseBytes+1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if strings.HasPrefix(body, `{"id"`) {
				body = `{"object":"list","data":[` + body + `]}`
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
			defer server.Close()
			got, err := newTestClient(t, server.URL+"/chat/completions").Discover(context.Background())
			if err == nil || got.Profile.Provider != "" || len(got.Response) != 0 || strings.Contains(err.Error(), "test-key") {
				t.Fatalf("expected closed discovery: %+v %v", got, err)
			}
		})
	}
}

func TestDiscoverRejectsRedirectAndPreservesCancellation(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Location", "/elsewhere")
		w.WriteHeader(http.StatusTemporaryRedirect)
		fmt.Fprint(w, "test-key")
	}))
	defer server.Close()
	client := newTestClient(t, server.URL+"/chat/completions")
	_, err := client.Discover(context.Background())
	var apiErr *HTTPError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 307 || calls.Load() != 1 || strings.Contains(err.Error(), "test-key") {
		t.Fatalf("redirect followed or leaked: %v, calls=%d", err, calls.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Discover(ctx); !errors.Is(err, context.Canceled) || calls.Load() != 1 {
		t.Fatalf("cancelled discovery sent request: %v", err)
	}
}

func TestModelsEndpoint(t *testing.T) {
	for _, endpoint := range []string{"http://example.com/chat/completions", "https://api.deepseek.com/models", "https://api.deepseek.com/chat/completions?key=secret", "https://user:secret@example.com/chat/completions", "https://example.com/%63hat/completions"} {
		if _, err := ModelsEndpoint(endpoint); err == nil {
			t.Fatalf("accepted invalid discovery URL: %s", endpoint)
		}
	}
	for endpoint, want := range map[string]string{
		"": "https://api.deepseek.com/models",
		"https://api.deepseek.com/v1/chat/completions": "https://api.deepseek.com/v1/models",
		"http://[::1]:1234/chat/completions":           "http://[::1]:1234/models",
	} {
		if got, err := ModelsEndpoint(endpoint); err != nil || got != want {
			t.Fatalf("got %s, %v; want %s", got, err, want)
		}
	}
}
