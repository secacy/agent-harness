package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/translation"
)

func translationConfig(t *testing.T) Config {
	t.Helper()
	config := configForTest(t, modelFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
		t.Fatal("failed baseline reached model")
		return agent.Reply{}, nil
	}))
	target, err := TargetFromTranslation("translation", filepath.Join(config.ScenarioDir, "l2/data/rpcx-l2-translation-v1/fixtures"), filepath.Join(config.ScenarioDir, "translation/specs/v0.4.0.md"))
	if err != nil {
		t.Fatal(err)
	}
	config.Target = &target
	config.ScenarioDir = ""
	config.Timeout = 10 * time.Minute
	return config
}

func TestTranslationProfileAndBaselineIsolation(t *testing.T) {
	config := translationConfig(t)
	p, err := lookupProfile(ProfileTranslation)
	if err != nil {
		t.Fatal(err)
	}
	if p.hash() != "4d71ed028b28ebe7a66ca1e2bc10a328d4990ebd5bd606eb35db50a8ec7e3279" {
		t.Fatal("published Translation profile material changed:", p.hash())
	}
	if err = p.validate(*config.Target); err != nil {
		t.Fatal(err)
	}
	if _, _, err = readTarget(context.Background(), *config.Target, p); err != nil {
		t.Fatal(err)
	}
	required, err := translation.RequiredTests()
	if err != nil || len(required) != 26 {
		t.Fatal("Translation inventory", err)
	}
	if _, err = baselineEvents(passedBaselineEvents(p), required); err != nil {
		t.Fatal(err)
	}
	p.HostMaterialsSHA256 = strings.Repeat("0", 64)
	if err = p.validate(*config.Target); err == nil {
		t.Fatal("wrong host materials accepted")
	}

	config.Backend = baselineBackend{run: func(_ context.Context, req execution.Request) (execution.Result, error) {
		if !strings.Contains(strings.Join(req.Args, " "), "GOMODCACHE=") || len(req.ReadOnlyDirs) != 4 {
			t.Fatalf("host environment missing: %+v", req)
		}
		if slices.Contains(req.Args, "-tags=rpcx_alternate") {
			return execution.Result{ExitCode: 1, Stderr: []byte("injected alternate baseline failure")}, nil
		}
		if slices.Contains(req.Args, "list") {
			data, _ := json.Marshal(map[string]any{"ImportPath": "github.com/evrone/go-clean-template/internal/repo/webapi", "GoFiles": []string{"translation_google.go"}, "IgnoredGoFiles": []string{"translation_rpcx_alternate.go"}})
			mocks, _ := json.Marshal(map[string]any{"ImportPath": "github.com/evrone/go-clean-template/internal/usecase", "XTestGoFiles": []string{"translation_test.go", "mocks_repo_test.go"}})
			return execution.Result{ExitCode: 0, Stdout: append(append(data, '\n'), mocks...)}, nil
		}
		if slices.Contains(req.Args, "test") {
			return execution.Result{ExitCode: 0, Stdout: passedBaselineEvents(p)}, nil
		}
		return execution.Result{ExitCode: 0}, nil
	}}
	record, err := Run(context.Background(), config)
	if err == nil || record.Status != StatusFailed || record.FailureCode != "baseline_failed" || record.Verdict != "" {
		t.Fatalf("%+v %v", record, err)
	}
}

