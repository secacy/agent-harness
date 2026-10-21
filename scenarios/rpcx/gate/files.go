// Package gate 提供场景验证共用的文件身份、路径与模块声明检查。
package gate

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func Digest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func Hashes(files map[string][]byte) map[string]string {
	out := map[string]string{}
	for name, data := range files {
		out[name] = Digest(data)
	}
	return out
}

func TreeHash(files map[string][]byte) string {
	data, _ := json.Marshal(Hashes(files))
	return Digest(data)
}

func Module(files map[string][]byte, name string) map[string][]byte {
	out := map[string][]byte{}
	for path, data := range files {
		if rel, ok := strings.CutPrefix(path, name+"/"); ok {
			out[rel] = data
		}
	}
	return out
}

func Write(name string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		return err
	}
	return os.WriteFile(name, data, 0644)
}

func WriteJSON(name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return Write(name, append(data, '\n'))
}

func CopyFiles(root string, files map[string][]byte) error {
	for name, data := range files {
		if err := Write(filepath.Join(root, filepath.FromSlash(name)), data); err != nil {
			return err
		}
	}
	return nil
}

func ReadFiles(root string) (map[string][]byte, error) {
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

func TestFiles(files map[string][]byte) map[string][]byte {
	out := map[string][]byte{}
	for name, data := range files {
		if strings.HasSuffix(name, "_test.go") {
			out[name] = data
		}
	}
	return out
}

func SortedKeys[V any](values map[string]V) []string { return slices.Sorted(maps.Keys(values)) }

func ModEntries(data []byte) (map[string]string, error) {
	out := map[string]string{}
	block := ""
	for _, line := range strings.Split(string(data), "\n") {
		line, _, _ = strings.Cut(line, "//")
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		for i, field := range fields {
			if strings.HasPrefix(field, "\"") {
				value, err := strconv.Unquote(field)
				if err != nil {
					return nil, fmt.Errorf("unsupported quoted module token: %w", err)
				}
				fields[i] = value
			}
		}
		if len(fields) == 1 && fields[0] == ")" {
			if block == "" {
				return nil, fmt.Errorf("unexpected module block end")
			}
			block = ""
			continue
		}
		directive := block
		if directive == "" {
			directive = fields[0]
			fields = fields[1:]
		}
		if len(fields) == 1 && fields[0] == "(" {
			if block != "" || directive != "require" && directive != "replace" {
				return nil, fmt.Errorf("unsupported module block")
			}
			block = directive
			continue
		}
		var key, value string
		switch directive {
		case "module", "go", "toolchain":
			if len(fields) != 1 {
				return nil, fmt.Errorf("invalid %s entry", directive)
			}
			key = directive
			value = fields[0]
		case "require":
			if len(fields) != 2 {
				return nil, fmt.Errorf("unsupported requirement")
			}
			key = "require " + fields[0]
			value = fields[1]
		case "replace":
			if len(fields) != 3 || fields[1] != "=>" {
				return nil, fmt.Errorf("unsupported replacement")
			}
			key = "replace " + fields[0]
			value = fields[2]
		default:
			return nil, fmt.Errorf("unsupported module directive: %s", directive)
		}
		if _, ok := out[key]; ok {
			return nil, fmt.Errorf("duplicate module entry: %s", key)
		}
		out[key] = value
	}
	if block != "" {
		return nil, fmt.Errorf("unclosed module block")
	}
	return out, nil
}

func MigratedMod(data []byte) []byte {
	s := strings.Replace(string(data), "example.com/rpcx v1.0.0", "example.com/rpcx/v2 v2.0.0", 1)
	s = strings.Replace(s, "replace example.com/rpcx => ../rpcx-v1\n", "", 1)
	return []byte(s)
}

func Canonical(name string) (string, error) {
	if name == "" {
		return "", errors.New("verification paths are required")
	}
	absolute, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil || !os.IsNotExist(err) {
		return resolved, err
	}
	if filepath.Dir(absolute) == absolute {
		return "", err
	}
	parent, err := Canonical(filepath.Dir(absolute))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(absolute)), nil
}

func Overlap(a, b string) bool {
	for _, pair := range [][2]string{{a, b}, {b, a}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err == nil && (rel == "." || filepath.IsLocal(rel)) {
			return true
		}
	}
	return false
}
