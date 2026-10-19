package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// tree 只描述常规文件的路径与内容；夹具副本统一使用 0644 权限。
type tree map[string][]byte

func readTree(ctx context.Context, root string) (tree, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a regular directory: %s", root)
	}
	files := tree{}
	total := 0
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err := errors.Join(err, ctx.Err()); err != nil {
			return err
		}
		if entry.Name() == ".git" {
			return fmt.Errorf("Git metadata is not a scenario input: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
			return fmt.Errorf("expected a regular file of at most 1 MiB: %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		total += len(data)
		if len(files) >= 10000 || total > 16*1024*1024 {
			return errors.New("scenario tree exceeds 10000 files or 16 MiB")
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = data
		return nil
	})
	return files, err
}

func (files tree) write(ctx context.Context, root string) error {
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	for relative, data := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			return err
		}
	}
	return nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (files tree) manifest() map[string]string {
	manifest := make(map[string]string, len(files))
	for path, data := range files {
		manifest[path] = digest(data)
	}
	return manifest
}

// 摘要格式与独立验证器一致：对按键排序的文件摘要 JSON 再计算摘要。
func (files tree) hash() string {
	data, _ := json.Marshal(files.manifest())
	return digest(data)
}

func (files tree) module(name string) tree {
	module := tree{}
	for path, data := range files {
		if relative, ok := strings.CutPrefix(path, name+"/"); ok {
			module[relative] = data
		}
	}
	return module
}

// 解析尚未创建的输出路径时也处理已有祖先的符号链接。
func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil || !os.IsNotExist(err) {
		return resolved, err
	}
	parent := filepath.Dir(abs)
	if parent == abs {
		return "", err
	}
	resolved, err = canonicalPath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(abs)), nil
}

func within(child, parent string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
