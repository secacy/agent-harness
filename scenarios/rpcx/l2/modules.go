package l2

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/baseline"
)

// ModuleBackend 为可信宿主装配固定依赖，不向 Agent 注册文件根或额外工具。
type ModuleBackend struct {
	base    execution.Executor
	root    string
	files   map[string]string
	version string
}

func HostMaterialsSHA256(files map[string][]byte) string {
	return gate.TreeHash(map[string][]byte{"modules.json": files["dependencies/modules.json"], "modules.tar.gz": files["dependencies/modules.tar.gz"]})
}

// PrepareBackend 接收调用方已认证的发布材料，只允许新的空目录。
// 底层执行器的槽位和回收规则继续生效。
func PrepareBackend(ctx context.Context, directory string, base execution.Executor, files map[string][]byte) (*ModuleBackend, error) {
	return PrepareBackendVersion(ctx, directory, base, files, baseline.Version)
}

// PrepareBackendVersion 的策略由已知发布版本选择，不能由索引声明提高。
func PrepareBackendVersion(ctx context.Context, directory string, base execution.Executor, files map[string][]byte, version string) (*ModuleBackend, error) {
	if base == nil {
		return nil, errors.New("execution backend is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(root), 0755); err != nil {
		return nil, err
	}
	if err := os.Mkdir(root, 0755); err != nil {
		return nil, err
	}
	if err := baseline.ExtractModulesVersion(files, root, version); err != nil {
		return nil, err
	}
	var m struct {
		Files map[string]string `json:"files"`
	}
	if err := json.Unmarshal(files["dependencies/modules.json"], &m); err != nil {
		return nil, err
	}
	backend := &ModuleBackend{base: base, root: root, files: m.Files, version: version}
	if err := gate.Write(root+".json", files["dependencies/modules.json"]); err != nil {
		return nil, err
	}
	if version == baseline.GHCLIVersion {
		for name, data := range files {
			if strings.HasPrefix(name, "dependencies/") && name != "dependencies/modules.json" {
				if err := gate.Write(filepath.Join(root+".bundle", strings.TrimPrefix(name, "dependencies/")), data); err != nil {
					return nil, err
				}
			}
		}
	} else if err := gate.Write(root+".tar.gz", files["dependencies/modules.tar.gz"]); err != nil {
		return nil, err
	}
	return backend, backend.Check(ctx)
}
func (b *ModuleBackend) Info() execution.Info { return b.base.Info() }
func (b *ModuleBackend) Environment() map[string]string {
	root := b.root
	if b.Info().Kind == execution.KindDocker {
		root = "/workspace/" + filepath.Base(root)
	}
	return map[string]string{"GOMODCACHE": root}
}
func (b *ModuleBackend) Run(ctx context.Context, req execution.Request) (result execution.Result, err error) {
	if err := b.Check(ctx); err != nil {
		return execution.Result{ExitCode: -1}, err
	}
	defer func() { err = errors.Join(err, b.Check(context.WithoutCancel(ctx))) }()
	req.Args = append([]string{"env", "GOMODCACHE=" + b.Environment()["GOMODCACHE"]}, req.Args...)
	req.ReadOnlyDirs = append(append([]string{}, req.ReadOnlyDirs...), b.root)
	return b.base.Run(ctx, req)
}
func (b *ModuleBackend) Check(ctx context.Context) error {
	version := b.version
	if version == "" {
		version = baseline.Version
	}
	return baseline.CheckModuleTree(ctx, b.root, b.files, version)
}

// HostMaterialsSHA256Version 为新分片表示逐件绑定身份，旧表示保持原算法。
func HostMaterialsSHA256Version(files map[string][]byte, version string) (string, error) {
	if _, err := baseline.ModulePolicy(version); err != nil {
		return "", err
	}
	if version != baseline.GHCLIVersion {
		return HostMaterialsSHA256(files), nil
	}
	selected := map[string][]byte{}
	for name, data := range files {
		if strings.HasPrefix(name, "dependencies/") {
			selected[strings.TrimPrefix(name, "dependencies/")] = data
		}
	}
	return gate.TreeHash(selected), nil
}
