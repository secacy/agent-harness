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
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/round"
	"github.com/secacy/agent-harness/scenarios/rpcx/runner"
)

func enrolledBatch(t *testing.T, mixed bool) batchConfig {
	t.Helper()
	config := batchTestConfig(t)
	input, err := runner.TargetFromGoddd("goddd", "../../scenarios/rpcx/l2/data/rpcx-l2-goddd-v1/fixtures", "../../scenarios/rpcx/goddd/specs/v0.3.0.md")
	if err != nil {
		t.Fatal(err)
	}
	config.Targets[0].Input = input
	if mixed {
		missing := config.Targets[0]
		missing.Input.ID = "missing"
		missing.Input.Repository.Directory = filepath.Join(filepath.Dir(config.Directory), "absent")
		config.Targets = append(config.Targets, missing)
	}
	identity, err := evaluation.PublishedIdentity("rpcx-l2-reference-goddd-data-v2")
	if err != nil {
		t.Fatal(err)
	}
	enrollment := round.Enrollment{SchemaVersion: 1, ID: "mock-http-calibration", Kind: round.Calibration}
	for _, target := range config.Targets {
		in := target.Input
		enrollment.Targets = append(enrollment.Targets, round.TargetPin{ID: in.ID, ProfileID: in.ProfileID, BuildConfig: in.BuildConfig,
			BaselineSHA256: in.Repository.SHA256, SpecSHA256: in.Specification.SHA256, ReferenceVersion: identity.Version,
			ReferenceSHA256: identity.ReferenceSHA256, ToolSHA256: identity.ToolSHA256})
	}
	data, _ := json.MarshalIndent(enrollment, "", "  ")
	data = append(data, '\n')
	path := filepath.Join(filepath.Dir(config.Directory), "enrollment-source.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	config.Enrollment = &round.Artifact{Path: filepath.Base(path), SHA256: gate.Digest(data)}
	return config
}

func TestBatchPublishesEnrollmentBeforeGeneration(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "enrollment-test-key")
	config := enrolledBatch(t, true)
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			io.WriteString(w, batchMetadata)
			return
		}
		posts.Add(1)
		plan, err := os.ReadFile(filepath.Join(config.Directory, "campaign/plan.json"))
		if err != nil {
			t.Error(err)
		}
		raw, err := os.ReadFile(filepath.Join(config.Directory, "round-manifest.json"))
		var manifest round.Manifest
		if err != nil || round.Decode(raw, &manifest) != nil || manifest.PlanSHA256 != gate.Digest(plan) || manifest.CampaignID != config.ID || len(manifest.Targets) != 2 {
			t.Errorf("POST preceded enrollment binding: %s %v", raw, err)
		}
		for _, name := range []string{"enrollment.json", "round-request.json"} {
			if _, err := os.Stat(filepath.Join(config.Directory, name)); err != nil {
				t.Errorf("POST preceded material publication: %s %v", name, err)
			}
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || bytes.Contains(body, []byte("rpcx-l2-reference-goddd-data-v2")) || bytes.Contains(body, []byte("round-manifest.json")) || bytes.Contains(body, []byte("enrollment-source.json")) {
			t.Error("evaluation metadata reached model messages", err)
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Submitted unchanged."},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25}}`)
	}))
	defer server.Close()
	config.Model.Endpoint = server.URL + "/chat/completions"
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"batch", "--config", writeBatchTestConfig(t, config)}, &stdout, &stderr); code != 1 || posts.Load() != 1 {
		t.Fatalf("mixed enrolled batch: exit=%d posts=%d stdout=%s stderr=%s", code, posts.Load(), &stdout, &stderr)
	}
	var request round.Request
	raw, err := os.ReadFile(filepath.Join(config.Directory, "round-request.json"))
	if err != nil || round.Decode(raw, &request) != nil {
		t.Fatalf("missing generated request: %s %v", raw, err)
	}
	report, err := round.Summarize(context.Background(), request)
	if err != nil || !report.Valid || report.Counts.Targets != 2 || report.Counts.Runs != 2 || report.Counts.Execution["Failed"] != 1 ||
		report.Counts.Reference[evaluation.Inconclusive] != 2 || report.Counts.Success != 0 || report.Cost == nil || report.Cost.Totals.KnownTokens != 25 {
		t.Fatalf("generated request did not preserve failed/unknown targets: %+v %v", report, err)
	}
	t.Logf("enrolled controlled batch: %s", config.Directory)
}

