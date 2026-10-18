package acceptance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
)

func testCheck(data []byte, exit int, required []string) (Check, error) {
	check := Check{Status: "pass", Rules: []string{}, Tests: map[string]string{}}
	rules := map[string]bool{}
	d := json.NewDecoder(bytes.NewReader(data))
	packageEnded := false
	for {
		var event struct{ Action, Package, Test, Output string }
		if err := d.Decode(&event); err == io.EOF {
			break
		} else if err != nil {
			return check, fmt.Errorf("invalid Go test events: %w", err)
		}
		if event.Test != "" {
			check.Tests[event.Package+"/"+event.Test] = event.Action
		}
		if event.Test == "" && (event.Action == "pass" || event.Action == "fail") {
			packageEnded = true
		}
		if strings.Contains(event.Output, "panic:") || strings.Contains(event.Output, "fatal error:") || strings.Contains(event.Output, "[build failed]") {
			return check, fmt.Errorf("Go test process failed outside a reference assertion")
		}
		if i := strings.Index(event.Output, "REFERENCE_FINDING "); i >= 0 {
			var finding struct{ Rule, Detail string }
			if err := json.Unmarshal([]byte(strings.TrimSpace(event.Output[i+len("REFERENCE_FINDING "):])), &finding); err != nil || finding.Rule == "" || finding.Detail == "" {
				return check, fmt.Errorf("invalid reference finding")
			}
			rules[finding.Rule] = true
		}
	}
	if !packageEnded {
		return check, fmt.Errorf("Go test did not produce a package terminal event")
	}
	for _, name := range required {
		switch check.Tests[name] {
		case "pass":
		case "skip":
			check.Status = "inconclusive"
		case "fail":
			if check.Status != "inconclusive" {
				check.Status = "fail"
			}
		default:
			return check, fmt.Errorf("required test did not finish: %s", name)
		}
	}
	if exit != 0 && check.Status == "pass" {
		return check, fmt.Errorf("Go test failed without a required test failure")
	}
	for rule := range rules {
		check.Rules = append(check.Rules, rule)
	}
	slices.Sort(check.Rules)
	if check.Status == "fail" && len(check.Rules) == 0 && strings.Contains(string(data), "TestReference") {
		return check, fmt.Errorf("reference failed without a rule finding")
	}
	return check, nil
}

func businessTests() []string {
	return []string{
		"example.com/demo-orders/internal/inventory/TestReserve", "example.com/demo-orders/internal/inventory/TestRelease",
		"example.com/demo-orders/internal/orders/TestPlace", "example.com/demo-orders/internal/orders/TestPlaceFailure",
		"example.com/demo-orders/internal/orders/TestCancel", "example.com/demo-orders/internal/orders/TestCancelFailure",
		"example.com/demo-orders/cmd/server/TestPlace", "example.com/demo-orders/cmd/server/TestCancel",
		"example.com/demo-orders/cmd/server/TestBadRequest", "example.com/demo-orders/cmd/server/TestNotFound",
	}
}

func referenceTests() []string {
	names := []string{}
	for _, op := range []string{"Reserve", "Release"} {
		for _, mode := range []string{"default", "short-parent", "long-parent", "metadata", "already-cancelled", "cancel-in-backend", "release-success", "release-error", "repeat"} {
			names = append(names, "TestReferenceBoundary/"+op+"/"+mode)
		}
	}
	for _, op := range []string{"Place", "Cancel"} {
		for _, mode := range []string{"metadata", "already-cancelled", "expired-parent", "cancel-in-backend"} {
			names = append(names, "TestReferenceRequest/"+op+"/"+mode)
		}
		for _, mode := range []string{"cancelled", "expired", "ordinary"} {
			names = append(names, "TestReferenceServiceError/"+op+"/"+mode)
		}
	}
	for i := range names {
		names[i] = "example.com/demo-orders/cmd/server/" + names[i]
	}
	return names
}
