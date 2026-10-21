//go:build darwin || linux

package recovery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/audit"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/task"
	"github.com/secacy/agent-harness/internal/writer"
)

type controlledModel struct{ profile budget.Profile }

func (m controlledModel) BudgetProfile() (budget.Profile, error) { return m.profile, nil }
func (controlledModel) Complete(context.Context, []agent.Message, []agent.ToolSpec) (agent.Reply, error) {
	panic("recovery checks must not call the model")
}

func controlledConfig(directory string) campaign.Config {
	hash := strings.Repeat("a", 64)
	profile := budget.Profile{Provider: "controlled", Endpoint: "in-process", Model: "check-only", ContextTokens: 90, MaxOutputTokens: 10, SourceSHA256: hash, VerifiedAt: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)}
	limits := budget.Limits{MaxCalls: 5, MaxTokens: 500}
	input := task.Target{ID: "orders", ProfileID: "controlled", BuildConfig: "default", Repository: task.Snapshot{Directory: filepath.Join(filepath.Dir(directory), "input"), SHA256: hash}, Specification: task.Specification{Path: filepath.Join(filepath.Dir(directory), "spec.md"), SHA256: hash}}
	return campaign.Config{
		Directory: directory, Backend: execution.Local{},
		Plan:     campaign.Plan{ID: "writer-check", Concurrency: 1, ExecutionConcurrency: 1, Budget: limits, Profile: profile, Targets: []campaign.Target{{Input: input, Budget: limits, Limits: agent.Limits{MaxModelCalls: 5, MaxToolCalls: 10}, Timeout: time.Minute}}},
		NewModel: func(task.Target) (budget.ProfiledModel, error) { return controlledModel{profile}, nil },
		Execute: func(context.Context, campaign.Target, campaign.Services) (campaign.Attempt, error) {
			return campaign.Attempt{Status: campaign.Failed, FailureCode: "controlled"}, errors.New("controlled pre-run failure")
		},
	}
}

func inspectNoRun(context.Context, *audit.View, audit.RunRef) (audit.RunInspection, error) {
	return audit.RunInspection{}, errors.New("unexpected Run in control-only fixture")
}

func checkRequest(root, output, pin string) Request {
	return Request{CampaignDir: root, OutputDir: output, ExpectedPlanSHA256: pin, InspectRun: inspectNoRun,
		VerifyResourcesStopped: func(context.Context, string) error { return nil }}
}

