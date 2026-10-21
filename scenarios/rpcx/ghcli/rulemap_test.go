package ghcli

import (
	"encoding/json"
	"fmt"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"strings"
	"testing"
)

func TestRuleMapBoundSourcesAndCompleteCheckReferences(t *testing.T) {
	mapping, err := ruleMapping()
	if err != nil {
		t.Fatal(err)
	}
	host, err := HostMaterialsSHA256()
	if err != nil {
		t.Fatal(err)
	}
	if mapping.SpecSHA256 != gate.Digest(Spec()) || mapping.ProfileID != ProfileID || mapping.HostMaterialsSHA256 != host || mapping.IdentifierOrigin != "host_assigned_not_spec_labels" {
		t.Fatal("rule mapping is not bound to the publication")
	}
	lines := strings.Split(string(Spec()), "\n")
	paragraphs := 0
	paragraphLines := map[int]int{}
	for i, line := range lines {
		if line != "" && !strings.HasPrefix(line, "#") {
			paragraphs++
			paragraphLines[paragraphs] = i + 1
		}
	}
	mapped := map[string]bool{}
	for _, r := range mapping.Rules {
		if r.ID == "" || mapped[r.ID] || len(r.Sources) == 0 {
			t.Fatal("invalid rule inventory")
		}
		mapped[r.ID] = true
		for _, s := range r.Sources {
			switch s.Origin {
			case "migration_spec":
				if s.Path != "spec.md" || s.StartLine != paragraphLines[s.Paragraph] || s.EndLine != s.StartLine || s.StartLine > len(lines) {
					t.Fatal("rule does not locate an actual specification paragraph")
				}
			case "host_policy":
				found := false
				for i, req := range policy().Requirements {
					if s.Path == "policy.json" && s.JSONPointer == fmt.Sprintf("/Requirements/%d", i) {
						for _, id := range req.RuleIDs {
							if id == r.ID {
								found = true
							}
						}
					}
				}
				if !found {
					t.Fatal("host rule does not locate its requirement")
				}
			default:
				t.Fatal("unknown rule origin")
			}
		}
	}
	for _, requirement := range policy().Requirements {
		c := check(requirement.ID, quality.StatusUnknown, "mapping check", "input-snapshots")
		seen := false
		for _, ref := range c.EvidenceIDs {
			if ref == "rule-map" {
				seen = true
			}
		}
		if !seen {
			t.Fatal("check lacks rule mapping evidence")
		}
		for _, id := range c.RuleIDs {
			if !mapped[id] {
				t.Fatal("required rule has no mapping")
			}
		}
	}
	raw, err := json.Marshal(mapping)
	if err != nil || !json.Valid(raw) {
		t.Fatal("rule mapping is not serializable")
	}
}

func TestDependencyStatusPreservesPublishedToolchain(t *testing.T) {
	files := gate.Module(baselineFixture(t), "ghcli")
	base := files["go.mod"]
	expected := migratedMod(base)
	if !strings.Contains(string(expected), "toolchain go1.25.5") {
		t.Fatal("published toolchain declaration was removed")
	}
	for _, mode := range []string{"preserved", "changed", "deleted", "duplicate", "malformed", "expected-error"} {
		t.Run(mode, func(t *testing.T) {
			before, candidate := base, expected
			switch mode {
			case "changed":
				candidate = []byte(strings.Replace(string(expected), "toolchain go1.25.5", "toolchain go1.25.6", 1))
			case "deleted":
				candidate = []byte(strings.Replace(string(expected), "toolchain go1.25.5\n", "", 1))
			case "duplicate":
				candidate = append(append([]byte{}, expected...), []byte("\ntoolchain go1.25.5\n")...)
			case "malformed":
				candidate = []byte(strings.Replace(string(expected), "toolchain go1.25.5", "toolchain go1.25.5 extra", 1))
			case "expected-error":
				before = append(append([]byte{}, base...), []byte("\nunknown directive\n")...)
			}
			status, err := dependencyStatus(before, candidate)
			if mode == "expected-error" {
				if err == nil || status != "" {
					t.Fatal("authenticated expected parse error was ignored")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := quality.StatusPass
			if mode == "changed" || mode == "deleted" {
				want = quality.StatusFail
			}
			if mode == "duplicate" || mode == "malformed" {
				want = quality.StatusUnknown
			}
			if status != want {
				t.Fatalf("got %s want %s", status, want)
			}
		})
	}
}
