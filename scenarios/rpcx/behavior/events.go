package behavior

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"example.com/agent-harness/internal/execution"
)

const serverPackage = "example.com/demo-orders/cmd/server"

// requiredTests 由宿主定义，不从候选、manifest 或参考标准读取预期结果。
func requiredTests(slow bool) map[string][]string {
	out := map[string][]string{}
	if slow {
		for _, op := range []string{"Place", "Cancel"} {
			out[serverPackage+"/TestHarnessExpiry/"+op] = []string{"TIME-001", "TIME-004", "BEHAVIOR-001"}
		}
		return out
	}
	for pkg, names := range map[string][]string{
		"internal/inventory": {"TestReserve", "TestRelease"},
		"internal/orders":    {"TestPlace", "TestPlaceFailure", "TestCancel", "TestCancelFailure"},
		"cmd/server":         {"TestPlace", "TestCancel", "TestBadRequest", "TestNotFound"},
	} {
		for _, name := range names {
			out["example.com/demo-orders/"+pkg+"/"+name] = []string{"BEHAVIOR-001"}
		}
	}
	for _, op := range []string{"Reserve", "Release"} {
		for _, scenario := range []string{"default", "short-parent", "long-parent", "canceled", "expired", "cancel-in-flight", "backend-error", "repeat"} {
			out[serverPackage+"/TestHarnessBoundary/"+op+"/"+scenario] = []string{"TIME-001", "TIME-002", "TIME-003", "TIME-004", "CTX-003", "BEHAVIOR-001"}
		}
	}
	for _, op := range []string{"Place", "Cancel"} {
		for _, scenario := range []string{"canceled", "expired", "backend-error"} {
			out[serverPackage+"/TestHarnessService/"+op+"/"+scenario] = []string{"TIME-004", "BEHAVIOR-001"}
		}
		for _, scenario := range []string{"metadata", "canceled", "cancel-in-flight"} {
			out[serverPackage+"/TestHarnessHTTP/"+op+"/"+scenario] = []string{"CTX-002", "CTX-003", "TIME-004", "BEHAVIOR-001"}
		}
	}
	return out
}

// observe 要求真实 run、终结事件及包通过；无事件、跳过和异常退出都不能成为 pass。
func observe(result execution.Result, slow bool) ([]Observation, []string) {
	started, terminal, packages := map[string]int{}, map[string][]string{}, map[string]string{}
	gaps := []string{}
	abnormal := false
	decoder := json.NewDecoder(bytes.NewReader(result.Stdout))
	for {
		var event struct{ Package, Test, Action, Output string }
		err := decoder.Decode(&event)
		if err == io.EOF {
			break
		}
		if err != nil {
			gaps = append(gaps, "malformed_test_events")
			abnormal = true
			break
		}
		if strings.Contains(event.Output, "panic:") || strings.Contains(event.Output, "fatal error:") || strings.Contains(event.Output, "[build failed]") {
			abnormal = true
		}
		id := event.Package + "/" + event.Test
		if event.Test != "" {
			if event.Action == "run" {
				started[id]++
			}
			if slices.Contains([]string{"pass", "fail", "skip"}, event.Action) {
				terminal[id] = append(terminal[id], event.Action)
			}
		} else if slices.Contains([]string{"pass", "fail", "skip"}, event.Action) {
			packages[event.Package] = event.Action
		}
	}
	observations := []Observation{}
	for id, rules := range requiredTests(slow) {
		status := "unknown"
		pkg := id[:strings.Index(id, "/Test")]
		if !abnormal && started[id] == 1 && len(terminal[id]) == 1 {
			switch terminal[id][0] {
			case "pass":
				if packages[pkg] == "pass" || packages[pkg] == "fail" {
					status = "pass"
				}
			case "fail":
				if packages[pkg] == "fail" && result.ExitCode != 0 {
					status = "fail"
				}
			}
		}
		observations = append(observations, Observation{id, status, rules})
	}
	if abnormal {
		gaps = append(gaps, "abnormal_test_execution")
	}
	for pkg, status := range packages {
		if status == "fail" {
			gaps = append(gaps, "package_failed:"+pkg)
		}
	}
	if result.ExitCode != 0 {
		gaps = append(gaps, fmt.Sprintf("test_exit:%d", result.ExitCode))
	}
	sort.Slice(observations, func(i, j int) bool { return observations[i].ID < observations[j].ID })
	sort.Strings(gaps)
	return observations, gaps
}

func aggregate(tests []Observation, gaps []string) string {
	status := "pass"
	if len(gaps) != 0 {
		status = "unknown"
	}
	for _, test := range tests {
		if test.Status == "fail" {
			return "fail"
		}
		if test.Status != "pass" {
			status = "unknown"
		}
	}
	return status
}
