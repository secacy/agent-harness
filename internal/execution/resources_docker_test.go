package execution

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestOwnedDockerConcurrentCommandCleanup(t *testing.T) {
	d := dockerForTest(t, 0)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	policy, err := d.ResourcePolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if output := os.Getenv("HARNESS_RECOVERY_ARTIFACTS"); output != "" {
		if err := os.MkdirAll(output, 0700); err != nil {
			t.Fatal(err)
		}
		root, err = os.MkdirTemp(output, "owned-concurrent-")
		if err != nil {
			t.Fatal(err)
		}
	}
	root, _ = filepath.EvalSymlinks(root)
	registry := ResourceRegistry{SchemaVersion: 1, CampaignID: "parallel", PlanSHA256: strings.Repeat("a", 64), Policy: policy}
	if err := CreateResourceRegistry(root, registry); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0755); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for n := range 4 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			o := ResourceOwner{Root: root, Registry: registry, TargetID: "target", RunID: fmt.Sprintf("run-%d", n), RunDirectory: fmt.Sprintf("targets/target/runs/run-%d", n)}
			result, err := d.Run(ctx, Request{Directory: source, ReadOnlyDirs: []string{source}, Args: []string{"sh", "-c", "sleep 0.2; printf concurrent"}, Owner: &o})
			if err != nil || result.ExitCode != 0 || string(result.Stdout) != "concurrent" {
				t.Error(result, err)
			}
		}(n)
	}
	wg.Wait()
	entries, err := ReadResources(root, registry)
	if err != nil || len(entries) != 4 {
		t.Fatal(entries, err)
	}
	for _, e := range entries {
		if e.Removed == nil {
			t.Fatal("concurrent command left an unconfirmed resource", e.Intent.CommandID)
		}
	}
	r, err := d.ReconcileResources(ctx, root, registry, false)
	if err != nil || !r.Stopped {
		t.Fatal(r, err)
	}
	if err := publishResource(filepath.Join(root, "evidence.json"), r); err != nil {
		t.Fatal(err)
	}
	t.Log("concurrent owned command evidence:", root)
}
