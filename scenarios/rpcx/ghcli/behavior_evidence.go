package ghcli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

const behaviorEvidenceID = "behavior-observations"

var behaviorArgs = []string{"go", "test", "-json", "-count=1", "-timeout=45s", "-run=^TestHarnessApplication", "./pkg/cmd/release/delete"}

// behaviorEvidence 的清单表示实际执行前后均核对过的只读输入。
type behaviorEvidence struct {
	SchemaVersion int                          `json:"schema_version"`
	Binding       quality.Binding              `json:"binding"`
	ToolSHA256    string                       `json:"tool_sha256"`
	CommandSHA256 string                       `json:"command_sha256"`
	StdoutSHA256  string                       `json:"stdout_sha256"`
	StderrSHA256  string                       `json:"stderr_sha256"`
	Production    map[string]string            `json:"production"`
	Trusted       map[string]string            `json:"trusted"`
	SDKs          map[string]map[string]string `json:"sdks"`
	Observation   behaviorObservations         `json:"observation"`
}

func productionFiles(candidate map[string][]byte) map[string][]byte {
	out := map[string][]byte{}
	for name, data := range candidate {
		if !strings.HasSuffix(name, "_test.go") {
			out[name] = data
		}
	}
	return out
}

func behaviorCheck(observation behaviorObservations) quality.Check {
	refs := []string{"behavior-probe", "behavior-return-probe", "behavior", "behavior-stdout", "behavior-stderr", behaviorEvidenceID}
	c := check("semantic_behavior", observation.Status, "independent request/parent/expiry/ownership/return observations: "+observation.Status, refs...)
	for _, finding := range observation.Findings {
		finding.EvidenceIDs = append(append([]string{}, refs...), "rule-map")
		c.Findings = append(c.Findings, finding)
	}
	return c
}

// ValidateBehaviorEvidence 离线核对格式 1 的输入清单、原始事件和报告归因，不执行仓库代码。
// originalEvidenceDir 是执行时的原始证据根，归档迁移后仍使用原身份。
// read 和 tree 必须由调用方限制在当前证据目录内；旧版报告不调用本格式校验。
func ValidateBehaviorEvidence(v checks.Evaluation, originalEvidenceDir string, read func(string) ([]byte, error), tree func(string) (map[string]string, error)) error {
	ids := map[string]quality.Evidence{}
	for _, e := range v.Evidence {
		if _, ok := ids[e.ID]; ok {
			return errors.New("duplicate behavior evidence ID")
		}
		ids[e.ID] = e
	}
	expected := []struct{ id, path string }{{behaviorEvidenceID, "behavior-observations.json"}, {"behavior", "commands/behavior.json"}, {"behavior-stdout", "logs/behavior.stdout.log"}, {"behavior-stderr", "logs/behavior.stderr.log"}, {"behavior-probe", "probe.go.txt"}, {"behavior-return-probe", "returns-probe.go.txt"}}
	binding := quality.Binding{SpecSHA256: v.SpecSHA256, BaselineSHA256: v.BaselineSHA256, CandidateSHA256: v.CandidateSHA256}
	data := map[string][]byte{}
	for _, entry := range expected {
		id, path := entry.id, entry.path
		e, ok := ids[id]
		if !ok || e.Path != path || e.Binding != binding {
			return fmt.Errorf("missing or unbound behavior evidence: %s", id)
		}
		raw, err := read(path)
		if err != nil {
			return err
		}
		if gate.Digest(raw) != e.SHA256 {
			return fmt.Errorf("behavior evidence digest mismatch: %s", id)
		}
		data[id] = raw
	}
	var saved behaviorEvidence
	decoder := json.NewDecoder(bytes.NewReader(data[behaviorEvidenceID]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&saved); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || saved.SchemaVersion != 1 {
		return errors.New("unsupported behavior evidence format")
	}
	if saved.Binding != binding || saved.ToolSHA256 != v.MaterialsSHA256 || saved.CommandSHA256 != gate.Digest(data["behavior"]) || saved.StdoutSHA256 != gate.Digest(data["behavior-stdout"]) || saved.StderrSHA256 != gate.Digest(data["behavior-stderr"]) {
		return errors.New("behavior observation binding mismatch")
	}
	var command checks.Command
	if err := json.Unmarshal(data["behavior"], &command); err != nil {
		return err
	}
	if !slices.ContainsFunc(v.Commands, func(c checks.Command) bool { return reflect.DeepEqual(c, command) }) || len(command.Args) != len(behaviorArgs)+2 || command.Args[0] != "env" || !strings.HasPrefix(command.Args[1], "GOMODCACHE=") || !slices.Equal(command.Args[2:], behaviorArgs) || command.Log != "logs/behavior.stdout.log" {
		return errors.New("invalid behavior command")
	}
	if command.TimedOut || command.Cancelled || command.OOMKilled || command.OutputLimit || command.ExitCode < 0 || command.Backend.Kind != "docker" {
		return errors.New("behavior command did not complete within execution limits")
	}
	if filepath.Clean(command.Directory) != filepath.Join(originalEvidenceDir, "trusted", "ghcli") {
		return errors.New("behavior command belongs to another execution directory")
	}
	candidate, err := tree("candidate/ghcli")
	if err != nil {
		return err
	}
	encoded, _ := json.Marshal(candidate)
	if gate.Digest(encoded) != v.CandidateSHA256 {
		return errors.New("behavior candidate snapshot mismatch")
	}
	production := map[string]string{}
	for name, hash := range candidate {
		if !strings.HasSuffix(name, "_test.go") {
			production[name] = hash
		}
	}
	if !maps.Equal(saved.Production, production) {
		return errors.New("behavior production projection mismatch")
	}
	trusted := maps.Clone(production)
	for _, probe := range []struct{ id, source, target string }{{"behavior-probe", "boundary_test.go.txt", "harness_application_test.go"}, {"behavior-return-probe", "returns_test.go.txt", "harness_returns_test.go"}} {
		raw, err := read("tool/probes/" + probe.source)
		if err != nil {
			return err
		}
		if !bytes.Equal(raw, data[probe.id]) {
			return errors.New("behavior probe differs from archived tool")
		}
		path := "pkg/cmd/release/delete/" + probe.target
		if _, exists := trusted[path]; exists {
			return errors.New("behavior probe overlaps production")
		}
		trusted[path] = gate.Digest(raw)
	}
	actual, err := tree("trusted/ghcli")
	if err != nil {
		return err
	}
	if !maps.Equal(trusted, saved.Trusted) || !maps.Equal(trusted, actual) {
		return errors.New("behavior trusted tree mismatch")
	}
	if len(saved.SDKs) != 2 {
		return errors.New("invalid behavior SDK inventory")
	}
	for _, sdk := range []string{"rpcx-v1", "rpcx-v2"} {
		baseline, err := tree("baseline/" + sdk)
		if err != nil {
			return err
		}
		actual, err := tree("trusted/" + sdk)
		if err != nil {
			return err
		}
		if !maps.Equal(baseline, saved.SDKs[sdk]) || !maps.Equal(baseline, actual) {
			return errors.New("behavior SDK snapshot mismatch")
		}
	}
	observation := observeBehavior(data["behavior-stdout"], behaviorPackage, behaviorContracts(), command.ExitCode)
	if !reflect.DeepEqual(saved.Observation, observation) || !reflect.DeepEqual(v.Checks["semantic_behavior"], behaviorCheck(observation)) {
		return errors.New("behavior observation does not match raw events or reported check")
	}
	return nil
}
