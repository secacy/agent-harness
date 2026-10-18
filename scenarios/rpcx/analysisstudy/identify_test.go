package analysisstudy

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"path/filepath"
	"strconv"
)

const sdkPath = "example.com/rpcx/v2"

type site struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Kind string `json:"kind"`
}

type observation struct {
	SDK       []site  `json:"sdk"`
	Other     int     `json:"other"`
	Unknown   []site  `json:"unknown"`
	Durations []int64 `json:"duration_ns"`
}

func position(fset *token.FileSet, node ast.Node, kind string) site {
	p := fset.Position(node.Pos())
	return site{File: filepath.Base(p.Filename), Line: p.Line, Kind: kind}
}

// 两个原型都只回答身份与常量问题；不把识别 WithTimeout 当作语义证明。
func identify(p loaded, typed bool) observation {
	var out observation
	for _, file := range p.files {
		imports := map[string]string{}
		for _, spec := range file.Imports {
			path, _ := strconv.Unquote(spec.Path.Value)
			name := filepath.Base(path)
			if path == sdkPath {
				name = "rpcx"
			}
			if spec.Name != nil {
				name = spec.Name.Name
			}
			imports[name] = path
		}
		// 父节点映射只用于区分直接方法调用和取出方法值。
		calls := map[ast.Expr]bool{}
		ast.Inspect(file, func(node ast.Node) bool {
			if call, ok := node.(*ast.CallExpr); ok {
				calls[call.Fun] = true
			}
			return true
		})
		ast.Inspect(file, func(node ast.Node) bool {
			if sel, ok := node.(*ast.SelectorExpr); ok && sel.Sel.Name == "Call" {
				kind := "unknown_receiver"
				if typed {
					if selection := p.info.Selections[sel]; selection != nil {
						method, ok := selection.Obj().(*types.Func)
						if _, dynamic := selection.Recv().Underlying().(*types.Interface); dynamic {
							kind = "interface_dispatch"
						} else if ok && method.Pkg() != nil && method.Pkg().Path() == sdkPath {
							kind = "sdk_call"
						} else {
							kind = "non_sdk_call"
						}
					}
				} else {
					typ := expressionType(sel.X, 0)
					if star, ok := typ.(*ast.StarExpr); ok {
						typ = star.X
					}
					if selected, ok := typ.(*ast.SelectorExpr); ok && imported(selected, imports, sdkPath, "Client") {
						kind = "sdk_call"
					} else if id, ok := typ.(*ast.Ident); ok && id.Obj != nil {
						if spec, ok := id.Obj.Decl.(*ast.TypeSpec); ok && !spec.Assign.IsValid() {
							if _, ok := spec.Type.(*ast.StructType); ok {
								kind = "non_sdk_call"
							}
						}
					}
				}
				switch kind {
				case "sdk_call":
					out.SDK = append(out.SDK, position(p.fset, sel, kind))
					if !calls[sel] {
						out.Unknown = append(out.Unknown, position(p.fset, sel, "method_value_or_expression"))
					}
				case "non_sdk_call":
					out.Other++
				default:
					out.Unknown = append(out.Unknown, position(p.fset, sel, kind))
				}
			}
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			isTimeout := imported(sel, imports, "context", "WithTimeout")
			if typed {
				fn, ok := p.info.Uses[sel.Sel].(*types.Func)
				isTimeout = ok && fn.Pkg() != nil && fn.Pkg().Path() == "context" && fn.Name() == "WithTimeout"
			}
			if !isTimeout {
				return true
			}
			var value int64
			var known bool
			if typed {
				if tv := p.info.Types[call.Args[1]]; tv.Value != nil {
					value, known = constant.Int64Val(tv.Value)
				}
			} else if mul, ok := call.Args[1].(*ast.BinaryExpr); ok && mul.Op == token.MUL {
				// AST 原型只支持字面整数乘 time.Second；更一般表达式保留未知。
				if number, ok := mul.X.(*ast.BasicLit); ok && number.Kind == token.INT {
					if unit, ok := mul.Y.(*ast.SelectorExpr); ok && imported(unit, imports, "time", "Second") {
						n, err := strconv.ParseInt(number.Value, 0, 32)
						value, known = n*1_000_000_000, err == nil
					}
				}
			}
			if known {
				out.Durations = append(out.Durations, value)
			} else {
				out.Unknown = append(out.Unknown, position(p.fset, call, "duration_unresolved"))
			}
			return true
		})
	}
	return out
}

func imported(sel *ast.SelectorExpr, imports map[string]string, path, name string) bool {
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Obj == nil && imports[id.Name] == path && sel.Sel.Name == name
}

// 利用 parser 的词法对象处理参数、接收者、结构字段与局部复合字面量。
// 跨文件绑定、类型别名、赋值传播及递归类型不在这个 AST 原型支持范围内。
func expressionType(expr ast.Expr, depth int) ast.Expr {
	if depth > 8 {
		return nil
	}
	switch expr := expr.(type) {
	case *ast.Ident:
		if expr.Obj != nil {
			switch decl := expr.Obj.Decl.(type) {
			case *ast.Field:
				return decl.Type
			case *ast.AssignStmt:
				for i, lhs := range decl.Lhs {
					if id, ok := lhs.(*ast.Ident); ok && id.Obj == expr.Obj && i < len(decl.Rhs) {
						return expressionType(decl.Rhs[i], depth+1)
					}
				}
			}
		}
	case *ast.UnaryExpr:
		if expr.Op == token.AND {
			return expressionType(expr.X, depth+1)
		}
	case *ast.CompositeLit:
		return expr.Type
	case *ast.SelectorExpr:
		typ := expressionType(expr.X, depth+1)
		if star, ok := typ.(*ast.StarExpr); ok {
			typ = star.X
		}
		if id, ok := typ.(*ast.Ident); ok && id.Obj != nil {
			if spec, ok := id.Obj.Decl.(*ast.TypeSpec); ok {
				if st, ok := spec.Type.(*ast.StructType); ok {
					for _, field := range st.Fields.List {
						for _, name := range field.Names {
							if name.Name == expr.Sel.Name {
								return field.Type
							}
						}
					}
				}
			}
		}
	}
	return nil
}
