package audit

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/secacy/agent-harness/internal/writer"
)

func TestAuditCampaignWriterFormats(t *testing.T) {
	for _, mode := range []string{"v1", "v2", "v1-protocol", "v2-missing-protocol", "v2-unknown-protocol", "v2-missing-lock", "v2-lock-content", "v2-lock-unreadable"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			if mode != "v1" && mode != "v1-protocol" {
				f.record.SchemaVersion = 2
				f.record.WriterProtocol = writer.Protocol
				putBytes(t, f.root, writer.FileName, nil)
			}
			switch mode {
			case "v1-protocol":
				f.record.WriterProtocol = writer.Protocol
			case "v2-missing-protocol":
				f.record.WriterProtocol = ""
			case "v2-unknown-protocol":
				f.record.WriterProtocol = "pid-v1"
			case "v2-missing-lock":
				if err := os.Remove(filepath.Join(f.root, writer.FileName)); err != nil {
					t.Fatal(err)
				}
			case "v2-lock-content":
				putBytes(t, f.root, writer.FileName, []byte("invalid"))
			case "v2-lock-unreadable":
				p := filepath.Join(f.root, writer.FileName)
				if err := os.Chmod(p, 0); err != nil {
					t.Fatal(err)
				}
				defer os.Chmod(p, 0600)
				if _, err := os.ReadFile(p); err == nil {
					t.Skip("current identity can read files without read permission")
				}
			}
			f.save(t)
			// 场景替身只声明覆盖完成，本测试检查控制格式，不认证业务证据。
			r := auditFixture(t, f, func(context.Context, *View, RunRef) (RunInspection, error) {
				return RunInspection{Complete: true}, nil
			})
			if mode == "v1" || mode == "v2" {
				if r.Consistency != "pass" || r.WriterState != "not_checked" {
					t.Fatal(r)
				}
			} else if mode == "v2-lock-unreadable" {
				if len(r.Violations) != 0 || r.Consistency != "inconclusive" || !has(r.Gaps, "source_unreadable", writer.FileName) {
					t.Fatal("unreadable lock must not become a definite violation", r)
				}
			} else if len(r.Violations) == 0 {
				t.Fatal("invalid ownership declaration accepted", r)
			}
		})
	}
}
