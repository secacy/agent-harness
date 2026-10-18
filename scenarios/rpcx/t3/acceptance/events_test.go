package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/execution"
)

func eventStream(t *testing.T, events ...map[string]string) []byte {
	t.Helper()
	var data []byte
	for _, event := range events {
		line, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, append(line, '\n')...)
	}
	return data
}

func TestTestCheckRequiresCompleteEvidence(t *testing.T) {
	const name = "example.com/demo-orders/cmd/server/TestReferenceBoundary/Reserve/default"
	for _, tc := range []struct {
		name, action, output string
		exit                 int
		want                 string
		wantError            bool
	}{
		{name: "pass", action: "pass", want: "pass"},
		{name: "missing", action: "run", wantError: true},
		{name: "skip-is-inconclusive", action: "skip", want: "inconclusive"},
		{name: "rule-failure", action: "fail", output: `REFERENCE_FINDING {"rule":"TIME-001","detail":"missing deadline"}`, exit: 1, want: "fail"},
		{name: "unexplained-failure", action: "fail", exit: 1, wantError: true},
		{name: "zero-exit-does-not-hide-failure", action: "fail", output: `REFERENCE_FINDING {"rule":"TIME-001","detail":"missing deadline"}`, want: "fail"},
		{name: "package-failed-only", action: "pass", exit: 1, wantError: true},
		{name: "panic-is-not-semantic-failure", action: "fail", output: "panic: test timed out after 90s", exit: 1, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := eventStream(t,
				map[string]string{"Action": "output", "Package": "example.com/demo-orders/cmd/server", "Test": "TestReferenceBoundary/Reserve/default", "Output": tc.output},
				map[string]string{"Action": tc.action, "Package": "example.com/demo-orders/cmd/server", "Test": "TestReferenceBoundary/Reserve/default"},
				map[string]string{"Action": "pass", "Package": "example.com/demo-orders/cmd/server"},
			)
			got, err := testCheck(data, tc.exit, []string{name})
			if (err != nil) != tc.wantError || (!tc.wantError && got.Status != tc.want) {
				t.Fatalf("check=%+v error=%v", got, err)
			}
		})
	}
	if _, err := testCheck([]byte("garbage"), 0, []string{name}); err == nil {
		t.Fatal("accepted malformed test stream")
	}
	if _, err := testCheck(nil, 0, []string{name}); err == nil {
		t.Fatal("accepted empty test stream")
	}
}

func TestProcessFailureAfterFindingIsStillExecutionError(t *testing.T) {
	data := eventStream(t,
		map[string]string{"Action": "output", "Package": "p", "Test": "TestReference", "Output": `REFERENCE_FINDING {"rule":"TIME-001","detail":"missing deadline"}`},
		map[string]string{"Action": "fail", "Package": "p", "Test": "TestReference"},
		map[string]string{"Action": "output", "Package": "p", "Output": "panic: reference call did not stop"},
		map[string]string{"Action": "fail", "Package": "p"},
	)
	if _, err := testCheck(data, 1, []string{"p/TestReference"}); err == nil {
		t.Fatal("process panic was hidden by an earlier expected rule failure")
	}
}

func TestBusinessTestsKeepPackageIdentity(t *testing.T) {
	data := eventStream(t, map[string]string{"Action": "pass", "Package": "example.com/demo-orders/cmd/server", "Test": "TestPlace"}, map[string]string{"Action": "pass", "Package": "example.com/demo-orders/cmd/server"})
	if _, err := testCheck(data, 0, []string{"example.com/demo-orders/internal/orders/TestPlace"}); err == nil {
		t.Fatal("same name in another package hid missing test")
	}
	if len(businessTests()) != 10 || len(referenceTests()) != 32 {
		t.Fatal("unexpected scenario test identity set")
	}
}

type brokenBackend struct{}

func (brokenBackend) Info() execution.Info { return execution.Info{Kind: execution.KindDocker} }
func (brokenBackend) Run(context.Context, execution.Request) (execution.Result, error) {
	return execution.Result{}, errors.New("injected backend failure")
}

func TestVerifyPreservesExecutionErrorWithoutMatchingItToExpectedFailure(t *testing.T) {
	report, err := Verify(context.Background(), Config{ScenarioDir: "..", RunsDir: t.TempDir(), Backend: brokenBackend{}, Development: true, Quick: true})
	if err == nil || !strings.Contains(err.Error(), "injected backend failure") || report == nil || report.Accepted || report.Matched || report.Error == "" {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	var saved Report
	if err := readJSON(filepath.Join(report.Directory, "report.json"), &saved); err != nil || saved.Error == "" || saved.Accepted {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
}

func TestVerifyRejectsUnsafeModesAndInputOverlap(t *testing.T) {
	for _, config := range []Config{
		{ScenarioDir: "..", RunsDir: t.TempDir(), Backend: execution.Local{}},
		{ScenarioDir: "..", RunsDir: t.TempDir(), Backend: brokenBackend{}, Quick: true},
		{ScenarioDir: "..", RunsDir: "../fixtures/output", Backend: brokenBackend{}},
	} {
		if _, err := Verify(context.Background(), config); err == nil {
			t.Fatal("accepted invalid configuration")
		}
	}
}

func TestReadJSONRejectsTrailingValueAndUnknownFields(t *testing.T) {
	for _, data := range []string{`{} {}`, `{"unexpected":1}`} {
		p := filepath.Join(t.TempDir(), "input.json")
		if err := os.WriteFile(p, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
		var value struct{}
		if err := readJSON(p, &value); err == nil {
			t.Fatal("accepted invalid JSON contract")
		}
	}
}

func TestFrozenVerificationRejectsChangedMaterials(t *testing.T) {
	scenario := filepath.Join(t.TempDir(), "scenario")
	if err := copyTree("..", scenario, false); err != nil {
		t.Fatal(err)
	}
	inputs, err := snapshot(scenario)
	if err != nil {
		t.Fatal(err)
	}
	delete(inputs, "freeze.json")
	if err := writeJSON(filepath.Join(scenario, "freeze.json"), inputs); err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(scenario, "specs/v0.2.0.md")
	if err := os.WriteFile(spec, []byte("changed contract"), 0644); err != nil {
		t.Fatal(err)
	}
	report, err := Verify(context.Background(), Config{ScenarioDir: scenario, RunsDir: t.TempDir(), Backend: brokenBackend{}})
	if err == nil || !strings.Contains(err.Error(), "freeze.json") || report == nil || report.FreezeMatched || report.Accepted || len(report.Commands) != 0 {
		t.Fatalf("report=%+v err=%v", report, err)
	}
}
