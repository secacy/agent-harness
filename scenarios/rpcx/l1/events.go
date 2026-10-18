package l1

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
)

const pkg = "example.com/demo-orders/cmd/server/"

func referenceNames(slow bool) []string {
	names := []string{}
	if slow {
		return []string{pkg + "TestReferenceExpiry/Place", pkg + "TestReferenceExpiry/Cancel"}
	}
	for _, op := range []string{"Reserve", "Release"} {
		for _, mode := range []string{"default", "short-parent", "long-parent", "metadata", "already-cancelled", "cancel-in-backend", "release-success", "release-error", "repeat"} {
			names = append(names, pkg+"TestReferenceBoundary/"+op+"/"+mode)
		}
	}
	for _, op := range []string{"Place", "Cancel"} {
		for _, mode := range []string{"metadata", "already-cancelled", "expired-parent", "cancel-in-backend"} {
			names = append(names, pkg+"TestReferenceRequest/"+op+"/"+mode)
		}
		for _, mode := range []string{"cancelled", "expired", "ordinary"} {
			names = append(names, pkg+"TestReferenceServiceError/"+op+"/"+mode)
		}
	}
	return append(names, pkg+"TestReferenceAlternateRequest")
}

// observe 不消费门禁结论；非断言退出或缺事件是错误，不是预期语义失败。
func observe(data []byte, exit int, slow bool) (Observation, error) {
	o := Observation{Status: "pass", Tests: map[string]string{}, Rules: []string{}}
	runs, ends, findings := map[string]int{}, map[string]int{}, map[string]bool{}
	rules := map[string]bool{}
	packageEnd := ""
	d := json.NewDecoder(bytes.NewReader(data))
	for {
		var e struct{ Action, Package, Test, Output string }
		if err := d.Decode(&e); err == io.EOF {
			break
		} else if err != nil {
			return o, errors.New("invalid reference test JSON")
		}
		if strings.Contains(e.Output, "panic:") || strings.Contains(e.Output, "fatal error:") || strings.Contains(e.Output, "[build failed]") {
			return o, errors.New("reference process failed outside assertions")
		}
		id := e.Package + "/" + e.Test
		if e.Action == "run" {
			runs[id]++
		}
		if e.Action == "pass" || e.Action == "fail" || e.Action == "skip" {
			if e.Test == "" {
				if packageEnd != "" {
					return o, errors.New("duplicate reference package end")
				}
				packageEnd = e.Action
			} else {
				ends[id]++
				o.Tests[id] = e.Action
			}
		}
		if at := strings.Index(e.Output, "REFERENCE_FINDING "); at >= 0 {
			var f struct{ Rule, Detail string }
			if err := decode([]byte(strings.TrimSpace(e.Output[at+len("REFERENCE_FINDING "):])), &f); err != nil || f.Rule == "" || f.Detail == "" || e.Test == "" {
				return o, errors.New("invalid reference assertion finding")
			}
			rules[f.Rule], findings[id] = true, true
		}
	}
	if packageEnd == "" {
		return o, errors.New("reference package did not finish")
	}
	for _, id := range referenceNames(slow) {
		if runs[id] != 1 || ends[id] != 1 {
			return o, errors.New("required reference test missing or duplicated: " + id)
		}
		switch o.Tests[id] {
		case "skip":
			o.Status = "inconclusive"
		case "fail":
			if !findings[id] || packageEnd != "fail" || exit == 0 {
				return o, errors.New("reference test failed without attributable assertion: " + id)
			}
			if o.Status != "inconclusive" {
				o.Status = "fail"
			}
		case "pass":
		default:
			return o, errors.New("reference test has no result")
		}
	}
	if o.Status == "pass" && (exit != 0 || packageEnd != "pass") {
		return o, errors.New("reference exited unsuccessfully without required failure")
	}
	for rule := range rules {
		o.Rules = append(o.Rules, rule)
	}
	slices.Sort(o.Rules)
	return o, nil
}
