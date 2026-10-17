package repotools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

type pathArgs struct {
	Root string `json:"root"`
	Path string `json:"path"`
}

type readArgs struct {
	pathArgs
	StartLine *int `json:"start_line"`
	EndLine   *int `json:"end_line"`
}

type searchArgs struct {
	pathArgs
	Text string `json:"text"`
}

type replaceArgs struct {
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

func (e *Executor) list(ctx context.Context, args pathArgs, output *boundedOutput) error {
	path, err := e.resolve(args.Root, args.Path, false)
	if err != nil {
		return err
	}
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(maxEntries + 1)
	if err != nil && err != io.EOF {
		return err
	}
	if len(entries) > maxEntries {
		return errors.New("directory entry limit exceeded; choose a smaller directory")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Name() == ".git" {
			continue
		}
		suffix := ""
		if entry.IsDir() {
			suffix = "/"
		} else if !entry.Type().IsRegular() {
			suffix = " [不可访问]"
		}
		fmt.Fprintln(output, entry.Name()+suffix)
		if output.full() {
			break
		}
	}
	return nil
}

func readText(path string) ([]byte, fs.FileMode, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxFileBytes {
		return nil, 0, errors.New("expected a regular text file no larger than 1 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxFileBytes+1))
	if err != nil {
		return nil, 0, err
	}
	if len(data) > maxFileBytes || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return nil, 0, errors.New("expected UTF-8 text without NUL, no larger than 1 MiB")
	}
	return data, info.Mode().Perm(), nil
}

func (e *Executor) read(ctx context.Context, args readArgs, output *boundedOutput) error {
	start, end := 1, maxFileBytes+1
	if args.StartLine != nil {
		start = *args.StartLine
	}
	if args.EndLine != nil {
		end = *args.EndLine
	}
	if start < 1 || end < 1 || start > end {
		return errors.New("invalid line range")
	}
	path, err := e.resolve(args.Root, args.Path, false)
	if err != nil {
		return err
	}
	data, _, err := readText(path)
	if err != nil {
		return err
	}
	for index, line := range strings.SplitAfter(string(data), "\n") {
		if err := ctx.Err(); err != nil {
			return err
		}
		number := index + 1
		if number < start {
			continue
		}
		if number > end {
			break
		}
		fmt.Fprint(output, line)
		if output.full() {
			break
		}
	}
	return nil
}

func (e *Executor) search(ctx context.Context, args searchArgs, output *boundedOutput) error {
	if args.Text == "" || strings.ContainsAny(args.Text, "\n\r") {
		return errors.New("search text must be a nonempty single-line literal")
	}
	base, err := e.resolve(args.Root, args.Path, false)
	if err != nil {
		return err
	}
	visited, skipped := 0, 0
	err = filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		visited++
		if visited > maxEntries {
			fmt.Fprintln(output, "[搜索达到路径数量上限，请缩小目录范围]")
			return fs.SkipAll
		}
		if entry.Name() == ".git" {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			skipped++
			return nil
		}
		data, _, err := readText(path)
		if err != nil {
			skipped++
			return nil
		}
		relative, err := filepath.Rel(e.roots[args.Root], path)
		if err != nil {
			return err
		}
		for number, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, args.Text) {
				fmt.Fprintf(output, "%s:%d:%s\n", filepath.ToSlash(relative), number+1, line)
				if output.full() {
					return fs.SkipAll
				}
			}
		}
		return nil
	})
	if skipped > 0 {
		fmt.Fprintf(output, "[跳过 %d 个不可读取、非文本、过大或特殊文件]\n", skipped)
	}
	return err
}

func (e *Executor) replace(ctx context.Context, args replaceArgs, output *boundedOutput) error {
	if args.Old == "" {
		return errors.New("old text cannot be empty")
	}
	path, err := e.resolve("workspace", args.Path, true)
	if err != nil {
		return err
	}
	data, mode, err := readText(path)
	if err != nil {
		return err
	}
	first := strings.Index(string(data), args.Old)
	if first < 0 || first != strings.LastIndex(string(data), args.Old) {
		return errors.New("old text must match exactly once; choose a larger unique fragment")
	}
	updated := strings.Replace(string(data), args.Old, args.New, 1)
	if len(updated) > maxFileBytes || strings.ContainsRune(updated, 0) {
		return errors.New("replacement exceeds file size limit or contains NUL")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// 同目录临时文件加重命名，避免部分写入，也避免改动硬链接共享的原始内容。
	temp, err := os.CreateTemp(filepath.Dir(path), ".agent-edit-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if err := temp.Chmod(mode); err != nil {
		return err
	}
	if _, err := io.WriteString(temp, updated); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return err
	}
	fmt.Fprintln(output, "已替换 1 处匹配文本。")
	return nil
}
