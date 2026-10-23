package ghcli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

func allBehaviorEvents(fail bool) []byte {
	events := []observationEvent{{Action: "start", Package: behaviorPackage}}
	tests := behaviorContracts()
	var visit func(string)
	visit = func(name string) {
		events = append(events, observationEvent{Action: "run", Package: behaviorPackage, Test: name})
		for _, test := range tests {
			if parent, ok := directTestParent(test.Name); ok && parent == name {
				visit(test.Name)
			}
		}
		status := "pass"
		if fail && (name == "TestHarnessApplicationReturns/redirect-declined" || name == "TestHarnessApplicationReturns") {
			status = "fail"
		}
		if fail && name == "TestHarnessApplicationReturns/redirect-declined" {
			events = append(events, observationEvent{Action: "output", Package: behaviorPackage, Test: name, Output: behaviorMarker + `{"Rule":"BEHAVIOR-001","Code":"response_missing_on_redirect_error"}`})
		}
		events = append(events, observationEvent{Action: status, Package: behaviorPackage, Test: name})
	}
	for _, test := range tests {
		if _, ok := directTestParent(test.Name); !ok {
			visit(test.Name)
		}
	}
	status := "pass"
	if fail {
		status = "fail"
	}
	return eventBytes(append(events, observationEvent{Action: status, Package: behaviorPackage}))
}

func directTestParent(name string) (string, bool) {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '/' {
			return name[:i], true
		}
	}
	return "", false
}

func TestBehaviorEvidenceRecomputesAttribution(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			files := map[string][]byte{}
			save := func(path string, value any) []byte {
				data, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				files[path] = data
				return data
			}
			candidate := map[string][]byte{"go.mod": []byte("module contract.invalid"), "boundary.go": []byte("package example"), "original_test.go": []byte("package example")}
			trees := map[string]map[string]string{"candidate/ghcli": gate.Hashes(candidate)}
			production := gate.Hashes(productionFiles(candidate))
			trusted := map[string]string{}
			for path, hash := range production {
				trusted[path] = hash
			}
			for _, p := range []struct{ source, saved, target string }{{"boundary_test.go.txt", "probe.go.txt", "harness_application_test.go"}, {"returns_test.go.txt", "returns-probe.go.txt", "harness_returns_test.go"}} {
				raw, err := materials.ReadFile("probes/" + p.source)
				if err != nil {
					t.Fatal(err)
				}
				files["tool/probes/"+p.source] = raw
				files[p.saved] = raw
				trusted["pkg/cmd/release/delete/"+p.target] = gate.Digest(raw)
			}
			trees["trusted/ghcli"] = trusted
			sdks := map[string]map[string]string{}
			for _, sdk := range []string{"rpcx-v1", "rpcx-v2"} {
				hashes := map[string]string{"go.mod": gate.Digest([]byte(sdk))}
				trees["baseline/"+sdk] = hashes
				trees["trusted/"+sdk] = hashes
				sdks[sdk] = hashes
			}
			exit := 0
			if fail {
				exit = 1
			}
			command := checks.Command{Directory: "/original/evidence/trusted/ghcli", Args: append([]string{"env", "GOMODCACHE=/cache"}, behaviorArgs...), Log: "logs/behavior.stdout.log", ExitCode: exit, Backend: execution.Info{Kind: execution.KindDocker}}
			commandData := save("commands/behavior.json", command)
			stdout := allBehaviorEvents(fail)
			files["logs/behavior.stdout.log"] = stdout
			files["logs/behavior.stderr.log"] = []byte{}
			observation := observeBehavior(stdout, behaviorPackage, behaviorContracts(), exit)
			want := quality.StatusPass
			if fail {
				want = quality.StatusFail
			}
			if observation.Status != want {
				t.Fatal(observation)
			}
			binding := quality.Binding{SpecSHA256: SpecSHA256(), BaselineSHA256: gate.Digest([]byte("baseline")), CandidateSHA256: gate.TreeHash(candidate)}
			tool := gate.Digest([]byte("tool"))
			record := behaviorEvidence{SchemaVersion: 1, Binding: binding, ToolSHA256: tool, CommandSHA256: gate.Digest(commandData), StdoutSHA256: gate.Digest(stdout), StderrSHA256: gate.Digest(nil), Production: production, Trusted: trusted, SDKs: sdks, Observation: observation}
			save("behavior-observations.json", record)
			v := checks.Evaluation{SpecSHA256: binding.SpecSHA256, BaselineSHA256: binding.BaselineSHA256, CandidateSHA256: binding.CandidateSHA256, MaterialsSHA256: tool, Commands: []checks.Command{command}, Checks: map[string]quality.Check{"semantic_behavior": behaviorCheck(observation)}}
			for id, path := range map[string]string{behaviorEvidenceID: "behavior-observations.json", "behavior": "commands/behavior.json", "behavior-stdout": "logs/behavior.stdout.log", "behavior-stderr": "logs/behavior.stderr.log", "behavior-probe": "probe.go.txt", "behavior-return-probe": "returns-probe.go.txt"} {
				v.Evidence = append(v.Evidence, quality.Evidence{ID: id, Path: path, SHA256: gate.Digest(files[path]), Binding: binding})
			}
			read := func(path string) ([]byte, error) {
				data, ok := files[path]
				if !ok {
					return nil, os.ErrNotExist
				}
				return data, nil
			}
			tree := func(path string) (map[string]string, error) {
				data, ok := trees[path]
				if !ok {
					return nil, os.ErrNotExist
				}
				return data, nil
			}
			if err := ValidateBehaviorEvidence(v, "/original/evidence", read, tree); err != nil {
				t.Fatal(err)
			}
			testBehaviorEvidenceTampering(t, v, "/original/evidence", read, tree)
		})
	}
}

func TestBehaviorContractsAttributeLegacyRules(t *testing.T) {
	for _, tc := range []struct{ test, code, rule string }{
		{"TestHarnessApplicationBoundary/default", "call_deadline_changed", "TIME-001"},
		{"TestHarnessApplicationBoundary/transport-error", "transport_context_not_released", "TIME-002"},
		{"TestHarnessApplicationBodyParent", "body_parent_cancellation_lost", "CTX-001"},
		{"TestHarnessApplicationExpiry", "actual_expiry_changed", "TIME-001"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			decoder := json.NewDecoder(bytes.NewReader(allBehaviorEvents(false)))
			var events []observationEvent
			for decoder.More() {
				var e observationEvent
				if err := decoder.Decode(&e); err != nil {
					t.Fatal(err)
				}
				if e.Action == "pass" && (e.Test == tc.test || strings.HasPrefix(tc.test, e.Test+"/") || e.Test == "") {
					e.Action = "fail"
				}
				if e.Action == "fail" && e.Test == tc.test {
					events = append(events, observationEvent{Action: "output", Package: behaviorPackage, Test: tc.test, Output: fmt.Sprintf(`%s{"Rule":%q,"Code":%q}`, behaviorMarker, tc.rule, tc.code)})
				}
				events = append(events, e)
			}
			got := observeBehavior(eventBytes(events), behaviorPackage, behaviorContracts(), 1)
			if got.Status != quality.StatusFail || len(got.Findings) != 1 || got.Findings[0].RuleIDs[0] != tc.rule {
				t.Fatalf("legacy attribution changed: %+v", got)
			}
		})
	}
}
