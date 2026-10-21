//go:build darwin || linux

package recovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/writer"
)

// 真实 Campaign 在已初始化账本、尚未创建 Run 或执行命令的位置阻塞。
func TestRecoveryCampaignHelper(t *testing.T) {
	root := os.Getenv("HARNESS_RECOVERY_HELPER")
	if root == "" {
		t.Skip("subprocess helper")
	}
	config := controlledConfig(root)
	config.Execute = func(context.Context, campaign.Target, campaign.Services) (campaign.Attempt, error) {
		pipe := os.NewFile(3, "ready")
		if err := json.NewEncoder(pipe).Encode(os.Getpid()); err != nil {
			t.Fatal(err)
		}
		_ = pipe.Close()
		var one [1]byte
		_, _ = os.Stdin.Read(one[:])
		return campaign.Attempt{Status: campaign.Failed}, errors.New("barrier unexpectedly released")
	}
	if _, _, err := campaign.Run(context.Background(), config); err != nil {
		t.Fatal(err)
	}
}

func TestCheckAfterActualCampaignSIGKILL(t *testing.T) {
	parent := t.TempDir()
	if output := os.Getenv("HARNESS_WRITER_ARTIFACTS"); output != "" {
		if err := os.MkdirAll(output, 0700); err != nil {
			t.Fatal(err)
		}
		var err error
		parent, err = os.MkdirTemp(output, "campaign-stop-")
		if err != nil {
			t.Fatal(err)
		}
	}
	parent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "campaign")
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
	cmd := exec.Command(os.Args[0], "-test.run=^TestRecoveryCampaignHelper$")
	cmd.Env = append(os.Environ(), "HARNESS_RECOVERY_HELPER="+root)
	cmd.ExtraFiles = []*os.File{w}
	cmd.Stdout, cmd.Stderr = log, log
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	ready := make(chan int, 1)
	go func() {
		var pid int
		if json.NewDecoder(r).Decode(&pid) == nil {
			ready <- pid
		}
	}()
	select {
	case pid := <-ready:
		if pid != cmd.Process.Pid {
			t.Fatal("barrier PID does not match the owned child")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("campaign did not reach its persisted barrier")
	}
	record := load[campaign.Record](t, root, "campaign.json")
	if record.Status != campaign.Running || record.SchemaVersion != 2 || record.WriterProtocol != writer.Protocol {
		t.Fatal(record)
	}
	request := checkRequest(root, filepath.Join(parent, "blocked-audit"), record.PlanSHA256)
	resourceChecks := 0
	request.VerifyResourcesStopped = func(context.Context, string) error { resourceChecks++; return nil }
	before := tree(t, root)
	if _, err := Check(context.Background(), request); !errors.Is(err, writer.ErrBusy) || resourceChecks != 0 {
		t.Fatal("active campaign did not block recovery inspection", err, resourceChecks)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	stopErr := cmd.Wait()
	waited = true
	var exit *exec.ExitError
	if !errors.As(stopErr, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatal("child was not terminated by SIGKILL", stopErr)
	}
	// 这里只认证本测试的屏障：它从未启动命令，且所属宿主已由 Wait 确认死亡。
	for i := range 2 {
		request.OutputDir = filepath.Join(parent, fmt.Sprintf("audit-%d", i+1))
		result, err := Check(context.Background(), request)
		if err != nil || result.Audit.Consistency != "pass" || result.Audit.ExecutionState != "incomplete" {
			t.Fatal(result, err)
		}
	}
	if resourceChecks != 2 || !reflect.DeepEqual(before, tree(t, root)) {
		t.Fatal("check modified the stopped campaign or bypassed resource verification")
	}
	ledger := load[budget.Snapshot](t, root, "budget/budget.json")
	if ledger.Totals != (budget.Totals{}) || len(ledger.Calls) != 0 {
		t.Fatal("check changed the ledger", ledger)
	}
	save(t, parent, "evidence.json", map[string]any{
		"signal": "SIGKILL", "pid": cmd.Process.Pid, "schema_version": record.SchemaVersion,
		"writer_protocol": record.WriterProtocol, "active_writer_rejected": true,
		"checks_after_kill": resourceChecks, "source_unchanged": true, "execution_state": "incomplete",
		"budget_totals": ledger.Totals, "scope": "control-only barrier; no command, Run, model call or retry",
	})
	t.Log("actual Campaign SIGKILL: active writer rejected, two stopped checks passed, original control/ledger bytes unchanged")
}
