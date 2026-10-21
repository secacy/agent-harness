package ghcli

import (
	"context"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"path/filepath"
	"strings"
)

func (e *evaluation) behavior(ctx context.Context, candidate, fixtures map[string][]byte) error {
	root := filepath.Join(e.req.EvidenceDir, "trusted/ghcli")
	production := map[string][]byte{}
	for name, data := range candidate {
		if !strings.HasSuffix(name, "_test.go") {
			production[name] = data
		}
	}
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
	prefix := "github.com/cli/cli/v2/pkg/cmd/release/delete/"
	names := []string{"TestHarnessApplicationBoundary", "TestHarnessApplicationBodyParent", "TestHarnessApplicationExpiry"}
	for _, mode := range []string{"default", "short", "long", "cancelled", "transport-error", "close-error"} {
		names = append(names, "TestHarnessApplicationBoundary/"+mode)
	}
	required := []string{}
	for _, name := range names {
		required = append(required, prefix+name)
	}
	r, err := e.command(ctx, "behavior", root, []string{"go", "test", "-json", "-count=1", "-timeout=45s", "-run=^TestHarnessApplication", "./pkg/cmd/release/delete"})
	if err != nil {
		return err
	}
	s, findings := gate.Observe(r.Stdout, required, r.ExitCode, rules("semantic_behavior"), "GHCLI_FINDING ")
	if s == quality.StatusFail && len(findings) == 0 {
		s = quality.StatusUnknown
	}
	refs := []string{"behavior-probe", "behavior", "behavior-stdout", "behavior-stderr"}
	c := check("semantic_behavior", s, "independent request/parent/expiry/ownership observations: "+s, refs...)
	for i := range findings {
		findings[i].EvidenceIDs = refs
	}
	c.Findings = append(c.Findings, findings...)
	e.result.Checks["semantic_behavior"] = c
	return nil
}
