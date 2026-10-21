//go:build darwin || linux

package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/audit"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/recovery"
	"github.com/secacy/agent-harness/internal/task"
)

const crashHelperEnv = "HARNESS_CRASH_HELPER"

type crashBarrier struct {
	Point           string `json:"point"`
	PID             int    `json:"pid"`
	TargetID        string `json:"target_id,omitempty"`
	RunID           string `json:"run_id,omitempty"`
	CandidateSHA256 string `json:"candidate_sha256,omitempty"`
}

// 子进程只在该测试显式配置的点停住，管道不向生产命令暴露暂停能力。
func stopAtCrashBarrier(point, target, run string) {
	stopAtCrashEvent(crashBarrier{Point: point, PID: os.Getpid(), TargetID: target, RunID: run})
}

func stopAtCrashEvent(event crashBarrier) {
	file := os.NewFile(3, "barrier")
	if file == nil {
		panic("checkpoint pipe is unavailable")
	}
	if err := json.NewEncoder(file).Encode(event); err != nil {
		panic(err)
	}
	if err := file.Close(); err != nil {
		panic(err)
	}
	var one [1]byte
	_, err := os.Stdin.Read(one[:])
	panic(fmt.Sprintf("checkpoint must only exit through SIGKILL: %v", err))
}

func TestAuditCrashHelper(t *testing.T) {
	if os.Getenv(crashHelperEnv) != "1" {
		t.Skip("subprocess helper")
	}
	point := os.Getenv("HARNESS_CRASH_POINT")
	directory := os.Getenv("HARNESS_CRASH_DIRECTORY")
	if directory == "" {
		t.Fatal("helper directory is required")
	}
	scenario, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	scenario, err = filepath.EvalSymlinks(scenario)
	if err != nil {
		t.Fatal(err)
	}
	profile := budget.Profile{Provider: "controlled", Endpoint: "in-process", Model: "crash-test-model", ContextTokens: 90, MaxOutputTokens: 10, SourceSHA256: strings.Repeat("e", 64), VerifiedAt: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)}
	config := campaign.Config{Directory: directory, Backend: execution.Local{}, Plan: campaign.Plan{ID: "crash-test", Concurrency: 1, ExecutionConcurrency: 1, Budget: budget.Limits{MaxCalls: 10, MaxTokens: 1000}, Profile: profile}}
	if os.Getenv("HARNESS_CRASH_RECOVERY") == "1" {
		config.Plan.Recovery = &campaign.RecoveryPolicy{MaxAttempts: 2, GenerationDeadline: time.Now().Add(10 * time.Minute).UTC()}
	}
	if os.Getenv("HARNESS_CRASH_RECOVERY_BUDGET") == "exhausted" {
		config.Plan.Budget.MaxTokens = 120
	}
	for _, id := range []string{"completed", "interrupted"} {
		input, err := TargetFromScenario(id, scenario, "v0.1.0")
		if err != nil {
			t.Fatal(err)
		}
		config.Plan.Targets = append(config.Plan.Targets, campaign.Target{Input: input, Budget: budget.Limits{MaxCalls: 5, MaxTokens: 500}, Limits: agent.Limits{MaxModelCalls: 5, MaxToolCalls: 10}, Timeout: 3 * time.Minute})
	}
	config.Prepare = func(context.Context, campaign.Artifact) error {
		if point == "initialized" {
			stopAtCrashBarrier(point, "", "")
		}
		return nil
	}
	config.NewModel = func(input task.Target) (budget.ProfiledModel, error) {
		return budgetTestModel{profile: profile, modelFunc: func(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
			if input.ID == "interrupted" && point == "model_pending" {
				stopAtCrashBarrier(point, input.ID, "")
			}
			return agent.Reply{Finish: agent.FinishStop, Content: "Submit unchanged candidate for validation.", Usage: &agent.Usage{InputTokens: 15, OutputTokens: 5, TotalTokens: 20}}, nil
		}}, nil
	}
	execute := campaignExecutorWithObserver(true, func(checkpoint runCheckpoint, record Record) {
		if record.TargetID == "interrupted" && point == string(checkpoint) {
			stopAtCrashEvent(crashBarrier{Point: point, PID: os.Getpid(), TargetID: record.TargetID, RunID: record.ID, CandidateSHA256: record.CandidateSHA256})
		}
	})
	if point == "complete" {
		execute = CampaignExecutor(true)
	}
	config.Execute = func(ctx context.Context, target campaign.Target, services campaign.Services) (campaign.Attempt, error) {
		attempt, err := execute(ctx, target, services)
		if target.Input.ID == "interrupted" && point == "run_terminal" && err == nil {
			stopAtCrashBarrier(point, target.Input.ID, attempt.RunID)
		}
		return attempt, err
	}
	_, _, err = campaign.Run(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if point != "complete" {
		t.Fatal("campaign returned without reaching the requested checkpoint")
	}
}

