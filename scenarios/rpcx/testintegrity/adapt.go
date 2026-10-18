package testintegrity

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed policy.json adapt.go syntax.go compare.go types.go
var materials embed.FS

type policy struct {
	Version       string            `json:"version"`
	SpecSHA256    string            `json:"spec_sha256"`
	BaselineFiles map[string]string `json:"baseline_files"`
}

func loadPolicy() (policy, string, error) {
	var p policy
	data, err := materials.ReadFile("policy.json")
	if err != nil {
		return p, "", err
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return p, "", err
	}
	hashes := map[string]string{}
	for _, name := range []string{"policy.json", "adapt.go", "syntax.go", "compare.go", "types.go"} {
		data, err := materials.ReadFile(name)
		if err != nil {
			return p, "", err
		}
		hashes[name] = digest(data)
	}
	data, _ = json.Marshal(hashes)
	return p, digest(data), nil
}

// AdaptBaseline 只适配本场景已冻结的三份测试，不读取候选、公开补丁或 oracle。
// 返回的新文件可以供后续可信生产副本装配使用，输入字节不被修改。
func AdaptBaseline(baseline map[string][]byte) (map[string][]byte, error) {
	p, _, err := loadPolicy()
	if err != nil {
		return nil, err
	}
	binding, err := bind(baseline)
	if err != nil {
		return nil, err
	}
	if len(binding.Files) != len(p.BaselineFiles) {
		return nil, errors.New("unsupported baseline test inventory")
	}
	for path, hash := range p.BaselineFiles {
		if binding.Files[path] != hash {
			return nil, fmt.Errorf("unsupported baseline test content: %s", path)
		}
	}
	out := map[string][]byte{}
	for _, path := range keys(baseline) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, baseline[path], parser.SkipObjectResolution)
		if err != nil {
			return nil, fmt.Errorf("parse trusted baseline: %w", err)
		}
		for _, imp := range file.Imports {
			if imp.Path.Value == `"example.com/rpcx"` {
				imp.Path.Value = `"example.com/rpcx/v2"`
			}
		}
		adapted := 0
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			receiver, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			inventory := path == "internal/inventory/client_test.go" && receiver.Name == "c" && (sel.Sel.Name == "Reserve" || sel.Sel.Name == "Release") && len(call.Args) == 2
			cancel := path == "internal/orders/service_test.go" && receiver.Name == "s" && sel.Sel.Name == "Cancel" && len(call.Args) == 1
			if inventory || cancel {
				ctx := &ast.CallExpr{Fun: &ast.SelectorExpr{X: ast.NewIdent("context"), Sel: ast.NewIdent("Background")}}
				call.Args = append([]ast.Expr{ctx}, call.Args...)
				adapted++
			}
			return true
		})
		want := map[string]int{"internal/inventory/client_test.go": 2, "internal/orders/service_test.go": 3}[path]
		if adapted != want {
			return nil, errors.New("trusted test adaptation plan did not match baseline")
		}
		var source bytes.Buffer
		if err := format.Node(&source, fset, file); err != nil {
			return nil, err
		}
		out[path] = source.Bytes()
	}
	return out, nil
}

func digest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func bind(files map[string][]byte) (Binding, error) {
	hashes := map[string]string{}
	total := 0
	for path, data := range files {
		if !filepath.IsLocal(path) || path == "." || filepath.ToSlash(filepath.Clean(path)) != path || strings.Contains(path, `\`) || !strings.HasSuffix(path, "_test.go") {
			return Binding{}, errors.New("expected canonical relative test file paths")
		}
		total += len(data)
		if len(data) > 1<<20 || len(files) > 10000 || total > 16<<20 {
			return Binding{}, errors.New("test source limit exceeded")
		}
		hashes[path] = digest(data)
	}
	data, _ := json.Marshal(hashes)
	return Binding{SHA256: digest(data), Files: hashes}, nil
}

func keys[T any](m map[string]T) []string {
	result := make([]string, 0, len(m))
	for key := range m {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
