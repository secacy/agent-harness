// 分析程序与包加载在同一执行环境内运行，不执行候选的 init 或测试。
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// 通用事实留在原报告内；GHCLI 信封与来源证明由专用消费端校验。
type report struct {
	proofs        []sourceProof
	SchemaVersion int            `json:"schema_version"`
	Status        string         `json:"status"`
	LoadExitCode  int            `json:"load_exit_code"`
	InputSHA256   string         `json:"input_sha256"`
	ToolSHA256    string         `json:"tool_sha256"`
	Config        configuration  `json:"config"`
	Packages      []packageFacts `json:"packages"`
	Calls         []callFact     `json:"calls"`
	Gaps          []gap          `json:"gaps"`
	Diagnostic    string         `json:"diagnostic,omitempty"`
}

type configuration struct {
	GoVersion string   `json:"go_version"`
	GOOS      string   `json:"goos"`
	GOARCH    string   `json:"goarch"`
	CGO       string   `json:"cgo"`
	Tags      []string `json:"tags"`
}

type packageFacts struct {
	Path    string            `json:"path"`
	Files   map[string]string `json:"files"`
	Ignored []string          `json:"ignored"`
}

type callFact struct {
	Package   string  `json:"package"`
	File      string  `json:"file"`
	Line      int     `json:"line"`
	Function  string  `json:"function"`
	SDK       string  `json:"sdk"`
	Kind      string  `json:"kind"`
	Status    string  `json:"status"`
	Reason    string  `json:"reason"`
	Operation *string `json:"operation,omitempty"`
	TimeoutNS *int64  `json:"timeout_ns,omitempty"`
}

type gap struct {
	File   string `json:"file"`
	Line   int    `json:"line,omitempty"`
	Reason string `json:"reason"`
}

type listedPackage struct {
	ImportPath     string
	Dir            string
	Export         string
	GoFiles        []string
	IgnoredGoFiles []string
	DepOnly        bool
	Module         *struct{ GoVersion string }
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func run() error {
	input := flag.String("input", "", "expected input digest")
	tool := flag.String("tool", "", "analysis material digest")
	tagText := flag.String("tags", "", "build tags")
	flag.Parse()
	root, err := os.Getwd()
	if err != nil || len(flag.Args()) == 0 {
		return errors.New("working directory and input roots are required")
	}
	before, err := snapshot(flag.Args())
	if err != nil {
		return err
	}
	if before != *input {
		return errors.New("analysis input binding mismatch")
	}
	tags := []string{}
	if *tagText != "" {
		tags = strings.Split(*tagText, ",")
	}
	r := report{SchemaVersion: 2, Status: "complete", InputSHA256: before, ToolSHA256: *tool,
		Config:   configuration{runtime.Version(), runtime.GOOS, runtime.GOARCH, os.Getenv("CGO_ENABLED"), tags},
		Packages: []packageFacts{}, Calls: []callFact{}, Gaps: []gap{}}
	if r.Config.CGO != "0" || os.Getenv("GOPROXY") != "off" || os.Getenv("GOWORK") != "off" {
		return errors.New("analysis requires offline modules, disabled workspaces and cgo")
	}
	args := []string{"list", "-deps", "-export", "-json"}
	if len(tags) > 0 {
		args = append(args, "-tags="+*tagText)
	}
	args = append(args, "./...")
	loadCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(loadCtx, "go", args...)
	cmd.WaitDelay = time.Second
	// 元数据仍需有界，超限时停止读取并回收子进程。
	stdout, stderr := &limitedBuffer{limit: 16 << 20, cancel: cancel}, &limitedBuffer{limit: 1 << 20, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err = cmd.Run()
	if stdout.exceeded || stderr.exceeded {
		return errors.New("package metadata exceeds analysis limit")
	}
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return fmt.Errorf("start package loader: %w", err)
		}
		r.Status, r.LoadExitCode = "load_failed", exit.ExitCode()
		r.Diagnostic = stderr.String()
		if r.Diagnostic == "" {
			r.Diagnostic = err.Error()
		}
	} else if err := inspect(root, stdout.Bytes(), &r); err != nil {
		return err
	}
	after, err := snapshot(flag.Args())
	if err != nil || after != before {
		return errors.New("analysis inputs changed during inspection")
	}
	if err := sealProofs(root, r.proofs); err != nil {
		return err
	}
	proofs := r.proofs
	if proofs == nil {
		proofs = []sourceProof{}
	}
	return json.NewEncoder(os.Stdout).Encode(sourceEnvelope{SchemaVersion: 1, Report: r, Proofs: proofs})
}