func crashRead[T any](t *testing.T, p string) T {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var value T
	if err = json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

// 包括缓存在内记录全部文件和目录成员；只比较字节/成员，不比较访问时间。
func crashSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if e.IsDir() {
			files[filepath.ToSlash(relative)] = "directory"
			return nil
		}
		if !e.Type().IsRegular() {
			return fmt.Errorf("unexpected non-regular file: %s", relative)
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		sum := sha256.New()
		_, readErr := io.Copy(sum, f)
		err = errors.Join(readErr, f.Close())
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = hex.EncodeToString(sum.Sum(nil))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func startCrashChild(t *testing.T, point, directory, logPath string) (*exec.Cmd, <-chan crashBarrier, <-chan error, func()) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestAuditCrashHelper$", "-test.v")
	cmd.Env = append(os.Environ(), crashHelperEnv+"=1", "HARNESS_CRASH_POINT="+point, "HARNESS_CRASH_DIRECTORY="+directory)
	cmd.ExtraFiles = []*os.File{write}
	cmd.Stdout = log
	cmd.Stderr = log
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	write.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); close(done) }()
	ready := make(chan crashBarrier, 1)
	go func() {
		var event crashBarrier
		if json.NewDecoder(read).Decode(&event) == nil {
			ready <- event
		}
		close(ready)
	}()
	cleanup := func() { cmd.Process.Kill(); stdin.Close(); read.Close(); log.Close() }
	t.Cleanup(cleanup)
	return cmd, ready, done, cleanup
}

type crashEvidence struct {
	SchemaVersion      int            `json:"schema_version"`
	Barrier            crashBarrier   `json:"barrier"`
	Signal             string         `json:"signal"`
	RunStatus          string         `json:"run_status,omitempty"`
	RunPhase           string         `json:"run_phase,omitempty"`
	ReportStatus       string         `json:"report_status,omitempty"`
	CompletedPeerRun   string         `json:"completed_peer_run,omitempty"`
	Totals             *budget.Totals `json:"budget_totals,omitempty"`
	SourceFiles        int            `json:"source_entries"`
	SourceUnchanged    bool           `json:"source_unchanged"`
	RepeatedAuditEqual bool           `json:"repeated_audit_equal"`
}

