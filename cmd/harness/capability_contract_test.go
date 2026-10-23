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

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/round"
)

func TestBatchModelContractAcceptsMetadataAndPreservesActualSource(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "synthetic-contract-key")
	config := enrolledBatch(t, false)
	// 同时改变排版、未使用的 API 元数据和其他模型，所用文本能力保持。
	response := strings.Replace(batchMetadata, `"context_window":2000`, `"api_capabilities":{"anthropic_messages":{"tool_update":"addition-only"}},"context_window":2000`, 1)
	response = strings.TrimSuffix(response, "]}") + `,{"id":"another-model","new_metadata":true}]}` + "\n"
	actualSource := gate.Digest([]byte(response))
	var gets, posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/models" {
			gets.Add(1)
			io.WriteString(w, response)
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		posts.Add(1)
		var snapshot budget.Snapshot
		raw, err := os.ReadFile(filepath.Join(config.Directory, "campaign/budget/budget.json"))
		if err != nil || json.Unmarshal(raw, &snapshot) != nil || snapshot.Profile.SourceSHA256 != actualSource || snapshot.Totals.ReservedTokens != 2123 {
			t.Errorf("actual source or reservation missing before POST: %s %v", raw, err)
		}
		raw, err = os.ReadFile(filepath.Join(config.Directory, "models.json"))
		if err != nil || string(raw) != response {
			t.Error("original response not published before POST", err)
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Submitted unchanged."},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25}}`)
	}))
	defer server.Close()
	config.Model.Endpoint = server.URL + "/chat/completions"
	config, _ = protocolBatch(t, config, round.ModelContractBinding)
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"batch", "--config", writeBatchTestConfig(t, config)}, &stdout, &stderr); code != 0 || gets.Load() != 1 || posts.Load() != 1 {
		t.Fatalf("compatible metadata blocked: exit=%d GET=%d POST=%d stdout=%s stderr=%s", code, gets.Load(), posts.Load(), &stdout, &stderr)
	}
	var request round.Request
	readBatchJSON(t, filepath.Join(config.Directory, "round-request.json"), &request)
	out, err := round.Summarize(context.Background(), request)
	if err != nil || !out.Valid || !out.InputsUnchanged || out.Counts.Targets != 1 || out.Counts.Verdicts["Rejected"] != 1 || out.Cost.Totals.Calls != 1 || out.Cost.Totals.KnownTokens != 25 {
		t.Fatalf("new binding cannot be consumed offline: %+v %v", out, err)
	}
	var manifest round.Manifest
	readBatchJSON(t, request.ManifestPath, &manifest)
	raw, err := os.ReadFile(filepath.Join(config.Directory, "campaign/plan.json"))
	var plan campaign.Plan
	if err != nil || round.Decode(raw, &plan) != nil || manifest.PlanSHA256 != gate.Digest(raw) || plan.Profile.SourceSHA256 != actualSource || manifest.Protocol.CapabilityBinding != round.ModelContractBinding {
		t.Fatalf("actual plan binding lost: %+v %v", manifest, err)
	}
	for _, name := range []string{"models.json", "capabilities.json"} {
		path, err := gate.Canonical(filepath.Join(config.Directory, name))
		if err != nil {
			t.Fatal(err)
		}
		if source, ok := out.Sources[path]; !ok || source.Missing || source.Copy == "" {
			t.Fatalf("current capability evidence omitted: %s %+v", name, source)
		}
	}
	t.Logf("localhost-only contract campaign and offline round: %s", config.Directory)
}

func TestBatchModelContractRejectsCapabilityDriftBeforeGeneration(t *testing.T) {
	cases := map[string]string{
		"context-increased": strings.Replace(batchMetadata, ":2000", ":2100", 1),
		"context-decreased": strings.Replace(batchMetadata, ":2000", ":1900", 1),
		"missing-model":     strings.Replace(batchMetadata, "test-model", "another-model", 1),
		"missing-context":   strings.Replace(batchMetadata, `"context_window":2000,`, "", 1),
		"no-text-input":     strings.Replace(batchMetadata, `"input_modalities":["text"]`, `"input_modalities":["image"]`, 1),
		"output-too-small":  strings.Replace(batchMetadata, ":500", ":100", 1),
	}
	for name, response := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("DEEPSEEK_API_KEY", "synthetic-contract-key")
			config := enrolledBatch(t, false)
			var gets, posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					gets.Add(1)
					io.WriteString(w, response)
					return
				}
				posts.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()
			config.Model.Endpoint = server.URL + "/chat/completions"
			config, _ = protocolBatch(t, config, round.ModelContractBinding)
			var stdout, stderr bytes.Buffer
			if code := run(context.Background(), []string{"batch", "--config", writeBatchTestConfig(t, config)}, &stdout, &stderr); code == 0 || gets.Load() != 1 || posts.Load() != 0 {
				t.Fatalf("incompatible capability reached generation: exit=%d GET=%d POST=%d stderr=%s", code, gets.Load(), posts.Load(), &stderr)
			}
			if _, err := os.Stat(filepath.Join(config.Directory, "campaign/budget/budget.json")); !os.IsNotExist(err) {
				t.Fatalf("incompatible capability reached budget admission: %v", err)
			}
		})
	}
}

func readBatchJSON(t *testing.T, path string, out any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := round.Decode(raw, out); err != nil {
		t.Fatal(err)
	}
}
