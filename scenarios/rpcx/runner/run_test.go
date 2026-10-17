package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"example.com/agent-harness/internal/agent"
)

type modelFunc func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error)

func (f modelFunc) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolSpec) (agent.Reply, error) {
	return f(ctx, messages, tools)
}

func configForTest(t *testing.T, model agent.Model) Config {
	t.Helper()
	scenario, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	scenario, err = filepath.EvalSymlinks(scenario)
	if err != nil {
		t.Fatal(err)
	}
	return Config{
		ScenarioDir: scenario, RunsDir: filepath.Join(t.TempDir(), "runs"),
		Model: model, ModelName: "scripted-test-model", Timeout: 2 * time.Minute,
		Limits: agent.Limits{MaxModelCalls: 5, MaxToolCalls: 30}, TrustedLocal: true,
	}
}

func mustReadTree(t *testing.T, root string) tree {
	t.Helper()
	files, err := readTree(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func readJSON[T any](t *testing.T, path string) T {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func applyPatch(t *testing.T, dir, patch string) {
	t.Helper()
	command := exec.Command("git", "apply", "--", patch)
	command.Dir = dir
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull}
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("apply patch: %s: %v", data, err)
	}
}

// 参考补丁只用于构造测试期望；Runner 和模型可见目录不读取公开用例。
func migratedTree(t *testing.T, scenario string) tree {
	t.Helper()
	baseline := mustReadTree(t, filepath.Join(scenario, "fixtures/demo-orders"))
	dir := t.TempDir()
	if err := baseline.write(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	applyPatch(t, dir, filepath.Join(scenario, "cases/patches/correct.patch"))
	return mustReadTree(t, dir)
}

func replacement(t *testing.T, id, path string, old, new []byte) agent.ToolCall {
	t.Helper()
	data, err := json.Marshal(map[string]string{"path": path, "old": string(old), "new": string(new)})
	if err != nil {
		t.Fatal(err)
	}
	return agent.ToolCall{ID: id, Name: "replace_text", Arguments: string(data)}
}

func TestRunDeliversVerifiedCandidateAndKeepsHistory(t *testing.T) {
	config := configForTest(t, nil)
	before := mustReadTree(t, filepath.Join(config.ScenarioDir, "fixtures"))
	baseline := before.module("demo-orders")
	migrated := migratedTree(t, config.ScenarioDir)
	paths := []string{}
	for path, contents := range migrated {
		if !bytes.Equal(contents, baseline[path]) {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	calls := 0
	config.Model = modelFunc(func(ctx context.Context, messages []agent.Message, tools []agent.ToolSpec) (agent.Reply, error) {
		calls++
		switch calls {
		case 1:
			if !strings.Contains(messages[1].Content, "CTX-002") || strings.Contains(messages[1].Content, "correct.patch") {
				t.Fatal("task did not contain the specification, or leaked a reference patch")
			}
			var schema map[string]any
			for _, tool := range tools {
				if tool.Name == "list_files" {
					if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
						t.Fatal(err)
					}
				}
			}
			roots := schema["properties"].(map[string]any)["root"].(map[string]any)["enum"]
			if !reflect.DeepEqual(roots, []any{"rpcx-v1", "rpcx-v2", "spec", "workspace"}) {
				t.Fatalf("unexpected visible roots: %v", roots)
			}
			entries, err := os.ReadDir(config.RunsDir)
			if err != nil || len(entries) != 1 {
				t.Fatalf("missing run: %v %v", entries, err)
			}
			record := readJSON[Record](t, filepath.Join(config.RunsDir, entries[0].Name(), "run.json"))
			if record.Status != StatusRunning || record.Phase != PhaseGenerating || record.FinishedAt != nil {
				t.Fatalf("generation was not persisted before model call: %+v", record)
			}
			return agent.Reply{Finish: agent.FinishToolCalls, ToolCalls: []agent.ToolCall{
				{ID: "spec", Name: "list_files", Arguments: `{"root":"spec","path":"."}`},
				{ID: "forbidden", Name: "replace_text", Arguments: `{"path":"internal/pb/orders.pb.go","old":"package pb","new":"package changed"}`},
			}}, nil
		case 2:
			if last := messages[len(messages)-1]; !last.IsError || !strings.Contains(last.Content, "protected path") {
				t.Fatalf("missing path protection feedback: %+v", last)
			}
			if text := messages[len(messages)-2].Content; !strings.Contains(text, "spec.md") || strings.Contains(text, "task.json") {
				t.Fatalf("unexpected spec root: %s", text)
			}
			var edits []agent.ToolCall
			for i, path := range paths {
				edits = append(edits, replacement(t, fmt.Sprint(i), path, baseline[path], migrated[path]))
			}
			edits = append(edits, agent.ToolCall{ID: "build", Name: "go_check", Arguments: `{"kind":"build"}`})
			return agent.Reply{Finish: agent.FinishToolCalls, ToolCalls: edits, Usage: &agent.Usage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150}}, nil
		case 3:
			if last := messages[len(messages)-1]; last.IsError || !strings.Contains(last.Content, "go build passed") {
				t.Fatalf("build feedback: %+v", last)
			}
			return agent.Reply{Finish: agent.FinishStop, Content: "Migration submitted."}, nil
		default:
			t.Fatal("unexpected model call")
			return agent.Reply{}, nil
		}
	})
	first, err := Run(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != StatusCompleted || first.Verdict != "ReadyForReview" || first.Phase != PhasePublishing || first.FinishedAt == nil {
		t.Fatalf("unexpected run: %+v", first)
	}
	report := readJSON[Report](t, filepath.Join(first.Directory, "artifacts/report.json"))
	if report.Validation == nil || report.Validation.Verdict != first.Verdict || report.CandidateSHA256 != migrated.hash() || report.Validation.CandidateSHA256 != first.CandidateSHA256 {
		t.Fatalf("report does not bind the candidate: %+v", report)
	}
	if report.Input.BaselineSHA256 != baseline.hash() || report.Input.ExecutionMode != "trusted_local" || report.Input.SpecSHA256 == "" {
		t.Fatalf("missing input provenance: %+v", report.Input)
	}
	for name, check := range report.Validation.Checks {
		if check.Status != "pass" {
			t.Fatalf("%s: %+v", name, check)
		}
	}
	trace := readJSON[agent.Result](t, filepath.Join(first.Directory, "artifacts/agent.json"))
	if trace.ModelCalls != 3 || len(trace.Usage) != 3 || trace.Usage[1].TotalTokens != 150 || trace.Usage[0] != nil {
		t.Fatalf("unexpected agent trace: %+v", trace)
	}
	if _, err := os.Stat(filepath.Join(first.Directory, "workspace/demo-orders/.git")); !os.IsNotExist(err) {
		t.Fatal("patch export created Git metadata inside the candidate")
	}
	replay := t.TempDir()
	if err := baseline.write(context.Background(), replay); err != nil {
		t.Fatal(err)
	}
	applyPatch(t, replay, filepath.Join(first.Directory, "artifacts/candidate.patch"))
	if got := mustReadTree(t, replay).hash(); got != first.CandidateSHA256 {
		t.Fatalf("patch round trip: got %s, want %s", got, first.CandidateSHA256)
	}
	firstArtifacts := mustReadTree(t, filepath.Join(first.Directory, "artifacts")).hash()
	// 正常提交但未迁移的候选应被门禁拒绝，而非成为运行器异常。
	config.Model = modelFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
		return agent.Reply{Finish: agent.FinishStop, Content: "Submitted unchanged."}, nil
	})
	second, err := Run(context.Background(), config)
	if err != nil || second.Status != StatusCompleted || second.Verdict != "Rejected" || second.ID == first.ID {
		t.Fatalf("rejected candidate / new run: %+v %v", second, err)
	}
	if mustReadTree(t, filepath.Join(first.Directory, "artifacts")).hash() != firstArtifacts || mustReadTree(t, filepath.Join(config.ScenarioDir, "fixtures")).hash() != before.hash() {
		t.Fatal("second run changed previous artifacts or source fixtures")
	}
}

func TestRunKeepsPartialCandidateOnGenerationFailure(t *testing.T) {
	for _, mode := range []string{"api-error", "budget", "cancel", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			config := configForTest(t, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New("model service unavailable")
			calls := 0
			config.Model = modelFunc(func(ctx context.Context, _ []agent.Message, _ []agent.ToolSpec) (agent.Reply, error) {
				calls++
				if calls == 1 {
					return agent.Reply{Finish: agent.FinishToolCalls, ToolCalls: []agent.ToolCall{
						replacement(t, "edit", "go.mod", []byte("require example.com/rpcx v1.0.0"), []byte("require example.com/rpcx/v2 v2.0.0")),
					}}, nil
				}
				switch mode {
				case "api-error":
					return agent.Reply{}, failure
				case "cancel":
					cancel()
				case "timeout":
					<-ctx.Done()
				default:
					t.Fatal("model called after budget exhaustion")
				}
				return agent.Reply{}, ctx.Err()
			})
			if mode == "budget" {
				config.Limits.MaxModelCalls = 1
			}
			if mode == "timeout" {
				config.Timeout = 100 * time.Millisecond
			}
			record, err := Run(ctx, config)
			wantStatus, wantReason, wantError := StatusFailed, agent.ExitFailed, failure
			switch mode {
			case "budget":
				wantReason, wantError = agent.ExitLimitReached, ErrAgentIncomplete
			case "cancel":
				wantStatus, wantReason, wantError = StatusCancelled, agent.ExitCancelled, context.Canceled
			case "timeout":
				wantReason, wantError = agent.ExitDeadlineExceeded, context.DeadlineExceeded
			}
			if !errors.Is(err, wantError) || record.Status != wantStatus || record.AgentExitReason != wantReason || record.Verdict != "" {
				t.Fatalf("unexpected failure: %+v %v", record, err)
			}
			saved := readJSON[Record](t, filepath.Join(record.Directory, "run.json"))
			if saved.Status != record.Status || saved.Error == "" || saved.FinishedAt == nil {
				t.Fatalf("missing terminal record: %+v", saved)
			}
			report := readJSON[Report](t, filepath.Join(record.Directory, "artifacts/report.json"))
			if report.Validation != nil || report.Error == "" || report.AgentRecord == "" {
				t.Fatalf("failed generation was validated or lost evidence: %+v", report)
			}
			patch, err := os.ReadFile(filepath.Join(record.Directory, "artifacts/candidate.patch"))
			if err != nil || !bytes.Contains(patch, []byte("+require example.com/rpcx/v2 v2.0.0")) {
				t.Fatalf("missing partial patch: %s %v", patch, err)
			}
			if _, err := os.Stat(filepath.Join(record.Directory, "evidence")); !os.IsNotExist(err) {
				t.Fatal("failed generation started independent validation")
			}
		})
	}
}

