package gate

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestModEntriesToolchainSingleValue(t *testing.T) {
	base := []byte("module example.com/m\n\ngo 1.25.0\n\ntoolchain go1.25.5\nrequire example.com/dep v1.2.3\n")
	entries, err := ModEntries(base)
	if err != nil {
		t.Fatal(err)
	}
	if entries["toolchain"] != "go1.25.5" || entries["go"] != "1.25.0" || len(entries) != 4 {
		t.Fatal("directive identity lost")
	}
	for _, mode := range []string{"preserved", "changed", "deleted"} {
		t.Run(mode, func(t *testing.T) {
			candidate := string(base)
			switch mode {
			case "changed":
				candidate = strings.Replace(candidate, "toolchain go1.25.5", "toolchain go1.25.6", 1)
			case "deleted":
				candidate = strings.Replace(candidate, "toolchain go1.25.5\n", "", 1)
			}
			actual, err := ModEntries([]byte(candidate))
			if err != nil {
				t.Fatal(err)
			}
			if equal := reflect.DeepEqual(entries, actual); equal != (mode == "preserved") {
				t.Fatal("toolchain identity comparison changed")
			}
		})
	}
	for _, source := range []string{"toolchain\n", "toolchain go1.25.5 extra\n", "toolchain (\n go1.25.5\n)\n", "toolchain go1.25.5\ntoolchain go1.25.5\n", "unknown value\n", "toolchain \"unterminated\n", "module x\nmodule y\n", "require x v1\nrequire x v1\n"} {
		if _, err := ModEntries([]byte(source)); err == nil {
			t.Fatal("invalid or duplicate declaration accepted")
		}
	}
}

// TestModEntriesPublishedLayouts 只读公开模块声明，保持旧样本解析身份不变。
func TestModEntriesPublishedLayouts(t *testing.T) {
	files, err := filepath.Glob("../l2/data/*/fixtures/*/go.mod")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, "../fixtures/demo-orders/go.mod", "../fixtures/rpcx-v1/go.mod", "../fixtures/rpcx-v2/go.mod")
	if len(files) < 10 {
		t.Fatal("published module coverage missing")
	}
	for _, name := range files {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		entries, err := ModEntries(data)
		if err != nil {
			t.Fatalf("published module declaration rejected: %v", err)
		}
		withoutToolchain := map[string]string{}
		for k, v := range entries {
			if k != "toolchain" {
				withoutToolchain[k] = v
			}
		}
		lines := []string{}
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(strings.TrimSpace(line), "toolchain ") {
				lines = append(lines, line)
			}
		}
		previous, err := ModEntries([]byte(strings.Join(lines, "\n")))
		if err != nil || !reflect.DeepEqual(withoutToolchain, previous) {
			t.Fatal("other module identities changed")
		}
	}
}
