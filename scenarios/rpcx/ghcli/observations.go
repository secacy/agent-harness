package ghcli

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"strings"

	"github.com/secacy/agent-harness/internal/quality"
)

const behaviorMarker = "GHCLI_FINDING "

// behaviorTest 固定可信测试允许报告的原因及其规范条款；父测试不报告违规。
type behaviorTest struct {
	Name    string
	Reasons map[string]string
}

type testObservation struct {
	Test    string   `json:"test"`
	Status  string   `json:"status"`
	Reasons []string `json:"reasons,omitempty"`
}

// behaviorObservations 只描述事件归因；执行与输入身份由集成层绑定。
type behaviorObservations struct {
	SchemaVersion int               `json:"schema_version"`
	Package       string            `json:"package"`
	Status        string            `json:"status"`
	Problem       string            `json:"problem,omitempty"`
	Tests         []testObservation `json:"tests"`
	Findings      []quality.Finding `json:"findings,omitempty"`
}

func returnContracts() []behaviorTest {
	common := map[string]string{}
	for _, code := range []string{"unexpected_error", "response_missing", "response_properties_changed", "body_closed_before_caller", "response_body_missing", "body_read_changed", "close_error_changed", "body_not_closed"} {
		common[code] = "BEHAVIOR-001"
	}
	return []behaviorTest{
		{Name: "TestHarnessApplicationReturns"},
		{Name: "TestHarnessApplicationReturns/success", Reasons: common},
		{Name: "TestHarnessApplicationReturns/transport-error", Reasons: map[string]string{"error_chain_changed": "BEHAVIOR-001", "unexpected_response_on_transport_error": "BEHAVIOR-001"}},
		{Name: "TestHarnessApplicationReturns/redirect-declined", Reasons: map[string]string{"error_chain_changed": "BEHAVIOR-001", "redirect_body_not_closed": "BEHAVIOR-001", "response_missing_on_redirect_error": "BEHAVIOR-001", "response_properties_changed": "BEHAVIOR-001"}},
		{Name: "TestHarnessApplicationReturns/use-last-response", Reasons: common},
	}
}

// observeBehavior 只在所有必需测试完整结束、违规归属于同一失败叶子时确认失败。
// 事件来自候选同进程的 stdout，此处不提供抵抗任意伪造输出的保证。
func observeBehavior(data []byte, pkg string, required []behaviorTest, exit int) behaviorObservations {
	out := behaviorObservations{SchemaVersion: 1, Package: pkg, Status: quality.StatusUnknown}
	reject := func(problem string) behaviorObservations {
		out.Problem = problem
		out.Findings = nil
		return out
	}
	type state struct {
		started bool
		ended   bool
		status  string
		reasons []string
	}
	states := map[string]*state{}
	catalog := map[string]behaviorTest{}
	for _, test := range required {
		if test.Name == "" || states[test.Name] != nil {
			return reject("invalid_test_catalog")
		}
		states[test.Name] = &state{}
		catalog[test.Name] = test
	}
	if len(states) == 0 {
		return reject("empty_test_catalog")
	}
	packageEnd := ""
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var event struct{ Action, Package, Test, Output string }
		if err := decoder.Decode(&event); err == io.EOF {
			break
		} else if err != nil {
			return reject("invalid_json")
		}
		if event.Package != pkg || packageEnd != "" {
			return reject("unexpected_package_event")
		}
		if strings.Contains(event.Output, "panic:") || strings.Contains(event.Output, "fatal error:") {
			return reject("process_panic")
		}
		if event.Test == "" {
			if strings.Contains(event.Output, behaviorMarker) {
				return reject("unowned_finding")
			}
			switch event.Action {
			case "pass", "fail":
				packageEnd = event.Action
			case "start", "output":
			default:
				return reject("invalid_package_action")
			}
			continue
		}
		current := states[event.Test]
		if current == nil {
			return reject("unexpected_test")
		}
		switch event.Action {
		case "run":
			if current.started || current.ended {
				return reject("duplicate_test_run")
			}
			if parent, _, nested := strings.Cut(event.Test, "/"); nested {
				owner := states[parent]
				if owner == nil || !owner.started || owner.ended {
					return reject("invalid_parent_lifecycle")
				}
			}
			current.started = true
		case "pass", "fail":
			if !current.started || current.ended {
				return reject("invalid_test_terminal")
			}
			for name, child := range states {
				if strings.HasPrefix(name, event.Test+"/") && !child.ended {
					return reject("incomplete_child")
				}
			}
			current.ended, current.status = true, event.Action
		case "output":
			if !current.started || current.ended {
				return reject("output_outside_test")
			}
		default:
			return reject("unsupported_test_action")
		}
		if at := strings.Index(event.Output, behaviorMarker); at >= 0 {
			if event.Action != "output" {
				return reject("invalid_finding_event")
			}
			var finding struct{ Rule, Code string }
			encoded := strings.TrimSpace(event.Output[at+len(behaviorMarker):])
			d := json.NewDecoder(strings.NewReader(encoded))
			d.DisallowUnknownFields()
			if err := d.Decode(&finding); err != nil {
				return reject("invalid_finding")
			}
			var extra any
			if d.Decode(&extra) != io.EOF {
				return reject("invalid_finding")
			}
			rule := catalog[event.Test].Reasons[finding.Code]
			if rule == "" || finding.Rule != rule {
				return reject("unsupported_finding")
			}
			if slices.Contains(current.reasons, finding.Code) {
				return reject("duplicate_finding")
			}
			current.reasons = append(current.reasons, finding.Code)
		}
	}
	failed := false
	for _, test := range required {
		current := states[test.Name]
		out.Tests = append(out.Tests, testObservation{Test: test.Name, Status: current.status, Reasons: current.reasons})
		if !current.started || !current.ended {
			return reject("incomplete_test")
		}
		leaf := true
		childFailed := false
		for name, child := range states {
			if strings.HasPrefix(name, test.Name+"/") {
				leaf = false
				childFailed = childFailed || child.status == "fail"
			}
		}
		if current.status == "pass" && (len(current.reasons) > 0 || childFailed) {
			return reject("contradictory_test_result")
		}
		if current.status == "fail" {
			failed = true
			if leaf && len(current.reasons) == 0 || !leaf && (!childFailed || len(current.reasons) > 0) {
				return reject("unattributed_failure")
			}
			for _, code := range current.reasons {
				out.Findings = append(out.Findings, quality.Finding{Code: code, Detail: test.Name + ": " + code, RuleIDs: []string{test.Reasons[code]}})
			}
		}
	}
	if failed {
		if exit != 1 || packageEnd != "fail" {
			return reject("exit_mismatch")
		}
		out.Status = quality.StatusFail
	} else {
		if exit != 0 || packageEnd != "pass" {
			return reject("exit_mismatch")
		}
		out.Status = quality.StatusPass
	}
	return out
}
