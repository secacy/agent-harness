package audit

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/writer"
)

func TestRecoveryCheckpointTargetResolutions(t *testing.T) {
	for _, mode := range []string{"recorded", "adopted", "interrupted", "abandoned", "unstarted"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.record.SchemaVersion, f.record.WriterProtocol = 2, writer.Protocol
			putBytes(t, f.root, writer.FileName, nil)
			if mode != "recorded" {
				f.record.Status, f.record.FinishedAt, f.record.Summary = campaign.Running, nil, campaign.Artifact{}
				f.target.State, f.target.Attempt = campaign.Active, nil
			}
			if mode == "interrupted" {
				f.run.Status, f.run.FinishedAt, f.run.Verdict = "Running", nil, ""
				f.ledger.Calls[0].State, f.ledger.Calls[0].Usage, f.ledger.Calls[0].Outcome = budget.StateReserved, nil, ""
				f.ledger.Totals = budget.Totals{Calls: 1, ReservedTokens: 100}
				a := f.ledger.Targets["orders"]
				a.Totals = f.ledger.Totals
				f.ledger.Targets["orders"] = a
			}
			if mode == "abandoned" || mode == "unstarted" {
				f.ledger.Calls, f.ledger.Totals = []budget.Call{}, budget.Totals{}
				a := f.ledger.Targets["orders"]
				a.RunID, a.Totals = "", budget.Totals{}
				f.ledger.Targets["orders"] = a
				if mode == "unstarted" {
					f.target.State = campaign.Pending
				}
			}
			f.save(t)
			if mode != "recorded" {
				f.record.Summary = campaign.Artifact{}
				put(t, f.root, "campaign.json", f.record)
				_ = os.Remove(filepath.Join(f.root, "summary.json"))
				_ = os.Remove(filepath.Join(f.root, "summary.md"))
			}
			if mode == "abandoned" {
				_ = os.Remove(filepath.Join(f.root, runPath("orders", "run-1"), "run.json"))
			}
			if mode == "unstarted" {
				if err := os.RemoveAll(filepath.Join(f.root, runPath("orders", "run-1"))); err != nil {
					t.Fatal(err)
				}
			}
			// 控制层构造数据只验证索引与转换，不宣称执行过业务门禁。
			r := auditFixture(t, f, func(_ context.Context, v *View, ref RunRef) (RunInspection, error) {
				if mode == "adopted" || mode == "recorded" {
					if _, err := v.ReadFile(ref.Path + "/artifacts/report.json"); err != nil {
						return RunInspection{}, err
					}
				}
				return RunInspection{Complete: true}, nil
			})
			if r.Consistency != "pass" {
				t.Fatal(r)
			}
			files := map[string][]byte{}
			for _, s := range r.Sources {
				if s.Kind == "file" && RecoveryControlFile(s.Path) {
					data, err := os.ReadFile(filepath.Join(f.root, s.Path))
					if err != nil {
						t.Fatal(err)
					}
					files[s.Path] = data
				}
			}
			c, err := BuildRecoveryCheckpoint(r.Sources, files, "recovery", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			a := c.After.Targets[0]
			want := map[string]string{"recorded": "Recorded", "adopted": "Adopted", "interrupted": "Interrupted", "abandoned": "Interrupted", "unstarted": "Unstarted"}[mode]
			if a.Resolution != want {
				t.Fatal(a)
			}
			if mode == "recorded" || mode == "adopted" {
				if a.Current == nil || a.Current.Verdict != "NeedsAttention" || a.Attempts[0].Resolution != want {
					t.Fatal("quality or completed identity changed", a)
				}
			} else if a.Current != nil {
				t.Fatal("incomplete attempt was adopted", a)
			}
			if mode == "abandoned" && a.Attempts[0].Resolution != "Abandoned" || mode == "unstarted" && len(a.Attempts) != 0 {
				t.Fatal(a)
			}
			if mode == "interrupted" && c.After.Budget.Totals != (budget.Totals{Calls: 1, UnknownTokens: 100}) {
				t.Fatal(c.After.Budget)
			}
		})
	}
}
