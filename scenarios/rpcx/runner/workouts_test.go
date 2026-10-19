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
	"github.com/secacy/agent-harness/scenarios/rpcx/workouts"
)

func workoutsConfig(t *testing.T) Config {
	t.Helper()
	config := configForTest(t, modelFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
		t.Fatal("failed baseline reached model")
		return agent.Reply{}, nil
	}))
	target, err := TargetFromWorkouts("workouts", filepath.Join(config.ScenarioDir, "l2/data/rpcx-l2-workouts-v1/fixtures"), filepath.Join(config.ScenarioDir, "workouts/specs/v0.5.0.md"))
	if err != nil {
		t.Fatal(err)
	}
	config.Target = &target
	config.ScenarioDir = ""
	config.Timeout = 10 * time.Minute
	return config
}

func TestWorkoutsProfileAndBaselineIsolation(t *testing.T) {
	config := workoutsConfig(t)
	p, err := lookupProfile(ProfileWorkouts)
	if err != nil {
		t.Fatal(err)
	}
	if p.hash() != "0f792983fa48a9b0ff65ddb182ca0ce56d1c580de2937eef13c9498e8bdb8318" {
		t.Fatal("published Workouts profile material changed:", p.hash())
	}
	if err = p.validate(*config.Target); err != nil {
		t.Fatal(err)
	}
	if _, _, err = readTarget(context.Background(), *config.Target, p); err != nil {
		t.Fatal(err)
	}
	required, err := workouts.RequiredTests()
	if err != nil || len(required) != 37 {
		t.Fatal("Workouts inventory", err)
	}
	if _, err = baselineEvents(passedBaselineEvents(p), required); err != nil {
		t.Fatal(err)
	}
	p.HostMaterialsSHA256 = strings.Repeat("0", 64)
	if err = p.validate(*config.Target); err == nil {
		t.Fatal("wrong host materials accepted")
	}

	for _, mode := range []string{"common-build-failure", "missing-common-events", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			config := workoutsConfig(t)
			config.Backend = baselineBackend{run: func(_ context.Context, req execution.Request) (execution.Result, error) {
				if filepath.Base(req.Directory) != "common" || len(req.ReadOnlyDirs) != 4 || !strings.Contains(strings.Join(req.Args, " "), "GOMODCACHE=") {
					t.Fatalf("invalid common prerequisite: %+v", req)
				}
				if mode == "cancelled" {
					return execution.Result{ExitCode: -1}, context.Canceled
				}
				if mode == "common-build-failure" {
					return execution.Result{ExitCode: 1}, nil
				}
				return execution.Result{ExitCode: 0}, nil
			}}
			record, err := Run(context.Background(), config)
			if err == nil || record.Verdict != "" || record.AgentExitReason != "" {
				t.Fatalf("invalid prerequisite admitted model: %+v %v", record, err)
			}
			if mode != "cancelled" && record.FailureCode != "baseline_failed" {
				t.Fatalf("wrong failure: %+v", record)
			}
		})
	}

}

// 此处的模型仅重放宿主预先编写的编辑，用于验证 Runner 交付链路。
func TestRunWorkoutsDelivery(t *testing.T) {
	if os.Getenv("HARNESS_WORKOUTS_ACCEPTANCE") != "1" {
		t.Skip("set HARNESS_WORKOUTS_ACCEPTANCE=1 for Workouts delivery")
	}
	config := workoutsConfig(t)
	if os.Getenv("HARNESS_WORKOUTS_BACKEND") == "docker" {
		backend, err := execution.NewDocker(context.Background(), execution.DockerConfig{})
		if err != nil {
			t.Fatal(err)
		}
		config.Backend = backend
		config.TrustedLocal = false
	}
	if output := os.Getenv("HARNESS_WORKOUTS_OUTPUT"); output != "" {
		var err error
		config.RunsDir, err = filepath.Abs(output)
		if err != nil {
			t.Fatal(err)
		}
	}
	layout := t.TempDir()
	p, _ := lookupProfile(ProfileWorkouts)
	fixtures, _, err := readTarget(context.Background(), *config.Target, p)
	if err != nil {
		t.Fatal(err)
	}
	if err = fixtures.write(context.Background(), layout); err != nil {
		t.Fatal(err)
	}
	patch, err := filepath.Abs("../workouts/cases/patches/correct.patch")
	if err != nil {
		t.Fatal(err)
	}
	applyPatch(t, filepath.Join(layout, "trainings"), patch)
	before, after := fixtures.module("trainings"), mustReadTree(t, filepath.Join(layout, "trainings"))
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
			if !strings.Contains(messages[1].Content, "v0.5.0") {
				t.Fatal("missing Workouts specification")
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
					if strings.Join(schema.Properties.Root.Enum, ",") != "common,rpcx-v1,rpcx-v2,spec,workspace" {
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
	t.Log("WORKOUTS_RUN", record.Directory)
	report := readJSON[Report](t, filepath.Join(record.Directory, "artifacts/report.json"))
	if report.Validation.VerifierVersion != workouts.VerifierVersion || record.CandidateSHA256 != after.hash() || report.Input.ProfileSHA256 != p.hash() || len(report.Baseline.ModuleTests["common"]) != 3 || len(report.Baseline.Tests) != 37 {
		t.Fatal("Workouts evidence binding mismatch")
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