func TestRunRejectsUnsafeInputsBeforeModelCall(t *testing.T) {
	for _, mode := range []string{"local-not-enabled", "overlap", "symlink-overlap", "specification", "fixture-symlink", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			config := configForTest(t, modelFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
				t.Fatal("invalid task reached the model")
				return agent.Reply{}, nil
			}))
			ctx := context.Background()
			switch mode {
			case "local-not-enabled":
				config.TrustedLocal = false
			case "overlap":
				config.RunsDir = filepath.Join(config.ScenarioDir, "fixtures/output")
			case "symlink-overlap":
				alias := filepath.Join(t.TempDir(), "alias")
				if err := os.Symlink(config.ScenarioDir, alias); err != nil {
					t.Fatal(err)
				}
				config.RunsDir = filepath.Join(alias, "new/output")
			case "specification", "fixture-symlink":
				copy := mustReadTree(t, filepath.Join(config.ScenarioDir, "fixtures"))
				config.ScenarioDir = t.TempDir()
				if err := copy.write(ctx, filepath.Join(config.ScenarioDir, "fixtures")); err != nil {
					t.Fatal(err)
				}
				if mode == "fixture-symlink" {
					if err := os.Symlink(os.DevNull, filepath.Join(config.ScenarioDir, "fixtures/demo-orders/link")); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.MkdirAll(filepath.Join(config.ScenarioDir, "specs"), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(config.ScenarioDir, "specs/v0.1.0.md"), []byte("modified specification"), 0644); err != nil {
						t.Fatal(err)
					}
				}
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			record, err := Run(ctx, config)
			if err == nil || record.Directory != "" || record.Verdict != "" {
				t.Fatalf("invalid task created a run: %+v %v", record, err)
			}
		})
	}
}

