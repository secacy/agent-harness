//go:build darwin || linux

package audit

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestFIFOIsRejectedWithoutOpening(t *testing.T) {
	f := newFixture(t)
	file := filepath.Join(f.root, ledgerPath)
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(file, 0600); err != nil {
		t.Fatal(err)
	}
	r := auditFixture(t, f, nil)
	assertViolation(t, r, "unsafe_path", ledgerPath)
}
