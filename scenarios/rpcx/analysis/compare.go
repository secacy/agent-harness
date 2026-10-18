package analysis

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
)

// Compare 消费可信 Inspect 结果，不重新认证磁盘报告或证明业务等价。
// 一对一的同函数调用才可配对；结构变化与双方已有缺口均独立保留。
func Compare(baseline, candidate Report) (Comparison, error) {
	for _, input := range []struct {
		name   string
		report Report
	}{{"baseline", baseline}, {"candidate", candidate}} {
		if err := comparable(input.report); err != nil {
			return Comparison{}, fmt.Errorf("%s analysis: %w", input.name, err)
		}
	}
	if baseline.ToolSHA256 != candidate.ToolSHA256 || !reflect.DeepEqual(normalizeConfig(baseline.Config), normalizeConfig(candidate.Config)) {
		return Comparison{}, errors.New("analysis tool or build configuration differs")
	}
	out := Comparison{SchemaVersion: 1, Baseline: bindReport(baseline), Candidate: bindReport(candidate),
		Matches: []Match{}, Changes: []Change{}, Gaps: []ComparisonGap{}}
	for _, input := range []struct {
		tree   string
		report Report
	}{{"baseline", baseline}, {"candidate", candidate}} {
		for _, gap := range input.report.Gaps {
			out.Gaps = append(out.Gaps, ComparisonGap{Tree: input.tree, File: gap.File, Line: gap.Line, Reason: gap.Reason})
		}
	}
	type owner struct{ pkg, function string }
	type group struct{ before, after []Call }
	groups := map[owner]*group{}
	for side, calls := range [][]Call{baseline.Calls, candidate.Calls} {
		for _, call := range calls {
			key := owner{call.Package, call.Function}
			if groups[key] == nil {
				groups[key] = &group{}
			}
			if side == 0 {
				groups[key].before = append(groups[key].before, call)
			} else {
				groups[key].after = append(groups[key].after, call)
			}
		}
	}
	keys := make([]owner, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].pkg == keys[j].pkg {
			return keys[i].function < keys[j].function
		}
		return keys[i].pkg < keys[j].pkg
	})
	if len(baseline.Calls) == 0 {
		out.Changes = append(out.Changes, Change{Reason: "baseline_calls_empty", Baseline: []Site{}, Candidate: []Site{}})
	}
	for _, key := range keys {
		g := groups[key]
		change := func(reason string) {
			out.Changes = append(out.Changes, Change{Reason: reason, Baseline: sites(g.before), Candidate: sites(g.after)})
		}
		switch {
		case key.function == "":
			change("unidentified_owner")
		case len(g.before) == 0:
			change("call_added")
		case len(g.after) == 0:
			change("call_removed")
		case len(g.before) != 1 || len(g.after) != 1:
			change("ambiguous_function_calls")
		default:
			before, after := g.before[0], g.after[0]
			switch {
			case before.Kind != "direct" || after.Kind != "direct":
				change("indirect_correspondence")
			case after.SDK == "example.com/rpcx":
				change("v1_call_remaining")
			case before.Operation == nil || after.Operation == nil:
				change("operation_unresolved")
			case *before.Operation != *after.Operation:
				change("operation_changed")
			default:
				kind := "v1_to_v2"
				if before.SDK == "example.com/rpcx/v2" {
					kind = "v2_retained"
				}
				out.Matches = append(out.Matches, Match{Baseline: siteOf(before), Candidate: siteOf(after),
					Operation: *after.Operation, Kind: kind, CandidateStatus: after.Status, CandidateReason: after.Reason})
			}
		}
	}
	sort.Slice(out.Gaps, func(i, j int) bool {
		a, b := out.Gaps[i], out.Gaps[j]
		if a.Tree != b.Tree {
			return a.Tree < b.Tree
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Reason < b.Reason
	})
	return out, nil
}

func siteOf(call Call) Site {
	site := Site{Package: call.Package, File: call.File, Line: call.Line, Function: call.Function}
	if call.Operation != nil {
		operation := *call.Operation
		site.Operation = &operation
	}
	return site
}

func sites(calls []Call) []Site {
	out := make([]Site, 0, len(calls))
	for _, call := range calls {
		out = append(out, siteOf(call))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		return out[i].Line < out[j].Line
	})
	return out
}

func bindReport(r Report) ReportBinding {
	data, _ := json.Marshal(r)
	return ReportBinding{InputSHA256: r.InputSHA256, ToolSHA256: r.ToolSHA256, RecordSHA256: digest(data)}
}

func normalizeConfig(c Configuration) Configuration {
	c.Tags = append([]string{}, c.Tags...)
	slices.Sort(c.Tags)
	c.Tags = slices.Compact(c.Tags)
	return c
}

func comparable(r Report) error {
	if r.SchemaVersion != 2 || r.Status != "complete" || r.LoadExitCode != 0 || r.Diagnostic != "" || r.Packages == nil || len(r.Packages) == 0 || r.Calls == nil || r.Gaps == nil {
		return errors.New("complete schema 2 analysis is required")
	}
	validDigest := func(value string) bool {
		data, err := hex.DecodeString(value)
		return err == nil && len(data) == 32 && strings.ToLower(value) == value
	}
	if !validDigest(r.InputSHA256) || !validDigest(r.ToolSHA256) || r.Config.GoVersion == "" || r.Config.GOOS == "" || r.Config.GOARCH == "" || r.Config.CGO != "0" || r.Config.Tags == nil {
		return errors.New("invalid analysis binding or configuration")
	}
	known := func(path string) bool {
		return path != "." && strings.HasSuffix(path, ".go") && filepath.IsLocal(path) && !strings.Contains(path, `\`) && filepath.ToSlash(filepath.Clean(path)) == path
	}
	active, packages := map[string]string{}, map[string]bool{}
	for _, pkg := range r.Packages {
		if pkg.Path == "" || packages[pkg.Path] || len(pkg.Files) == 0 || pkg.Ignored == nil {
			return errors.New("invalid analysis package inventory")
		}
		packages[pkg.Path] = true
		for path, hash := range pkg.Files {
			if !known(path) || !validDigest(hash) || active[path] != "" {
				return errors.New("invalid analysis source inventory")
			}
			active[path] = pkg.Path
		}
		for _, path := range pkg.Ignored {
			if !known(path) {
				return errors.New("invalid ignored source path")
			}
		}
	}
	return validateFacts(r, active, known)
}
