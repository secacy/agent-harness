package analysisstudy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"example.com/agent-harness/internal/execution"
)

// 原型只处理本实验生成的可信单包模块，不作为候选代码加载器使用。
type packageInfo struct {
	ImportPath     string
	Dir            string
	Export         string
	GoFiles        []string
	IgnoredGoFiles []string
}

type loaded struct {
	fset    *token.FileSet
	files   []*ast.File
	info    *types.Info
	ignored []string
	listMS  int64
	typeMS  int64
}

func load(ctx context.Context, dir, cache string, tags ...string) (loaded, error) {
	var result loaded
	start := time.Now()
	args := []string{"go", "list", "-deps", "-export", "-json"}
	if len(tags) > 0 {
		args = append(args, "-tags="+strings.Join(tags, ","))
	}
	args = append(args, "./...")
	run, err := (execution.Local{}).Run(ctx, execution.Request{
		Directory: dir, CacheDir: cache,
		Args: args,
	})
	result.listMS = time.Since(start).Milliseconds()
	if err != nil {
		return result, err
	}
	if run.ExitCode != 0 {
		return result, fmt.Errorf("load packages: %s", run.Stderr)
	}
	exports := map[string]string{}
	var root packageInfo
	decoder := json.NewDecoder(bytes.NewReader(run.Stdout))
	for {
		var pkg packageInfo
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return result, err
		}
		exports[pkg.ImportPath] = pkg.Export
		if pkg.ImportPath == "example.com/study" {
			root = pkg
		}
	}
	if len(root.GoFiles) == 0 {
		return result, fmt.Errorf("study package has no active files")
	}
	result.ignored = root.IgnoredGoFiles
	result.fset = token.NewFileSet()
	for _, name := range root.GoFiles {
		file, err := parser.ParseFile(result.fset, filepath.Join(root.Dir, name), nil, parser.ParseComments)
		if err != nil {
			return result, err
		}
		result.files = append(result.files, file)
	}
	start = time.Now()
	result.info = &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{},
		Uses:  map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	config := types.Config{
		GoVersion: "go1.23",
		Importer: importer.ForCompiler(result.fset, "gc", func(path string) (io.ReadCloser, error) {
			if exports[path] == "" {
				return nil, fmt.Errorf("missing export data: %s", path)
			}
			return os.Open(exports[path])
		}),
	}
	_, err = config.Check(root.ImportPath, result.fset, result.files, result.info)
	result.typeMS = time.Since(start).Milliseconds()
	return result, err
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T, source string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"go.mod", "client.go"} {
		data, err := os.ReadFile(filepath.Join("..", "t3", "fixtures", "rpcx-v2", name))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, "rpcx-v2", name), string(data))
	}
	module := filepath.Join(dir, "study")
	writeFile(t, filepath.Join(module, "go.mod"), "module example.com/study\n\ngo 1.23.0\n\nrequire example.com/rpcx/v2 v2.0.0\nreplace example.com/rpcx/v2 => ../rpcx-v2\n")
	writeFile(t, filepath.Join(module, "client.go"), source)
	return module
}

func runTest(t *testing.T, ctx context.Context, dir, cache string) (bool, execution.Result) {
	t.Helper()
	run, err := (execution.Local{}).Run(ctx, execution.Request{
		Directory: dir, CacheDir: cache,
		Args: []string{"go", "test", "-json", "-count=1", "-timeout=10s", "./..."},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 必须观察到宿主测试本身的通过事件，退出码为零不足以证明执行过。
	passed := false
	decoder := json.NewDecoder(bytes.NewReader(run.Stdout))
	for {
		var event struct{ Action, Package, Test string }
		if err := decoder.Decode(&event); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if event.Package == "example.com/study" && event.Test == "TestTrusted" && event.Action == "pass" {
			passed = true
		}
	}
	return run.ExitCode == 0 && passed, run
}
