// Package goddd 在固定业务切片上独立验证 rpcx 迁移。
package goddd

import (
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/secacy/agent-harness/scenarios/rpcx/l2"
)

const (
	SpecVersion         = "v0.3.0"
	VerifierVersion     = "rpcx-goddd-evidence-v1"
	SpecificationSHA256 = "917575e0f4fc571f6f72c743ffded00771da838144371435ab0edfb61b578610"
)

//go:embed specs/*.md probes/*.txt *.go
var materials embed.FS

func digest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }
func hashes(files map[string][]byte) map[string]string {
	out := map[string]string{}
	for name, data := range files {
		out[name] = digest(data)
	}
	return out
}
func treeHash(files map[string][]byte) string {
	data, _ := json.Marshal(hashes(files))
	return digest(data)
}
func Spec() []byte       { data, _ := materials.ReadFile("specs/v0.3.0.md"); return data }
func SpecSHA256() string { return SpecificationSHA256 }

// LoadFixtures 读取显式业务/SDK 快照，并以发布清单认证完整内容。
func LoadFixtures(root string) (map[string][]byte, error) {
	files := map[string][]byte{}
	for _, name := range []string{"goddd", "rpcx-v1", "rpcx-v2"} {
		m, err := readFiles(filepath.Join(root, name))
		if err != nil {
			return nil, err
		}
		for p, data := range m {
			files[name+"/"+p] = data
		}
	}
	return files, authenticateFixtures(files)
}
func authenticateFixtures(files map[string][]byte) error {
	data, err := l2.GodddMaterials()
	if err != nil {
		return err
	}
	var release struct {
		Files map[string]string `json:"files"`
	}
	if err := json.Unmarshal(data["freeze.json"], &release); err != nil {
		return err
	}
	expected := map[string]string{}
	for name, hash := range release.Files {
		if rel, ok := strings.CutPrefix(name, "fixtures/"); ok {
			expected[rel] = hash
		}
	}
	if !maps.Equal(hashes(files), expected) {
		return fmt.Errorf("unsupported Goddd fixture/SDK snapshot")
	}
	return nil
}

func module(files map[string][]byte, name string) map[string][]byte {
	out := map[string][]byte{}
	for path, data := range files {
		if rel, ok := strings.CutPrefix(path, name+"/"); ok {
			out[rel] = data
		}
	}
	return out
}
func write(name string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		return err
	}
	return os.WriteFile(name, data, 0644)
}
func writeJSON(name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return write(name, append(data, '\n'))
}
func copyFiles(root string, files map[string][]byte) error {
	for name, data := range files {
		if err := write(filepath.Join(root, filepath.FromSlash(name)), data); err != nil {
			return err
		}
	}
	return nil
}
func readFiles(root string) (map[string][]byte, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("expected regular directory: %s", root)
	}
	out := map[string][]byte{}
	total := 0
	err = filepath.WalkDir(root, func(name string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.Name() == ".git" {
			return fmt.Errorf("Git metadata is not a module input: %s", name)
		}
		if e.IsDir() {
			return nil
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return fmt.Errorf("invalid module input: %s", name)
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		total += len(data)
		if len(out) >= 10000 || total > 16<<20 {
			return fmt.Errorf("module input limit exceeded")
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = data
		return nil
	})
	return out, err
}
func testFiles(files map[string][]byte) map[string][]byte {
	out := map[string][]byte{}
	for name, data := range files {
		if strings.HasSuffix(name, "_test.go") {
			out[name] = data
		}
	}
	return out
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

func baselineData() (map[string][]byte, error) { return l2.GodddMaterials() }

func sortedKeys[V any](values map[string]V) []string { return slices.Sorted(maps.Keys(values)) }
