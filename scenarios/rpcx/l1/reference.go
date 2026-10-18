package l1

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/secacy/agent-harness/internal/execution"
)

func runCommand(parent context.Context, backend execution.Executor, cache, root, label, dir string, args ...string) (execution.Result, Command, error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	start := time.Now()
	mounts := []string{dir}
	if filepath.Base(dir) == "demo-orders" {
		mounts = append(mounts, filepath.Join(filepath.Dir(dir), "rpcx-v1"), filepath.Join(filepath.Dir(dir), "rpcx-v2"))
	}
	r, err := backend.Run(ctx, execution.Request{Args: args, Directory: dir, ReadOnlyDirs: mounts, CacheDir: cache})
	meta := Command{Args: args, Directory: dir, Stdout: label + ".stdout.jsonl", Stderr: label + ".stderr.log", ExitCode: r.ExitCode, ContainerID: r.ContainerID, DurationMS: time.Since(start).Milliseconds()}
	err = errors.Join(err, os.WriteFile(filepath.Join(root, meta.Stdout), r.Stdout, 0644), os.WriteFile(filepath.Join(root, meta.Stderr), r.Stderr, 0644))
	if r.OOMKilled || r.OutputLimit {
		err = errors.Join(err, errors.New("L1 command exceeded resource limits"))
	}
	return r, meta, err
}

func verifyReference(ctx context.Context, backend execution.Executor, cache, root string, candidate, trustedSDK, oracle map[string][]byte, quick bool, r *Result) (err error) {
	module := filepath.Join(root, "reference/demo-orders")
	prod := production(candidate)
	r.Reference = Reference{ProductionSHA256: treeHash(prod), MaterialSHA256: treeHash(oracle), Build: "not_run", Fast: Observation{Status: "not_run"}, Slow: Observation{Status: "not_run"}, Scope: "default build; listed boundary/request/error paths plus alternate Place request; no test-integrity, dependency or all-configuration proof"}
	if r.Reference.ProductionSHA256 != r.ProductionSHA256 {
		return errors.New("reference production does not match candidate")
	}
	if err := writeTree(module, prod); err != nil {
		return err
	}
	// 独立参考只使用冻结 SDK，不用候选对 SDK 的修改解释行为正确性。
	if err := writeTree(filepath.Join(root, "reference"), trustedSDK); err != nil {
		return err
	}
	for name, data := range oracle {
		if err := writeTree(filepath.Join(module, "cmd/server"), map[string][]byte{name: data}); err != nil {
			return err
		}
	}
	before, err := readTree(ctx, filepath.Join(root, "reference"))
	if err != nil {
		return err
	}
	defer func() {
		after, e := readTree(context.WithoutCancel(ctx), filepath.Join(root, "reference"))
		if e != nil || treeHash(after) != treeHash(before) {
			err = errors.Join(err, errors.New("reference copy changed during execution"), e)
		}
	}()
	run := func(label string, args ...string) (execution.Result, error) {
		output, meta, err := runCommand(ctx, backend, cache, root, label, module, args...)
		r.Commands = append(r.Commands, meta)
		return output, err
	}
	out := "/tmp/"
	if backend.Info().Kind == execution.KindLocal {
		out = filepath.Join(root, "build")
		if err := os.MkdirAll(out, 0755); err != nil {
			return err
		}
		out += string(filepath.Separator)
	}
	build, err := run("reference-build", "go", "build", "-o", out, "./...")
	if err != nil {
		return err
	}
	r.Reference.Build = "pass"
	if build.ExitCode != 0 {
		r.Reference.Build, r.Reference.Fast.Status = "fail", "blocked"
		return nil
	}
	fast, err := run("reference-fast", "go", "test", "-json", "-count=1", "-timeout=30s", "-run=^TestReference(Boundary|Request|ServiceError|AlternateRequest)$", "./cmd/server")
	if err != nil {
		return err
	}
	r.Reference.Fast, err = observe(fast.Stdout, fast.ExitCode, false)
	if err != nil {
		return err
	}
	if !quick && r.Reference.Fast.Status == "pass" {
		slow, err := run("reference-expiry", "go", "test", "-json", "-count=1", "-timeout=90s", "-parallel=2", "-run=^TestReferenceExpiry$", "./cmd/server")
		if err != nil {
			return err
		}
		r.Reference.Slow, err = observe(slow.Stdout, slow.ExitCode, true)
		if err != nil {
			return err
		}
	}
	return nil
}
