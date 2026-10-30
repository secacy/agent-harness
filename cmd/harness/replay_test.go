package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOfflineCommandsDoNotInitializeModel(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	t.Setenv("DEEPSEEK_MODEL", "")
	for _, name := range []string{"replay", "failures"} {
		var out, errOut bytes.Buffer
		if code := run(context.Background(), []string{name, "--help"}, &out, &errOut); code != 0 {
			t.Fatalf("%s: %d %s", name, code, errOut.String())
		}
		out.Reset()
		errOut.Reset()
		if code := run(context.Background(), []string{name}, &out, &errOut); code != 2 {
			t.Fatalf("missing args %s: %d", name, code)
		}
		out.Reset()
		errOut.Reset()
		if code := run(context.Background(), []string{name, "--run", filepath.Join(t.TempDir(), "absent")}, &out, &errOut); code != 1 || strings.Contains(errOut.String(), "API key") {
			t.Fatalf("%s: %d %s", name, code, errOut.String())
		}
	}
}
func TestFailureCommandTableHasCountsAndUndefinedDenominator(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "artifacts"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.json"), []byte(`{"id":"r","status":"Completed","verdict":"ReadyForReview","finished_at":"2026-10-31T00:00:00Z"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "artifacts/report.json"), []byte(`{"run_id":"r","status":"Completed"}`), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"failures", "--run", dir}, &out, &errOut); code != 0 {
		t.Fatalf("%d %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "100.00%") || !strings.Contains(out.String(), "N/A") || !strings.Contains(out.String(), "success") {
		t.Fatal(out.String())
	}
	out.Reset()
	if code := run(context.Background(), []string{"failures", "--run", dir, "--json"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), `"failures":0`) {
		t.Fatalf("%d %s %s", code, out.String(), errOut.String())
	}
}
