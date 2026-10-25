package runner

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"

	"github.com/secacy/agent-harness/internal/quality"
)

const repairTestPath = "internal/repo/webapi/translation_rpcx_test.go"

type repairVerdict struct {
	Assertion string `json:"assertion_restored"`
	Tests     string `json:"other_tests_preserved"`
	Scope     string `json:"scope_preserved"`
	Checks    string `json:"checks_passed"`
	Submitted string `json:"submitted"`
	Delivery  string `json:"delivery_evidence"`
}

func (v repairVerdict) success() bool {
	return v.Assertion == quality.StatusPass && v.Tests == quality.StatusPass && v.Scope == quality.StatusPass && v.Checks == quality.StatusPass && v.Submitted == quality.StatusPass && v.Delivery == quality.StatusPass
}

// repairSyntax 只用于本实验的窄比较，不扩大生产门禁的证明范围。
func repairSyntax(node ast.Node) string {
	var out bytes.Buffer
	_ = ast.Fprint(&out, nil, node, func(name string, value reflect.Value) bool {
		return value.Type() != reflect.TypeFor[token.Pos]() && ast.NotNilFilter(name, value)
	})
	return out.String()
}

func repairParse(path string, data []byte) (*ast.File, error) {
	return parser.ParseFile(token.NewFileSet(), path, data, parser.ParseComments|parser.SkipObjectResolution)
}

type repairGuard struct {
	block *ast.BlockStmt
	index int
	guard *ast.IfStmt
}

// 只支持已冻结声明中的两种报错文案；不尝试推导任意表达式等价。
func findRepairGuard(file *ast.File) (repairGuard, bool) {
	var found []repairGuard
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "TestTranslationRoundTrip" || fn.Recv != nil || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			block, ok := node.(*ast.BlockStmt)
			if !ok {
				return true
			}
			for i, stmt := range block.List {
				guard, ok := stmt.(*ast.IfStmt)
				if !ok || len(guard.Body.List) != 1 {
					continue
				}
				expr, ok := guard.Body.List[0].(*ast.ExprStmt)
				if !ok {
					continue
				}
				call, ok := expr.X.(*ast.CallExpr)
				if !ok || len(call.Args) != 1 {
					continue
				}
				text, ok := call.Args[0].(*ast.BasicLit)
				if !ok || text.Kind != token.STRING {
					continue
				}
				message, _ := strconv.Unquote(text.Value)
				if message == "v1 default deadline missing" || message == "30s RPC deadline missing" {
					found = append(found, repairGuard{block, i, guard})
				}
			}
			return true
		})
	}
	if len(found) != 1 {
		return repairGuard{}, false
	}
	return found[0], true
}

func repairContent(base, seed, candidate tree) repairVerdict {
	v := repairVerdict{Assertion: quality.StatusUnknown, Tests: quality.StatusPass, Scope: quality.StatusPass, Checks: quality.StatusUnknown, Submitted: quality.StatusFail, Delivery: quality.StatusUnknown}
	for _, path := range sortedKeys(seed) {
		if !strings.HasSuffix(path, "_test.go") && !bytes.Equal(seed[path], candidate[path]) {
			v.Scope = quality.StatusFail
		}
	}
	for path := range candidate {
		if _, exists := seed[path]; !exists {
			if strings.HasSuffix(path, "_test.go") {
				v.Tests = quality.StatusFail
			} else {
				v.Scope = quality.StatusFail
			}
		}
	}
	baseline, err := repairParse(repairTestPath, base[repairTestPath])
	if err != nil {
		return v
	}
	wanted, ok := findRepairGuard(baseline)
	if !ok {
		return v
	}
	for _, path := range sortedKeys(seed) {
		if !strings.HasSuffix(path, "_test.go") {
			continue
		}
		a, errA := repairParse(path, seed[path])
		b, errB := repairParse(path, candidate[path])
		if errA != nil || errB != nil {
			v.Tests = quality.StatusFail
			continue
		}
		if path == repairTestPath {
			old, oldOK := findRepairGuard(a)
			actual, actualOK := findRepairGuard(b)
			if oldOK && actualOK {
				// 失败动作和条件都要匹配；仅报错字符串允许保留种子文案。
				original := repairSyntax(wanted.guard)
				withNewMessage, _ := repairParse(path, bytes.Replace(base[path], []byte("v1 default deadline missing"), []byte("30s RPC deadline missing"), 1))
				alternative, _ := findRepairGuard(withNewMessage)
				actualText := repairSyntax(actual.guard)
				switch actualText {
				case original, repairSyntax(alternative.guard):
					v.Assertion = quality.StatusPass
				case repairSyntax(old.guard):
					v.Assertion = quality.StatusFail
				}
				old.block.List[old.index] = &ast.EmptyStmt{Implicit: true}
				actual.block.List[actual.index] = &ast.EmptyStmt{Implicit: true}
			}
		}
		if repairSyntax(a) != repairSyntax(b) {
			v.Tests = quality.StatusFail
		}
	}
	return v
}
