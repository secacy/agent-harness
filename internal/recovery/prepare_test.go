//go:build darwin || linux

package recovery

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/audit"
	"github.com/secacy/agent-harness/internal/campaign"
)

func prepareFixture(t *testing.T) (string, PrepareRequest) {
	t.Helper()
	config := controlledConfig(filepath.Join(t.TempDir(), "campaign"))
	record, _, err := campaign.Run(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	// 原始空白也属于封存身份，不能被 RawMessage 格式化改变。
	p := filepath.Join(record.Directory, "campaign.json")
	data, _ := os.ReadFile(p)
	if err := os.WriteFile(p, append(data, '\n', ' ', '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	return record.Directory, PrepareRequest{Request: checkRequest(record.Directory, filepath.Join(t.TempDir(), "audit"), record.PlanSHA256), ID: "recovery-one"}
}

func originalTree(t *testing.T, root string) map[string]string {
	t.Helper()
	all := tree(t, root)
	for p := range all {
		if p == "recoveries" || strings.HasPrefix(p, "recoveries"+string(filepath.Separator)) {
			delete(all, p)
		}
	}
	return all
}

func nextOutput(t *testing.T, request PrepareRequest) PrepareRequest {
	t.Helper()
	request.OutputDir = filepath.Join(t.TempDir(), "audit")
	return request
}

func TestPrepareCheckpointPreservesOriginalsAndIsIdempotent(t *testing.T) {
	root, request := prepareFixture(t)
	before := originalTree(t, root)
	raw, _ := os.ReadFile(filepath.Join(root, "campaign.json"))
	result, err := Prepare(context.Background(), request)
	if err != nil || result.CommitState != "Prepared" || result.Audit.Consistency != "pass" || result.State.Budget.SchemaVersion != 2 {
		t.Fatal(result, err)
	}
	c := load[campaign.RecoveryCheckpoint](t, root, result.Checkpoint.Path)
	if !bytes.Equal(c.Before["campaign.json"], raw) || c.After.Targets[0].Resolution != "Recorded" || len(c.After.Targets[0].Attempts) != 1 || c.After.Targets[0].Current.Status != campaign.Failed {
		t.Fatal(c)
	}
	all := tree(t, root)
	again, err := Prepare(context.Background(), nextOutput(t, request))
	if err != nil || again.Checkpoint != result.Checkpoint || !reflect.DeepEqual(all, tree(t, root)) || !reflect.DeepEqual(before, originalTree(t, root)) {
		t.Fatal("repeated prepare changed history or sources", err)
	}
	conflict := nextOutput(t, request)
	conflict.ID = "other"
	if _, err := Prepare(context.Background(), conflict); !errors.Is(err, ErrConflict) || !reflect.DeepEqual(all, tree(t, root)) {
		t.Fatal("conflicting recovery ID was accepted", err)
	}
	if _, err := Check(context.Background(), nextOutput(t, request).Request); !errors.Is(err, ErrBlocked) {
		t.Fatal("old check-only path accepted a prepared transaction", err)
	}
}

func TestPrepareEmptyArchivedControl(t *testing.T) {
	root, request := prepareFixture(t)
	if err := os.WriteFile(filepath.Join(root, "summary.md"), []byte{}, 0600); err != nil {
		t.Fatal(err)
	}
	result, err := Prepare(context.Background(), request)
	if err != nil || result.CommitState != CommitPrepared {
		t.Fatal(result, err)
	}
	c := load[campaign.RecoveryCheckpoint](t, root, result.Checkpoint.Path)
	if data, ok := c.Before["summary.md"]; !ok || data == nil || len(data) != 0 {
		t.Fatal("empty control bytes did not retain an explicit base64 value")
	}
}

func TestPreparePublicationFailuresAndReentry(t *testing.T) {
	for _, mode := range []string{"checkpoint-before", "checkpoint-after", "head-before", "head-after", "directory-cancel"} {
		t.Run(mode, func(t *testing.T) {
			root, request := prepareFixture(t)
			before := originalTree(t, root)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			injected := errors.New("uncertain publication")
			hooks := prepareHooks{write: func(p string, data []byte) error {
				match := strings.HasPrefix(filepath.Base(p), "checkpoint-") && strings.HasPrefix(mode, "checkpoint-") || filepath.Base(p) == "head.json" && strings.HasPrefix(mode, "head-")
				if match && strings.HasSuffix(mode, "before") {
					return injected
				}
				if err := publishImmutable(p, data); err != nil {
					return err
				}
				if match {
					return injected
				}
				return nil
			}, after: func(point string) {
				if mode == "directory-cancel" && point == "directory" {
					cancel()
				}
			}}
			result, err := prepare(ctx, request, hooks)
			if err == nil || mode != "directory-cancel" && result.CommitState != "Unknown" {
				t.Fatal("failed publication was reported as confirmed", result, err)
			}
			if !reflect.DeepEqual(before, originalTree(t, root)) {
				t.Fatal("publication failure changed old controls")
			}
			result, err = Prepare(context.Background(), nextOutput(t, request))
			if err != nil || result.CommitState != "Prepared" || !reflect.DeepEqual(before, originalTree(t, root)) {
				t.Fatal("cannot resume publication", result, err)
			}
		})
	}
}

func TestPreparedCheckpointTamperingIsRejected(t *testing.T) {
	for _, mode := range []string{"after", "before", "sources", "head", "future", "extra-file", "missing-payload"} {
		t.Run(mode, func(t *testing.T) {
			root, request := prepareFixture(t)
			result, err := Prepare(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			cp := load[campaign.RecoveryCheckpoint](t, root, result.Checkpoint.Path)
			head := load[campaign.RecoveryHead](t, root, "recoveries/head.json")
			switch mode {
			case "after":
				cp.After.Budget.Totals.KnownTokens++
				save(t, root, result.Checkpoint.Path, cp)
			case "before":
				cp.Before["campaign.json"] = append(cp.Before["campaign.json"], '\n')
				save(t, root, result.Checkpoint.Path, cp)
			case "sources":
				cp.Sources = cp.Sources[1:]
				save(t, root, result.Checkpoint.Path, cp)
			case "head":
				head.Checkpoint.SHA256 = strings.Repeat("f", 64)
				save(t, root, "recoveries/head.json", head)
			case "future":
				cp.SchemaVersion = 99
				save(t, root, result.Checkpoint.Path, cp)
			case "extra-file":
				if err := os.WriteFile(filepath.Join(root, "recoveries", "unrecognized"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-payload":
				if err := os.Remove(filepath.Join(root, result.Checkpoint.Path)); err != nil {
					t.Fatal(err)
				}
			}
			before := tree(t, root)
			inspection, err := audit.Inspect(context.Background(), audit.Request{CampaignDir: root, OutputDir: filepath.Join(t.TempDir(), "tamper-audit")}, inspectNoRun)
			if err != nil || inspection.Consistency == "pass" {
				t.Fatal("audit did not expose checkpoint tampering", inspection, err)
			}
			want := map[string]string{"after": "recovery_checkpoint_invalid", "before": "recovery_checkpoint_invalid", "sources": "recovery_checkpoint_invalid", "head": "recovery_head_mismatch", "future": "unsupported_schema", "extra-file": "recovery_entry_invalid", "missing-payload": "recovery_conflict"}[mode]
			found := false
			for _, f := range append(inspection.Violations, inspection.Gaps...) {
				found = found || f.Code == want
			}
			if !found {
				t.Fatal("missing expected reason", want, inspection)
			}
			if _, err := Prepare(context.Background(), nextOutput(t, request)); !errors.Is(err, ErrBlocked) {
				t.Fatal("tampered checkpoint was accepted", err)
			}
			if !reflect.DeepEqual(before, tree(t, root)) {
				t.Fatal("tampering was silently repaired")
			}
		})
	}
}
