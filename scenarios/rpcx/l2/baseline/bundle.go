package baseline

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// ModuleLimits 由宿主固定版本选择，不从依赖索引读取限额。
type ModuleLimits struct {
	ExpandedBytes, FileBytes, CompressedBytes int64
	Files                                     int
}

func ModulePolicy(version string) (ModuleLimits, error) {
	if _, err := lookupDataset(version); err != nil {
		return ModuleLimits{}, err
	}
	if version == GHCLIVersion {
		return ModuleLimits{128 << 20, 16 << 20, 64 << 20, 10000}, nil
	}
	return ModuleLimits{32 << 20, 8 << 20, 32 << 20, 10000}, nil
}

type bundleChunk struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type bundleIndex struct {
	SchemaVersion  int           `json:"schema_version"`
	Version        string        `json:"version"`
	ManifestSHA256 string        `json:"manifest_sha256"`
	LogicalSize    int64         `json:"logical_size"`
	LogicalSHA256  string        `json:"logical_sha256"`
	Chunks         []bundleChunk `json:"chunks"`
}

func validDigest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && s == strings.ToLower(s)
}
func validMember(n string) bool {
	return n != "." && fs.ValidPath(n) && !strings.Contains(n, "\\") && path.Clean(n) == n
}

// bundleStream 在逐片认证后串联流，避免构造第二份逻辑归档。
func bundleStream(files map[string][]byte, version string) (io.Reader, error) {
	var index bundleIndex
	if len(files["dependencies/bundle.json"]) > 1<<20 {
		return nil, errors.New("module bundle index limit exceeded")
	}
	if err := decode(files["dependencies/bundle.json"], &index); err != nil {
		return nil, err
	}
	if index.SchemaVersion != 1 || index.Version != version || len(index.Chunks) == 0 || len(index.Chunks) > 8 || index.LogicalSize <= 0 || index.LogicalSize > 64<<20 || !validDigest(index.LogicalSHA256) || digest(files["dependencies/modules.json"]) != index.ManifestSHA256 {
		return nil, errors.New("invalid module bundle index")
	}
	readers := make([]io.Reader, 0, len(index.Chunks))
	expected := map[string]bool{"dependencies/modules.json": true, "dependencies/bundle.json": true}
	hash := sha256.New()
	var total int64
	for i, c := range index.Chunks {
		name := fmt.Sprintf("dependencies/modules-%03d.tar.gz.part", i)
		if c.Name != name || c.Size <= 0 || c.Size > 8<<20 || !validDigest(c.SHA256) || int64(len(files[name])) != c.Size || digest(files[name]) != c.SHA256 {
			return nil, errors.New("invalid module bundle chunk")
		}
		total += c.Size
		if total > 64<<20 {
			return nil, errors.New("module bundle limit exceeded")
		}
		hash.Write(files[name])
		readers = append(readers, bytes.NewReader(files[name]))
		expected[name] = true
	}
	for name := range files {
		if strings.HasPrefix(name, "dependencies/") && !expected[name] {
			return nil, errors.New("unexpected module bundle file")
		}
	}
	if total != index.LogicalSize || fmt.Sprintf("%x", hash.Sum(nil)) != index.LogicalSHA256 {
		return nil, errors.New("module bundle logical identity mismatch")
	}
	return io.MultiReader(readers...), nil
}

// ExtractModulesVersion 仅给已发布版本装配专属依赖，索引不能自行升级策略。
func ExtractModulesVersion(files map[string][]byte, directory, version string) error {
	if version != GHCLIVersion {
		if _, err := lookupDataset(version); err != nil {
			return err
		}
		return unpack(files, directory)
	}
	ds, _ := lookupDataset(version)
	if digest(files["freeze.json"]) != ds.FreezeSHA256 {
		return errors.New("published dataset identity changed")
	}
	var release manifest
	if err := decode(files["freeze.json"], &release); err != nil {
		return err
	}
	if release.Version != version || release.SchemaVersion != 1 {
		return errors.New("invalid dataset manifest")
	}
	for name, data := range files {
		if strings.HasPrefix(name, "dependencies/") && digest(data) != release.Files[name] {
			return errors.New("module material differs from frozen release")
		}
	}
	return unpackVersion(files, directory, version)
}

