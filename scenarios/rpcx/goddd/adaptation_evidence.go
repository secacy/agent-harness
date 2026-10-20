package goddd

import (
	"fmt"
	"path/filepath"

	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/testintegrity"
)

type adaptationVariant struct {
	ID          string            `json:"id"`
	Path        string            `json:"path"`
	Declaration string            `json:"declaration,omitempty"`
	Files       map[string]string `json:"files"`
	RuleIDs     []string          `json:"rule_ids"`
}
type adaptationEvidence struct {
	SchemaVersion     int                 `json:"schema_version"`
	Binding           quality.Binding     `json:"binding"`
	MaterialsSHA256   string              `json:"materials_sha256"`
	Variants          []adaptationVariant `json:"variants"`
	ProductionMatches map[string]string   `json:"production_matches"`
	TestVariant       string              `json:"test_variant"`
}

// checkAdaptations 发布固定变体及两份完整测试比较，返回同一份选中测试供可信执行。
func (e *evaluation) checkAdaptations(base, candidate map[string][]byte) (map[string][]byte, error) {
	a, err := prepareAdaptations(base)
	if err != nil {
		return nil, err
	}
	index := adaptationEvidence{SchemaVersion: 1, Binding: e.binding(), MaterialsSHA256: e.result.MaterialsSHA256}
	var productionIDs, testIDs []string
	for _, variant := range []struct {
		id, declaration string
		files           map[string][]byte
		rules           []string
		tests           bool
	}{
		{"production-canonical", "", a.production, []string{"CTX-001", "SCOPE-001"}, false},
		{"production-forwarded", "(*RoutingService).FetchRoutesFn / FetchRoutesForSpecification", map[string][]byte{mockPath: a.forwarded}, []string{"CTX-001", "SCOPE-001"}, false},
		{"tests-canonical", "", a.tests[0], []string{"TEST-001"}, true},
		{"tests-blank-context", "(*stubRoutingService).FetchRoutesForSpecification parameter 0", a.tests[1], []string{"TEST-001"}, true},
	} {
		path := "adaptations/" + variant.id
		index.Variants = append(index.Variants, adaptationVariant{ID: variant.id, Path: path, Declaration: variant.declaration, Files: hashes(variant.files), RuleIDs: variant.rules})
		for i, name := range gate.SortedKeys(variant.files) {
			id := fmt.Sprintf("%s-%d", variant.id, i)
			if err := e.save(id, "host_adaptation", filepath.ToSlash(filepath.Join(path, name)), variant.files[name]); err != nil {
				return nil, err
			}
			if variant.tests {
				testIDs = append(testIDs, id)
			} else {
				productionIDs = append(productionIDs, id)
			}
		}
	}
	plan := testintegrity.Plan{Version: "rpcx-goddd-test-structure-v1", SpecSHA256: SpecSHA256(), MaterialSHA256: e.result.MaterialsSHA256, BaselineFiles: hashes(testFiles(base)), AdditionalTestImports: []string{"errors", "time"}}
	var reports [2]testintegrity.Report
	for i, files := range a.tests {
		reports[i], err = testintegrity.CompareAdapted(testFiles(base), files, testFiles(candidate), plan)
		if err != nil {
			return nil, err
		}
		id := []string{"tests-canonical-report", "tests-blank-context-report"}[i]
		if err := e.saveJSON(id, "test_structure", "adaptations/"+id+".json", reports[i]); err != nil {
			return nil, err
		}
		testIDs = append(testIDs, id)
	}
	selected, err := selectIntegrity(reports)
	if err != nil {
		return nil, err
	}
	integrity := reports[selected]
	index.TestVariant = []string{"tests-canonical", "tests-blank-context"}[selected]
	scope, matches, routing := requestScope(base, candidate, a)
	if routing != nil {
		routing.Binding = e.binding()
		routing.MaterialsSHA256 = e.result.MaterialsSHA256
		if err := e.saveJSON("routing-scope", "bounded_structure", "routing-scope.json", routing); err != nil {
			return nil, err
		}
		scope.EvidenceIDs = append(scope.EvidenceIDs, "routing-scope")
	}
	index.ProductionMatches = matches
	if err := e.saveJSON("adaptations", "host_adaptation_selection", "adaptations.json", index); err != nil {
		return nil, err
	}
	if err := e.saveJSON("test-integrity", "test_structure", "test-integrity.json", integrity); err != nil {
		return nil, err
	}
	if err := copyFiles(filepath.Join(e.req.EvidenceDir, "adapted-tests"), a.tests[selected]); err != nil {
		return nil, err
	}
	status := map[string]string{"preserved": quality.StatusPass, "violated": quality.StatusFail, "unknown": quality.StatusUnknown}[integrity.Status]
	testIDs = append(testIDs, "test-integrity", "adaptations", "input-snapshots")
	e.result.Checks["test_preservation"] = check("test_preservation", status, "original test structure: "+integrity.Status, testIDs...)
	scope.EvidenceIDs = append(scope.EvidenceIDs, productionIDs...)
	for i := range scope.Findings {
		scope.Findings[i].EvidenceIDs = scope.EvidenceIDs
	}
	e.result.Checks["request_scope"] = scope
	if err := e.saveJSON("scope", "production_scope", "scope.json", scope); err != nil {
		return nil, err
	}
	return a.tests[selected], nil
}