func TestRunDoesNotPublishSuccessWhenEvidenceCannotBeWritten(t *testing.T) {
	config := configForTest(t, nil)
	config.Model = modelFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
		entries, err := os.ReadDir(config.RunsDir)
		if err != nil || len(entries) != 1 {
			t.Fatalf("run directory: %v %v", entries, err)
		}
		// 测试注入落盘故障；模型工具本身没有访问 artifacts 的能力。
		if err := os.Mkdir(filepath.Join(config.RunsDir, entries[0].Name(), "artifacts/report.json"), 0755); err != nil {
			t.Fatal(err)
		}
		return agent.Reply{Finish: agent.FinishStop, Content: "Submitted."}, nil
	})
	record, err := Run(context.Background(), config)
	if err == nil || !strings.Contains(err.Error(), "publish report") || record.Status != StatusFailed || record.Verdict != "" {
		t.Fatalf("publishing failure reported success: %+v %v", record, err)
	}
	saved := readJSON[Record](t, filepath.Join(record.Directory, "run.json"))
	if saved.Status != StatusFailed || saved.Verdict != "" || !strings.Contains(saved.Error, "publish report") {
		t.Fatalf("publishing failure was not persisted: %+v", saved)
	}
}

func TestRunRejectsChangedBaselineBeforeValidation(t *testing.T) {
	for _, target := range []string{"baseline/demo-orders/go.mod", "workspace/rpcx-v2/go.mod", "inputs/spec/spec.md"} {
		t.Run(target, func(t *testing.T) {
			config := configForTest(t, nil)
			config.Model = modelFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
				entries, err := os.ReadDir(config.RunsDir)
				if err != nil || len(entries) != 1 {
					t.Fatalf("missing run: %v %v", entries, err)
				}
				// 测试模拟工具之外的并发修改，检查受信输入失效时不会继续验收。
				if err := os.WriteFile(filepath.Join(config.RunsDir, entries[0].Name(), target), []byte("changed"), 0644); err != nil {
					t.Fatal(err)
				}
				return agent.Reply{Finish: agent.FinishStop}, nil
			})
			record, err := Run(context.Background(), config)
			if err == nil || !strings.Contains(err.Error(), "changed during execution") || record.Status != StatusFailed || record.Verdict != "" {
				t.Fatalf("changed trusted input was accepted: %+v %v", record, err)
			}
			report := readJSON[Report](t, filepath.Join(record.Directory, "artifacts/report.json"))
			if report.Validation != nil {
				t.Fatal("changed baseline reached validation")
			}
		})
	}
}

