package ghcli

import "fmt"

const ProfileID = "rpcx-ghcli-t3-v1"

type ruleSource struct {
	Origin      string `json:"origin"`
	Path        string `json:"path"`
	Paragraph   int    `json:"paragraph,omitempty"`
	StartLine   int    `json:"start_line,omitempty"`
	EndLine     int    `json:"end_line,omitempty"`
	JSONPointer string `json:"json_pointer,omitempty"`
}
type mappedRule struct {
	ID      string       `json:"rule_id"`
	Meaning string       `json:"meaning"`
	Sources []ruleSource `json:"sources"`
}
type ruleMap struct {
	SchemaVersion       int          `json:"schema_version"`
	Version             string       `json:"version"`
	IdentifierOrigin    string       `json:"identifier_origin"`
	SpecSHA256          string       `json:"spec_sha256"`
	ProfileID           string       `json:"profile_id"`
	HostMaterialsSHA256 string       `json:"host_materials_sha256"`
	Rules               []mappedRule `json:"rules"`
}

// ruleMapping 明确宿主规则编号的来源，不把它们冒充为纯规范自带条款。
func ruleMapping() (ruleMap, error) {
	host, err := HostMaterialsSHA256()
	if err != nil {
		return ruleMap{}, err
	}
	spec := func(paragraph, line int) ruleSource {
		return ruleSource{Origin: "migration_spec", Path: "spec.md", Paragraph: paragraph, StartLine: line, EndLine: line}
	}
	hostPolicy := func(check string) ruleSource {
		for i, r := range policy().Requirements {
			if r.ID == check {
				return ruleSource{Origin: "host_policy", Path: "policy.json", JSONPointer: fmt.Sprintf("/Requirements/%d", i)}
			}
		}
		panic("missing fixed host requirement")
	}
	return ruleMap{SchemaVersion: 1, Version: "rpcx-ghcli-rule-map-v1", IdentifierOrigin: "host_assigned_not_spec_labels", SpecSHA256: SpecSHA256(), ProfileID: ProfileID, HostMaterialsSHA256: host, Rules: []mappedRule{
		{ID: "DEP-001", Meaning: "required local dependency/replacement and published read-only dependency material", Sources: []ruleSource{spec(1, 3), spec(4, 9)}},
		{ID: "API-001", Meaning: "v2 Dial and context-bearing Call", Sources: []ruleSource{spec(1, 3)}},
		{ID: "PATH-001", Meaning: "protected original inputs and supporting application sources", Sources: []ruleSource{spec(4, 9)}},
		{ID: "BUILD-001", Meaning: "host requires candidate build completion", Sources: []ruleSource{hostPolicy("build")}},
		{ID: "TEST-001", Meaning: "host requires original test events and preservation", Sources: []ruleSource{hostPolicy("baseline_tests"), hostPolicy("test_preservation"), spec(4, 9)}},
		{ID: "CTX-001", Meaning: "original request parent, cancellation and shorter deadline", Sources: []ruleSource{spec(2, 5), spec(3, 7)}},
		{ID: "TIME-001", Meaning: "explicit per-call 30 second maximum", Sources: []ruleSource{spec(2, 5)}},
		{ID: "TIME-002", Meaning: "response lifetime and derived context resource release", Sources: []ruleSource{spec(3, 7)}},
		{ID: "BEHAVIOR-001", Meaning: "preserved requests, outputs, errors, status and response ownership", Sources: []ruleSource{spec(3, 7)}},
		{ID: "SCOPE-001", Meaning: "only RPC integration and necessary module metadata may change", Sources: []ruleSource{spec(4, 9)}},
	}}, nil
}
