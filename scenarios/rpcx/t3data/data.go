// Package t3data 定义 T3 数据发布的读取范围，不包含验收驱动和根说明文档。
package t3data

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

const Version = "rpcx-t3-data-v1"

//go:embed data.go
var source string

// Source 供验收工具保存实际采用的读取和校验实现。
func Source() string { return source }

type Manifest struct {
	SchemaVersion int               `json:"schema_version"`
	Version       string            `json:"version"`
	Files         map[string]string `json:"files"`
}

// Read 扫描完整数据根，保留清单字节；根 README 仅作可维护说明。
func Read(ctx context.Context, root string) (map[string][]byte, error) {
	files := map[string][]byte{}
	total := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err = errors.Join(err, ctx.Err()); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		top, _, _ := strings.Cut(rel, "/")
		dataRoot := top == "fixtures" || top == "specs" || top == "cases" || top == "oracle"
		if !dataRoot && rel != "freeze.json" && rel != "README.md" {
			return fmt.Errorf("unexpected T3 input path: %s", rel)
		}
		if entry.IsDir() {
			if !dataRoot {
				return fmt.Errorf("expected regular T3 file: %s", rel)
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 1<<20 || rel == top && dataRoot {
			return fmt.Errorf("invalid T3 input file: %s", rel)
		}
		if rel == "README.md" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		total += len(data)
		if len(data) > 1<<20 || len(files) >= 10000 || total > 32<<20 {
			return errors.New("T3 data limit exceeded")
		}
		files[rel] = data
		return nil
	})
	return files, err
}

// Frozen 区分未冻结的数据与不支持的清单格式，不执行输入中的代码。
func Frozen(files map[string][]byte) (bool, error) {
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(files["freeze.json"]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return false, fmt.Errorf("T3 freeze.json: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return false, errors.New("trailing JSON in T3 freeze.json")
	}
	if manifest.SchemaVersion != 1 || manifest.Version != Version || len(manifest.Files) == 0 {
		return false, errors.New("unsupported T3 data manifest")
	}
	actual := make(map[string]string, len(files)-1)
	for path, data := range files {
		if path != "freeze.json" {
			actual[path] = fmt.Sprintf("%x", sha256.Sum256(data))
		}
	}
	return reflect.DeepEqual(actual, manifest.Files), nil
}
