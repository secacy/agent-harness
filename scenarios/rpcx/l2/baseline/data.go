// Package baseline 复验已冻结的 L2 开发基线，不对迁移候选作质量判定。
package baseline

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const Version = "rpcx-l2-goddd-v1"

type manifest struct {
	SchemaVersion int               `json:"schema_version"`
	Version       string            `json:"version"`
	Files         map[string]string `json:"files"`
}

func digest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func decode(data []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

// read 扫描整个发布根，不允许通过漏列文件缩小冻结范围。
func read(ctx context.Context, root string) (map[string][]byte, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("invalid dataset directory: %s", root)
	}
	files := map[string][]byte{}
	total := 0
	err = filepath.WalkDir(root, func(filename string, entry fs.DirEntry, err error) error {
		if err := errors.Join(err, ctx.Err()); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 8<<20 {
			return fmt.Errorf("invalid dataset file: %s", filename)
		}
		if len(files) >= 10000 || total+int(info.Size()) > 32<<20 {
			return errors.New("dataset limit exceeded")
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = data
		total += len(data)
		return nil
	})
	return files, err
}

func frozen(files map[string][]byte) error { return frozenVersion(files, Version) }

func frozenVersion(files map[string][]byte, version string) error {
	dataset, err := lookupDataset(version)
	if err != nil {
		return err
	}
	if digest(files["freeze.json"]) != dataset.FreezeSHA256 {
		return errors.New("published dataset identity changed")
	}
	var m manifest
	if err := decode(files["freeze.json"], &m); err != nil {
		return fmt.Errorf("dataset manifest: %w", err)
	}
	if m.SchemaVersion != 1 || m.Version != version || len(m.Files) == 0 {
		return errors.New("unsupported dataset manifest")
	}
	actual := map[string]string{}
	for name, data := range files {
		if name != "freeze.json" {
			actual[name] = digest(data)
		}
	}
	if !maps.Equal(actual, m.Files) {
		return errors.New("dataset does not match frozen files")
	}
	return nil
}

func write(filename string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0644)
}

func writeJSON(filename string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return write(filename, append(data, '\n'))
}

// unpack 只接受独立文件，并核对缓存内每一份实际材料。
func unpack(files map[string][]byte, root string) error {
	var m struct {
		SchemaVersion int               `json:"schema_version"`
		Modules       []json.RawMessage `json:"modules"`
		Files         map[string]string `json:"files"`
		ExpandedBytes int64             `json:"expanded_bytes"`
	}
	if err := decode(files["dependencies/modules.json"], &m); err != nil {
		return err
	}
	if m.SchemaVersion != 1 || len(m.Files) == 0 || len(m.Files) > 10000 || m.ExpandedBytes < 0 || m.ExpandedBytes > 32<<20 {
		return errors.New("invalid module cache manifest")
	}
	gz, err := gzip.NewReader(bytes.NewReader(files["dependencies/modules.tar.gz"]))
	if err != nil {
		return err
	}
	defer gz.Close()
	archive := tar.NewReader(gz)
	actual := map[string]string{}
	var total int64
	for {
		h, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || h.Name == "." || !fs.ValidPath(h.Name) || strings.Contains(h.Name, "\\") || path.Clean(h.Name) != h.Name || h.Size < 0 || h.Size > 8<<20 {
			return fmt.Errorf("invalid module archive member: %s", h.Name)
		}
		if _, exists := actual[h.Name]; exists || m.Files[h.Name] == "" || total+h.Size > m.ExpandedBytes {
			return fmt.Errorf("unexpected module archive member: %s", h.Name)
		}
		data, err := io.ReadAll(io.LimitReader(archive, h.Size+1))
		if err != nil {
			return err
		}
		if int64(len(data)) != h.Size || digest(data) != m.Files[h.Name] {
			return fmt.Errorf("module cache hash mismatch: %s", h.Name)
		}
		actual[h.Name] = digest(data)
		total += h.Size
		if err := write(filepath.Join(root, filepath.FromSlash(h.Name)), data); err != nil {
			return err
		}
	}
	// 消费 gzip 尾部以校验校验和，并拒绝 tar 结束后的非零数据。
	tail, err := io.ReadAll(io.LimitReader(gz, 1<<20))
	if err != nil || len(bytes.Trim(tail, "\x00")) != 0 || len(tail) == 1<<20 {
		return errors.New("invalid module archive trailer")
	}
	if total != m.ExpandedBytes || !maps.Equal(actual, m.Files) {
		return errors.New("module cache does not match manifest")
	}
	return nil
}

// provenance 检查逐文件说明引用的原件和交付文件确实匹配。
func provenance(files map[string][]byte) error {
	var p struct {
		Files []struct {
			Source       string `json:"source"`
			SourceSHA256 string `json:"source_sha256"`
			Action       string `json:"action"`
			Target       string `json:"target"`
			TargetSHA256 string `json:"target_sha256"`
		} `json:"files"`
	}
	if err := json.Unmarshal(files["provenance.json"], &p); err != nil {
		return err
	}
	if len(p.Files) == 0 {
		return errors.New("empty provenance record")
	}
	covered := map[string]bool{}
	for _, entry := range p.Files {
		if entry.Action == "excluded" && entry.Target == "" {
			continue
		}
		target, exists := files[entry.Target]
		if !exists || !strings.HasPrefix(entry.Target, "fixtures/") || digest(target) != entry.TargetSHA256 || covered[entry.Target] {
			return fmt.Errorf("invalid provenance target: %s", entry.Target)
		}
		covered[entry.Target] = true
		switch entry.Action {
		case "kept":
			if entry.SourceSHA256 != entry.TargetSHA256 {
				return fmt.Errorf("retained source changed: %s", entry.Source)
			}
		case "modified":
			original, exists := files["provenance/upstream/"+entry.Source+".txt"]
			if !exists || digest(original) != entry.SourceSHA256 {
				return fmt.Errorf("original source missing: %s", entry.Source)
			}
		case "added":
		default:
			return fmt.Errorf("unsupported provenance action: %s", entry.Action)
		}
	}
	for name := range files {
		if strings.HasPrefix(name, "fixtures/") && !covered[name] {
			return fmt.Errorf("fixture missing provenance: %s", name)
		}
	}
	return nil
}

// ExtractModules 将已认证数据的依赖材料展开到宿主分配的空目录。
func ExtractModules(files map[string][]byte, directory string) error {
	return unpack(files, directory)
}
