package baseline_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/baseline"
)

type hostExecutor struct {
	calls int
	mount string
}

func (f *hostExecutor) Info() execution.Info { return execution.Info{Kind: execution.KindDocker} }
func (f *hostExecutor) Run(_ context.Context, r execution.Request) (execution.Result, error) {
	f.calls++
	if !slices.Contains(r.ReadOnlyDirs, f.mount) {
		return execution.Result{ExitCode: 1}, nil
	}
	return execution.Result{ExitCode: 0}, nil
}

// 替身仅核对宿主装配与防篡改；真实容器结果单独保留。
func TestPublishedHostBackendIsolation(t *testing.T) {
	files, err := l2.GHCLIMaterials()
	if err != nil {
		t.Fatal(err)
	}
	before, err := l2.HostMaterialsSHA256Version(files, baseline.GHCLIVersion)
	if err != nil || before == "" {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "modules")
	fake := &hostExecutor{mount: root}
	backend, err := l2.PrepareBackendVersion(context.Background(), root, fake, files, baseline.GHCLIVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Run(context.Background(), execution.Request{Args: []string{"go", "version"}}); err != nil || fake.calls != 1 {
		t.Fatal("mounted backend did not execute", err)
	}
	if err := os.WriteFile(filepath.Join(root, "unexpected"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Run(context.Background(), execution.Request{Args: []string{"go", "version"}}); err == nil || fake.calls != 1 {
		t.Fatal("changed cache reached executor")
	}
	after, err := l2.HostMaterialsSHA256Version(files, baseline.GHCLIVersion)
	if err != nil || before != after {
		t.Fatal("published materials changed", err)
	}
	files["dependencies/bundle.json"] = append(files["dependencies/bundle.json"], byte(' '))
	if _, err := l2.PrepareBackendVersion(context.Background(), filepath.Join(t.TempDir(), "modules"), fake, files, baseline.GHCLIVersion); err == nil {
		t.Fatal("changed host material accepted")
	}
}
