package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"
)

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(data, '\n'))
}

func writeAtomic(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".publish-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

// 用独立 Git 索引比较基线和候选，不在候选中创建 .git，也不生成提交。
// 索引中的基线使新增、删除、二进制内容和无末尾换行均可正确导出。
func exportPatch(ctx context.Context, dir string, baseline, candidate tree) ([]byte, error) {
	temp, err := os.MkdirTemp(dir, ".patch-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temp)
	oldDir, newDir := filepath.Join(temp, "old"), filepath.Join(temp, "new")
	if err := baseline.write(ctx, oldDir); err != nil {
		return nil, err
	}
	if err := candidate.write(ctx, newDir); err != nil {
		return nil, err
	}
	repo := filepath.Join(temp, "repo")
	runGit := func(worktree string, args ...string) ([]byte, error) {
		flags := []string{"-c", "core.autocrlf=false", "-c", "core.filemode=false", "-c", "core.quotePath=true"}
		if worktree != "" {
			flags = append(flags, "--git-dir="+filepath.Join(repo, ".git"), "--work-tree="+worktree)
		}
		command := exec.CommandContext(ctx, "git", append(flags, args...)...)
		command.Dir = temp
		command.Env = []string{
			"PATH=" + os.Getenv("PATH"), "HOME=" + temp,
			"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull,
			"GIT_TERMINAL_PROMPT=0", "LC_ALL=C",
		}
		command.WaitDelay = time.Second
		var stderr bytes.Buffer
		command.Stderr = &stderr
		data, err := command.Output()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err != nil {
			return nil, fmt.Errorf("export patch: git %s: %w: %s", args[0], err, stderr.String())
		}
		return data, nil
	}
	if _, err := runGit("", "init", "--quiet", "--template=", repo); err != nil {
		return nil, err
	}
	if len(baseline) != 0 {
		if _, err := runGit(oldDir, "add", "--force", "--all", "--", "."); err != nil {
			return nil, err
		}
	}
	added := []string{}
	for path := range candidate {
		if _, exists := baseline[path]; !exists {
			added = append(added, path)
		}
	}
	sort.Strings(added)
	// 分批登记新增文件，避免大量文件名超过系统参数长度限制。
	for start := 0; start < len(added); start += 100 {
		end := min(start+100, len(added))
		if _, err := runGit(newDir, append([]string{"add", "--force", "--intent-to-add", "--"}, added[start:end]...)...); err != nil {
			return nil, err
		}
	}
	return runGit(newDir, "diff", "--binary", "--no-ext-diff", "--no-textconv", "--no-renames", "--src-prefix=a/", "--dst-prefix=b/", "--", ".")
}
