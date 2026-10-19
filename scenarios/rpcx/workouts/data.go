// Package workouts 验证固定取消切片的回调与多模块 SDK 迁移。
package workouts

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"strings"

	"github.com/secacy/agent-harness/internal/execution"

	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2"
)

const SpecVersion = "v0.5.0"
const SpecificationSHA256 = "789d30540799249d0f5d96b3ba921b7e00d4cbc1e457491a2a79eba89829a526"
const VerifierVersion = "rpcx-workouts-evidence-v1"

//go:embed specs/*.md probes/*.txt *.go
var materials embed.FS

func Spec() []byte       { data, _ := materials.ReadFile("specs/v0.5.0.md"); return data }
func SpecSHA256() string { return SpecificationSHA256 }
func HostMaterialsSHA256() (string, error) {
	files, err := l2.WorkoutsMaterials()
	if err != nil {
		return "", err
	}
	return l2.HostMaterialsSHA256(files), nil
}
func PrepareBackend(ctx context.Context, root string, backend execution.Executor) (*l2.ModuleBackend, error) {
	files, err := l2.WorkoutsMaterials()
	if err != nil {
		return nil, err
	}
	return l2.PrepareBackend(ctx, root, backend, files)
}
func LoadFixtures(root string) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, name := range []string{"trainings", "common", "rpcx-v1", "rpcx-v2"} {
		files, err := gate.ReadFiles(filepath.Join(root, name))
		if err != nil {
			return nil, err
		}
		for p, data := range files {
			out[name+"/"+p] = data
		}
	}
	return out, authenticate(out)
}
func authenticate(files map[string][]byte) error {
	data, err := l2.WorkoutsMaterials()
	if err != nil {
		return err
	}
	var release struct {
		Files map[string]string `json:"files"`
	}
	if err = json.Unmarshal(data["freeze.json"], &release); err != nil {
		return err
	}
	expected := map[string]string{}
	for name, hash := range release.Files {
		if rel, ok := strings.CutPrefix(name, "fixtures/"); ok {
			expected[rel] = hash
		}
	}
	if !maps.Equal(gate.Hashes(files), expected) {
		return fmt.Errorf("unsupported Workouts fixture/SDK snapshot")
	}
	return nil
}
func RequiredTests() ([]string, error) { return ModuleTests("trainings") }

// ModuleTests 从已认证发布材料读取完整测试身份。
func ModuleTests(name string) ([]string, error) {
	files, err := l2.WorkoutsMaterials()
	if err != nil {
		return nil, err
	}
	var inventory struct {
		Modules map[string]struct {
			Original []string `json:"original_tests"`
			Added    []string `json:"added_tests"`
		} `json:"modules"`
	}
	if err = json.Unmarshal(files["tests.json"], &inventory); err != nil {
		return nil, err
	}
	m, ok := inventory.Modules[name]
	if !ok || len(m.Original)+len(m.Added) == 0 {
		return nil, fmt.Errorf("missing required module tests: %s", name)
	}
	return append(m.Original, m.Added...), nil
}
func materialFiles() (map[string][]byte, error) {
	out := map[string][]byte{}
	err := fs.WalkDir(materials, ".", func(name string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		if strings.HasSuffix(name, "_test.go") {
			return nil
		}
		data, err := materials.ReadFile(name)
		out[name] = data
		return err
	})
	return out, err
}