func TestAuditSIGKILLCheckpoints(t *testing.T) {
	if os.Getenv("HARNESS_CRASH_ACCEPTANCE") != "1" {
		t.Skip("set HARNESS_CRASH_ACCEPTANCE=1 for real subprocess SIGKILL acceptance")
	}
	for _, point := range []string{"initialized", "baseline_passed", "model_pending", "report_published", "run_terminal"} {
		t.Run(point, func(t *testing.T) {
			parent := t.TempDir()
			if output := os.Getenv("HARNESS_CRASH_OUTPUT"); output != "" {
				if err := os.MkdirAll(output, 0700); err != nil {
					t.Fatal(err)
				}
				var err error
				parent, err = os.MkdirTemp(output, point+"-")
				if err != nil {
					t.Fatal(err)
				}
			}
			parent, err := filepath.EvalSymlinks(parent)
			if err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(parent, "campaign")
			cmd, ready, done, _ := startCrashChild(t, point, directory, filepath.Join(parent, "child.log"))
			// 不用 sleep 猜窗口：子进程只有落盘完成并进入阻塞点后才发这条消息。
			var barrier crashBarrier
			select {
			case event, ok := <-ready:
				if !ok {
					t.Fatalf("child did not report a checkpoint; see %s", parent)
				}
				barrier = event
			case err := <-done:
				t.Fatalf("child exited before checkpoint: %v; see %s", err, parent)
			case <-time.After(4 * time.Minute):
				t.Fatalf("checkpoint timed out; see %s", parent)
			}
			if barrier.PID != cmd.Process.Pid || barrier.Point != point {
				t.Fatalf("wrong child checkpoint: %+v", barrier)
			}
			record := crashRead[campaign.Record](t, filepath.Join(directory, "campaign.json"))
			if record.Status != campaign.Running {
				t.Fatal(record)
			}
			evidence := crashEvidence{SchemaVersion: 1, Barrier: barrier, Signal: "SIGKILL"}
			var peerBefore map[string]string
			if point != "initialized" {
				peer := crashRead[campaign.TargetRecord](t, filepath.Join(directory, "targets/completed/target.json"))
				if peer.State != campaign.Finished || peer.Attempt == nil || peer.Attempt.Status != campaign.Completed {
					t.Fatalf("peer not completed: %+v", peer)
				}
				evidence.CompletedPeerRun = peer.Attempt.Directory
				peerBefore = crashSnapshot(t, filepath.Join(directory, peer.Attempt.Directory))
				target := crashRead[campaign.TargetRecord](t, filepath.Join(directory, "targets/interrupted/target.json"))
				if target.State != campaign.Active || target.Attempt != nil {
					t.Fatal(target)
				}
				ledger := crashRead[budget.Snapshot](t, filepath.Join(directory, "budget/budget.json"))
				evidence.Totals = &ledger.Totals
				entries, err := os.ReadDir(filepath.Join(directory, "targets/interrupted/runs"))
				if err != nil || len(entries) != 1 {
					t.Fatalf("run directories: %v %v", entries, err)
				}
				runDir := filepath.Join(directory, "targets/interrupted/runs", entries[0].Name())
				run := crashRead[Record](t, filepath.Join(runDir, "run.json"))
				evidence.RunStatus = string(run.Status)
				evidence.RunPhase = string(run.Phase)
				if barrier.RunID != "" && barrier.RunID != run.ID {
					t.Fatal("barrier run identity mismatch")
				}
				if point == "run_terminal" {
					if run.Status != StatusCompleted || run.Verdict == "" {
						t.Fatal(run)
					}
				} else if run.Status != StatusRunning || run.Verdict != "" {
					t.Fatal(run)
				}
				want := budget.Totals{Calls: 2, KnownTokens: 40}
				switch point {
				case "baseline_passed":
					want = budget.Totals{Calls: 1, KnownTokens: 20}
					b := crashRead[BaselineReport](t, filepath.Join(runDir, "preflight/baseline.json"))
					if b.Status != "pass" || run.Phase != PhaseCheckingBaseline {
						t.Fatal(b.Status, run.Phase)
					}
				case "model_pending":
					want = budget.Totals{Calls: 2, KnownTokens: 20, ReservedTokens: 100}
					if ledger.Calls[len(ledger.Calls)-1].State != budget.StateReserved || run.Phase != PhaseGenerating {
						t.Fatal(ledger.Calls, run.Phase)
					}
					call := ledger.Calls[len(ledger.Calls)-1]
					if call.TargetID != "interrupted" || call.RunID != run.ID || ledger.Targets["interrupted"].RunID != run.ID {
						t.Fatal("reservation is not bound to the interrupted run")
					}
				case "report_published", "run_terminal":
					report := crashRead[Report](t, filepath.Join(runDir, "artifacts/report.json"))
					evidence.ReportStatus = string(report.Status)
					if report.Status != StatusCompleted || report.Validation == nil || report.PatchSHA256 == "" || run.Phase != PhasePublishing {
						t.Fatal(report.Status, run.Phase)
					}
					if point == "report_published" && run.PatchSHA256 != "" {
						t.Fatal("terminal patch pin was already persisted")
					}
				}
				if ledger.Totals != want {
					t.Fatalf("totals=%+v, expected %+v", ledger.Totals, want)
				}
			} else {
				if _, err := os.Stat(filepath.Join(directory, "budget/budget.json")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("initialization checkpoint already has a budget")
				}
			}
			// 上述断言直接复读磁盘；此时子进程仍阻塞在同一个边界。
			if err = cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			select {
			case err = <-done:
				var exit *exec.ExitError
				if !errors.As(err, &exit) {
					t.Fatalf("expected signal exit, got %v", err)
				}
				status, ok := exit.Sys().(syscall.WaitStatus)
				if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
					t.Fatalf("not SIGKILL: %v", exit)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("killed child was not reaped")
			}
			before := crashSnapshot(t, directory)
			evidence.SourceFiles = len(before)
			first, err := audit.Inspect(context.Background(), audit.Request{CampaignDir: directory, OutputDir: filepath.Join(parent, "audit-first")}, InspectRun)
			if err != nil || first.Consistency != "pass" || !first.ScanComplete || first.ExecutionState != "incomplete" {
				t.Fatalf("%v violations=%+v gaps=%+v state=%s; see %s", err, first.Violations, first.Gaps, first.ExecutionState, parent)
			}
			second, err := audit.Inspect(context.Background(), audit.Request{CampaignDir: directory, OutputDir: filepath.Join(parent, "audit-second")}, InspectRun)
			if err != nil || !reflect.DeepEqual(first, second) {
				t.Fatalf("repeated audit differed: %v", err)
			}
			evidence.RepeatedAuditEqual = true
			after := crashSnapshot(t, directory)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("read-only audit changed stopped campaign")
			}
			evidence.SourceUnchanged = true
			if peerBefore != nil && !reflect.DeepEqual(peerBefore, crashSnapshot(t, filepath.Join(directory, evidence.CompletedPeerRun))) {
				t.Fatal("completed peer changed")
			}
			if point == "model_pending" && !auditHas(first.Observations, "unresolved_reservation") {
				t.Fatal("missing unresolved reservation observation")
			}
			if point == "report_published" && !auditHas(first.Observations, "publication_pending") {
				t.Fatal("missing publication observation")
			}
			auditPut(t, parent, "fault-evidence.json", evidence)
			t.Logf("checkpoint=%s pid=%d signal=SIGKILL source_entries=%d output=%s", point, barrier.PID, len(before), parent)
		})
	}
}

