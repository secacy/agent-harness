package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/secacy/agent-harness/scenarios/rpcx/l2/ablation"
)

func TestAblationPrepareNeedsNoProviderAndCannotLaunchDraft(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "draft")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"ablation", "prepare", "--project", root, "--output", out}, &stdout, &stderr); code != 0 {
		t.Fatalf("prepare %d: %s", code, stderr.String())
	}
	raw, err := os.ReadFile(filepath.Join(out, "protocol.draft.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p ablation.Protocol
	if err = ablation.Decode(raw, &p); err != nil {
		t.Fatal(err)
	}
	if len(p.Schedule) != 36 || p.Validate() == nil {
		t.Fatal("draft represented as formal executable protocol")
	}
	if _, err = os.Stat(filepath.Join(out, "generation-definitions.json")); err != nil {
		t.Fatal(err)
	}
	if code := run(context.Background(), []string{"ablation", "prepare", "--project", root, "--output", out}, &stdout, &stderr); code == 0 {
		t.Fatal("overwrote review materials")
	}
}