func save(t *testing.T, root, name string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err == nil {
		err = os.WriteFile(filepath.Join(root, name), append(data, '\n'), 0600)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func load[T any](t *testing.T, root, name string) T {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	var result T
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		switch {
		case e.IsDir():
			result[rel] = "directory"
		case e.Type()&os.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			result[rel] = "link:" + target
		default:
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			hash := sha256.Sum256(data)
			result[rel] = hex.EncodeToString(hash[:])
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCheckRecoveryPrerequisites(t *testing.T) {
	for _, mode := range []string{"valid", "legacy", "future", "duplicate", "unknown-field", "protocol", "missing-lock", "lock-link", "busy", "plan-pin", "plan-bytes", "ledger-totals", "missing-ledger", "halted", "pending-transaction", "resources-missing", "resources-active", "inspector-missing", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			config := controlledConfig(filepath.Join(t.TempDir(), "campaign"))
			record, _, err := campaign.Run(context.Background(), config)
			if err != nil {
				t.Fatal(err)
			}
			root := record.Directory
			request := checkRequest(root, filepath.Join(t.TempDir(), "audit"), record.PlanSHA256)
			var held *writer.Lease
			switch mode {
			case "legacy":
				record.SchemaVersion, record.WriterProtocol = 1, ""
				save(t, root, "campaign.json", record)
				err = os.Remove(filepath.Join(root, writer.FileName))
			case "future":
				record.SchemaVersion = 99
				save(t, root, "campaign.json", record)
			case "duplicate":
				p := filepath.Join(root, "campaign.json")
				data, _ := os.ReadFile(p)
				data = bytes.Replace(data, []byte(`"schema_version": 2`), []byte(`"schema_version":2,"schema_version":2`), 1)
				err = os.WriteFile(p, data, 0600)
			case "unknown-field":
				data := load[map[string]any](t, root, "campaign.json")
				data["future"] = true
				save(t, root, "campaign.json", data)
			case "protocol":
				record.WriterProtocol = "pid-v1"
				save(t, root, "campaign.json", record)
			case "missing-lock":
				err = os.Remove(filepath.Join(root, writer.FileName))
			case "lock-link":
				p := filepath.Join(root, writer.FileName)
				if err = os.Remove(p); err == nil {
					err = os.Symlink(filepath.Join(t.TempDir(), "outside"), p)
				}
			case "busy":
				held, err = writer.Open(root)
				if held != nil {
					defer held.Close()
				}
			case "plan-pin":
				request.ExpectedPlanSHA256 = strings.Repeat("f", 64)
			case "plan-bytes":
				plan := config.Plan
				plan.ID = "other"
				save(t, root, "plan.json", plan)
			case "ledger-totals":
				ledger := load[budget.Snapshot](t, root, "budget/budget.json")
				ledger.Totals.KnownTokens = 1
				save(t, root, "budget/budget.json", ledger)
			case "missing-ledger":
				err = os.Remove(filepath.Join(root, "budget/budget.json"))
			case "halted":
				ledger := load[budget.Snapshot](t, root, "budget/budget.json")
				ledger.HaltReason = "controlled persistence fault"
				save(t, root, "budget/budget.json", ledger)
				summary := load[campaign.Summary](t, root, "summary.json")
				summary.Budget = ledger
				save(t, root, "summary.json", summary)
				data, _ := os.ReadFile(filepath.Join(root, "summary.json"))
				hash := sha256.Sum256(data)
				record.Summary.SHA256 = hex.EncodeToString(hash[:])
				record.Status, record.Error = campaign.Failed, "controlled control fault"
				save(t, root, "campaign.json", record)
			case "pending-transaction":
				err = os.Mkdir(filepath.Join(root, "recoveries"), 0700)
			case "resources-missing":
				request.VerifyResourcesStopped = nil
			case "resources-active":
				request.VerifyResourcesStopped = func(context.Context, string) error { return errors.New("owned command is still active") }
			case "inspector-missing":
				request.InspectRun = nil
			}
			if err != nil {
				t.Fatal(err)
			}
			before := tree(t, root)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			result, checkErr := Check(ctx, request)
			if (mode == "valid") != (checkErr == nil) {
				t.Fatalf("mode=%s result=%+v err=%v", mode, result, checkErr)
			}
			if mode == "valid" && (result.Audit.Consistency != "pass" || result.CampaignID != record.ID) {
				t.Fatal(result)
			}
			if mode == "halted" && result.Audit.Consistency != "pass" {
				t.Fatalf("halt must block independently of audit pass: %+v", result.Audit)
			}
			if mode == "busy" && !errors.Is(checkErr, writer.ErrBusy) || mode == "legacy" && !errors.Is(checkErr, ErrUnsupported) {
				t.Fatal(checkErr)
			}
			if !reflect.DeepEqual(before, tree(t, root)) {
				t.Fatal("recovery check changed source bytes or directory members")
			}
			if held == nil && mode != "missing-lock" && mode != "legacy" && mode != "lock-link" {
				l, err := writer.Open(root)
				if err != nil {
					t.Fatal("check leaked writer ownership", err)
				}
				_ = l.Close()
			}
		})
	}
}

func TestCheckHoldsOwnershipAndDetectsChangedHeader(t *testing.T) {
	config := controlledConfig(filepath.Join(t.TempDir(), "campaign"))
	record, _, err := campaign.Run(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	request := checkRequest(record.Directory, filepath.Join(t.TempDir(), "audit"), record.PlanSHA256)
	request.VerifyResourcesStopped = func(context.Context, string) error {
		l, err := writer.Open(record.Directory)
		if !errors.Is(err, writer.ErrBusy) {
			if l != nil {
				_ = l.Close()
			}
			t.Fatal("resources checked without writer ownership", err)
		}
		// 故意违反可信回调的只读约定，验证身份复核会拒绝该变化。
		record.StartedAt = record.StartedAt.Add(-time.Second)
		save(t, record.Directory, "campaign.json", record)
		return nil
	}
	if _, err = Check(context.Background(), request); !errors.Is(err, ErrBlocked) {
		t.Fatal("changed header was accepted", err)
	}
}