func TestAuditUninterruptedCampaignControl(t *testing.T) {
	if os.Getenv("HARNESS_CRASH_ACCEPTANCE") != "1" {
		t.Skip("set HARNESS_CRASH_ACCEPTANCE=1 for the real subprocess control")
	}
	parent := t.TempDir()
	if output := os.Getenv("HARNESS_CRASH_OUTPUT"); output != "" {
		if err := os.MkdirAll(output, 0700); err != nil {
			t.Fatal(err)
		}
		var err error
		parent, err = os.MkdirTemp(output, "complete-control-")
		if err != nil {
			t.Fatal(err)
		}
	}
	parent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(parent, "campaign")
	_, _, done, _ := startCrashChild(t, "complete", directory, filepath.Join(parent, "child.log"))
	select {
	case err = <-done:
		if err != nil {
			t.Fatalf("uninterrupted child: %v; see %s", err, parent)
		}
	case <-time.After(4 * time.Minute):
		t.Fatalf("control timed out; see %s", parent)
	}
	record := crashRead[campaign.Record](t, filepath.Join(directory, "campaign.json"))
	summary := crashRead[campaign.Summary](t, filepath.Join(directory, "summary.json"))
	if record.Status != campaign.Completed || summary.Counts.Completed != 2 || summary.Budget.Totals != (budget.Totals{Calls: 2, KnownTokens: 40}) {
		t.Fatalf("%+v %+v", record, summary)
	}
	before := crashSnapshot(t, directory)
	report, err := audit.Inspect(context.Background(), audit.Request{CampaignDir: directory, OutputDir: filepath.Join(parent, "audit")}, InspectRun)
	if err != nil || report.Consistency != "pass" || report.ExecutionState != "completed" || !report.ScanComplete {
		t.Fatalf("%v %+v", err, report)
	}
	checked, err := recovery.Check(context.Background(), recovery.Request{
		CampaignDir: directory, OutputDir: filepath.Join(parent, "recovery-check"),
		ExpectedPlanSHA256: record.PlanSHA256, InspectRun: InspectRun,
		// 仅认证本对照：可信夹具的同步命令均已回收，且已 Wait 到所属宿主正常退出。
		VerifyResourcesStopped: func(ctx context.Context, _ string) error { return ctx.Err() },
	})
	if err != nil || checked.Audit.Consistency != "pass" || checked.Audit.ExecutionState != "completed" {
		t.Fatalf("recovery check did not accept complete rpcx evidence: %v %+v", err, checked)
	}
	if !reflect.DeepEqual(before, crashSnapshot(t, directory)) {
		t.Fatal("audit or recovery check changed control campaign")
	}
	auditPut(t, parent, "control-evidence.json", struct {
		Signal          string        `json:"signal"`
		Completed       int           `json:"completed_runs"`
		Totals          budget.Totals `json:"budget_totals"`
		SourceEntries   int           `json:"source_entries"`
		SourceUnchanged bool          `json:"source_unchanged"`
		RecoveryChecked bool          `json:"recovery_checked"`
	}{"none", 2, summary.Budget.Totals, len(before), true, true})
	t.Logf("uninterrupted control output=%s", parent)
}
