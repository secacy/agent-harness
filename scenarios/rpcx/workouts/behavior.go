package workouts

import (
	"context"
	"errors"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"path/filepath"
	"strings"
)

func (e *evaluation) behavior(ctx context.Context, candidate, fixtures, tests map[string][]byte, required []string) error {
	root := filepath.Join(e.req.EvidenceDir, "trusted", "trainings")
	production := map[string][]byte{}
	for name, data := range candidate {
		if !strings.HasSuffix(name, "_test.go") {
			production[name] = data
		}
	}
	for _, entry := range []struct {
		root  string
		files map[string][]byte
	}{{root, production}, {root, tests}, {filepath.Join(filepath.Dir(root), "rpcx-v1"), gate.Module(fixtures, "rpcx-v1")}, {filepath.Join(filepath.Dir(root), "rpcx-v2"), gate.Module(fixtures, "rpcx-v2")}, {filepath.Join(filepath.Dir(root), "common"), gate.Module(fixtures, "common")}} {
		if err := gate.CopyFiles(entry.root, entry.files); err != nil {
			return err
		}
	}
	probe, err := materials.ReadFile("probes/boundary_test.go.txt")
	if err != nil {
		return err
	}
	if err = gate.Write(filepath.Join(root, "adapters/harness_boundary_test.go"), probe); err != nil {
		return err
	}
	id := "behavior-probe"
	if err = e.save(id, "trusted_probe", "probe.go.txt", probe); err != nil {
		return err
	}
	ids := []string{id}
	status := quality.StatusPass
	var findings []quality.Finding
	names := map[string]bool{}
	for _, test := range required {
		at := strings.Index(test, "/Test")
		name, _, _ := strings.Cut(test[at+1:], "/")
		names[name] = true
	}
	steps := []struct {
		name, pattern, pkg string
		tests              []string
		attributed         bool
	}{
		{"trusted-tests", "^(" + strings.Join(gate.SortedKeys(names), "|") + ")$", "./...", required, false},
		{"behavior-quick", "^TestHarnessWorkoutsBoundary$", "./adapters", probeTests(), true},
		{"behavior-expiry", "^TestHarnessWorkoutsExpiry$", "./adapters", expiryTests(), true},
	}
	for _, step := range steps {
		name := step.name
		r, err := e.command(ctx, name, root, []string{"go", "test", "-json", "-count=1", "-timeout=45s", "-run=" + step.pattern, step.pkg})
		if err != nil {
			return err
		}
		observed, violations := gate.Observe(r.Stdout, step.tests, r.ExitCode, rules("semantic_behavior"), "WORKOUTS_FINDING ")
		if step.attributed && observed == quality.StatusFail && len(violations) == 0 {
			observed = quality.StatusUnknown
		}
		findings = append(findings, violations...)
		if observed == quality.StatusFail || status == quality.StatusPass {
			status = observed
		}
		ids = append(ids, name, name+"-stdout", name+"-stderr")
		// 原适配测试已明确失败时仍运行独立探针，避免把业务失败冒称期限证据。
		if observed != quality.StatusPass && !(step.name == "trusted-tests" && observed == quality.StatusFail) {
			break
		}
	}
	result := check("semantic_behavior", status, "trusted request/deadline/business observations: "+status, ids...)
	for i := range findings {
		findings[i].EvidenceIDs = ids
	}
	result.Findings = append(result.Findings, findings...)
	e.result.Checks["semantic_behavior"] = result
	return nil
}

const probePrefix = "github.com/ThreeDotsLabs/wild-workouts-go-ddd-example/internal/trainings/adapters/"

func probeModes() []string {
	return []string{"default", "short-parent", "long-parent", "cancelled", "expired", "cancel-users", "cancel-trainer", "balance-error", "trainer-error", "zero-balance", "repeat"}
}
func probeTests() []string {
	out := []string{probePrefix + "TestHarnessWorkoutsBoundary"}
	for _, mode := range probeModes() {
		out = append(out, probePrefix+"TestHarnessWorkoutsBoundary/"+mode)
	}
	return out
}
func expiryTests() []string {
	return []string{probePrefix + "TestHarnessWorkoutsExpiry", probePrefix + "TestHarnessWorkoutsExpiry/users", probePrefix + "TestHarnessWorkoutsExpiry/trainer"}
}
func (e *evaluation) localModule(ctx context.Context, root string) error {
	required, err := ModuleTests("common")
	if err != nil {
		return err
	}
	for _, step := range []string{"build", "test"} {
		args := []string{"go", "build", "-o", "/dev/null", "./..."}
		if step == "test" {
			args = []string{"go", "test", "-json", "-count=1", "-timeout=45s", "./..."}
		}
		result, err := e.command(ctx, "common-"+step, root, args)
		if err != nil {
			return err
		}
		if result.ExitCode != 0 {
			return errors.New("authenticated common module failed: " + step)
		}
		if step == "test" {
			status, _ := gate.Observe(result.Stdout, required, result.ExitCode, nil, "")
			if status != quality.StatusPass {
				return errors.New("authenticated common test evidence incomplete")
			}
		}
	}
	e.result.Checks["local_modules"] = check("local_modules", quality.StatusPass, "authenticated common build and test events passed", "common-build", "common-build-stdout", "common-build-stderr", "common-test", "common-test-stdout", "common-test-stderr", "input-snapshots")
	return nil
}
func observe(data []byte, required []string, exit int) (string, []quality.Finding) {
	return gate.Observe(data, required, exit, rules("semantic_behavior"), "WORKOUTS_FINDING ")
}
