package goddd

import (
	"context"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2"
)

// ModuleBackend 沿用 L2 宿主材料后端，保留既有场景 API。
type ModuleBackend = l2.ModuleBackend

func HostMaterialsSHA256() (string, error) {
	files, err := l2.GodddMaterials()
	if err != nil {
		return "", err
	}
	return l2.HostMaterialsSHA256(files), nil
}
func PrepareBackend(ctx context.Context, directory string, base execution.Executor) (*ModuleBackend, error) {
	files, err := l2.GodddMaterials()
	if err != nil {
		return nil, err
	}
	return l2.PrepareBackend(ctx, directory, base, files)
}