type limitedBuffer struct {
	bytes.Buffer
	limit    int
	exceeded bool
	cancel   context.CancelFunc
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		b.exceeded = true
		b.cancel()
		return 0, errors.New("analysis output limit exceeded")
	}
	return b.Buffer.Write(p)
}

func inspect(root string, data []byte, r *report) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	exports := map[string]string{}
	var packages []listedPackage
	for {
		var p listedPackage
		if err := decoder.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("decode package metadata: %w", err)
		}
		exports[p.ImportPath] = p.Export
		if !p.DepOnly {
			packages = append(packages, p)
		}
	}
	if len(packages) == 0 {
		return errors.New("loader returned no target packages")
	}
	for _, p := range packages {
		if err := inspectPackage(root, p, exports, r); err != nil {
			return err
		}
	}
	// ./... 不列出完全由未激活文件组成的目录，仍须逐文件登记覆盖缺口。
	listed := map[string]bool{}
	for _, p := range r.Packages {
		for path := range p.Files {
			listed[path] = true
		}
		for _, path := range p.Ignored {
			listed[path] = true
		}
	}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			rel, err := relative(root, path)
			if err != nil {
				return err
			}
			if !listed[rel] {
				r.Gaps = append(r.Gaps, gap{File: rel, Reason: "unlisted_build_file"})
			}
		}
		return nil
	}); err != nil {
		return err
	}
	sort.Slice(r.Calls, func(i, j int) bool {
		if r.Calls[i].File == r.Calls[j].File {
			return r.Calls[i].Line < r.Calls[j].Line
		}
		return r.Calls[i].File < r.Calls[j].File
	})
	return nil
}

func inspectPackage(root string, p listedPackage, exports map[string]string, r *report) error {
	fset := token.NewFileSet()
	files := []*ast.File{}
	facts := packageFacts{Path: p.ImportPath, Files: map[string]string{}, Ignored: []string{}}
	for _, name := range p.GoFiles {
		path := filepath.Join(p.Dir, name)
		rel, err := relative(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(fset, path, data, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		files = append(files, file)
		facts.Files[rel] = digest(data)
		for _, imp := range file.Imports {
			if imp.Path.Value == `"reflect"` || imp.Path.Value == `"unsafe"` || imp.Path.Value == `"plugin"` {
				r.Gaps = append(r.Gaps, gap{File: rel, Line: fset.Position(imp.Pos()).Line, Reason: "dynamic_import"})
			}
		}
	}
	for _, name := range p.IgnoredGoFiles {
		rel, err := relative(root, filepath.Join(p.Dir, name))
		if err != nil {
			return err
		}
		if !strings.HasSuffix(name, "_test.go") {
			facts.Ignored = append(facts.Ignored, rel)
			r.Gaps = append(r.Gaps, gap{File: rel, Reason: "inactive_build_file"})
		}
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{},
		Uses: map[*ast.Ident]types.Object{}, Selections: map[*ast.SelectorExpr]*types.Selection{}}
	config := types.Config{Sizes: types.SizesFor("gc", runtime.GOARCH),
		Importer: importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
			if exports[path] == "" {
				return nil, fmt.Errorf("missing export data: %s", path)
			}
			return os.Open(exports[path])
		})}
	if p.Module != nil && p.Module.GoVersion != "" {
		config.GoVersion = "go" + p.Module.GoVersion
	}
	if _, err := config.Check(p.ImportPath, fset, files, info); err != nil {
		return fmt.Errorf("type-check loaded package %s: %w", p.ImportPath, err)
	}
	r.Packages = append(r.Packages, facts)
	for _, file := range files {
		inspectCalls(root, p.ImportPath, fset, file, info, r)
	}
	return nil
}

func relative(root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil || !filepath.IsLocal(rel) {
		return "", errors.New("loaded source is outside target module")
	}
	return filepath.ToSlash(rel), nil
}

func digest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func snapshot(roots []string) (string, error) {
	files := map[string]string{}
	total := int64(0)
	for _, root := range roots {
		if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			stat, err := entry.Info()
			if err != nil || !stat.Mode().IsRegular() || stat.Size() > 1<<20 {
				return errors.New("analysis inputs must be regular files of at most 1 MiB")
			}
			total += stat.Size()
			if len(files) >= 10000 || total > 16<<20 {
				return errors.New("analysis input limit exceeded")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, err := relative(root, path)
			if err != nil {
				return err
			}
			files[filepath.Base(root)+"/"+rel] = digest(data)
			return nil
		}); err != nil {
			return "", err
		}
	}
	data, err := json.Marshal(files)
	return digest(data), err
}
