package runner

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/execution"
)

func TestGenerationBaselineComparisonIsReadOnly(t *testing.T) {
	config := configForTest(t, nil)
	before := mustReadTree(t, filepath.Join(config.ScenarioDir, "fixtures"))
	p, err := lookupProfile(ProfileT1T2)
	if err != nil {
		t.Fatal(err)
	}
	checkedCandidate := false
	// 基线事件由替身提供；本测试验证工具读写和执行挂载，不宣称运行了真实构建。
	config.Backend = baselineBackend{run: func(_ context.Context, req execution.Request) (execution.Result, error) {
		if strings.Contains(filepath.ToSlash(req.Directory), "/workspace/") {
			checkedCandidate = true
			workspace := filepath.Dir(req.Directory)
			want := []string{req.Directory, filepath.Join(workspace, "rpcx-v1"), filepath.Join(workspace, "rpcx-v2")}
			if !reflect.DeepEqual(req.ReadOnlyDirs, want) {
				t.Fatalf("candidate command mounts: got %v, want %v", req.ReadOnlyDirs, want)
			}
			return execution.Result{ExitCode: 0}, nil
		}
		return execution.Result{ExitCode: 0, Stdout: passedBaselineEvents(p)}, nil
	}}
	const marker = "// 候选对照标记。"
	stop := errors.New("stop after baseline comparison")
	step := 0
	config.Model = modelFunc(func(_ context.Context, messages []agent.Message, _ []agent.ToolSpec) (agent.Reply, error) {
		step++
		switch step {
		case 1:
			for _, instruction := range []string{"baseline root", "compare changed tests with baseline", "each RPC boundary", "Passing build/test checks does not establish"} {
				if !strings.Contains(messages[0].Content, instruction) {
					t.Fatalf("missing generation instruction: %s", instruction)
				}
			}
			return agent.Reply{Finish: agent.FinishToolCalls, ToolCalls: []agent.ToolCall{
				replacement(t, "edit", "internal/inventory/client.go", []byte("package inventory"), []byte("package inventory\n\n"+marker)),
				{ID: "original", Name: "read_file", Arguments: `{"root":"baseline","path":"internal/inventory/client.go"}`},
				{ID: "candidate", Name: "read_file", Arguments: `{"root":"workspace","path":"internal/inventory/client.go"}`},
				{ID: "list", Name: "list_files", Arguments: `{"root":"baseline","path":"."}`},
				{ID: "search", Name: "search", Arguments: `{"root":"baseline","path":"internal/inventory","text":"rpcx.NewClient"}`},
				{ID: "deny-parent", Name: "read_file", Arguments: `{"root":"baseline","path":"../../inputs/profile.json"}`},
				{ID: "deny-oracle", Name: "read_file", Arguments: `{"root":"oracle","path":"reference.json"}`},
				{ID: "deny-root-write", Name: "replace_text", Arguments: `{"root":"baseline","path":"internal/inventory/client.go","old":"package inventory","new":"package changed"}`},
				{ID: "deny-escape-write", Name: "replace_text", Arguments: `{"path":"../../baseline/demo-orders/internal/inventory/client.go","old":"package inventory","new":"package changed"}`},
			}}, nil
		case 2:
			results := map[string]agent.Message{}
			for _, message := range messages {
				if message.Role != agent.RoleTool {
					continue
				}
				results[message.ToolCallID] = message
				if message.IsError != strings.HasPrefix(message.ToolCallID, "deny-") {
					t.Fatalf("unexpected tool result: %+v", message)
				}
			}
			if len(results) != 9 || !strings.Contains(results["original"].Content, "package inventory") || strings.Contains(results["original"].Content, marker) || !strings.Contains(results["candidate"].Content, marker) {
				t.Fatalf("baseline and candidate are not independently readable: %+v", results)
			}
			if !strings.Contains(results["list"].Content, "go.mod") || !strings.Contains(results["search"].Content, "rpcx.NewClient") {
				t.Fatal("baseline listing or search did not return original files")
			}
			return agent.Reply{Finish: agent.FinishToolCalls, ToolCalls: []agent.ToolCall{
				{ID: "build", Name: "go_check", Arguments: `{"kind":"build"}`},
			}}, nil
		case 3:
			last := messages[len(messages)-2]
			if last.ToolCallID != "build" || last.IsError || !checkedCandidate {
				t.Fatalf("candidate command was not checked: %+v", last)
			}
			return agent.Reply{}, stop
		default:
			t.Fatal("unexpected model call")
			return agent.Reply{}, stop
		}
	})
	record, err := Run(context.Background(), config)
	if !errors.Is(err, stop) || step != 3 || record.Status != StatusFailed || record.Verdict != "" {
		t.Fatalf("unexpected run: %+v, err=%v, calls=%d", record, err, step)
	}
	if after := mustReadTree(t, filepath.Join(config.ScenarioDir, "fixtures")); after.hash() != before.hash() {
		t.Fatal("original fixtures changed")
	}
	if baseline := mustReadTree(t, filepath.Join(record.Directory, "baseline")); baseline.hash() != before.hash() {
		t.Fatal("prepared baseline changed")
	}
	report := readJSON[Report](t, filepath.Join(record.Directory, "artifacts/report.json"))
	if report.Validation != nil {
		t.Fatal("generation self-review unexpectedly produced independent validation")
	}
}
