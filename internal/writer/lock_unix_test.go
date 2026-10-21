//go:build darwin || linux

package writer

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

type processEvent struct {
	State string
	PID   int
}

// 辅助进程仅通过专用管道报告状态，标准输入保持到父测试释放。
func TestWriterProcessHelper(t *testing.T) {
	mode := os.Getenv("HARNESS_WRITER_HELPER")
	if mode == "" {
		t.Skip("subprocess helper")
	}
	pipe := os.NewFile(3, "events")
	emit := func(state string) {
		if err := json.NewEncoder(pipe).Encode(processEvent{state, os.Getpid()}); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "descendant" {
		emit("descendant")
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	if mode == "contend" {
		start := os.NewFile(4, "start")
		var one [1]byte
		if _, err := start.Read(one[:]); err != nil {
			t.Fatal(err)
		}
		_ = start.Close()
	}
	l, err := Open(os.Getenv("HARNESS_WRITER_DIRECTORY"))
	if errors.Is(err, ErrBusy) {
		emit("busy")
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	emit("held")
	if mode == "owner-with-child" {
		child := exec.Command(os.Args[0], "-test.run=^TestWriterProcessHelper$")
		child.Env = append(os.Environ(), "HARNESS_WRITER_HELPER=descendant")
		child.ExtraFiles = []*os.File{pipe}
		child.Stdin = os.NewFile(4, "descendant-lifetime")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}

type helper struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	events <-chan processEvent
	waited bool
}

func startHelper(t *testing.T, root, mode string, extra *os.File) *helper {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestWriterProcessHelper$")
	cmd.Env = append(os.Environ(), "HARNESS_WRITER_HELPER="+mode, "HARNESS_WRITER_DIRECTORY="+root)
	cmd.ExtraFiles = []*os.File{w}
	if extra != nil {
		cmd.ExtraFiles = append(cmd.ExtraFiles, extra)
	}
	log, err := os.CreateTemp(t.TempDir(), "child-*.log")
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = log, log
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	events := make(chan processEvent, 2)
	go func() {
		defer close(events)
		defer r.Close()
		d := json.NewDecoder(r)
		for {
			var event processEvent
			if d.Decode(&event) != nil {
				return
			}
			events <- event
		}
	}()
	h := &helper{cmd: cmd, stdin: stdin, events: events}
	t.Cleanup(func() {
		_ = stdin.Close()
		if !h.waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		_ = r.Close()
		_ = log.Close()
	})
	return h
}

func (h *helper) event(t *testing.T) processEvent {
	t.Helper()
	select {
	case e, ok := <-h.events:
		if !ok {
			t.Fatal("helper exited without an event")
		}
		return e
	case <-time.After(10 * time.Second):
		t.Fatal("helper checkpoint timed out")
	}
	return processEvent{}
}

func (h *helper) kill(t *testing.T) {
	t.Helper()
	if err := h.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err := h.cmd.Wait()
	h.waited = true
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("expected SIGKILL, got %v", err)
	}
}

func newRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	l, err := Create(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestWriterProcessExclusionAndSIGKILL(t *testing.T) {
	root := newRoot(t)
	before, _ := os.Stat(filepath.Join(root, FileName))
	var contenders []*helper
	var starts []*os.File
	for range 2 {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		contenders = append(contenders, startHelper(t, root, "contend", r))
		_ = r.Close()
		starts = append(starts, w)
	}
	for _, start := range starts {
		_, err := start.Write([]byte{1})
		_ = start.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	var owner *helper
	busy := 0
	for _, h := range contenders {
		e := h.event(t)
		if e.PID != h.cmd.Process.Pid {
			t.Fatal("event belongs to another process")
		}
		switch e.State {
		case "held":
			if owner != nil {
				t.Fatal("two writers acquired the same lock")
			}
			owner = h
		case "busy":
			busy++
		default:
			t.Fatal(e)
		}
	}
	if owner == nil || busy != 1 {
		t.Fatal("expected exactly one owner and one rejected contender")
	}
	if err := owner.cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	var stopped syscall.WaitStatus
	// BSD 的 Stopped 会将 SIGSTOP 编码归入 Continued，直接核对本次内核停止状态。
	pid, err := syscall.Wait4(owner.cmd.Process.Pid, &stopped, syscall.WUNTRACED, nil)
	if err != nil || pid != owner.cmd.Process.Pid || stopped&0xff != 0x7f || (stopped>>8)&0xff != syscall.WaitStatus(syscall.SIGSTOP) {
		t.Fatalf("owner did not stop: %v %v", stopped, err)
	}
	if l, err := Open(root); !errors.Is(err, ErrBusy) {
		if l != nil {
			_ = l.Close()
		}
		t.Fatalf("paused owner was displaced: %v", err)
	}
	owner.kill(t)
	l, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	after, _ := os.Stat(filepath.Join(root, FileName))
	if !os.SameFile(before, after) {
		t.Fatal("lock inode was replaced")
	}
	t.Log("two contenders: one owner; SIGSTOP retained ownership; SIGKILL released the original inode")
}

func TestWriterDescriptorNotInherited(t *testing.T) {
	root := newRoot(t)
	r, lifetime, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer lifetime.Close()
	owner := startHelper(t, root, "owner-with-child", r)
	_ = r.Close()
	held := owner.event(t)
	child := owner.event(t)
	if held.State != "held" || held.PID != owner.cmd.Process.Pid || child.State != "descendant" || child.PID == held.PID {
		t.Fatal(held, child)
	}
	defer syscall.Kill(child.PID, syscall.SIGKILL)
	owner.kill(t)
	if err := syscall.Kill(child.PID, 0); err != nil {
		t.Fatalf("descendant is no longer alive: %v", err)
	}
	l, err := Open(root)
	if err != nil {
		t.Fatalf("live descendant retained writer descriptor: %v", err)
	}
	_ = l.Close()
	t.Log("owner killed, descendant still alive, writer lock acquired")
}

func TestWriterGracefulRelease(t *testing.T) {
	root := newRoot(t)
	h := startHelper(t, root, "hold", nil)
	if e := h.event(t); e.State != "held" {
		t.Fatal(e)
	}
	_ = h.stdin.Close()
	if err := h.cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	h.waited = true
	l, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal("close must be idempotent")
	}
}

func TestWriterRejectUnsafeFiles(t *testing.T) {
	for _, mode := range []string{"missing", "symlink", "directory", "fifo", "hardlink", "content"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			p := filepath.Join(root, FileName)
			var err error
			switch mode {
			case "symlink", "hardlink":
				other := filepath.Join(t.TempDir(), "other")
				if err = os.WriteFile(other, nil, 0600); err != nil {
					t.Fatal(err)
				}
				if mode == "symlink" {
					err = os.Symlink(other, p)
				} else {
					err = os.Link(other, p)
				}
			case "directory":
				err = os.Mkdir(p, 0700)
			case "fifo":
				err = syscall.Mkfifo(p, 0600)
			case "content":
				err = os.WriteFile(p, []byte("not a writer lock"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			l, err := Open(root)
			if err == nil {
				_ = l.Close()
				t.Fatal("unsafe file accepted")
			}
			if mode == "missing" {
				if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("missing lock was created")
				}
			}
		})
	}
	root := newRoot(t)
	if l, err := Create(root); err == nil {
		_ = l.Close()
		t.Fatal("existing lock was overwritten")
	}
	l, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := os.Rename(filepath.Join(root, FileName), filepath.Join(root, "old-lock")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, FileName), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(l.Verify(), ErrUnsafe) {
		t.Fatal("lock replacement was not detected")
	}
}