func TestRunReturnsErrorWhenTerminalStateCannotBeWritten(t *testing.T) {
	config := configForTest(t, nil)
	config.Model = modelFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
		entries, err := os.ReadDir(config.RunsDir)
		if err != nil || len(entries) != 1 {
			t.Fatalf("missing run: %v %v", entries, err)
		}
		path := filepath.Join(config.RunsDir, entries[0].Name(), "run.json")
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0755); err != nil {
			t.Fatal(err)
		}
		return agent.Reply{Finish: agent.FinishStop}, nil
	})
	record, err := Run(context.Background(), config)
	if err == nil || !strings.Contains(err.Error(), "persist terminal state") || record.Status != StatusFailed || record.Verdict != "" {
		t.Fatalf("terminal state failure was hidden: %+v %v", record, err)
	}
	report := readJSON[Report](t, filepath.Join(record.Directory, "artifacts/report.json"))
	if !strings.Contains(report.Error, "persist terminal state") {
		t.Fatalf("report did not retain terminal state failure: %+v", report)
	}
}

func TestRunPreservesFailedBaselineEvidence(t *testing.T) {
	config := configForTest(t, modelFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
		return agent.Reply{Finish: agent.FinishStop}, nil
	}))
	fixtures := mustReadTree(t, filepath.Join(config.ScenarioDir, "fixtures"))
	spec, err := os.ReadFile(filepath.Join(config.ScenarioDir, "specs/v0.1.0.md"))
	if err != nil {
		t.Fatal(err)
	}
	config.ScenarioDir = t.TempDir()
	fixtures["demo-orders/internal/orders/broken.go"] = []byte("package orders\nvar _ = missingBaselineSymbol\n")
	if err := fixtures.write(context.Background(), filepath.Join(config.ScenarioDir, "fixtures")); err != nil {
		t.Fatal(err)
	}
	if err := (tree{"v0.1.0.md": spec}).write(context.Background(), filepath.Join(config.ScenarioDir, "specs")); err != nil {
		t.Fatal(err)
	}
	record, err := Run(context.Background(), config)
	if err == nil || record.Status != StatusFailed || record.Verdict != "" || record.Phase != PhaseValidating || !strings.Contains(err.Error(), "baseline tests failed") {
		t.Fatalf("broken baseline was treated as a candidate verdict: %+v %v", record, err)
	}
	report := readJSON[Report](t, filepath.Join(record.Directory, "artifacts/report.json"))
	if report.Validation == nil || report.Validation.Verdict != "" || len(report.Validation.Commands) != 1 || report.Validation.Commands[0].ExitCode == 0 {
		t.Fatalf("missing baseline failure evidence: %+v", report)
	}
}

