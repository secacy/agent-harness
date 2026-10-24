package translation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"

	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/analysis"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

const ScopeVerifierVersion = "rpcx-translation-evidence-v2"

// ScopeAnalysis 保存同一次配置的原始分析事实，不消费最终质量结论。
type ScopeAnalysis struct {
	Baseline  analysis.Report `json:"baseline"`
	Candidate analysis.Report `json:"candidate"`
}
type ScopeAnalysisBinding struct {
	BaselineSHA256   string `json:"baseline_sha256"`
	CandidateSHA256  string `json:"candidate_sha256"`
	ComparisonSHA256 string `json:"comparison_sha256"`
}
type ScopeFile struct {
	Path                     string                  `json:"path"`
	BaselineSHA256           string                  `json:"baseline_sha256"`
	ExpectedSHA256           string                  `json:"expected_sha256"`
	CandidateSHA256          string                  `json:"candidate_sha256"`
	ExpectedStructureSHA256  string                  `json:"expected_structure_sha256"`
	RemainingStructureSHA256 string                  `json:"remaining_structure_sha256"`
	Nodes                    []analysis.BoundaryNode `json:"nodes,omitempty"`
	IgnoredComments          []analysis.BoundaryNode `json:"ignored_comments,omitempty"`
}

// ScopeProof 保存归约依据和完整剩余比较；原候选始终保持原字节。
type ScopeProof struct {
	SchemaVersion int                             `json:"schema_version"`
	Binding       quality.Binding                 `json:"binding"`
	WorkerSHA256  string                          `json:"worker_sha256"`
	Analyses      map[string]ScopeAnalysisBinding `json:"analyses"`
	Files         []ScopeFile                     `json:"files"`
	Matched       bool                            `json:"matched"`
	Gaps          []string                        `json:"gaps"`
}

func jsonDigest(v any) string { data, _ := json.Marshal(v); return gate.Digest(data) }

// ProveScope 仅消费可信 Inspect 报告；审计须同时核对归档工具、命令及输入绑定。
func ProveScope(base, candidate, fixtures map[string][]byte, reports map[string]ScopeAnalysis) (ScopeProof, error) {
	worker, err := analysis.WorkerMaterials()
	if err != nil {
		return ScopeProof{}, err
	}
	return proveScope(base, candidate, fixtures, reports, gate.TreeHash(worker))
}

// VerifyScopeProof 由审计消费已核对原始工具摘要的归档材料，不运行类型加载或业务代码。
func VerifyScopeProof(base, candidate, fixtures map[string][]byte, reports map[string]ScopeAnalysis, saved ScopeProof) error {
	actual, err := proveScope(base, candidate, fixtures, reports, saved.WorkerSHA256)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(actual, saved) {
		return fmt.Errorf("archived scope proof differs from its input and analysis evidence")
	}
	return nil
}

