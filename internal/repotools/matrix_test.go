package repotools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/task"
)

type matrixBackend struct {
	calls       [][]string
	cancel      context.CancelFunc
	failDefault bool
}

func (*matrixBackend) Info() execution.Info { return execution.Local{}.Info() }
func (b *matrixBackend) Run(_ context.Context, r execution.Request) (execution.Result, error) {
	b.calls = append(b.calls, append([]string{}, r.Args...))
	if b.cancel != nil {
		b.cancel()
	}
	if b.failDefault && !strings.Contains(strings.Join(r.Args, " "), "-tags=") {
		return execution.Result{ExitCode: 1}, nil
	}
	return execution.Result{ExitCode: 0}, nil
}
func TestGoCheckUsesHostMatrix(t *testing.T) {
	backend := &matrixBackend{}
	configs := []task.BuildConfiguration{{ID: "default"}, {ID: "alternate", Tags: []string{"rpcx_alternate"}}}
	e, err := New(Config{Workspace: t.TempDir(), GoCheck: &GoCheckConfig{CacheDir: t.TempDir(), Backend: backend, BuildConfigurations: configs}})
	if err != nil {
		t.Fatal(err)
	}
	configs[1].Tags[0] = "changed"
	result, err := e.Execute(context.Background(), agent.ToolCall{Name: "go_check", Arguments: `{"kind":"test"}`})
	if err != nil || result.IsError || len(backend.calls) != 2 {
		t.Fatalf("%+v %v", result, err)
	}
	if !strings.Contains(strings.Join(backend.calls[1], " "), "-tags=rpcx_alternate") || strings.Contains(strings.Join(backend.calls[0], " "), "-tags=") {
		t.Fatal("host configurations not preserved")
	}
	if !strings.Contains(result.Content, "configuration alternate") {
		t.Fatal("missing per-configuration feedback")
	}
	before := len(backend.calls)
	result, err = e.Execute(context.Background(), agent.ToolCall{Name: "go_check", Arguments: `{"kind":"test","tags":["other"]}`})
	if err != nil || !result.IsError || len(backend.calls) != before {
		t.Fatal("model could override tags")
	}
	backend.failDefault = true
	result, err = e.Execute(context.Background(), agent.ToolCall{Name: "go_check", Arguments: `{"kind":"build"}`})
	if err != nil || !result.IsError || len(backend.calls) != before+2 || !strings.Contains(result.Content, "configuration alternate") {
		t.Fatal("failed default hid the alternate check", err)
	}
	before = len(backend.calls)
	ctx, cancel := context.WithCancel(context.Background())
	backend.cancel = cancel
	_, err = e.Execute(ctx, agent.ToolCall{Name: "go_check", Arguments: `{"kind":"test"}`})
	if !errors.Is(err, context.Canceled) || len(backend.calls) != before+1 {
		t.Fatal("cancelled matrix continued", err)
	}
}
