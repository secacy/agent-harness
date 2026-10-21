//go:build darwin || linux

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/audit"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/recovery"
	"github.com/secacy/agent-harness/internal/task"
	"github.com/secacy/agent-harness/internal/writer"
)

type resourceCrashBackend struct {
	*execution.Docker
	root   string
	policy execution.ResourcePolicy
}

func (b resourceCrashBackend) Run(ctx context.Context, req execution.Request) (execution.Result, error) {
	if req.Owner == nil || req.Owner.TargetID != "interrupted" {
		return b.Docker.Run(ctx, req)
	}
	req.Args = []string{"sh", "-c", "sleep 120"}
	result := make(chan error, 1)
	go func() { _, err := b.Docker.Run(ctx, req); result <- err }()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-result:
			return execution.Result{}, fmt.Errorf("container returned before barrier: %w", err)
		case <-ctx.Done():
			return execution.Result{}, ctx.Err()
		case <-ticker.C:
			entries, err := execution.ReadResources(b.root, req.Owner.Registry)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.Intent.Owner.RunID == req.Owner.RunID && e.Created != nil && e.Removed == nil {
					out, err := exec.CommandContext(ctx, "docker", "--context", b.policy.Context, "inspect", "--format", "{{.State.Running}}", e.Created.ContainerID).Output()
					if err == nil && strings.TrimSpace(string(out)) == "true" {
						stopAtCrashBarrier("resource_running", req.Owner.TargetID, req.Owner.RunID)
					}
				}
			}
		}
	}
}

