package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/execution"
)

type campaignBackend struct {
	backend execution.Executor
	run     func(context.Context, execution.Request) (execution.Result, error)
}

func (b campaignBackend) Info() execution.Info { return b.backend.Info() }
func (b campaignBackend) Run(ctx context.Context, req execution.Request) (execution.Result, error) {
	return b.run(ctx, req)
}

func TestCampaignDockerKilledExecutionIsolation(t *testing.T) {
	if os.Getenv("HARNESS_DOCKER_TEST") != "1" {
		t.Skip("set HARNESS_DOCKER_TEST=1 to run real Docker integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	docker, err := execution.NewDocker(ctx, execution.DockerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	solo := campaignConfig(t, []string{"correct"}, 1, docker)
	soloRecord, single, err := campaign.Run(ctx, solo)
	if err != nil || single.Counts.Verdicts["ReadyForReview"] != 1 {
		t.Fatalf("%+v %+v %v", soloRecord, single.Counts, err)
	}
	original := campaignReport(t, soloRecord.Directory, single.Targets[0])
	var killed atomic.Int32
	backend := campaignBackend{backend: docker, run: func(ctx context.Context, req execution.Request) (execution.Result, error) {
		if !strings.Contains(filepath.ToSlash(req.Directory), "/targets/execution-killed/") {
			return docker.Run(ctx, req)
		}
		// 只把故障任务的首条命令换成驻留探针，给外部 SIGKILL 一个确定窗口。
		req.Args = []string{"bash", "-c", "sleep 60"}
		killCtx, stop := context.WithTimeout(ctx, 20*time.Second)
		defer stop()
		killedID := make(chan string, 1)
		go func() {
			id, err := killOwnedContainer(killCtx, req.Directory)
			if err != nil {
				t.Error(err)
			}
			killedID <- id
		}()
		result, err := docker.Run(ctx, req)
		stop()
		id := <-killedID
		if id == "" || result.ContainerID != id || result.ExitCode != 137 {
			t.Errorf("kill result=%+v id=%s err=%v", result, id, err)
		} else {
			killed.Add(1)
		}
		if data, inspectErr := exec.Command("docker", "inspect", result.ContainerID).CombinedOutput(); inspectErr == nil || !strings.Contains(strings.ToLower(string(data)), "no such") {
			t.Errorf("owned container remains: %s %v", data, inspectErr)
		}
		t.Logf("killed and removed execution container %s", id)
		return result, err
	}}
	config := campaignConfig(t, []string{"execution-killed", "correct"}, 2, backend)
	record, summary, err := campaign.Run(ctx, config)
	if err != nil || record.Status != campaign.Completed || summary.Counts.Completed != 1 || summary.Counts.Failed != 1 || killed.Load() != 1 || summary.Budget.Targets["execution-killed"].Totals.Calls != 0 {
		t.Fatalf("%+v %+v %v", record, summary, err)
	}
	compareCampaignEvidence(t, original, campaignReport(t, record.Directory, summary.Targets[1]))
	t.Logf("solo=%s fault-batch=%s; unaffected candidate and gate outcomes match", soloRecord.Directory, record.Directory)
}

// 标签、精确挂载源和运行态三者同时匹配才杀进程，不按模糊名称清理容器。
func killOwnedContainer(ctx context.Context, source string) (string, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := exec.CommandContext(ctx, "docker", "ps", "--quiet", "--no-trunc", "--filter", "label=agent-harness.execution=true").Output()
		if err != nil {
			return "", err
		}
		for _, id := range strings.Fields(string(data)) {
			data, err := exec.CommandContext(ctx, "docker", "inspect", id).Output()
			if err != nil {
				continue
			}
			var infos []struct {
				State  struct{ Running bool }
				Config struct{ Labels map[string]string }
				Mounts []struct{ Source string }
			}
			if err = json.Unmarshal(data, &infos); err != nil {
				return "", err
			}
			if len(infos) != 1 || !infos[0].State.Running || infos[0].Config.Labels["agent-harness.execution"] != "true" {
				continue
			}
			for _, mount := range infos[0].Mounts {
				if mount.Source == source {
					if data, err = exec.CommandContext(ctx, "docker", "kill", "--signal=KILL", id).CombinedOutput(); err != nil {
						return "", fmt.Errorf("kill owned container: %w: %s", err, data)
					}
					return id, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}