func TestEnrollmentFailureRejectsBeforeModelAccess(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	for _, mode := range []string{"source drift", "tool drift", "unknown field", "source missing"} {
		t.Run(mode, func(t *testing.T) {
			config := enrolledBatch(t, false)
			source := filepath.Join(filepath.Dir(config.Directory), config.Enrollment.Path)
			data, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "source missing":
				if err := os.Remove(source); err != nil {
					t.Fatal(err)
				}
			case "source drift":
				if err := os.WriteFile(source, append(data, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
			default:
				var enrollment map[string]any
				if err := json.Unmarshal(data, &enrollment); err != nil {
					t.Fatal(err)
				}
				if mode == "unknown field" {
					enrollment["expected_verdict"] = "forbidden"
				} else {
					enrollment["targets"].([]any)[0].(map[string]any)["tool_sha256"] = strings.Repeat("c", 64)
				}
				data, _ = json.Marshal(enrollment)
				config.Enrollment.SHA256 = gate.Digest(data)
				if err := os.WriteFile(source, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			var stdout, stderr bytes.Buffer
			if code := run(context.Background(), []string{"batch", "--config", writeBatchTestConfig(t, config), "--check"}, &stdout, &stderr); code != 2 || strings.Contains(stderr.String(), "API key") {
				t.Fatalf("bad enrollment reached model initialization: %d %s", code, &stderr)
			}
		})
	}
}

func TestEnrollmentPublicationFailureReturnsError(t *testing.T) {
	config := enrolledBatch(t, false)
	config, plan, err := loadBatchConfig(writeBatchTestConfig(t, config))
	if err != nil {
		t.Fatal(err)
	}
	plan.Profile = budget.Profile{Provider: "controlled", Endpoint: "in-process", Model: "fixed", ContextTokens: 90,
		MaxOutputTokens: 10, SourceSHA256: strings.Repeat("a", 64), VerifiedAt: time.Now().UTC()}
	raw, _ := json.MarshalIndent(plan, "", "  ")
	raw = append(raw, '\n')
	if err := os.MkdirAll(filepath.Join(config.Directory, "campaign"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config.Directory, "campaign/plan.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(config.Directory, "round-manifest.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := prepareBatchEnrollment(config, nil)(context.Background(), campaign.Artifact{Path: path, SHA256: gate.Digest(raw)}); err == nil {
		t.Fatal("publication failure accepted")
	}
	if _, err := os.Stat(filepath.Join(config.Directory, "round-request.json")); !os.IsNotExist(err) {
		t.Fatal("failed preparation published a request")
	}
}

func TestEnrollmentExampleMatchesCurrentPublications(t *testing.T) {
	base, err := filepath.Abs("../../examples")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(base, "rpcx-l2-batch.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config batchConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	config.Directory = filepath.Join(t.TempDir(), "batch")
	config.Enrollment.Path = filepath.Join(base, config.Enrollment.Path)
	for i := range config.Targets {
		input := &config.Targets[i].Input
		input.Repository.Directory = filepath.Join(base, input.Repository.Directory)
		input.Specification.Path = filepath.Join(base, input.Specification.Path)
		for name, dependency := range input.Dependencies {
			dependency.Directory = filepath.Join(base, dependency.Directory)
			input.Dependencies[name] = dependency
		}
	}
	_, plan, err := loadBatchConfig(writeBatchTestConfig(t, config))
	if err != nil || len(plan.Targets) != 3 {
		t.Fatalf("example enrollment is stale or invalid: %v", err)
	}
}
