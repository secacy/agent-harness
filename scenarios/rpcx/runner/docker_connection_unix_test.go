//go:build darwin || linux

package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/recovery"
)

type dockerConnection struct {
	socket   string
	endpoint string
	context  string
}

func dockerOutput(t *testing.T, ctx context.Context, args ...string) string {
	t.Helper()
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newDockerConnection(t *testing.T) *dockerConnection {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	current := dockerOutput(t, ctx, "context", "show")
	host := dockerOutput(t, ctx, "context", "inspect", current, "--format", "{{.Endpoints.docker.Host}}")
	if !strings.HasPrefix(host, "unix://") {
		t.Fatal("connection acceptance requires a local Unix Docker endpoint")
	}
	endpoint := strings.TrimPrefix(host, "unix://")
	info, err := os.Stat(endpoint)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatal("Docker endpoint is not an accessible Unix socket", err)
	}
	// Unix socket 路径有长度上限，不能放在层级很深的运行归档目录中。
	root, err := os.MkdirTemp("/tmp", "harness-docker-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	connection := &dockerConnection{socket: filepath.Join(root, "engine.sock"), endpoint: endpoint, context: filepath.Base(root)}
	connection.restore(t)
	config := filepath.Join(root, "config")
	if err := os.Mkdir(config, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_CONFIG", config)
	for _, name := range []string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_TLS_VERIFY", "DOCKER_CERT_PATH"} {
		t.Setenv(name, "")
	}
	dockerOutput(t, ctx, "--config", config, "context", "create", connection.context, "--docker", "host=unix://"+connection.socket)
	dockerOutput(t, ctx, "--config", config, "context", "use", connection.context)
	return connection
}

func (c *dockerConnection) switchTo(t *testing.T, endpoint string) {
	t.Helper()
	// 只替换本测试拥有的别名，不接触真实引擎 socket 或用户 context。
	next := c.socket + ".next"
	if err := os.Symlink(endpoint, next); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, c.socket); err != nil {
		t.Fatal(err)
	}
}

func (c *dockerConnection) restore(t *testing.T) { t.Helper(); c.switchTo(t, c.endpoint) }

func (c *dockerConnection) disconnect(t *testing.T, ctx context.Context, policy execution.ResourcePolicy) string {
	t.Helper()
	c.switchTo(t, filepath.Join(filepath.Dir(c.socket), "unavailable.sock"))
	output, err := exec.CommandContext(ctx, "docker", "--context", c.context, "info", "--format", "{{.ID}}").CombinedOutput()
	if err == nil {
		t.Fatal("disconnected Docker context still connected to an engine")
	}
	// 直接查询原 endpoint，区分测试连接故障与共享 daemon 故障。
	engine := dockerOutput(t, ctx, "--host", "unix://"+c.endpoint, "info", "--format", "{{.ID}}")
	if engine != policy.EngineID {
		t.Fatal("shared Docker engine changed during connection failure", engine)
	}
	return strings.TrimSpace(string(output))
}

func (c *dockerConnection) reconnect(t *testing.T, ctx context.Context, policy execution.ResourcePolicy) {
	t.Helper()
	c.restore(t)
	if c.context != policy.Context || dockerOutput(t, ctx, "--context", c.context, "info", "--format", "{{.ID}}") != policy.EngineID {
		t.Fatal("restored connection differs from the original plan")
	}
}

