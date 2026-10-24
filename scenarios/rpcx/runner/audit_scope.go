package runner

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/secacy/agent-harness/internal/audit"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/translation"
)

func (a *runAudit) checkTranslationScope(v checks.Evaluation, completed bool) {
	if a.profile.Verifier != translation.ScopeVerifierVersion {
		return
	}
	var proof translation.ScopeProof
	data, err := a.view.ReadFile(a.file("evidence/scope-proof.json"))
	if errors.Is(err, os.ErrNotExist) && !completed {
		return
	}
	sources := []string{a.file("artifacts/report.json")}
	issue := func(reason string) {
		a.issue(audit.Violation, "scope_proof_mismatch", "evidence/scope-proof.json", "", reason, sources...)
	}
	if err != nil || json.Unmarshal(data, &proof) != nil {
		issue("scope proof unavailable or malformed")
		return
	}
	if proof.SchemaVersion != 1 || proof.Binding.SpecSHA256 != v.SpecSHA256 || proof.Binding.BaselineSHA256 != v.BaselineSHA256 || proof.Binding.CandidateSHA256 != v.CandidateSHA256 {
		issue("scope proof input identity differs from validation")
		return
	}
	worker := map[string]string{}
	for _, name := range []string{"main.go", "rules.go", "evidence.go"} {
		sources = append(sources, a.file("evidence/tool/analysis-worker/"+name))
		raw, ok := a.bytes("evidence/tool/analysis-worker/"+name, true)
		if !ok {
			return
		}
		worker[name] = digest(raw)
	}
	raw, _ := json.Marshal(worker)
	if digest(raw) != proof.WorkerSHA256 {
		issue("scope worker material identity differs")
		return
	}
	readTree := func(dir string) map[string][]byte {
		inventory, ok := a.tree(dir)
		sources = append(sources, a.treeSources[dir]...)
		if !ok {
			return nil
		}
		files := map[string][]byte{}
		for name := range inventory {
			sources = append(sources, a.file(dir+"/"+name))
			data, ok := a.bytes(dir+"/"+name, true)
			if !ok {
				return nil
			}
			files[name] = data
		}
		return files
	}
	base := readTree("baseline/translation")
	candidate := readTree("workspace/translation")
	if base == nil || candidate == nil {
		return
	}
	fixtures := map[string][]byte{}
	for _, sdk := range []string{"rpcx-v1", "rpcx-v2"} {
		files := readTree("baseline/" + sdk)
		if files == nil {
			return
		}
		for name, data := range files {
			fixtures[sdk+"/"+name] = data
		}
	}
	reports := map[string]translation.ScopeAnalysis{}
	for _, config := range translation.Configurations() {
		sources = append(sources, a.file("evidence/analysis/"+config.ID+"/baseline-analysis.json"), a.file("evidence/analysis/"+config.ID+"/candidate-analysis.json"))
		var pair translation.ScopeAnalysis
		baseData, be := a.view.ReadFile(a.file("evidence/analysis/" + config.ID + "/baseline-analysis.json"))
		candidateData, ce := a.view.ReadFile(a.file("evidence/analysis/" + config.ID + "/candidate-analysis.json"))
		if errors.Is(be, os.ErrNotExist) && errors.Is(ce, os.ErrNotExist) {
			continue
		}
		if be != nil || ce != nil || json.Unmarshal(baseData, &pair.Baseline) != nil || json.Unmarshal(candidateData, &pair.Candidate) != nil {
			issue("incomplete or malformed scope analysis pair")
			return
		}
		reports[config.ID] = pair
	}
	if err := translation.VerifyScopeProof(base, candidate, fixtures, reports, proof); err != nil {
		issue(err.Error())
		return
	}
	if gate.TreeHash(base) != v.BaselineSHA256 || gate.TreeHash(candidate) != v.CandidateSHA256 {
		issue("scope snapshots differ from validation")
		return
	}
	check := v.Checks["request_scope"]
	if (check.Status == "pass") != proof.Matched {
		issue("scope check differs from residual proof")
	}
	found := false
	for _, ref := range v.Evidence {
		if ref.ID == "scope-proof" && ref.Path == "scope-proof.json" && ref.SHA256 == digest(data) {
			found = true
		}
	}
	if !found {
		issue("scope proof lacks bound evidence reference")
	}
}
