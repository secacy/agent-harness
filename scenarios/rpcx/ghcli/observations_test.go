package ghcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

const observationPackage = "contract.invalid/returns"

type observationEvent struct{ Action, Package, Test, Output string }

func contractEvents(fail bool) []observationEvent {
	events := []observationEvent{{Action: "start", Package: observationPackage}}
	for _, test := range returnContracts() {
		events = append(events, observationEvent{Action: "run", Package: observationPackage, Test: test.Name})
		if test.Name == "TestHarnessApplicationReturns" {
			continue
		}
		status := "pass"
		if fail && strings.HasSuffix(test.Name, "/redirect-declined") {
			status = "fail"
			events = append(events, observationEvent{Action: "output", Package: observationPackage, Test: test.Name, Output: behaviorMarker + `{"Rule":"BEHAVIOR-001","Code":"response_missing_on_redirect_error"}`})
		}
		events = append(events, observationEvent{Action: status, Package: observationPackage, Test: test.Name})
	}
	status := "pass"
	if fail {
		status = "fail"
	}
	return append(events, observationEvent{Action: status, Package: observationPackage, Test: "TestHarnessApplicationReturns"}, observationEvent{Action: status, Package: observationPackage})
}

func eventBytes(events []observationEvent) []byte {
	var buf bytes.Buffer
	for _, event := range events {
		_ = json.NewEncoder(&buf).Encode(event)
	}
	return buf.Bytes()
}

func TestObserveBehavior(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprintf("fail=%v", fail), func(t *testing.T) {
			exit, want := 0, quality.StatusPass
			if fail {
				exit, want = 1, quality.StatusFail
			}
			got := observeBehavior(eventBytes(contractEvents(fail)), observationPackage, returnContracts(), exit)
			if got.Status != want || len(got.Tests) != 5 || (len(got.Findings) == 1) != fail {
				t.Fatalf("unexpected observation: %+v", got)
			}
			if fail && (got.Findings[0].Code != "response_missing_on_redirect_error" || got.Findings[0].RuleIDs[0] != "BEHAVIOR-001") {
				t.Fatal(got.Findings)
			}
			data, err := json.Marshal(got)
			var copy behaviorObservations
			if err != nil || json.Unmarshal(data, &copy) != nil || copy.SchemaVersion != 1 || copy.Status != want {
				t.Fatal("invalid observation format")
			}
		})
	}
}

func TestObserveBehaviorIncompleteOrUnattributed(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func([]observationEvent) []observationEvent
	}{
		{"missing-terminal", func(e []observationEvent) []observationEvent { return append(e[:2], e[3:]...) }},
		{"duplicate-run", func(e []observationEvent) []observationEvent {
			return append(e[:2], append([]observationEvent{e[1]}, e[2:]...)...)
		}},
		{"duplicate-terminal", func(e []observationEvent) []observationEvent {
			return append(e[:4], append([]observationEvent{e[3]}, e[4:]...)...)
		}},
		{"skipped", func(e []observationEvent) []observationEvent { e[3].Action = "skip"; return e }},
		{"finding-on-pass", func(e []observationEvent) []observationEvent {
			e[7].Test = "TestHarnessApplicationReturns/success"
			return e
		}},
		{"wrong-package", func(e []observationEvent) []observationEvent { e[7].Package = "other.invalid/package"; return e }},
		{"unknown-test", func(e []observationEvent) []observationEvent { e[7].Test = "TestUntrusted"; return e }},
		{"package-marker", func(e []observationEvent) []observationEvent { e[7].Test = ""; return e }},
		{"parent-marker", func(e []observationEvent) []observationEvent { e[7].Test = "TestHarnessApplicationReturns"; return e }},
		{"missing-finding", func(e []observationEvent) []observationEvent { e[7].Output = "ordinary failure"; return e }},
		{"unknown-rule", func(e []observationEvent) []observationEvent {
			e[7].Output = strings.ReplaceAll(e[7].Output, "BEHAVIOR-001", "TIME-001")
			return e
		}},
		{"unknown-reason", func(e []observationEvent) []observationEvent {
			e[7].Output = strings.ReplaceAll(e[7].Output, "response_missing_on_redirect_error", "unknown")
			return e
		}},
		{"extra-finding-field", func(e []observationEvent) []observationEvent {
			e[7].Output = strings.TrimSuffix(e[7].Output, "}") + `,"Detail":"untrusted"}`
			return e
		}},
		{"trailing-finding", func(e []observationEvent) []observationEvent { e[7].Output += " {}"; return e }},
		{"panic", func(e []observationEvent) []observationEvent {
			e[7].Output = "panic: test timed out after 45s"
			return e
		}},
		{"fatal", func(e []observationEvent) []observationEvent {
			e[7].Output = "fatal error: concurrent map writes"
			return e
		}},
		{"incomplete-package", func(e []observationEvent) []observationEvent { return e[:len(e)-1] }},
		{"duplicate-package", func(e []observationEvent) []observationEvent { return append(e, e[len(e)-1]) }},
		{"parent-pass-child-fail", func(e []observationEvent) []observationEvent { e[len(e)-2].Action = "pass"; return e }},
		{"second-unattributed-fail", func(e []observationEvent) []observationEvent { e[3].Action = "fail"; return e }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := observeBehavior(eventBytes(tc.edit(contractEvents(true))), observationPackage, returnContracts(), 1)
			if got.Status != quality.StatusUnknown || got.Problem == "" || len(got.Findings) != 0 {
				t.Fatalf("unsafe attribution: %+v", got)
			}
		})
	}
	for _, tc := range []struct {
		name string
		data []byte
		exit int
	}{
		{"compile-failure", eventBytes([]observationEvent{{Action: "fail", Package: observationPackage}}), 1},
		{"invalid-json", []byte("{broken"), 1},
		{"truncated-json", append(eventBytes(contractEvents(true)), []byte("{")...), 1},
		{"exit-zero-with-failure", eventBytes(contractEvents(true)), 0},
		{"killed", eventBytes(contractEvents(true)), 137},
		{"exit-failure-with-pass", eventBytes(contractEvents(false)), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := observeBehavior(tc.data, observationPackage, returnContracts(), tc.exit)
			if got.Status != quality.StatusUnknown || len(got.Findings) > 0 {
				t.Fatalf("unsafe attribution: %+v", got)
			}
		})
	}
}

