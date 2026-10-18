package behavior

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func digest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func bind(files map[string][]byte) Binding {
	hashes := map[string]string{}
	for path, data := range files {
		hashes[path] = digest(data)
	}
	data, _ := json.Marshal(hashes)
	return Binding{SHA256: digest(data), Files: hashes}
}

func readTree(ctx context.Context, root string) (map[string][]byte, error) {
	files := map[string][]byte{}
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err = errors.Join(err, ctx.Err()); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return errors.New("behavior inputs must be regular files of at most 1 MiB")
		}
		total += info.Size()
		if len(files) >= 10000 || total > 16<<20 {
			return errors.New("behavior input limit exceeded")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	return files, err
}

func writeTree(root string, files map[string][]byte) error {
	for path, data := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(root, path), data, 0644); err != nil {
			return err
		}
	}
	return nil
}

func selectTests(files map[string][]byte, tests bool) map[string][]byte {
	selected := map[string][]byte{}
	for path, data := range files {
		if strings.HasSuffix(path, "_test.go") == tests {
			selected[path] = data
		}
	}
	return selected
}

// resolve 也解析尚未创建路径的已有祖先，避免目录别名绕过重叠检查。
func resolve(path string) (string, error) {
	if path == "" {
		return "", errors.New("behavior paths are required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for parent := abs; ; parent = filepath.Dir(parent) {
		if _, err := os.Lstat(parent); err == nil {
			actual, err := filepath.EvalSymlinks(parent)
			if err != nil {
				return "", err
			}
			tail, _ := filepath.Rel(parent, abs)
			return filepath.Join(actual, tail), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
}

func overlaps(a, b string) bool {
	x, _ := filepath.Rel(a, b)
	y, _ := filepath.Rel(b, a)
	return x == "." || y == "." || filepath.IsLocal(x) || filepath.IsLocal(y)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}
