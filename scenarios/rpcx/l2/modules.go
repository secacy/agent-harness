package l2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/baseline"
)

// ModuleBackend 为可信宿主装配固定依赖，不向 Agent 注册文件根或额外工具。
type ModuleBackend struct {
	base  execution.Executor
	root  string
	files map[string]string
}

func HostMaterialsSHA256(files map[string][]byte) string {
	return gate.TreeHash(map[string][]byte{"modules.json": files["dependencies/modules.json"], "modules.tar.gz": files["dependencies/modules.tar.gz"]})
}

// PrepareBackend 接收调用方已认证的发布材料，只允许新的空目录。
// 底层执行器的槽位和回收规则继续生效。
func PrepareBackend(ctx context.Context, directory string, base execution.Executor, files map[string][]byte) (*ModuleBackend, error) {
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
	if err := baseline.ExtractModules(files, root); err != nil {
		return nil, err
	}
	var m struct {
		Files map[string]string `json:"files"`
	}
	if err := json.Unmarshal(files["dependencies/modules.json"], &m); err != nil {
		return nil, err
	}
	backend := &ModuleBackend{base: base, root: root, files: m.Files}
	if err := gate.Write(root+".json", files["dependencies/modules.json"]); err != nil {
		return nil, err
	}
	if err := gate.Write(root+".tar.gz", files["dependencies/modules.tar.gz"]); err != nil {
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
	actual := map[string]string{}
	err := filepath.WalkDir(b.root, func(name string, e fs.DirEntry, err error) error {
		if err := errors.Join(err, ctx.Err()); err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 8<<20 {
			return fmt.Errorf("invalid host module material: %s", name)
		}
		rel, err := filepath.Rel(b.root, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if b.files[rel] == "" {
			return fmt.Errorf("unexpected host module material: %s", rel)
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		actual[rel] = gate.Digest(data)
		return nil
	})
	if err != nil {
		return err
	}
	if !maps.Equal(actual, b.files) {
		return errors.New("host module materials changed")
	}
	return nil
}
