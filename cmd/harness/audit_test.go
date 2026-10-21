package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/task"
	"github.com/secacy/agent-harness/scenarios/rpcx/runner"
)

func auditCommandInput(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "campaign")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("a", 64)
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	limits := budget.Limits{MaxCalls: 1, MaxTokens: 100}
	plan := campaign.Plan{ID: "audit-test", Concurrency: 1, ExecutionConcurrency: 1, Budget: limits, Profile: budget.Profile{Provider: "controlled", Endpoint: "not-used", Model: "fixed", ContextTokens: 90, MaxOutputTokens: 10, SourceSHA256: hash, VerifiedAt: now}, Targets: []campaign.Target{{Input: task.Target{ID: "orders", ProfileID: runner.ProfileT3, BuildConfig: "default", Repository: task.Snapshot{Directory: "/not-read/repo", SHA256: hash}, Specification: task.Specification{Path: "/not-read/spec", SHA256: hash}}, Budget: limits, Limits: agent.Limits{MaxModelCalls: 1, MaxToolCalls: 1}, Timeout: time.Minute}}}
	putAuditCommand(t, root, "plan.json", plan)
	raw, err := os.ReadFile(filepath.Join(root, "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	putAuditCommand(t, root, "campaign.json", campaign.Record{SchemaVersion: 1, ID: plan.ID, Directory: "/original/campaign", Status: campaign.Running, PlanSHA256: hex.EncodeToString(sum[:]), StartedAt: now})
	putAuditCommand(t, root, "targets/orders/target.json", campaign.TargetRecord{ID: "orders", State: campaign.Pending})
	if err = os.MkdirAll(filepath.Join(root, "targets/orders/runs"), 0700); err != nil {
		t.Fatal(err)
	}
	return root
}
func putAuditCommand(t *testing.T, root, p string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(root, p)
	if err = os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(full, append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestAuditCommandDispatchAndExitCodes(t *testing.T) {
	// 没有鉴权配置，也没有可执行 Go/Docker 的 PATH，审计仍能独立工作。
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("DEEPSEEK_MODEL", "")
	t.Setenv("PATH", t.TempDir())
	for _, tc := range []struct {
		name   string
		code   int
		change func(*testing.T, string)
		ctx    func() (context.Context, context.CancelFunc)
	}{
		{name: "legal-initialization", code: 0},
		{name: "violated", code: 1, change: func(t *testing.T, root string) {
			data, err := os.ReadFile(filepath.Join(root, "plan.json"))
			if err != nil {
				t.Fatal(err)
			}
			data = append(data, ' ')
			if err = os.WriteFile(filepath.Join(root, "plan.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "unsupported", code: 3, change: func(t *testing.T, root string) {
			putAuditCommand(t, root, "campaign.json", map[string]int{"schema_version": 99})
		}},
		{name: "cancelled", code: 130, ctx: func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, cancel
		}},
		{name: "deadline", code: 3, ctx: func() (context.Context, context.CancelFunc) {
			return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := auditCommandInput(t)
			if tc.change != nil {
				tc.change(t, root)
			}
			ctx := context.Background()
			if tc.ctx != nil {
				var cancel context.CancelFunc
				ctx, cancel = tc.ctx()
				defer cancel()
			}
			var out, errout bytes.Buffer
			output := filepath.Join(t.TempDir(), "audit")
			code := run(ctx, []string{"audit", "--campaign", root, "--output", output}, &out, &errout)
			if code != tc.code {
				t.Fatalf("exit=%d stdout=%s stderr=%s", code, &out, &errout)
			}
			if tc.code == 0 && !strings.Contains(out.String(), "execution_state: incomplete") {
				t.Fatal("legal prefix was presented as completed")
			}
			if _, err := os.Stat(filepath.Join(output, "published.json")); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestAuditCommandInvalidArguments(t *testing.T) {
	root := auditCommandInput(t)
	for _, args := range [][]string{{"audit"}, {"audit", "--help"}, {"audit", "--unknown"}, {"audit", "--campaign", root, "--output", root}, {"audit", "--campaign", root, "--output", filepath.Join(t.TempDir(), "audit"), "extra"}} {
		var out, errout bytes.Buffer
		expected := 2
		if len(args) == 2 && args[1] == "--help" {
			expected = 0
		}
		if code := run(context.Background(), args, &out, &errout); code != expected {
			t.Fatalf("args=%v code=%d stderr=%s", args, code, &errout)
		}
	}
}
