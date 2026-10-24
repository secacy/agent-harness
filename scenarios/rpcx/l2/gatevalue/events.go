package gatevalue

import (
	"bytes"
	"encoding/json"
	"io"
)

// testEvents 要求全部已出现包和测试有唯一终态，必需测试实际运行且未跳过。
func testEvents(raw []byte, required []string, exit int) string {
	if len(required) == 0 {
		return "insufficient"
	}
	type state struct {
		runs, ends int
		action     string
	}
	packages := map[string]*state{}
	tests := map[string]*state{}
	d := json.NewDecoder(bytes.NewReader(raw))
	failed := false
	for {
		var e struct{ Action, Package, Test string }
		err := d.Decode(&e)
		if err == io.EOF {
			break
		}
		if err != nil || e.Package == "" {
			return "insufficient"
		}
		p := packages[e.Package]
		if p == nil {
			p = &state{}
			packages[e.Package] = p
		}
		s := p
		if e.Test != "" {
			key := e.Package + "/" + e.Test
			s = tests[key]
			if s == nil {
				s = &state{}
				tests[key] = s
			}
		}
		switch e.Action {
		case "start", "run":
			s.runs++
		case "pass", "fail", "skip":
			s.ends++
			s.action = e.Action
			if e.Action == "fail" {
				failed = true
			}
		}
	}
	if len(packages) == 0 {
		return "insufficient"
	}
	for _, s := range packages {
		if s.runs != 1 || s.ends != 1 {
			return "insufficient"
		}
	}
	for _, s := range tests {
		if s.runs != 1 || s.ends != 1 {
			return "insufficient"
		}
	}
	for _, id := range required {
		s := tests[id]
		if s == nil || s.action == "skip" {
			return "insufficient"
		}
	}
	if failed && exit != 0 {
		return "failed"
	}
	if failed || exit != 0 {
		return "insufficient"
	}
	return "supported"
}