func TestResourceCleanupProcessHelper(t *testing.T) {
	root := os.Getenv("HARNESS_RESOURCE_CAMPAIGN")
	if root == "" {
		t.Skip("subprocess helper")
	}
	d, err := execution.NewDocker(context.Background(), execution.DockerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := d.ResourcePolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	config := campaignConfig(t, []string{"correct", "interrupted"}, 1, d)
	config.Directory = root
	config.Plan.Resources = &policy
	config.Plan.Recovery = &campaign.RecoveryPolicy{MaxAttempts: 2, GenerationDeadline: time.Now().Add(10 * time.Minute).UTC()}
	config.Backend = resourceCrashBackend{d, root, policy}
	_, _, err = campaign.Run(context.Background(), config)
	t.Fatal("helper returned without reaching its container barrier", err)
}

func TestDockerCampaignResourceCleanupAndRecovery(t *testing.T) {
	if os.Getenv("HARNESS_RESOURCE_ACCEPTANCE") != "1" {
		t.Skip("set HARNESS_RESOURCE_ACCEPTANCE=1 for Docker host SIGKILL, exact cleanup and recovery")
	}
	testDockerResourceRecovery(t, nil)
}

func testDockerResourceRecovery(t *testing.T, connection *dockerConnection) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	d, err := execution.NewDocker(ctx, execution.DockerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := d.ResourcePolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	if output := os.Getenv("HARNESS_RECOVERY_ARTIFACTS"); output != "" {
		if err := os.MkdirAll(output, 0700); err != nil {
			t.Fatal(err)
		}
		parent, err = os.MkdirTemp(output, "docker-resources-")
		if err != nil {
			t.Fatal(err)
		}
	}
	parent, _ = filepath.EvalSymlinks(parent)
	root := filepath.Join(parent, "campaign")
	// 只使用本测试创建的公共标签邻居；精确 ID 用于最终回收。
	output, err := exec.CommandContext(ctx, "docker", "--context", policy.Context, "create", "--pull=never", "--network=none", "--label", "agent-harness.execution=true", "--entrypoint", "sleep", d.Info().Image, "600").Output()
	if err != nil {
		t.Fatal(err)
	}
	neighbor := strings.TrimSpace(string(output))
	defer func() {
		_ = exec.Command("docker", "--context", policy.Context, "rm", "--force", "--volumes", neighbor).Run()
	}()
	if connection != nil {
		// 失败收尾也先恢复测试连接，不能因断连漏掉自建邻居的回收。
		defer connection.restore(t)
	}
	if err = exec.CommandContext(ctx, "docker", "--context", policy.Context, "start", neighbor).Run(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HARNESS_RESOURCE_CAMPAIGN", root)
	// 复用管道/PID 核对和退出回收；辅助测试名通过专用入口启动。
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	log, err := os.Create(filepath.Join(parent, "child.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestResourceCleanupProcessHelper$")
	cmd.Env = os.Environ()
	cmd.ExtraFiles = []*os.File{w}
	cmd.Stdout, cmd.Stderr = log, log
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	finished := false
	defer func() {
		if !finished {
			_ = cmd.Process.Kill()
			<-done
		}
	}()
	ready := make(chan crashBarrier, 1)
	go func() {
		var event crashBarrier
		if json.NewDecoder(r).Decode(&event) == nil {
			ready <- event
		}
	}()
	select {
	case event := <-ready:
		if event.Point != "resource_running" || event.PID != cmd.Process.Pid || event.TargetID != "interrupted" {
			t.Fatal(event)
		}
	case err := <-done:
		finished = true
		t.Fatal("child returned before Docker barrier", err)
	case <-time.After(4 * time.Minute):
		t.Fatal("Docker barrier timed out")
	}
	activeRecord := crashRead[campaign.Record](t, filepath.Join(root, "campaign.json"))
	if _, err := recovery.CleanupResources(ctx, recovery.CleanupRequest{Request: recovery.Request{CampaignDir: root, OutputDir: filepath.Join(parent, "active-cleanup-rejected"), ExpectedPlanSHA256: activeRecord.PlanSHA256, InspectRun: InspectRun}, Docker: d}); !errors.Is(err, writer.ErrBusy) {
		t.Fatal("active writer allowed resource cleanup", err)
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = <-done
	finished = true
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatal("host was not killed by SIGKILL", err)
	}
	before := crashSnapshot(t, root)
	record := crashRead[campaign.Record](t, filepath.Join(root, "campaign.json"))
	plan := crashRead[campaign.Plan](t, filepath.Join(root, "plan.json"))
	request := recovery.Request{CampaignDir: root, OutputDir: filepath.Join(parent, "check-before"), ExpectedPlanSHA256: record.PlanSHA256, InspectRun: InspectRun, VerifyResourcesStopped: recovery.DockerResourcesStopped(d)}
	var outages []map[string]any
	if connection != nil {
		outages = append(outages, connection.rejectCleanup(t, ctx, d, request, parent, policy))
	}
	if _, err := recovery.Check(ctx, request); err == nil {
		t.Fatal("live orphan permitted recovery")
	}
	request.OutputDir = filepath.Join(parent, "cleanup-1")
	first, err := recovery.CleanupResources(ctx, recovery.CleanupRequest{Request: request, Docker: d})
	if err != nil || !first.Resources.Stopped || first.After.Consistency != "pass" {
		t.Fatal(first, err)
	}
	all := crashSnapshot(t, root)
	request.OutputDir = filepath.Join(parent, "cleanup-2")
	second, err := recovery.CleanupResources(ctx, recovery.CleanupRequest{Request: request, Docker: d})
	if err != nil || !second.Resources.Stopped || !reflect.DeepEqual(all, crashSnapshot(t, root)) {
		t.Fatal("repeated cleanup changed history", err)
	}
	neighborState, err := exec.CommandContext(ctx, "docker", "--context", policy.Context, "inspect", "--format", "{{.State.Running}}", neighbor).Output()
	if err != nil || strings.TrimSpace(string(neighborState)) != "true" {
		t.Fatal("neighbor was stopped or deleted", err)
	}
	request.OutputDir = filepath.Join(parent, "prepare")
	if connection != nil {
		outages = append(outages, connection.rejectPrepare(t, ctx, request, parent, policy))
	}
	if _, err := recovery.Prepare(ctx, recovery.PrepareRequest{Request: request, ID: "docker-recovery"}); err != nil {
		t.Fatal(err)
	}
	script := campaignConfig(t, []string{"correct"}, 1, d)
	request.OutputDir = filepath.Join(parent, "execute")
	factories := 0
	verifications := 0
	runRequest := recovery.ExecuteRequest{Request: request, Targets: []string{"interrupted"}, Backend: d, Execute: CampaignExecutor(false), VerifyModel: func(context.Context, budget.Profile) error { verifications++; return nil }, NewModel: func(task.Target) (budget.ProfiledModel, error) {
		factories++
		m, err := script.NewModel(task.Target{ID: "correct"})
		if err != nil {
			return nil, err
		}
		return budgetTestModel{profile: plan.Profile, modelFunc: m.Complete}, nil
	}}
	if connection != nil {
		outages = append(outages, connection.rejectExecute(t, ctx, runRequest, parent, policy))
		if factories != 0 || verifications != 0 {
			t.Fatal("disconnected recovery reached model creation or verification", factories, verifications)
		}
	}
	out, err := recovery.Execute(ctx, runRequest)
	if err != nil || out.Result == nil || out.Audit.Consistency != "pass" || out.Audit.RecoveryExecutionState != "completed" || out.Result.Targets[1].Attempt.Verdict != "ReadyForReview" {
		t.Fatal(out.Audit, err)
	}
	if err := recovery.DockerResourcesStopped(d)(ctx, root); err != nil {
		t.Fatal("new execution leaked resources", err)
	}
	original := map[string]string{}
	for p, h := range crashSnapshot(t, root) {
		if p != "resources" && !strings.HasPrefix(p, "resources/") && p != "recoveries" && !strings.HasPrefix(p, "recoveries/") {
			original[p] = h
		}
	}
	old := map[string]string{}
	for p, h := range before {
		if p != "resources" && !strings.HasPrefix(p, "resources/") {
			old[p] = h
		}
	}
	if !reflect.DeepEqual(old, original) {
		t.Fatal("cleanup or retry changed original control/Run files")
	}
	finalAudit, err := audit.Inspect(ctx, audit.Request{CampaignDir: root, OutputDir: filepath.Join(parent, "audit-final")}, InspectRun)
	if err != nil || finalAudit.Consistency != "pass" {
		t.Fatal(finalAudit, err)
	}
	auditPut(t, parent, "evidence.json", map[string]any{"signal": "SIGKILL", "pid": cmd.Process.Pid, "policy": policy, "neighbor_id": neighbor, "neighbor_unchanged": true, "original_runs_unchanged": true, "cleanup": first.Resources, "result": out.Result, "connection_outages": outages, "model_factories": factories, "model_verifications": verifications, "scope": "real Docker containers; controlled migration model; no provider request"})
	t.Log("real Docker cleanup and recovery:", parent)
}
