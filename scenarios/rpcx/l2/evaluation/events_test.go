package evaluation

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestReferenceSemanticBoundary(t *testing.T) {
	tools, err := toolData()
	if err != nil {
		t.Fatal(err)
	}
	allowed := []string{"Canonical", "Overlap", "ReadFiles", "CopyFiles", "Write", "WriteJSON", "Digest", "Hashes", "TreeHash", "Module", "Materials"}
	for name, source := range tools {
		if !strings.HasPrefix(name, "evaluation/") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range file.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{"/rpcx/goddd", "/rpcx/translation", "/rpcx/workouts", "/rpcx/analysis", "/rpcx/testintegrity", "/internal/quality", "/rpcx/checks"} {
				if strings.Contains(path, forbidden) {
					t.Errorf("reference imports runtime semantics: %s", path)
				}
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if selector, ok := node.(*ast.SelectorExpr); ok {
				if ident, ok := selector.X.(*ast.Ident); ok && ident.Name == "gate" && !slices.Contains(allowed, selector.Sel.Name) {
					t.Errorf("reference reuses non-infrastructure gate function: %s", selector.Sel.Name)
				}
			}
			return true
		})
	}
}

func TestReferenceEventEvidence(t *testing.T) {
	name := godddPackage + "/server/TestL2ReferenceExpiry"
	pkg := godddPackage + "/server"
	for _, mode := range []string{"pass", "empty", "skipped", "malformed", "failure", "unattributed", "marker-pass", "missing-package"} {
		t.Run(mode, func(t *testing.T) {
			data := passedEvents([]string{name}, []string{pkg})
			exit := 0
			switch mode {
			case "empty":
				data = nil
			case "skipped":
				data = []byte(strings.ReplaceAll(string(data), `"Action":"pass","Package":"`+pkg+`","Test":"TestL2ReferenceExpiry"`, `"Action":"skip","Package":"`+pkg+`","Test":"TestL2ReferenceExpiry"`))
			case "malformed":
				data = append(data, []byte("invalid JSON")...)
			case "failure", "unattributed", "marker-pass":
				var stream []byte
				for _, event := range []map[string]string{
					{"Action": "run", "Package": pkg, "Test": "TestL2ReferenceExpiry"},
					{"Action": "output", "Package": pkg, "Test": "TestL2ReferenceExpiry", "Output": "L2_REFERENCE_FINDING {\"rule\":\"TIME-001\",\"detail\":\"wrong deadline\"}\n"},
					{"Action": "fail", "Package": pkg, "Test": "TestL2ReferenceExpiry"},
					{"Action": "fail", "Package": pkg},
				} {
					if mode == "unattributed" && event["Action"] == "output" {
						continue
					}
					if mode == "marker-pass" && event["Action"] == "fail" {
						event["Action"] = "pass"
					}
					blob, _ := json.Marshal(event)
					stream = append(stream, append(blob, '\n')...)
				}
				data = stream
				if mode != "marker-pass" {
					exit = 1
				}
			case "missing-package":
				lines := strings.Split(string(data), "\n")
				data = []byte(strings.Join(lines[:len(lines)-2], "\n"))
			}
			got := observe(data, exit, []string{name}, []string{pkg}, nil, true)
			want := Inconclusive
			if mode == "pass" {
				want = Pass
			}
			if mode == "failure" {
				want = Fail
			}
			if got.Status != want {
				t.Fatalf("%s=%s want %s: %+v", mode, got.Status, want, got)
			}
		})
	}
}

func TestIndependentModuleDeclarationComparison(t *testing.T) {
	before := []byte(`{"Go":"1.23.0","Require":[{"Path":"example.com/rpcx","Version":"v1.0.0"},{"Path":"other","Version":"v1.0.0"}],"Replace":[{"Old":{"Path":"example.com/rpcx"},"New":{"Path":"../rpcx-v1"}},{"Old":{"Path":"example.com/rpcx/v2"},"New":{"Path":"../rpcx-v2"}}]}`)
	after := []byte(`{"Go":"1.23.0","Require":[{"Path":"other","Version":"v1.0.0"},{"Path":"example.com/rpcx/v2","Version":"v2.0.0"}],"Replace":[{"Old":{"Path":"example.com/rpcx/v2"},"New":{"Path":"../rpcx-v2"}}]}`)
	if got := dependencyObservation(before, after, 0); got.Status != Pass {
		t.Fatal(got)
	}
	for _, change := range []string{strings.Replace(string(after), "../rpcx-v2", "/tmp/other-sdk", 1), strings.Replace(string(after), "1.23.0", "1.22.0", 1), strings.Replace(string(after), `"Path":"other","Version":"v1.0.0"`, `"Path":"other","Version":"v1.1.0"`, 1)} {
		if got := dependencyObservation(before, []byte(change), 0); got.Status != Fail {
			t.Fatal("unapproved module change accepted", got)
		}
	}
}
