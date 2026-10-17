package repotools

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func preparePaths(config Config) (map[string]string, []string, error) {
	workspace, err := canonicalDir(config.Workspace)
	if err != nil {
		return nil, nil, fmt.Errorf("workspace: %w", err)
	}
	roots := map[string]string{"workspace": workspace}
	for name, path := range config.ReadOnlyRoots {
		if name == "workspace" || name == "" || strings.ContainsAny(name, `/\\`) {
			return nil, nil, fmt.Errorf("invalid read-only root name %q", name)
		}
		root, err := canonicalDir(path)
		if err != nil {
			return nil, nil, fmt.Errorf("root %s: %w", name, err)
		}
		for otherName, other := range roots {
			if overlaps(root, other) {
				return nil, nil, fmt.Errorf("roots %s and %s overlap", name, otherName)
			}
		}
		roots[name] = root
	}
	protected := make([]string, 0, len(config.ProtectedPaths))
	for _, path := range config.ProtectedPaths {
		path = strings.TrimSuffix(path, "/")
		if err := validRelative(path); err != nil {
			return nil, nil, fmt.Errorf("protected path: %w", err)
		}
		protected = append(protected, path)
	}
	return roots, protected, nil
}

func canonicalDir(path string) (string, error) {
	if path == "" {
		return "", errors.New("directory is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", path)
	}
	return resolved, nil
}

func overlaps(a, b string) bool {
	within := func(child, parent string) bool {
		relative, err := filepath.Rel(parent, child)
		return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
	}
	return within(a, b) || within(b, a)
}

func validRelative(path string) error {
	if !fs.ValidPath(path) || strings.Contains(path, `\`) || strings.ContainsRune(path, 0) || filepath.IsAbs(path) {
		return fmt.Errorf("path must be a clean relative path: %q", path)
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".git" {
			return errors.New("Git metadata is not accessible")
		}
	}
	return nil
}

// resolve 检查每一层路径，拒绝符号链接及设备等特殊文件。
func (e *Executor) resolve(rootName, path string, writing bool) (string, error) {
	root, ok := e.roots[rootName]
	if !ok {
		return "", fmt.Errorf("unknown root %q", rootName)
	}
	if err := validRelative(path); err != nil {
		return "", err
	}
	if writing {
		if rootName != "workspace" {
			return "", errors.New("root is read-only")
		}
		for _, protected := range e.protected {
			if protected == "." || path == protected || strings.HasPrefix(path, protected+"/") {
				return "", fmt.Errorf("protected path: %s", path)
			}
		}
	}
	current := root
	for _, part := range append([]string{"."}, strings.Split(path, "/")...) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return "", fmt.Errorf("only regular files and directories are accessible: %s", path)
		}
	}
	return current, nil
}

func sortedRoots(roots map[string]string) []string {
	names := make([]string, 0, len(roots))
	for name := range roots {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
