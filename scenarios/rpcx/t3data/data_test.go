package t3data

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func copyScene(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files, err := Read(context.Background(), "../t3")
	if err != nil {
		t.Fatal(err)
	}
	for path, data := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestDataReleasePreservesOriginalBytes(t *testing.T) {
	archive := "../_archive/rpcx-t3-v0.2.0"
	data, err := os.ReadFile(filepath.Join(archive, "freeze.json"))
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != "5de45016c53e39b218a446d3f1725f545f7b0461c36b7908467737a33d80b96d" {
		t.Fatal("historical freeze manifest changed")
	}
	var original map[string]string
	if err := json.Unmarshal(data, &original); err != nil {
		t.Fatal(err)
	}
	actual := map[string]string{}
	err = filepath.WalkDir(archive, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(archive, path)
		if rel == "freeze.json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		actual[filepath.ToSlash(rel)] = fmt.Sprintf("%x", sha256.Sum256(data))
		return nil
	})
	if err != nil || len(original) != 34 || !reflect.DeepEqual(original, actual) {
		t.Fatalf("historical inputs differ: %v", err)
	}
	files, err := Read(context.Background(), "../t3")
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := Frozen(files)
	if err != nil || !frozen || len(files) != 28 {
		t.Fatalf("invalid active release: %v %v", frozen, err)
	}
	for path, data := range files {
		if path == "freeze.json" {
			continue
		}
		if fmt.Sprintf("%x", sha256.Sum256(data)) != original[path] {
			t.Fatalf("release changed original data: %s", path)
		}
	}
}

func TestDataBoundaryAndTampering(t *testing.T) {
	for _, mode := range []string{"readme", "add", "remove", "change", "unknown-root", "symlink", "manifest", "unknown-field", "trailing-json", "old-format", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			root := copyScene(t)
			path := filepath.Join(root, "specs/v0.2.0.md")
			expectReadError, expectManifestError := false, false
			switch mode {
			case "readme":
				path = filepath.Join(root, "README.md")
			case "add":
				path = filepath.Join(root, "fixtures/demo-orders/extra.go")
			case "remove":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "unknown-root":
				path = filepath.Join(root, "driver.go")
				expectReadError = true
			case "symlink":
				if err := os.Symlink(path, filepath.Join(root, "fixtures/link.go")); err != nil {
					t.Fatal(err)
				}
				expectReadError = true
			case "manifest", "unknown-field", "trailing-json", "old-format":
				path = filepath.Join(root, "freeze.json")
				expectManifestError = true
			}
			if mode != "remove" && mode != "symlink" && mode != "cancel" {
				data := []byte("changed")
				switch mode {
				case "unknown-field":
					data = []byte(`{"schema_version":1,"version":"rpcx-t3-data-v1","files":{"x":"y"},"extra":true}`)
				case "trailing-json":
					original, _ := os.ReadFile(path)
					data = append(original, []byte(" {}")...)
				case "old-format":
					data = []byte(`{"specs/v0.2.0.md":"hash"}`)
				}
				if err := os.WriteFile(path, data, 0644); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" {
				cancel()
				expectReadError = true
			}
			files, err := Read(ctx, root)
			if expectReadError {
				if err == nil {
					t.Fatal("unexpected input reached validation")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			frozen, err := Frozen(files)
			if expectManifestError {
				if err == nil {
					t.Fatal("invalid manifest accepted")
				}
				return
			}
			if err != nil || frozen != (mode == "readme") {
				t.Fatalf("unexpected frozen state: %v %v", frozen, err)
			}
		})
	}
}

func TestToolSourceIsOutsideDataIdentity(t *testing.T) {
	files, err := Read(context.Background(), "../t3")
	if err != nil {
		t.Fatal(err)
	}
	for path := range files {
		if strings.HasPrefix(path, "acceptance/") || path == "README.md" {
			t.Fatalf("non-data input: %s", path)
		}
	}
	if !strings.Contains(Source(), "func Frozen(") {
		t.Fatal("loader source was not retained")
	}
}
