//go:build darwin || linux

package writer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func acquire(root string, create bool) (_ *Lease, err error) {
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, ErrUnsafe
	}
	flags := os.O_RDWR | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_CLOEXEC
	if create {
		flags |= os.O_CREATE | os.O_EXCL
	}
	file, err := os.OpenFile(filepath.Join(root, FileName), flags, 0600)
	if err != nil {
		return nil, fmt.Errorf("open campaign writer lock: %w", err)
	}
	l := &Lease{file: file, root: root, rootInfo: info}
	defer func() {
		if err != nil {
			_ = file.Close()
		}
	}()
	locked, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !locked.Mode().IsRegular() || locked.Size() != 0 || locked.Sys().(*syscall.Stat_t).Nlink != 1 {
		return nil, ErrUnsafe
	}
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return nil, ErrBusy
	}
	if err != nil {
		return nil, fmt.Errorf("lock campaign writer: %w", err)
	}
	if err = l.Verify(); err != nil {
		return nil, err
	}
	if create {
		if err = file.Sync(); err != nil {
			return nil, err
		}
		dir, openErr := os.Open(root)
		if openErr != nil {
			return nil, openErr
		}
		if err = errors.Join(dir.Sync(), dir.Close()); err != nil {
			return nil, err
		}
	}
	return l, nil
}
