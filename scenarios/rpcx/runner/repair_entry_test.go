package runner

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/execution"
)

type repairLaunch struct {
	ProtocolSHA256 string `json:"protocol_sha256"`
	FreezeSHA256   string `json:"freeze_sha256"`
	BinarySHA256   string `json:"binary_sha256"`
}

func repairLaunchIdentity(project string) (repairLaunch, error) {
	var result repairLaunch
	protocol, err := os.ReadFile(filepath.Join(project, repairProtocolDir, "protocol.json"))
	if err != nil {
		return result, err
	}
	freeze, err := os.ReadFile(filepath.Join(project, repairProtocolDir, "freeze.json"))
	if err != nil {
		return result, err
	}
	var files map[string]string
	if err = repairDecode(freeze, &files); err != nil {
		return result, err
	}
	if len(files) == 0 {
		return result, errors.New("empty repair source inventory")
	}
	for name, hash := range files {
		if !filepath.IsLocal(name) {
			return result, errors.New("invalid frozen source path")
		}
		data, readErr := os.ReadFile(filepath.Join(project, name))
		if readErr != nil || digest(data) != hash {
			return result, errors.Join(readErr, errors.New("frozen repair source changed: "+name))
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return result, err
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		return result, err
	}
	return repairLaunch{digest(protocol), digest(freeze), digest(data)}, nil
}

// 本版只支持离线准备和校准，不读取凭证或提供真实模型入口。
func TestDiagnosticRepairExperiment(t *testing.T) {
	mode := os.Getenv("HARNESS_REPAIR_MODE")
	if mode == "" {
		t.Skip("set HARNESS_REPAIR_MODE to prepare or offline for the explicit experiment entry")
	}
	if mode == "real" {
		t.Fatal("real model execution is disabled for this offline protocol")
	}
	if mode != "prepare" && mode != "offline" {
		t.Fatal("invalid repair experiment mode")
	}
	if flag.Lookup("test.run").Value.String() != "^TestDiagnosticRepairExperiment$" {
		t.Fatal("the repair entry must be selected by its exact test name")
	}
	project, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	p, seed, prompt, err := loadRepairProtocol(project)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := repairLaunchIdentity(project)
	if err != nil {
		t.Fatal(err)
	}
	launch := os.Getenv("HARNESS_REPAIR_LAUNCH")
	if mode == "prepare" {
		data, _ := json.MarshalIndent(identity, "", "  ")
		file, err := os.OpenFile(launch, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := file.Write(append(data, '\n'))
		if err = errors.Join(writeErr, file.Close()); err != nil {
			t.Fatal(err)
		}
		t.Log("launch identity saved; no credentials or model requests")
		return
	}
	output, err := filepath.Abs(os.Getenv("HARNESS_REPAIR_OUTPUT"))
	if err != nil || os.Getenv("HARNESS_REPAIR_OUTPUT") == "" {
		t.Fatal("an explicit repair output directory is required", err)
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		t.Fatal("repair output must not exist")
	}
	// 先确认输出边界，再创建 Docker 后端。
	if err = repairOutputBoundary(project, output); err != nil {
		t.Fatal(err)
	}
	ctx, stopSignal := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignal()
	ctx, stopHost := context.WithTimeout(ctx, time.Duration(p.HostSeconds)*time.Second)
	defer stopHost()
	backend, err := execution.NewDocker(ctx, execution.DockerConfig{Image: p.Image})
	if err != nil {
		t.Fatal(err)
	}
	profile := budget.Profile{Provider: "controlled", Endpoint: "in-process", Model: p.Model, ContextTokens: 90, MaxOutputTokens: 10, SourceSHA256: strings.Repeat("e", 64), VerifiedAt: time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)}
	services := repairServices{Backend: backend, Profile: profile}
	services.NewModel = func(string) (budget.ProfiledModel, error) { return repairScript(profile), nil }
	summary, err := runRepairComparison(ctx, project, output, p, seed, prompt, services)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "offline" {
		for _, a := range summary.Attempts {
			if a.Verdict.Assertion != "pass" || a.Verdict.Tests != "pass" || a.Verdict.Scope != "pass" || a.Verdict.Checks != "pass" || a.Verdict.Submitted != "pass" || a.Verdict.Delivery != "unknown" {
				t.Fatalf("offline repair calibration failed: %+v", a)
			}
		}
	}
	t.Log("REPAIR_EXPERIMENT", output, mode)
}

// 脚本仅校准编辑、检查与交付，不能当作真实模型自主修复。
func repairScript(profile budget.Profile) budget.ProfiledModel {
	step := 0
	return budgetTestModel{profile: profile, modelFunc: func(_ context.Context, _ []agent.Message, _ []agent.ToolSpec) (agent.Reply, error) {
		step++
		reply := agent.Reply{Usage: &agent.Usage{InputTokens: 15, OutputTokens: 5, TotalTokens: 20}, Finish: agent.FinishToolCalls}
		switch step {
		case 1:
			// 完整文件中第二处 <=0 属于新增测试，工具替换必须只命中一次。
			old := "if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > 30*time.Second {"
			args, _ := json.Marshal(map[string]string{"path": repairTestPath, "old": old, "new": strings.Replace(old, "<= 0", "< 29*time.Second", 1)})
			reply.ToolCalls = []agent.ToolCall{{ID: "restore-assertion", Name: "replace_text", Arguments: string(args)}}
		case 2:
			reply.ToolCalls = []agent.ToolCall{{ID: "build", Name: "go_check", Arguments: `{"kind":"build"}`}, {ID: "test", Name: "go_check", Arguments: `{"kind":"test"}`}}
		default:
			reply.Finish, reply.Content = agent.FinishStop, "Restored the original deadline lower bound. Ran go_check build and test. Independent validation remains required."
		}
		return reply, nil
	}}
}
