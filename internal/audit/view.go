package audit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxFileBytes = 16 << 20
	maxFiles     = 100000
	maxBytes     = 512 << 20
)

var ErrUnsafePath = errors.New("unsafe source path")
var errReadLimit = errors.New("audit read limit exceeded")

// View 仅访问输入根下的常规文件和目录，并登记首次观察以供复核。
// 它不提供写入、外部路径读取或执行命令的能力，不支持并发调用。
type View struct {
	root    string
	ctx     context.Context
	sources map[string]*Source
	data    map[string][]byte
	bytes   int64
}

func checksum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func safeRelative(p string) bool {
	return p != "" && !strings.Contains(p, "\\") && !strings.ContainsRune(p, 0) && !path.IsAbs(p) && path.Clean(p) == p && p != ".." && !strings.HasPrefix(p, "../")
}

func (v *View) locate(p string) (string, fs.FileInfo, error) {
	if err := v.ctx.Err(); err != nil {
		return "", nil, err
	}
	if !safeRelative(p) {
		return "", nil, ErrUnsafePath
	}
	current := v.root
	parts := strings.Split(p, "/")
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return current, nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return current, info, ErrUnsafePath
		}
		if i < len(parts)-1 && !info.IsDir() {
			return current, info, ErrUnsafePath
		}
		if i == len(parts)-1 {
			return current, info, nil
		}
	}
	return "", nil, ErrUnsafePath
}

func (v *View) observe(p string, directory bool) (Source, []byte, []fs.DirEntry, error) {
	source := Source{Path: p}
	full, info, err := v.locate(p)
	if errors.Is(err, os.ErrNotExist) {
		source.Kind = "missing"
		return source, nil, nil, err
	}
	if err != nil {
		if errors.Is(err, ErrUnsafePath) {
			source.Kind = "unsafe"
		} else {
			source.Kind = "unreadable"
		}
		return source, nil, nil, err
	}
	if directory && !info.IsDir() || !directory && !info.Mode().IsRegular() {
		source.Kind = "unsafe"
		return source, nil, nil, ErrUnsafePath
	}
	if directory {
		source.Kind = "directory"
		file, err := os.Open(full)
		if err != nil {
			source.Kind = "unreadable"
			return source, nil, nil, err
		}
		entries, readErr := file.ReadDir(maxFiles + 1)
		closeErr := file.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return source, nil, nil, readErr
		}
		if closeErr != nil {
			return source, nil, nil, closeErr
		}
		if len(entries) > maxFiles {
			return source, nil, nil, errReadLimit
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		var names strings.Builder
		for _, entry := range entries {
			fmt.Fprintf(&names, "%q:%d\n", entry.Name(), entry.Type())
		}
		source.SHA256 = checksum([]byte(names.String()))
		return source, nil, entries, nil
	}
	source.Kind = "file"
	if info.Size() > maxFileBytes {
		return source, nil, nil, errReadLimit
	}
	file, err := os.Open(full)
	if err != nil {
		source.Kind = "unreadable"
		return source, nil, nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	err = errors.Join(readErr, file.Close())
	if err != nil {
		return source, nil, nil, err
	}
	if len(data) > maxFileBytes {
		return source, nil, nil, errReadLimit
	}
	source.SHA256 = checksum(data)
	return source, data, nil, nil
}

func (v *View) register(s Source, data []byte) error {
	if _, ok := v.sources[s.Path]; ok {
		return nil
	}
	if len(v.sources) >= maxFiles || v.bytes+int64(len(data)) > maxBytes {
		return errReadLimit
	}
	v.sources[s.Path] = &s
	v.bytes += int64(len(data))
	if data != nil {
		v.data[s.Path] = data
	}
	return nil
}

// ReadFile 返回独立字节副本，缺失路径也进入来源复核。
func (v *View) ReadFile(p string) ([]byte, error) {
	if s, ok := v.sources[p]; ok && s.Kind == "file" && s.SHA256 != "" {
		return append([]byte(nil), v.data[p]...), nil
	}
	s, data, _, err := v.observe(p, false)
	if registerErr := v.register(s, data); registerErr != nil {
		return nil, registerErr
	}
	return append([]byte(nil), data...), err
}

// ReadDir 返回排序后的目录成员；不会递归进入缓存或任意子目录。
func (v *View) ReadDir(p string) ([]fs.DirEntry, error) {
	s, _, entries, err := v.observe(p, true)
	if registerErr := v.register(s, nil); registerErr != nil {
		return nil, registerErr
	}
	return entries, err
}

func (v *View) verify() *bool {
	unchanged := true
	complete := true
	keys := make([]string, 0, len(v.sources))
	for p := range v.sources {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	for _, p := range keys {
		before := v.sources[p]
		if v.ctx.Err() != nil {
			complete = false
			continue
		}
		after, _, _, err := v.observe(p, before.Kind == "directory")
		if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, ErrUnsafePath) {
			complete = false
			continue
		}
		same := before.Kind == after.Kind && before.SHA256 == after.SHA256
		// 读取失败或超限时没有足够材料证明未变化。
		if before.Kind == "unreadable" || before.Kind == "file" && before.SHA256 == "" || before.Kind == "directory" && before.SHA256 == "" {
			complete = false
			continue
		}
		before.Verified = &same
		if !same {
			unchanged = false
		}
	}
	if !unchanged {
		return &unchanged
	}
	if !complete {
		return nil
	}
	return &unchanged
}

func canonical(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", errors.New("absolute paths are required")
	}
	full, err := filepath.EvalSymlinks(p)
	if errors.Is(err, os.ErrNotExist) {
		parent, parentErr := canonical(filepath.Dir(p))
		if parentErr != nil {
			return "", parentErr
		}
		return filepath.Join(parent, filepath.Base(p)), nil
	}
	return full, err
}

func within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
