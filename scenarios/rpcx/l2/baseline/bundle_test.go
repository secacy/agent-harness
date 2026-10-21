package baseline

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type archiveEntry struct {
	name string
	body []byte
	kind byte
	size int64
}

func testArchive(t *testing.T, entries []archiveEntry, tail []byte) []byte {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for _, e := range entries {
		kind := e.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		size := int64(len(e.body))
		if e.size != 0 {
			size = e.size
		}
		h := &tar.Header{Name: e.name, Size: size, Mode: 0644, Typeflag: kind}
		if kind != tar.TypeReg {
			h.Size = 0
			h.Linkname = "../outside"
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			if _, err := tw.Write(e.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	raw.Write(tail)
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	if _, err := gz.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func testBundle(t *testing.T, archive []byte, hashes map[string]string, expanded int64) map[string][]byte {
	t.Helper()
	m, err := json.Marshal(map[string]any{"schema_version": 1, "modules": []any{}, "files": hashes, "expanded_bytes": expanded})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{"dependencies/modules.json": m}
	index := bundleIndex{SchemaVersion: 1, Version: GHCLIVersion, ManifestSHA256: digest(m), LogicalSize: int64(len(archive)), LogicalSHA256: digest(archive)}
	for start, i := 0, 0; start < len(archive); i++ {
		end := min(start+8<<20, len(archive))
		name := fmt.Sprintf("dependencies/modules-%03d.tar.gz.part", i)
		b := archive[start:end]
		files[name] = b
		index.Chunks = append(index.Chunks, bundleChunk{name, int64(len(b)), digest(b)})
		start = end
	}
	files["dependencies/bundle.json"], err = json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	return files
}
func goodBundle(t *testing.T) map[string][]byte {
	return testBundle(t, testArchive(t, []archiveEntry{{name: "module/file.go", body: []byte("x")}}, nil), map[string]string{"module/file.go": digest([]byte("x"))}, 1)
}
func changeIndex(t *testing.T, files map[string][]byte, change func(*bundleIndex)) {
	t.Helper()
	var index bundleIndex
	if err := decode(files["dependencies/bundle.json"], &index); err != nil {
		t.Fatal(err)
	}
	change(&index)
	data, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	files["dependencies/bundle.json"] = data
}

func TestBundleIndexRejectsUntrustedRepresentation(t *testing.T) {
	for _, mode := range []string{"missing", "extra", "size", "hash", "name", "path", "duplicate", "reordered", "empty", "too-many", "chunk-limit", "compressed-limit", "logical-hash", "manifest-hash", "version", "unknown-field", "trailing-json"} {
		t.Run(mode, func(t *testing.T) {
			files := goodBundle(t)
			switch mode {
			case "missing":
				delete(files, "dependencies/modules-000.tar.gz.part")
			case "extra":
				files["dependencies/modules-001.tar.gz.part"] = []byte("extra")
			case "unknown-field":
				files["dependencies/bundle.json"] = bytes.Replace(files["dependencies/bundle.json"], []byte(`"schema_version":1`), []byte(`"schema_version":1,"expanded_limit":999999999`), 1)
			case "trailing-json":
				files["dependencies/bundle.json"] = append(files["dependencies/bundle.json"], []byte(` {}`)...)
			default:
				changeIndex(t, files, func(i *bundleIndex) {
					switch mode {
					case "size":
						i.Chunks[0].Size++
					case "hash":
						i.Chunks[0].SHA256 = strings.Repeat("0", 64)
					case "name":
						i.Chunks[0].Name = "arbitrary"
					case "path":
						i.Chunks[0].Name = "dependencies/../escape"
					case "duplicate":
						i.Chunks = append(i.Chunks, i.Chunks[0])
					case "reordered":
						i.Chunks = append([]bundleChunk{{Name: "dependencies/modules-001.tar.gz.part", Size: 1, SHA256: digest([]byte("x"))}}, i.Chunks...)
					case "empty":
						i.Chunks = nil
					case "too-many":
						for len(i.Chunks) < 9 {
							i.Chunks = append(i.Chunks, i.Chunks[0])
						}
					case "chunk-limit":
						i.Chunks[0].Size = 8<<20 + 1
					case "compressed-limit":
						i.LogicalSize = 64<<20 + 1
					case "logical-hash":
						i.LogicalSHA256 = strings.Repeat("0", 64)
					case "manifest-hash":
						i.ManifestSHA256 = strings.Repeat("0", 64)
					case "version":
						i.Version = Version
					}
				})
			}
			if err := unpackVersion(files, t.TempDir(), GHCLIVersion); err == nil {
				t.Fatal("invalid bundle accepted")
			}
		})
	}
}

func TestBundleArchiveRejectsMaliciousMembersAndTrailer(t *testing.T) {
	for _, mode := range []string{"parent", "absolute", "backslash", "dot", "symlink", "hardlink", "directory", "duplicate", "bad-hash", "missing", "size-mismatch", "crc", "truncated", "gzip-stream", "compressed-tail", "tar-tail", "zero-bomb", "expanded-limit", "negative-expanded", "count-limit", "manifest-path", "manifest-hash", "file-limit"} {
		t.Run(mode, func(t *testing.T) {
			entries := []archiveEntry{{name: "module/file.go", body: []byte("x")}}
			hashes := map[string]string{"module/file.go": digest([]byte("x"))}
			expanded := int64(1)
			var tail []byte
			switch mode {
			case "parent":
				entries[0].name = "../escape"
			case "absolute":
				entries[0].name = "/escape"
			case "backslash":
				entries[0].name = "module\\escape"
			case "dot":
				entries[0].name = "."
			case "symlink":
				entries[0].kind = tar.TypeSymlink
			case "hardlink":
				entries[0].kind = tar.TypeLink
			case "directory":
				entries[0].kind = tar.TypeDir
			case "duplicate":
				entries = append(entries, entries[0])
				expanded = 2
			case "bad-hash":
				hashes["module/file.go"] = digest([]byte("y"))
			case "missing":
				entries = nil
			case "size-mismatch":
				expanded = 2
			case "tar-tail":
				tail = []byte("nonzero")
			case "zero-bomb":
				tail = make([]byte, 1<<20+1)
			case "expanded-limit":
				expanded = 128<<20 + 1
			case "negative-expanded":
				expanded = -1
			case "count-limit":
				for i := 0; i < 10001; i++ {
					hashes[fmt.Sprintf("m/%d", i)] = digest(nil)
				}
			case "manifest-path":
				hashes["../escape"] = digest(nil)
			case "manifest-hash":
				hashes["module/file.go"] = "not-a-hash"
			case "file-limit":
				entries[0].body = make([]byte, 16<<20+1)
				expanded = int64(len(entries[0].body))
				hashes["module/file.go"] = digest(entries[0].body)
			}
			archive := testArchive(t, entries, tail)
			switch mode {
			case "crc":
				archive[len(archive)-8] ^= 0xff
			case "truncated":
				archive = archive[:len(archive)-4]
			case "gzip-stream":
				archive = append(archive, archive...)
			case "compressed-tail":
				archive = append(archive, 0, 0, 0)
			}
			if err := unpackVersion(testBundle(t, archive, hashes, expanded), t.TempDir(), GHCLIVersion); err == nil {
				t.Fatal("invalid module archive accepted")
			}
		})
	}
}

func TestBundleExtractionRejectsExistingAndLinkedTargets(t *testing.T) {
	for _, mode := range []string{"root-link", "directory-link", "existing"} {
		t.Run(mode, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "output")
			outside := t.TempDir()
			switch mode {
			case "root-link":
				if err := os.Symlink(outside, root); err != nil {
					t.Fatal(err)
				}
			case "directory-link":
				if err := os.Mkdir(root, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Join(root, "module")); err != nil {
					t.Fatal(err)
				}
			case "existing":
				if err := write(filepath.Join(root, "module/file.go"), []byte("original")); err != nil {
					t.Fatal(err)
				}
			}
			if err := unpackVersion(goodBundle(t), root, GHCLIVersion); err == nil {
				t.Fatal("unsafe output accepted")
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatal("outside directory changed", err)
			}
			if mode == "existing" {
				b, err := os.ReadFile(filepath.Join(root, "module/file.go"))
				if err != nil || string(b) != "original" {
					t.Fatal("existing file changed", err)
				}
			}
		})
	}
}

func TestModulePolicyAndPublishedLargeBundle(t *testing.T) {
	old, err := ModulePolicy(Version)
	if err != nil || old.ExpandedBytes != 32<<20 || old.FileBytes != 8<<20 {
		t.Fatal("old policy changed", err)
	}
	newPolicy, err := ModulePolicy(GHCLIVersion)
	if err != nil || newPolicy.ExpandedBytes != 128<<20 || newPolicy.FileBytes != 16<<20 || newPolicy.CompressedBytes != 64<<20 || newPolicy.Files != 10000 {
		t.Fatal("new policy changed", err)
	}
	if _, err := ModulePolicy("untrusted"); err == nil {
		t.Fatal("unknown policy accepted")
	}
	files, err := readVersion(context.Background(), filepath.Join(projectRoot(t), "scenarios/rpcx/l2/data", GHCLIVersion), GHCLIVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := frozenVersion(files, GHCLIVersion); err != nil {
		t.Fatal(err)
	}
	var index bundleIndex
	if err := decode(files["dependencies/bundle.json"], &index); err != nil {
		t.Fatal(err)
	}
	if index.LogicalSize <= 32<<20 {
		t.Fatal("published positive does not exceed old archive size")
	}
	root := t.TempDir()
	if err := ExtractModulesVersion(files, root, GHCLIVersion); err != nil {
		t.Fatal(err)
	}
	var m struct {
		Files map[string]string `json:"files"`
	}
	if err := json.Unmarshal(files["dependencies/modules.json"], &m); err != nil {
		t.Fatal(err)
	}
	if err := CheckModuleTree(context.Background(), root, m.Files, GHCLIVersion); err != nil {
		t.Fatal(err)
	}
	if err := CheckModuleTree(context.Background(), root, m.Files, Version); err == nil {
		t.Fatal("old cache policy accepted new cache")
	}
	if err := ExtractModules(files, t.TempDir()); err == nil {
		t.Fatal("old parser accepted new release")
	}
	if err := ExtractModulesVersion(files, t.TempDir(), "untrusted"); err == nil {
		t.Fatal("unknown release accepted")
	}
	for name := range m.Files {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte("changed"), 0644); err != nil {
			t.Fatal(err)
		}
		break
	}
	if err := CheckModuleTree(context.Background(), root, m.Files, GHCLIVersion); err == nil {
		t.Fatal("module cache mutation accepted")
	}
}

func TestModuleFileBoundaryAndFrozenAuthentication(t *testing.T) {
	body := make([]byte, 16<<20)
	archive := testArchive(t, []archiveEntry{{name: "m/file", body: body}}, nil)
	files := testBundle(t, archive, map[string]string{"m/file": digest(body)}, int64(len(body)))
	if err := unpackVersion(files, t.TempDir(), GHCLIVersion); err != nil {
		t.Fatal(err)
	}
	if err := ExtractModulesVersion(files, t.TempDir(), GHCLIVersion); err == nil {
		t.Fatal("unpublished release accepted")
	}
}
