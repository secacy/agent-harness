package ghcli

import (
	"context"
	"errors"
	"maps"
	"path/filepath"

	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

func (e *evaluation) behavior(ctx context.Context, candidate, fixtures map[string][]byte) error {
	root := filepath.Join(e.req.EvidenceDir, "trusted/ghcli")
	production := productionFiles(candidate)
	if err := gate.CopyFiles(root, production); err != nil {
		return err
	}
	for _, sdk := range []string{"rpcx-v1", "rpcx-v2"} {
		if err := gate.CopyFiles(filepath.Join(filepath.Dir(root), sdk), gate.Module(fixtures, sdk)); err != nil {
			return err
		}
	}
	probe, err := materials.ReadFile("probes/boundary_test.go.txt")
	if err != nil {
		return err
	}
	if err = gate.Write(filepath.Join(root, "pkg/cmd/release/delete/harness_application_test.go"), probe); err != nil {
		return err
	}
	if err = e.save("behavior-probe", "trusted_probe", "probe.go.txt", probe); err != nil {
		return err
	}
	returnProbe, err := materials.ReadFile("probes/returns_test.go.txt")
	if err != nil {
		return err
	}
	if err = gate.Write(filepath.Join(root, "pkg/cmd/release/delete/harness_returns_test.go"), returnProbe); err != nil {
		return err
	}
	if err = e.save("behavior-return-probe", "trusted_probe", "returns-probe.go.txt", returnProbe); err != nil {
		return err
	}
	trusted := maps.Clone(production)
	trusted["pkg/cmd/release/delete/harness_application_test.go"] = probe
	trusted["pkg/cmd/release/delete/harness_returns_test.go"] = returnProbe
	actual, err := gate.ReadFiles(root)
	if err != nil {
		return err
	}
	if gate.TreeHash(actual) != gate.TreeHash(trusted) {
		return errors.New("behavior copy differs from candidate production and trusted probes")
	}
	r, err := e.command(ctx, "behavior", root, behaviorArgs)
	if err != nil {
		return err
	}
	observation := observeBehavior(r.Stdout, behaviorPackage, behaviorContracts(), r.ExitCode)
	record := behaviorEvidence{SchemaVersion: 1, Binding: e.binding(), ToolSHA256: e.result.MaterialsSHA256, Production: gate.Hashes(production), Trusted: gate.Hashes(trusted), SDKs: map[string]map[string]string{}, Observation: observation}
	for _, sdk := range []string{"rpcx-v1", "rpcx-v2"} {
		record.SDKs[sdk] = gate.Hashes(gate.Module(fixtures, sdk))
	}
	for _, entry := range e.result.Evidence {
		switch entry.ID {
		case "behavior":
			record.CommandSHA256 = entry.SHA256
		case "behavior-stdout":
			record.StdoutSHA256 = entry.SHA256
		case "behavior-stderr":
			record.StderrSHA256 = entry.SHA256
		}
	}
	if err = e.saveJSON(behaviorEvidenceID, "behavior_observations", "behavior-observations.json", record); err != nil {
		return err
	}
	e.result.Checks["semantic_behavior"] = behaviorCheck(observation)
	return nil
}
