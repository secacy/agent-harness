// Package writer 管理单机 Campaign 写者的排他生命周期。
package writer

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
)

const (
	FileName = ".writer.lock"
	Protocol = "flock-v1"
)

var (
	ErrBusy        = errors.New("campaign writer is active")
	ErrUnsafe      = errors.New("unsafe campaign writer lock")
	ErrUnsupported = errors.New("campaign writer locks require macOS or Linux")
)

// Lease 持有固定 inode 的锁；关闭时不删除锁文件，不向子进程传递描述符。
type Lease struct {
	file     *os.File
	root     string
	rootInfo os.FileInfo
	once     sync.Once
	closeErr error
}

// Create 只用于新建 Campaign；失败也保留已有文件，不允许覆盖锁。
func Create(root string) (*Lease, error) { return acquire(root, true) }

// Open 只打开已有锁文件，不为历史 Campaign 补建锁。
func Open(root string) (*Lease, error) { return acquire(root, false) }

// Verify 核对锁和根目录仍是最初取得的身份，不证明外部执行资源已停止。
func (l *Lease) Verify() error {
	locked, err := l.file.Stat()
	if err != nil {
		return err
	}
	root, err := os.Lstat(l.root)
	if err != nil {
		return err
	}
	current, err := os.Lstat(filepath.Join(l.root, FileName))
	if err != nil {
		return err
	}
	if !root.IsDir() || !os.SameFile(l.rootInfo, root) || !current.Mode().IsRegular() || !os.SameFile(locked, current) {
		return ErrUnsafe
	}
	return nil
}

func (l *Lease) Close() error {
	l.once.Do(func() { l.closeErr = l.file.Close() })
	return l.closeErr
}
