package testintegrity

import (
	"errors"
	"go/ast"
	"reflect"
	"runtime"
)

// Compare 只接受可信宿主收集的完整测试文件集，不执行候选代码。
// baseline 须是固定场景；结构保留仍须结合实际测试事件及可信行为验证。
func Compare(baseline, candidate map[string][]byte) (Report, error) {
	p, material, err := loadPolicy()
	if err != nil {
		return Report{}, err
	}
	adapted, err := AdaptBaseline(baseline)
	if err != nil {
		return Report{}, err
	}
	candidateBinding, err := bind(candidate)
	if err != nil {
		return Report{}, err
	}
	baselineBinding, _ := bind(baseline)
	expectedBinding, _ := bind(adapted)
	out := Report{SchemaVersion: 1, VerifierVersion: p.Version, Status: "preserved", SpecSHA256: p.SpecSHA256, MaterialSHA256: material, GoVersion: runtime.Version(),
		Baseline: baselineBinding, Candidate: candidateBinding, Expected: expectedBinding,
		Declarations: []Declaration{}, AddedTests: []Location{}, Findings: []Finding{}}
	_, originals := parseSources(baseline)
	expectedFiles, expected := parseSources(adapted)
	actualFiles, actual := parseSources(candidate)
	add := func(status, code, detail string, locations ...Location) {
		out.Findings = append(out.Findings, Finding{Status: status, Code: code, Detail: detail, RuleIDs: []string{"TEST-001"}, Locations: locations})
	}
	complexChange, parseFailure := false, false
	unknownFiles := map[string]bool{}
	for _, path := range keys(actualFiles) {
		file, original := actualFiles[path], expectedFiles[path]
		if file.err != nil {
			complexChange, parseFailure = true, true
			add("unknown", "candidate_test_parse_failed", file.err.Error(), Location{Tree: "candidate", File: path, Line: 1})
			continue
		}
		if original == nil {
			unknownFiles[path] = true
			complexChange = true
			add("unknown", "test_file_added", "new test file build and binding scope needs confirmation", Location{Tree: "candidate", File: path, Line: 1})
		} else if file.file.Name.Name != original.file.Name.Name || !importsPreserved(original, file, p.AdditionalTestImports) || !reflect.DeepEqual(file.directives, original.directives) {
			unknownFiles[path] = true
			complexChange = true
			add("unknown", "test_file_scope_changed", "package, imports or semantic directives changed beyond the supported scope", Location{Tree: "candidate", File: path, Line: 1})
		}
		if len(file.other) > 0 {
			unknownFiles[path] = true
			complexChange = true
			add("unknown", "test_declaration_added", "additional test declarations may affect the original tests", Location{Tree: "candidate", File: path, Line: 1})
		}
	}
	for _, id := range keys(actual) {
		if _, exists := expected[id]; exists {
			continue
		}
		for _, fn := range actual[id] {
			if ordinaryTest(fn) {
				out.AddedTests = append(out.AddedTests, fn.location("candidate"))
			} else {
				complexChange = true
				add("unknown", "test_helper_or_entry_added", "new helper or execution entry requires review", fn.location("candidate"))
			}
		}
	}
	type deletion struct {
		index   int
		before  function
		after   function
		removed []ast.Stmt
	}
	var deletions []deletion
	for _, id := range keys(expected) {
		if len(expected[id]) != 1 || len(originals[id]) != 1 {
			return Report{}, errors.New("ambiguous trusted baseline declaration")
		}
		e, original := expected[id][0], originals[id][0]
		kind := "helper"
		if ordinaryTest(e) {
			kind = "test"
		}
		record := Declaration{ID: id, Kind: kind, ExpectedSHA256: digest([]byte(canonical(e.node))), Status: "preserved"}
		candidates := actual[id]
		if len(candidates) != 1 {
			record.Status = "unknown"
			complexChange = true
			if len(candidates) == 0 && kind == "test" && !parseFailure {
				record.Status = "violated"
				add("violated", "original_test_removed", "required original test name is absent", original.location("baseline"))
			} else {
				add("unknown", "original_declaration_unresolved", "original helper or test declaration cannot be uniquely located", original.location("baseline"))
			}
			out.Declarations = append(out.Declarations, record)
			continue
		}
		a := candidates[0]
		record.ActualSHA256 = digest([]byte(canonical(a.node)))
		if unknownFiles[a.source.path] {
			record.Status = "unknown"
		}
		if a.source.path != e.source.path || a.source.file.Name.Name != e.source.file.Name.Name {
			complexChange, record.Status = true, "unknown"
			add("unknown", "original_declaration_moved", "test binding or build scope changed with its file", original.location("baseline"), a.location("candidate"))
		}
		if record.ExpectedSHA256 != record.ActualSHA256 {
			record.Status = "unknown"
			if kind == "test" && canonical(e.node.Type) == canonical(a.node.Type) && importsPreserved(e.source, a.source, p.AdditionalTestImports) && a.node.Body != nil && len(a.node.Body.List) > 0 && testingCall(a.node.Body.List[0], "Skip", "Skipf", "SkipNow") {
				record.Status = "violated"
				add("violated", "original_test_skipped", "required original test starts with an unconditional skip", a.location("candidate"))
			} else if ok, removed := deletionOnly(reflect.ValueOf(e.node), reflect.ValueOf(a.node)); ok && len(removed) > 0 {
				deletions = append(deletions, deletion{len(out.Declarations), e, a, removed})
			} else {
				complexChange = true
				add("unknown", "original_declaration_changed", "change exceeds the trusted API/context adaptation", original.location("baseline"), a.location("candidate"))
			}
		}
		out.Declarations = append(out.Declarations, record)
	}
	for _, deletion := range deletions {
		status, code, detail := "violated", "original_assertion_deleted", "only recognized original assertions were removed without replacement"
		if complexChange {
			status, code, detail = "unknown", "assertion_replacement_unproven", "other changes prevent proving whether removed assertions have an equivalent replacement"
		}
		out.Declarations[deletion.index].Status = status
		for _, stmt := range deletion.removed {
			loc := deletion.before.location("baseline")
			// 删除位置来自适配后的可信测试；原始文件摘要和适配材料一并保留。
			loc.Tree = "adapted"
			loc.Line = deletion.before.source.fset.PositionFor(stmt.Pos(), false).Line
			add(status, code, detail, loc, deletion.after.location("candidate"))
		}
	}
	for _, finding := range out.Findings {
		if finding.Status == "violated" {
			out.Status = "violated"
			break
		}
		out.Status = "unknown"
	}
	return out, nil
}