func (c *dockerConnection) rejectCleanup(t *testing.T, ctx context.Context, d *execution.Docker, request recovery.Request, parent string, policy execution.ResourcePolicy) map[string]any {
	t.Helper()
	before := crashSnapshot(t, request.CampaignDir)
	diagnostic := c.disconnect(t, ctx, policy)
	request.OutputDir = filepath.Join(parent, "offline-cleanup")
	result, err := recovery.CleanupResources(ctx, recovery.CleanupRequest{Request: request, Docker: d})
	if !errors.Is(err, execution.ErrResourcesUnknown) || result.Resources.Stopped || result.Before.Consistency != "pass" {
		t.Fatal("disconnected cleanup inferred stopped resources", result, err)
	}
	saved := crashRead[execution.ResourceReport](t, filepath.Join(request.OutputDir, "cleanup.json"))
	if saved.Stopped {
		t.Fatal("cleanup diagnostic falsely declared resources stopped")
	}
	request.OutputDir = filepath.Join(parent, "offline-check")
	if _, err := recovery.Check(ctx, request); !errors.Is(err, recovery.ErrResourcesUnknown) {
		t.Fatal("disconnected check permitted recovery", err)
	}
	request.OutputDir = filepath.Join(parent, "offline-prepare-before-cleanup")
	if _, err := recovery.Prepare(ctx, recovery.PrepareRequest{Request: request, ID: "docker-recovery"}); !errors.Is(err, recovery.ErrResourcesUnknown) {
		t.Fatal("disconnected prepare passed before cleanup", err)
	}
	if !reflect.DeepEqual(before, crashSnapshot(t, request.CampaignDir)) {
		t.Fatal("disconnected cleanup changed campaign source")
	}
	c.reconnect(t, ctx, policy)
	return map[string]any{"stage": "before_cleanup", "cli_failure": diagnostic, "shared_engine_id": policy.EngineID, "source_unchanged": true, "cleanup_stopped": saved.Stopped, "check_blocked": true, "prepare_blocked": true}
}

func (c *dockerConnection) rejectPrepare(t *testing.T, ctx context.Context, request recovery.Request, parent string, policy execution.ResourcePolicy) map[string]any {
	t.Helper()
	before := crashSnapshot(t, request.CampaignDir)
	diagnostic := c.disconnect(t, ctx, policy)
	request.OutputDir = filepath.Join(parent, "offline-prepare-after-cleanup")
	if _, err := recovery.Prepare(ctx, recovery.PrepareRequest{Request: request, ID: "docker-recovery"}); !errors.Is(err, recovery.ErrResourcesUnknown) {
		t.Fatal("stale cleanup success permitted offline prepare", err)
	}
	if !reflect.DeepEqual(before, crashSnapshot(t, request.CampaignDir)) {
		t.Fatal("offline prepare changed campaign source")
	}
	c.reconnect(t, ctx, policy)
	return map[string]any{"stage": "after_cleanup", "cli_failure": diagnostic, "shared_engine_id": policy.EngineID, "source_unchanged": true, "prepare_blocked": true}
}

func (c *dockerConnection) rejectExecute(t *testing.T, ctx context.Context, request recovery.ExecuteRequest, parent string, policy execution.ResourcePolicy) map[string]any {
	t.Helper()
	before := crashSnapshot(t, request.CampaignDir)
	diagnostic := c.disconnect(t, ctx, policy)
	request.OutputDir = filepath.Join(parent, "offline-execute")
	result, err := recovery.Execute(ctx, request)
	if !errors.Is(err, recovery.ErrResourcesUnknown) || result.Result != nil {
		t.Fatal("disconnected execution was allocated", result, err)
	}
	if !reflect.DeepEqual(before, crashSnapshot(t, request.CampaignDir)) {
		t.Fatal("offline execute changed campaign source")
	}
	if _, err := os.Stat(filepath.Join(request.CampaignDir, campaign.RecoveryExecutionPath)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("offline execute created an execution directory", err)
	}
	c.reconnect(t, ctx, policy)
	return map[string]any{"stage": "after_checkpoint", "cli_failure": diagnostic, "shared_engine_id": policy.EngineID, "source_unchanged": true, "execute_blocked": true}
}

func TestDockerConnectionFailureCleanupAndRecovery(t *testing.T) {
	if os.Getenv("HARNESS_DOCKER_CONNECTION_ACCEPTANCE") != "1" {
		t.Skip("set HARNESS_DOCKER_CONNECTION_ACCEPTANCE=1 for real Unix Docker connection failures")
	}
	testDockerResourceRecovery(t, newDockerConnection(t))
}
