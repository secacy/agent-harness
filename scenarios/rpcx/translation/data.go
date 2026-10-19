// Package translation 验证固定翻译切片的双配置 SDK 迁移。
package translation

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
	"github.com/secacy/agent-harness/internal/task"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/baseline"
)

const SpecVersion = "v0.4.0"
const SpecificationSHA256 = "d45937d461cf056a82aba50741a27ba3c991c73967047e0d4e723cc70feb5e5f"
const VerifierVersion = "rpcx-translation-evidence-v1"

//go:embed specs/*.md probes/*.txt *.go
var materials embed.FS

func Spec() []byte       { data, _ := materials.ReadFile("specs/v0.4.0.md"); return data }
func SpecSHA256() string { return SpecificationSHA256 }
func Configurations() []task.BuildConfiguration {
	return []task.BuildConfiguration{{ID: "default", Tags: []string{}}, {ID: "rpcx_alternate", Tags: []string{"rpcx_alternate"}}}
}
func HostMaterialsSHA256() (string, error) {
	files, err := l2.TranslationMaterials()
	if err != nil {
		return "", err
	}
	return l2.HostMaterialsSHA256(files), nil
}
func PrepareBackend(ctx context.Context, root string, backend execution.Executor) (*l2.ModuleBackend, error) {
	files, err := l2.TranslationMaterials()
	if err != nil {
		return nil, err
	}
	return l2.PrepareBackend(ctx, root, backend, files)
}
func LoadFixtures(root string) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, name := range []string{"translation", "rpcx-v1", "rpcx-v2"} {
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
	data, err := l2.TranslationMaterials()
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
		return fmt.Errorf("unsupported Translation fixture/SDK snapshot")
	}
	return nil
}
func RequiredTests() ([]string, error) {
	files, err := l2.TranslationMaterials()
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
	m := inventory.Modules["translation"]
	return append(m.Original, m.Added...), nil
}
func ValidateConfiguration(data []byte, id string) error {
	return baseline.ValidateTranslationConfiguration(data, id)
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
