package evaluation

import (
	"bytes"
	"encoding/json"
	"io"
	"slices"
	"strings"
)

const godddPackage = "github.com/marcusolsson/goddd"

func referenceTests() []string {
	out := []string{godddPackage + "/server/TestL2ReferenceRoutes"}
	for _, mode := range []string{"default", "dispatch-delay", "short-parent", "long-parent", "cancelled", "expired", "remote-error", "unknown-cargo", "empty-response", "parent-cancel", "repeat"} {
		out = append(out, godddPackage+"/server/TestL2ReferenceRoutes/"+mode)
	}
	return out
}

// observe 不复用运行时门禁的事件解析或判定函数。
func observe(data []byte, exit int, required, packages, emptyPackages []string, reference bool) Observation {
	out := Observation{Status: Inconclusive, Tests: map[string]string{}}
	decoder := json.NewDecoder(bytes.NewReader(data))
	running := map[string]bool{}
	packageStates := map[string]string{}
	findings := map[string][]Finding{}
	failed := false
	for {
		var event struct{ Action, Package, Test, Output string }
		if err := decoder.Decode(&event); err == io.EOF {
			break
		} else if err != nil {
			out.Reason = "invalid Go test JSON"
			return out
		}
		key := event.Package + "/" + event.Test
		if reference && event.Action == "output" && slices.Contains(required, key) {
			if _, payload, ok := strings.Cut(event.Output, "L2_REFERENCE_FINDING "); ok {
				var finding Finding
				if json.Unmarshal([]byte(strings.TrimSpace(payload)), &finding) != nil || finding.Detail == "" || !slices.Contains([]string{"CTX-003", "TIME-001", "TIME-002", "TIME-003", "BEHAVIOR-001"}, finding.Rule) {
					out.Reason = "invalid reference finding"
					return out
				}
				findings[key] = append(findings[key], finding)
			}
		}
		if event.Test == "" {
			if event.Action == "pass" || event.Action == "fail" || event.Action == "skip" {
				if packageStates[event.Package] != "" {
					out.Reason = "duplicate package terminal event"
					return out
				}
				packageStates[event.Package] = event.Action
			}
			continue
		}
		if event.Action == "run" {
			running[key] = true
		}
		if event.Action == "pass" || event.Action == "fail" || event.Action == "skip" {
			if !running[key] || out.Tests[key] != "" {
				out.Reason = "invalid test lifecycle"
				return out
			}
			out.Tests[key] = event.Action
			if event.Action == "fail" {
				failed = true
			}
		}
	}
	for test, items := range findings {
		if out.Tests[test] != "fail" {
			out.Reason = "finding without a failed reference test"
			return out
		}
		out.Findings = append(out.Findings, items...)
	}
	slices.SortFunc(out.Findings, func(a, b Finding) int {
		if order := strings.Compare(a.Rule, b.Rule); order != 0 {
			return order
		}
		return strings.Compare(a.Detail, b.Detail)
	})
	if len(out.Findings) > 0 && exit != 0 {
		out.Status = Fail
		return out
	}
	if !reference && failed && exit != 0 {
		out.Status = Fail
		out.Findings = []Finding{{Rule: "TEST-001", Detail: "candidate test failed"}}
		return out
	}
	if exit != 0 {
		out.Reason = "test command failed without attributable evidence"
		return out
	}
	for _, test := range required {
		if out.Tests[test] != "pass" {
			out.Reason = "required test did not pass: " + test
			return out
		}
	}
	for _, pkg := range packages {
		if packageStates[pkg] != "pass" {
			out.Reason = "required package did not pass: " + pkg
			return out
		}
	}
	for pkg, state := range packageStates {
		if state != "pass" && !(state == "skip" && slices.Contains(emptyPackages, pkg)) {
			out.Reason = "unexpected package terminal state: " + pkg
			return out
		}
	}
	if failed {
		out.Reason = "unexpected failed test"
		return out
	}
	out.Status = Pass
	return out
}

func aggregate(checks map[string]Observation) Status {
	result := Pass
	for _, check := range checks {
		if check.Status == Fail {
			return Fail
		}
		if check.Status != Pass {
			result = Inconclusive
		}
	}
	return result
}