func unpackVersion(files map[string][]byte, root, version string) error {
	if version != GHCLIVersion {
		return unpack(files, root)
	}
	limits, err := ModulePolicy(version)
	if err != nil {
		return err
	}
	reader, err := bundleStream(files, version)
	if err != nil {
		return err
	}
	var m struct {
		SchemaVersion int               `json:"schema_version"`
		Modules       []any             `json:"modules"`
		Files         map[string]string `json:"files"`
		ExpandedBytes int64             `json:"expanded_bytes"`
	}
	if len(files["dependencies/modules.json"]) > 8<<20 {
		return errors.New("module manifest limit exceeded")
	}
	if err := decode(files["dependencies/modules.json"], &m); err != nil {
		return err
	}
	if m.SchemaVersion != 1 || len(m.Files) == 0 || len(m.Files) > limits.Files || m.ExpandedBytes < 0 || m.ExpandedBytes > limits.ExpandedBytes {
		return errors.New("invalid module cache manifest")
	}
	for n, h := range m.Files {
		if !validMember(n) || !validDigest(h) {
			return errors.New("invalid module cache file identity")
		}
	}
	// 使用 ByteReader 精确定位 gzip 尾部；禁止第二个流或压缩尾随数据。
	buffered := bufio.NewReader(reader)
	gz, err := gzip.NewReader(buffered)
	if err != nil {
		return err
	}
	defer gz.Close()
	gz.Multistream(false)
	// 允许 tar 文件头与有界零填充，压缩炸弹在展开层停止。
	bounded := &io.LimitedReader{R: gz, N: m.ExpandedBytes + int64(len(m.Files))*4096 + (1 << 20) + 1}
	archive := tar.NewReader(bounded)
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
		if h.Typeflag != tar.TypeReg || !validMember(h.Name) || h.Size < 0 || h.Size > limits.FileBytes {
			return errors.New("invalid module archive member")
		}
		if _, ok := actual[h.Name]; ok || m.Files[h.Name] == "" || h.Size > m.ExpandedBytes-total || len(actual) >= limits.Files {
			return errors.New("unexpected module archive member")
		}
		target, err := safeModuleTarget(root, h.Name)
		if err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}
		hash := sha256.New()
		count, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(archive, h.Size+1))
		closeErr := file.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return err
		}
		got := fmt.Sprintf("%x", hash.Sum(nil))
		if count != h.Size || got != m.Files[h.Name] {
			return errors.New("module cache hash mismatch")
		}
		actual[h.Name] = got
		total += count
	}
	tail := make([]byte, 32<<10)
	var trailing int64
	for {
		n, e := bounded.Read(tail)
		trailing += int64(n)
		if len(bytes.Trim(tail[:n], "\x00")) != 0 || trailing > 1<<20 {
			return errors.New("invalid module archive trailer")
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
	}
	if bounded.N == 0 {
		return errors.New("module archive expansion limit exceeded")
	}
	if _, err := buffered.ReadByte(); err != io.EOF {
		return errors.New("trailing compressed module data")
	}
	if total != m.ExpandedBytes || !maps.Equal(actual, m.Files) {
		return errors.New("module cache does not match manifest")
	}
	return nil
}

// safeModuleTarget 拒绝复用已有文件或通过目录链接逃出宿主根。
func safeModuleTarget(root, name string) (string, error) {
	if err := os.MkdirAll(root, 0755); err != nil {
		return "", err
	}
	current := root
	info, err := os.Lstat(current)
	if err != nil || !info.IsDir() {
		return "", errors.New("invalid module extraction root")
	}
	parts := strings.Split(name, "/")
	for _, p := range parts[:len(parts)-1] {
		current = filepath.Join(current, p)
		if err := os.Mkdir(current, 0755); err != nil && !os.IsExist(err) {
			return "", err
		}
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() {
			return "", errors.New("invalid module extraction directory")
		}
	}
	return filepath.Join(current, parts[len(parts)-1]), nil
}

// CheckModuleTree 流式核对宿主缓存；不把完整展开依赖装入业务快照读取器。
func CheckModuleTree(ctx context.Context, root string, expected map[string]string, version string) error {
	limits, err := ModulePolicy(version)
	if err != nil {
		return err
	}
	actual := map[string]string{}
	var total int64
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("invalid host module root")
	}
	err = filepath.WalkDir(root, func(name string, e fs.DirEntry, err error) error {
		if err := errors.Join(err, ctx.Err()); err != nil {
			return err
		}
		if e.IsDir() {
			return nil
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > limits.FileBytes || info.Size() > limits.ExpandedBytes-total || len(actual) >= limits.Files {
			return errors.New("invalid host module material")
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if expected[rel] == "" {
			return errors.New("unexpected host module material")
		}
		file, err := os.Open(name)
		if err != nil {
			return err
		}
		hash := sha256.New()
		count, copyErr := io.Copy(hash, io.LimitReader(file, limits.FileBytes+1))
		closeErr := file.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return err
		}
		if count != info.Size() {
			return errors.New("host module material size changed")
		}
		total += count
		actual[rel] = fmt.Sprintf("%x", hash.Sum(nil))
		return nil
	})
	if err != nil {
		return err
	}
	if !maps.Equal(actual, expected) {
		return errors.New("host module materials changed")
	}
	return nil
}
