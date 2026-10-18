package testintegrity

import (
	"go/ast"
	"go/token"
	"slices"
	"strings"
)

// importsPreserved 保留原导入的路径和别名，只接受策略列出的默认名称导入。
// 名称不得出现在原声明中；保守拒绝局部同名，避免在这里重建 Go 名称解析。
func importsPreserved(original, candidate *sourceFile, allowed []string) bool {
	remaining := make(map[string]bool, len(original.imports))
	for _, imp := range original.imports {
		remaining[imp] = true
	}
	seen := make(map[string]bool, len(candidate.imports))
	for _, imp := range candidate.imports {
		if seen[imp] {
			return false
		}
		seen[imp] = true
		if remaining[imp] {
			delete(remaining, imp)
			continue
		}
		alias, path, _ := strings.Cut(imp, "=")
		// 当前策略仅含 errors/time；包名与完整路径相同，不推测任意包的名称。
		if alias != "" || !slices.Contains(allowed, path) || strings.Contains(path, "/") || declarationUsesName(original.file, path) {
			return false
		}
	}
	return len(remaining) == 0
}

func declarationUsesName(file *ast.File, name string) bool {
	found := false
	for _, decl := range file.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.IMPORT {
			continue
		}
		ast.Inspect(decl, func(node ast.Node) bool {
			if id, ok := node.(*ast.Ident); ok && id.Name == name {
				found = true
			}
			return !found
		})
		if found {
			return true
		}
	}
	return false
}
