package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
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

func TestAblationProbeWithoutCredentialCannotStartExternalWork(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	project, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	p, err := ablation.Draft(project)
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	draft := filepath.Join(base, "draft.json")
	if err = gate.WriteJSON(draft, p); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(draft)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(base, "probe")
	err = probeAblation(context.Background(), draft, "", output)
	if err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("missing credential did not stop before Docker/provider work: %v", err)
	}
	if _, err = os.Lstat(output); !os.IsNotExist(err) {
		t.Fatal("failed credential check created probe output")
	}
	after, err := os.ReadFile(draft)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("metadata probe mutated draft")
	}
}