// 此处的模型仅重放宿主预先编写的编辑，用于验证 Runner 交付链路。
func TestRunTranslationDelivery(t *testing.T) {
	if os.Getenv("HARNESS_TRANSLATION_ACCEPTANCE") != "1" {
		t.Skip("set HARNESS_TRANSLATION_ACCEPTANCE=1 for Translation delivery")
	}
	config := translationConfig(t)
	if os.Getenv("HARNESS_TRANSLATION_BACKEND") == "docker" {
		backend, err := execution.NewDocker(context.Background(), execution.DockerConfig{})
		if err != nil {
			t.Fatal(err)
		}
		config.Backend = backend
		config.TrustedLocal = false
	}
	if output := os.Getenv("HARNESS_TRANSLATION_OUTPUT"); output != "" {
		var err error
		config.RunsDir, err = filepath.Abs(output)
		if err != nil {
			t.Fatal(err)
		}
	}
	layout := t.TempDir()
	p, _ := lookupProfile(ProfileTranslation)
	fixtures, _, err := readTarget(context.Background(), *config.Target, p)
	if err != nil {
		t.Fatal(err)
	}
	if err = fixtures.write(context.Background(), layout); err != nil {
		t.Fatal(err)
	}
	patch, err := filepath.Abs("../translation/cases/patches/correct.patch")
	if err != nil {
		t.Fatal(err)
	}
	applyPatch(t, filepath.Join(layout, "translation"), patch)
	before, after := fixtures.module("translation"), mustReadTree(t, filepath.Join(layout, "translation"))
	calls := 0
	config.Model = modelFunc(func(_ context.Context, messages []agent.Message, tools []agent.ToolSpec) (agent.Reply, error) {
		calls++
		for _, m := range messages {
			if m.Role == agent.RoleTool && m.IsError {
				t.Fatalf("tool error: %s", m.Content)
			}
		}
		switch calls {
		case 1:
			if !strings.Contains(messages[1].Content, "v0.4.0") {
				t.Fatal("missing Translation specification")
			}
			for _, tool := range tools {
				if tool.Name == "list_files" {
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
					if strings.Join(schema.Properties.Root.Enum, ",") != "rpcx-v1,rpcx-v2,spec,workspace" {
						t.Fatal("host material exposed to Agent")
					}
				}
			}
			var edits []agent.ToolCall
			for _, path := range sortedKeys(after) {
				if !bytes.Equal(before[path], after[path]) {
					edits = append(edits, replacement(t, fmt.Sprint(len(edits)), path, before[path], after[path]))
				}
			}
			return agent.Reply{Finish: agent.FinishToolCalls, ToolCalls: edits}, nil
		case 2:
			return agent.Reply{Finish: agent.FinishToolCalls, ToolCalls: []agent.ToolCall{{ID: "build", Name: "go_check", Arguments: `{"kind":"build"}`}, {ID: "test", Name: "go_check", Arguments: `{"kind":"test"}`}}}, nil
		default:
			for _, message := range messages {
				if message.Role == agent.RoleTool && (message.ToolCallID == "build" || message.ToolCallID == "test") && (!strings.Contains(message.Content, "configuration default") || !strings.Contains(message.Content, "configuration rpcx_alternate")) {
					t.Fatal("tool feedback omitted a required configuration")
				}
			}
			return agent.Reply{Finish: agent.FinishStop, Content: "Scripted migration submitted; independent verification remains required."}, nil
		}
	})
	record, err := Run(context.Background(), config)
	if err != nil || record.Status != StatusCompleted || record.Verdict != "ReadyForReview" {
		t.Fatalf("%+v %v", record, err)
	}
	t.Log("TRANSLATION_RUN", record.Directory)
	report := readJSON[Report](t, filepath.Join(record.Directory, "artifacts/report.json"))
	if report.Validation.VerifierVersion != translation.VerifierVersion || record.CandidateSHA256 != after.hash() || report.Input.ProfileSHA256 != p.hash() || len(report.Baseline.ConfigurationTests) != 2 {
		t.Fatal("Translation evidence binding mismatch")
	}
	replay := t.TempDir()
	if err = before.write(context.Background(), replay); err != nil {
		t.Fatal(err)
	}
	applyPatch(t, replay, filepath.Join(record.Directory, "artifacts/candidate.patch"))
	if mustReadTree(t, replay).hash() != after.hash() {
		t.Fatal("exported patch did not replay")
	}
	review, err := os.ReadFile(filepath.Join(record.Directory, "artifacts/review.md"))
	if err != nil {
		t.Fatal(err)
	}
	assertReviewLinks(t, record.Directory, review)
	if !strings.Contains(string(review), "TIME-001") || !strings.Contains(string(review), "ReadyForReview") {
		t.Fatal("missing review decision")
	}
}