// 仅执行固定、已审查的开发材料，不执行任意候选或读取独立参考。
func TestReturnProbeCalibration(t *testing.T) {
	root := t.TempDir()
	fixtures := "../l2/data/rpcx-l2-ghcli-v1/fixtures"
	for _, sdk := range []string{"rpcx-v1", "rpcx-v2"} {
		files, err := gate.ReadFiles(filepath.Join(fixtures, sdk))
		if err != nil {
			t.Fatal(err)
		}
		if err = gate.CopyFiles(filepath.Join(root, sdk), files); err != nil {
			t.Fatal(err)
		}
	}
	read := func(path string) []byte {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	baseline := read(filepath.Join(fixtures, "ghcli/pkg/cmd/release/delete/outbound.go"))
	saved := read("regression/testdata/response-v1/outbound.go.txt")
	replace := func(data []byte, old, new string) []byte {
		if bytes.Count(data, []byte(old)) != 1 {
			t.Fatal("fixture edit must match once")
		}
		return bytes.Replace(data, []byte(old), []byte(new), 1)
	}
	preserved := replace(saved, "\tif err != nil {\n\t\tresponse.Response = nil\n\t}\n", "")
	probe := read("probes/returns_test.go.txt")
	for _, tc := range []struct {
		name   string
		source []byte
		v1     bool
		codes  []string
	}{
		{"baseline", baseline, true, nil},
		{"saved", saved, false, []string{"response_missing_on_redirect_error"}},
		{"preserved", preserved, false, nil},
		{"discard-error", replace(preserved, "return response.Response, err", "return response.Response, nil"), false, []string{"error_chain_changed", "error_chain_changed"}},
		{"close-error", replace(preserved, "return body.ReadCloser.Close()", "_ = body.ReadCloser.Close()\nreturn nil"), false, []string{"close_error_changed", "close_error_changed"}},
		{"early-close", replace(preserved, "resp.Body = &cancelBody", "_ = resp.Body.Close()\nresp.Body = &cancelBody"), false, []string{"body_closed_before_caller", "body_closed_before_caller"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sdk, version, dir := "rpcx/v2", "v2.0.0", filepath.Join(root, tc.name)
			sibling := "rpcx-v2"
			if tc.v1 {
				sdk, version, sibling = "rpcx", "v1.0.0", "rpcx-v1"
			}
			mod := fmt.Sprintf("module %s\n\ngo 1.23.0\nrequire example.com/%s %s\nreplace example.com/%s => ../%s\n", observationPackage, sdk, version, sdk, sibling)
			if err := gate.CopyFiles(dir, map[string][]byte{"go.mod": []byte(mod), "outbound.go": tc.source, "returns_test.go": probe}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "test", "-json", "-count=1", "-timeout=10s", ".")
			cmd.Dir = dir
			for _, key := range []string{"PATH", "HOME", "TMPDIR", "GOCACHE"} {
				if value, ok := os.LookupEnv(key); ok {
					cmd.Env = append(cmd.Env, key+"="+value)
				}
			}
			cmd.Env = append(cmd.Env, "GOPROXY=off", "GOSUMDB=off", "GOWORK=off", "GOTOOLCHAIN=local", "GOENV=off", "GOFLAGS=-mod=readonly", "CGO_ENABLED=0", "GOTELEMETRY=off")
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			cmd.WaitDelay = time.Second
			data, err := cmd.Output()
			exit := 0
			if err != nil {
				if failure, ok := err.(*exec.ExitError); ok {
					exit = failure.ExitCode()
				} else {
					t.Fatal(err)
				}
			}
			got := observeBehavior(data, observationPackage, returnContracts(), exit)
			want := quality.StatusPass
			if len(tc.codes) > 0 {
				want = quality.StatusFail
			}
			var codes []string
			for _, finding := range got.Findings {
				codes = append(codes, finding.Code)
			}
			a, _ := json.Marshal(codes)
			b, _ := json.Marshal(tc.codes)
			if ctx.Err() != nil || got.Status != want || !bytes.Equal(a, b) {
				t.Fatalf("got %+v codes=%s want=%s\n%s\n%s", got, a, b, data, stderr.Bytes())
			}
			t.Logf("status=%s findings=%s", got.Status, a)
			observation, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("source_sha256=%s probe_sha256=%s observation=%s\nraw events:\n%s", gate.Digest(tc.source), gate.Digest(probe), observation, data)
		})
	}
}
