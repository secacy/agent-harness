package l1

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func digest(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
func treeHash(files map[string][]byte) string {
	data, _ := json.Marshal(hashes(files))
	return digest(data)
}
func hashes(files map[string][]byte) map[string]string {
	out := map[string]string{}
	for path, b := range files {
		out[path] = digest(b)
	}
	return out
}

func readTree(ctx context.Context, root string) (map[string][]byte, error) {
	out := map[string][]byte{}
	total := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err = errors.Join(err, ctx.Err()); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return errors.New("L1 inputs must be regular files of at most 1 MiB")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		total += len(data)
		if len(data) > 1<<20 || len(out) >= 10000 || total > 32<<20 {
			return errors.New("L1 input limit exceeded")
		}
		rel, _ := filepath.Rel(root, path)
		out[filepath.ToSlash(rel)] = data
		return nil
	})
	return out, err
}

func dataset(ctx context.Context, root string) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, file := range []string{"manifest.json", "reviews.md"} {
		data, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			return nil, err
		}
		out[file] = data
	}
	for _, dir := range []string{"patches", "oracle"} {
		files, err := readTree(ctx, filepath.Join(root, dir))
		if err != nil {
			return nil, err
		}
		for path, data := range files {
			out[dir+"/"+path] = data
		}
	}
	return out, nil
}

func writeTree(root string, files map[string][]byte) error {
	for path, data := range files {
		dest := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, data, 0644); err != nil {
			return err
		}
	}
	return nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

func decode(data []byte, value any) error {
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON document")
	}
	return nil
}

func resolve(path string) (string, error) {
	if path == "" {
		return "", errors.New("L1 paths are required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for ancestor := abs; ; ancestor = filepath.Dir(ancestor) {
		if _, err := os.Lstat(ancestor); err == nil {
			actual, err := filepath.EvalSymlinks(ancestor)
			if err != nil {
				return "", err
			}
			tail, _ := filepath.Rel(ancestor, abs)
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

func production(files map[string][]byte) map[string][]byte {
	out := map[string][]byte{}
	for path, data := range files {
		if !strings.HasSuffix(path, "_test.go") {
			out[path] = data
		}
	}
	return out
}
