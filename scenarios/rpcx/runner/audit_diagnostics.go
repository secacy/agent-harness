package runner

import (
	"encoding/json"
	"fmt"
	"maps"
	"path"
	"reflect"
	"strings"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/audit"
	"github.com/secacy/agent-harness/internal/campaign"
)

func (a *runAudit) checkDiagnostics(report Report) {
	limit := report.Input.DiagnosticLimit

	issue := func(reason string, sources ...string) {
		a.issue(audit.Violation, "generation_diagnostic_mismatch", "artifacts/report.json", "generation_diagnostics", reason, sources...)
	}
	var plan campaign.Plan
	raw, err := a.view.ReadFile("plan.json")
	matched := false
	planned := 0
	if err == nil && json.Unmarshal(raw, &plan) == nil {
		for _, target := range plan.Targets {
			if target.Input.ID == a.ref.Target.ID {
				planned = target.DiagnosticLimit
				matched = target.DiagnosticLimit == limit
			}
		}
	}
	if limit == 0 && planned == 0 && len(report.Diagnostics) == 0 {
		return
	}
	if ValidateDiagnostics(a.profile.ID, limit) != nil || limit != 2 || len(report.Diagnostics) > limit {
		issue("invalid diagnostic configuration or reference count")
		return
	}
	if !matched {
		issue("diagnostic configuration differs from campaign plan", "plan.json")
	}
	// 基线失败等情形尚未启动 Agent，不要求存在生成阶段记录。
	if report.AgentRecord == "" {
		if len(report.Diagnostics) != 0 {
			issue("diagnostic references exist without agent history")
		}
		return
	}
	var history agent.Result
	if !a.read("artifacts/agent.json", &history, 0, true) {
		return
	}
	calls := map[string]int{}
	arguments := map[string]string{}
	responses := map[string]agent.Message{}
	attempts := 0
	for _, message := range history.Messages {
		for _, call := range message.ToolCalls {
			if call.Name == diagnosticTool {
				attempts++
				calls[call.ID] = attempts
				arguments[call.ID] = call.Arguments
			}
		}
		if message.Role == agent.RoleTool {
			responses[message.ToolCallID] = message
		}
	}
	seen := map[string]bool{}
	previous := 0
	for _, ref := range report.Diagnostics {
		var entry diagnosticReport
		if !strings.HasPrefix(ref.Path, "generation-diagnostics/check-") || path.Clean(ref.Path) != ref.Path || !strings.HasSuffix(ref.Path, "/report.json") {
			issue("unsafe diagnostic reference")
			continue
		}
		data, ok := a.bytes(ref.Path, true)
		if !ok {
			continue
		}
		if digest(data) != ref.SHA256 || !a.decode(ref.Path, data, &entry, 1) {
			issue("diagnostic report digest or format mismatch", a.file(ref.Path))
			continue
		}
		dir := path.Dir(ref.Path)
		if ref.Path != fmt.Sprintf("generation-diagnostics/check-%04d/report.json", entry.Sequence) || entry.Sequence <= previous || entry.Sequence > limit || calls[entry.ToolCallID] != entry.Sequence || seen[entry.ToolCallID] || entry.RunID != report.RunID || entry.TargetID != a.ref.Target.ID || entry.ProfileSHA256 != report.Input.ProfileSHA256 || entry.Binding.SpecSHA256 != report.Input.SpecSHA256 || entry.Binding.BaselineSHA256 != report.Input.BaselineSHA256 {
			issue("diagnostic identity or tool call binding mismatch", a.file(ref.Path))
		}
		var args map[string]json.RawMessage
		if json.Unmarshal([]byte(arguments[entry.ToolCallID]), &args) != nil || args == nil || len(args) != 0 {
			issue("published diagnostic was not requested with empty arguments", a.file("artifacts/agent.json"))
		}
		previous = entry.Sequence
		seen[entry.ToolCallID] = true
		response, exists := responses[entry.ToolCallID]
		if !exists || response.Content != entry.Response.Content || response.IsError != entry.Response.IsError || entry.Response.IsError {
			issue("diagnostic response differs from agent history", a.file(ref.Path), a.file("artifacts/agent.json"))
		}
		inventory, ok := a.tree(dir)
		if !ok {
			continue
		}
		inventory = maps.Clone(inventory)
		delete(inventory, "report.json")
		if !reflect.DeepEqual(inventory, entry.Files) {
			issue("diagnostic material inventory differs from report", a.treeSources[dir]...)
		}
		a.pinnedTree(dir+"/candidate", entry.Binding.CandidateSHA256, a.file(ref.Path))
		a.pinnedTree(dir+"/tool", entry.Integrity.MaterialSHA256, a.file(ref.Path))
		a.pinnedTree(dir+"/baseline-tests", entry.Integrity.Baseline.SHA256, a.file(ref.Path))
		a.pinnedTree(dir+"/adapted-tests", entry.Integrity.Expected.SHA256, a.file(ref.Path))
		baseFiles, _ := a.tree(dir + "/baseline-tests")
		adaptedFiles, _ := a.tree(dir + "/adapted-tests")
		candidate, _ := a.tree(dir + "/candidate")
		if !reflect.DeepEqual(baseFiles, entry.Integrity.Baseline.Files) || !reflect.DeepEqual(adaptedFiles, entry.Integrity.Expected.Files) || entry.Integrity.SchemaVersion != 1 || entry.Integrity.GoVersion == "" {
			issue("test structure inventories or schema mismatch", a.file(ref.Path))
		}
		switch entry.Integrity.Status {
		case "preserved", "violated", "unknown":
		default:
			issue("invalid test structure status", a.file(ref.Path))
		}
		tests := map[string]string{}
		for name, hash := range candidate {
			if strings.HasSuffix(name, "_test.go") {
				tests[name] = hash
			}
		}
		testBytes, _ := json.Marshal(tests)
		if digest(testBytes) != entry.Integrity.Candidate.SHA256 || !reflect.DeepEqual(tests, entry.Integrity.Candidate.Files) || entry.Integrity.SpecSHA256 != entry.Binding.SpecSHA256 {
			issue("test comparison does not match its candidate or specification", a.file(ref.Path))
		}
		baseTests, _ := a.tree(dir + "/baseline-tests")
		base, _ := a.tree("baseline/" + a.profile.RepositoryName)
		expected := map[string]string{}
		for name, hash := range base {
			if strings.HasSuffix(name, "_test.go") {
				expected[name] = hash
			}
		}
		if !reflect.DeepEqual(baseTests, expected) {
			issue("diagnostic baseline tests differ from authenticated input", a.file(ref.Path))
		}
		var saved agent.ToolResult
		if a.read(dir+"/response.json", &saved, 0, true) && !reflect.DeepEqual(saved, entry.Response) {
			issue("saved diagnostic response differs from report", a.file(ref.Path))
		}
		feedback, err := renderDiagnostic(entry.Binding, entry.Integrity)
		if err != nil || !reflect.DeepEqual(feedback, entry.Response) {
			issue("diagnostic feedback projection differs from archived facts", a.file(ref.Path))
		}
	}
	for id := range calls {
		if response, ok := responses[id]; ok && !response.IsError && !seen[id] {
			issue("successful diagnostic response lacks an archived reference", a.file("artifacts/agent.json"))
		}
	}
}
