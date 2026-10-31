package ablation

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestP1ExportRetainsScheduledUnknownRowsAndOriginalReports(t *testing.T) {
	p := testProtocol()
	summary, err := Summarize(p, nil)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "report")
	if err = WriteReport(output, summary); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(output, "results.csv"))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(raw))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 37 {
		t.Fatalf("CSV dropped schedule: %d", len(rows))
	}
	for _, row := range rows[1:] {
		if row[5] != "not_started" || row[9] != "inconclusive" || row[13] != "" || row[20] != "" {
			t.Fatalf("missing run became known or free: %+v", row)
		}
	}
	md, err := os.ReadFile(filepath.Join(output, "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "N/A (0 denominator)") || !strings.Contains(string(md), "previously seen") {
		t.Fatal("report omitted zero denominator or dataset limitation")
	}
	if err = WriteReport(output, summary); err == nil {
		t.Fatal("overwrote derived report")
	}
}