func TestRunCancellationDuringValidationPersistsEvidence(t *testing.T) {
	config := configForTest(t, modelFunc(func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
		return agent.Reply{Finish: agent.FinishStop}, nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		record Record
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		record, err := Run(ctx, config)
		done <- outcome{record, err}
	}()
	// 等待验证器创建自己的执行副本，再触发取消。
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		matches, err := filepath.Glob(filepath.Join(config.RunsDir, "rpcx-agent-*/evidence/baseline/demo-orders"))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 0 {
			break
		}
		select {
		case out := <-done:
			t.Fatalf("run exited before validation: %+v", out)
		case <-deadline.C:
			t.Fatal("validation did not start")
		case <-ticker.C:
		}
	}
	cancel()
	select {
	case out := <-done:
		if !errors.Is(out.err, context.Canceled) || out.record.Status != StatusCancelled || out.record.Verdict != "" {
			t.Fatalf("validation cancellation: %+v", out)
		}
		report := readJSON[Report](t, filepath.Join(out.record.Directory, "artifacts/report.json"))
		if report.Validation == nil || report.Validation.Verdict != "" || report.Error == "" || report.PatchSHA256 == "" {
			t.Fatalf("cancellation lost partial evidence: %+v", report)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("runner did not finish cancellation and bounded cleanup")
	}
}

func TestExportPatchRoundTripsAllFileChanges(t *testing.T) {
	baseline := tree{"modified.txt": []byte("before\n"), "deleted.txt": []byte("delete me\n"), "unchanged.txt": []byte("same\n")}
	candidate := tree{
		"modified.txt": []byte("after without newline"), "unchanged.txt": []byte("same\n"),
		"nested/new file.txt": []byte("new\n"), "binary.dat": {0, 1, 2, 255}, "empty": {},
	}
	patch, err := exportPatch(context.Background(), t.TempDir(), baseline, candidate)
	if err != nil {
		t.Fatal(err)
	}
	patchPath := filepath.Join(t.TempDir(), "candidate.patch")
	if err := os.WriteFile(patchPath, patch, 0644); err != nil {
		t.Fatal(err)
	}
	replay := t.TempDir()
	if err := baseline.write(context.Background(), replay); err != nil {
		t.Fatal(err)
	}
	applyPatch(t, replay, patchPath)
	if got := mustReadTree(t, replay).hash(); got != candidate.hash() {
		t.Fatalf("patch failed to reproduce candidate: %s", patch)
	}
	empty, err := exportPatch(context.Background(), t.TempDir(), baseline, baseline)
	if err != nil || len(empty) != 0 {
		t.Fatalf("unchanged candidate: %s %v", empty, err)
	}
}
