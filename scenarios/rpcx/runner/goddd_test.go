package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/goddd"
)

func godddConfig(t *testing.T) Config {
	t.Helper()
	config := configForTest(t, modelFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
		t.Fatal("failed baseline reached model")
		return agent.Reply{}, nil
	}))
	target, err := TargetFromGoddd("goddd", filepath.Join(config.ScenarioDir, "l2/data/rpcx-l2-goddd-v1/fixtures"), filepath.Join(config.ScenarioDir, "goddd/specs/v0.3.0.md"))
	if err != nil {
		t.Fatal(err)
	}
	config.Target = &target
	config.ScenarioDir = ""
	config.Timeout = 10 * time.Minute
	return config
}

func TestGodddProfileAndBaselineIsolation(t *testing.T) {
	config := godddConfig(t)
	p, err := lookupProfile(ProfileGoddd)
	if err != nil {
		t.Fatal(err)
	}
	if p.hash() != "c987645fd869fc785081b32ee138102f37f8967ff0b544b742666faf23476544" {
		t.Fatal("published Goddd profile changed")
	}
	if err = p.validate(*config.Target); err != nil {
		t.Fatal(err)
	}
	if _, _, err = readTarget(context.Background(), *config.Target, p); err != nil {
		t.Fatal(err)
	}
	required, err := goddd.RequiredTests()
	if err != nil || len(required) != 22 {
		t.Fatal("Goddd inventory", err)
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
		return execution.Result{ExitCode: 1, Stderr: []byte("injected baseline failure")}, nil
	}}
	record, err := Run(context.Background(), config)
	if err == nil || record.Status != StatusFailed || record.FailureCode != "baseline_failed" || record.Verdict != "" {
		t.Fatalf("%+v %v", record, err)
	}
}

// 此处的模型仅重放宿主预先编写的编辑，用于验证 Runner 交付链路。
func TestRunGodddDelivery(t *testing.T) {
	if os.Getenv("HARNESS_GODDD_ACCEPTANCE") != "1" {
		t.Skip("set HARNESS_GODDD_ACCEPTANCE=1 for Goddd delivery")
	}
	config := godddConfig(t)
	if os.Getenv("HARNESS_GODDD_BACKEND") == "docker" {
		backend, err := execution.NewDocker(context.Background(), execution.DockerConfig{})
		if err != nil {
			t.Fatal(err)
		}
		config.Backend = backend
		config.TrustedLocal = false
	}
	if output := os.Getenv("HARNESS_GODDD_OUTPUT"); output != "" {
		var err error
		config.RunsDir, err = filepath.Abs(output)
		if err != nil {
			t.Fatal(err)
		}
	}
	layout := t.TempDir()
	p, _ := lookupProfile(ProfileGoddd)
	fixtures, _, err := readTarget(context.Background(), *config.Target, p)
	if err != nil {
		t.Fatal(err)
	}
	if err = fixtures.write(context.Background(), layout); err != nil {
		t.Fatal(err)
	}
	patch, err := filepath.Abs("../goddd/cases/patches/correct.patch")
	if err != nil {
		t.Fatal(err)
	}
	applyPatch(t, filepath.Join(layout, "goddd"), patch)
	before, after := fixtures.module("goddd"), mustReadTree(t, filepath.Join(layout, "goddd"))
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
			if !strings.Contains(messages[1].Content, "v0.3.0") {
				t.Fatal("missing Goddd specification")
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
			return agent.Reply{Finish: agent.FinishStop, Content: "Scripted migration submitted; independent verification remains required."}, nil
		}
	})
	record, err := Run(context.Background(), config)
	if err != nil || record.Status != StatusCompleted || record.Verdict != "ReadyForReview" {
		t.Fatalf("%+v %v", record, err)
	}
	t.Log("GODDD_RUN", record.Directory)
	report := readJSON[Report](t, filepath.Join(record.Directory, "artifacts/report.json"))
	if report.Validation.VerifierVersion != goddd.VerifierVersion || record.CandidateSHA256 != after.hash() || report.Input.ProfileSHA256 != p.hash() {
		t.Fatal("Goddd evidence binding mismatch")
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