func proveScope(base, candidate, fixtures map[string][]byte, reports map[string]ScopeAnalysis, workerSHA string) (ScopeProof, error) {
	out := ScopeProof{SchemaVersion: 1, Binding: quality.Binding{SpecSHA256: SpecSHA256(), BaselineSHA256: gate.TreeHash(base), CandidateSHA256: gate.TreeHash(candidate)}, Analyses: map[string]ScopeAnalysisBinding{}, Files: []ScopeFile{}, Gaps: []string{}}
	gap := func(reason string) (ScopeProof, error) { out.Gaps = append(out.Gaps, reason); return out, nil }
	out.WorkerSHA256 = workerSHA
	expected, err := adapt(base)
	if err != nil {
		return gap("baseline_adaptation_unavailable")
	}
	if len(reports) != 2 {
		return gap("required_configuration_analysis_missing")
	}
	nodes := map[string]map[string]ast.Node{}
	receipts := map[string][]analysis.BoundaryNode{}
	inputSHA := func(module map[string][]byte) string {
		layout := map[string][]byte{}
		for name, data := range module {
			layout["translation/"+name] = data
		}
		for _, sdk := range []string{"rpcx-v1", "rpcx-v2"} {
			for name, data := range gate.Module(fixtures, sdk) {
				layout[sdk+"/"+name] = data
			}
		}
		return gate.TreeHash(layout)
	}
	for i, config := range Configurations() {
		pair, ok := reports[config.ID]
		if !ok {
			return gap("required_configuration_analysis_missing")
		}
		if pair.Baseline.Status != "complete" || pair.Candidate.Status != "complete" {
			return gap("typed_analysis_incomplete")
		}
		if pair.Baseline.InputSHA256 != inputSHA(base) || pair.Candidate.InputSHA256 != inputSHA(candidate) || pair.Candidate.ToolSHA256 != out.WorkerSHA256 || pair.Baseline.ToolSHA256 != out.WorkerSHA256 || !reflect.DeepEqual(pair.Candidate.Config.Tags, config.Tags) {
			return out, fmt.Errorf("scope analysis input, tool or configuration mismatch: %s", config.ID)
		}
		comparison, err := analysis.Compare(pair.Baseline, pair.Candidate)
		if err != nil {
			return out, err
		}
		out.Analyses[config.ID] = ScopeAnalysisBinding{jsonDigest(pair.Baseline), jsonDigest(pair.Candidate), jsonDigest(comparison)}
		if !configuredCoverage(config.ID, comparison) || len(pair.Candidate.Calls) != 1 || pair.Candidate.Calls[0].Status != "covered" {
			return gap("direct_boundary_not_covered: " + config.ID)
		}
		if err := analysis.ValidateBoundaryEvidence(pair.Candidate, candidate); err != nil {
			return out, err
		}
		call := pair.Candidate.Calls[0]
		if call.File != adapters[i] || call.Boundary == nil {
			return gap("precise_boundary_evidence_missing: " + config.ID)
		}
		n, err := analysis.BoundaryNodes(call, candidate[call.File])
		if err != nil {
			return out, err
		}
		if n == nil {
			return gap("boundary_not_reducible: " + call.Boundary.Reason)
		}
		fn := n["function"].(*ast.FuncDecl)
		if fn.Name.Name != "Translate" || len(n["rpc"].(*ast.CallExpr).Args) != 4 {
			return out, fmt.Errorf("unexpected scope owner: %s", call.File)
		}
		nodes[call.File], receipts[call.File] = n, call.Boundary.Nodes
	}
	if reason := constantClosure(expected, candidate, nodes); reason != "" {
		return gap(reason)
	}
	paths := map[string]bool{}
	for name := range base {
		paths[name] = true
	}
	for name := range candidate {
		paths[name] = true
	}
	for _, name := range gate.SortedKeys(paths) {
		if strings.HasSuffix(name, "_test.go") || name == "go.mod" {
			continue
		}
		original, want, actual := base[name], expected[name], candidate[name]
		row := ScopeFile{Path: name, BaselineSHA256: gate.Digest(original), ExpectedSHA256: gate.Digest(want), CandidateSHA256: gate.Digest(actual)}
		left, right := gate.Digest(want), gate.Digest(actual)
		if n := nodes[name]; n != nil {
			f := n["file"].(*ast.File)
			host, err := parser.ParseFile(token.NewFileSet(), name, want, parser.ParseComments|parser.SkipObjectResolution)
			if err != nil {
				return out, err
			}
			if !scopeComments(original, f) {
				return gap("semantic_directive_or_build_prefix_changed: " + name)
			}
			fn := n["function"].(*ast.FuncDecl)
			var body []ast.Stmt
			for _, s := range fn.Body.List {
				if s != n["derive"] && s != n["release"] {
					body = append(body, s)
				}
			}
			fn.Body.List = body
			n["rpc"].(*ast.CallExpr).Args[0] = ast.NewIdent(n["parent"].(*ast.Ident).Name)
			var declarations []ast.Decl
			for _, d := range f.Decls {
				if d != n["constant"] {
					declarations = append(declarations, d)
				}
			}
			f.Decls = declarations
			for _, group := range f.Comments {
				for _, comment := range group.List {
					if strings.HasPrefix(comment.Text, "//go:build ") {
						continue
					}
					start, end := int(comment.Pos())-1, int(comment.End())-1
					row.IgnoredComments = append(row.IgnoredComments, analysis.BoundaryNode{Role: "ordinary_comment", File: name, Offset: start, End: end, SHA256: gate.Digest(actual[start:end])})
				}
			}
			removeUnusedTime(f)
			left, right = scopeSyntax(host), scopeSyntax(f)
			row.Nodes = receipts[name]
		} else if strings.HasSuffix(name, ".go") && original != nil && actual != nil {
			var err error
			left, err = syntax(name, want)
			if err != nil {
				return gap("baseline_parse_failed: " + name)
			}
			right, err = syntax(name, actual)
			if err != nil {
				return gap("candidate_parse_failed: " + name)
			}
		}
		row.ExpectedStructureSHA256, row.RemainingStructureSHA256 = left, right
		out.Files = append(out.Files, row)
		if left != right {
			out.Gaps = append(out.Gaps, "unexplained_production_structure: "+name)
		}
	}
	out.Matched = len(out.Gaps) == 0
	return out, nil
}

func constantClosure(expected, candidate map[string][]byte, nodes map[string]map[string]ast.Node) string {
	// 同名常量可属于互斥配置；完整生产快照中的其他同名位置保守保留为缺口。
	constants := map[string]map[*ast.Ident]bool{}
	for _, n := range nodes {
		if d, ok := n["constant"].(*ast.GenDecl); ok {
			id := d.Specs[0].(*ast.ValueSpec).Names[0]
			if constants[id.Name] == nil {
				constants[id.Name] = map[*ast.Ident]bool{}
			}
			constants[id.Name][id] = true
			constants[id.Name][n["duration"].(*ast.Ident)] = true
		}
	}
	for _, name := range gate.SortedKeys(expected) {
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), name, expected[name], parser.SkipObjectResolution)
		if err != nil {
			return "baseline_production_parse_failed"
		}
		fresh := true
		ast.Inspect(f, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && constants[id.Name] != nil {
				fresh = false
			}
			return true
		})
		if !fresh {
			return "constant_name_not_fresh"
		}
	}
	for _, name := range gate.SortedKeys(candidate) {
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		var f *ast.File
		if n := nodes[name]; n != nil {
			f = n["file"].(*ast.File)
		} else {
			var err error
			f, err = parser.ParseFile(token.NewFileSet(), name, candidate[name], parser.SkipObjectResolution)
			if err != nil {
				return "candidate_production_parse_failed"
			}
		}
		closed := true
		ast.Inspect(f, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && constants[id.Name] != nil && !constants[id.Name][id] {
				closed = false
			}
			return true
		})
		if !closed {
			return "extra_constant_reference: " + name
		}
	}
	return ""
}

func scopeSyntax(f *ast.File) string {
	var b bytes.Buffer
	_ = ast.Fprint(&b, nil, f, func(name string, value reflect.Value) bool {
		return name != "Doc" && name != "Comment" && name != "Comments" && value.Type() != reflect.TypeFor[token.Pos]() && ast.NotNilFilter(name, value)
	})
	return gate.Digest(b.Bytes())
}
func scopeComments(base []byte, file *ast.File) bool {
	before, err := parser.ParseFile(token.NewFileSet(), "baseline.go", base, parser.ParseComments)
	if err != nil {
		return false
	}
	type directive struct {
		Text   string
		Offset int
	}
	inspect := func(f *ast.File) ([]directive, bool) {
		tags := []directive{}
		for _, group := range f.Comments {
			for _, c := range group.List {
				text := strings.TrimSpace(c.Text)
				if strings.HasPrefix(text, "//go:build ") && c.End() < f.Package {
					tags = append(tags, directive{c.Text, int(c.Pos()) - 1})
					continue
				}
				if strings.HasPrefix(text, "//go:") || strings.HasPrefix(text, "// +build") || strings.HasPrefix(text, "//line ") || strings.HasPrefix(text, "/*line ") || strings.HasPrefix(text, "//export ") || strings.Contains(text, "Code generated") && strings.Contains(text, "DO NOT EDIT") {
					return nil, false
				}
				prefix, _, found := strings.Cut(strings.TrimPrefix(text, "//"), ":")
				if found && prefix != "" && !strings.ContainsAny(prefix, " \t\n") {
					return nil, false
				}
			}
		}
		return tags, true
	}
	old, ok := inspect(before)
	if !ok {
		return false
	}
	current, ok := inspect(file)
	return ok && reflect.DeepEqual(old, current)
}

func removeUnusedTime(f *ast.File) {
	var imports []*ast.ImportSpec
	for _, imp := range f.Imports {
		remove := false
		if imp.Path.Value == `"time"` {
			name := "time"
			if imp.Name != nil {
				name = imp.Name.Name
			}
			used := name == "_" || name == "."
			for _, d := range f.Decls {
				if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.IMPORT {
					continue
				}
				ast.Inspect(d, func(n ast.Node) bool {
					if id, ok := n.(*ast.Ident); ok && id.Name == name {
						used = true
					}
					return true
				})
			}
			remove = !used
		}
		if !remove {
			imports = append(imports, imp)
		}
	}
	keep := map[*ast.ImportSpec]bool{}
	for _, imp := range imports {
		keep[imp] = true
	}
	f.Imports = imports
	for _, d := range f.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.IMPORT {
			var specs []ast.Spec
			for _, s := range g.Specs {
				if keep[s.(*ast.ImportSpec)] {
					specs = append(specs, s)
				}
			}
			g.Specs = specs
		}
	}
}
func scopeCheck(p ScopeProof) quality.Check {
	status := quality.StatusUnknown
	if p.Matched {
		status = quality.StatusPass
	}
	out := check("request_scope", status, "typed bounded production structure: "+status, "input-snapshots", "scope", "scope-proof")
	for _, reason := range p.Gaps {
		out.Findings = append(out.Findings, quality.Finding{Code: "unexplained_scope", Detail: reason, RuleIDs: rules("request_scope"), EvidenceIDs: []string{"scope-proof"}})
	}
	return out
}
