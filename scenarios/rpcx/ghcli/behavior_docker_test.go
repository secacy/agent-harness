package ghcli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

// TestBehaviorGateDocker 在固定开发输入上运行完整门禁及观察材料审计，不调用模型或独立参考。
func TestBehaviorGateDocker(t *testing.T) {
	output := os.Getenv("HARNESS_GHCLI_BEHAVIOR_OUTPUT")
	if output == "" {
		t.Skip("set HARNESS_GHCLI_BEHAVIOR_OUTPUT for runtime behavior Docker verification")
	}
	output, err := gate.Canonical(output)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(output, 0755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Minute)
	defer cancel()
	backend, err := execution.NewDocker(ctx, execution.DockerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	fixtures := baselineFixture(t)
	baseline := gate.Module(fixtures, "ghcli")
	saved, err := os.ReadFile("regression/testdata/response-v1/outbound.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	replace := func(data []byte, old, new string) []byte {
		if bytes.Count(data, []byte(old)) != 1 {
			t.Fatal("fixture edit must match once")
		}
		return bytes.Replace(data, []byte(old), []byte(new), 1)
	}
	// 正例只在既有边界添加显式期限，不加入额外业务修改。
	direct := replace(baseline[boundaryFile], `"example.com/rpcx"`, `"example.com/rpcx/v2"`)
	direct = replace(direct, `"sync/atomic"`, "\"sync/atomic\"\n\"time\"")
	direct = replace(direct, "Call(string, any, any)", "Call(context.Context, string, any, any)")
	direct = replace(direct, "rpcx.NewClient(address)", "rpcx.Dial(address)")
	direct = replace(direct, `err = caller.Call("http.do",`, `callCtx,cancel:=context.WithTimeout(request.Context(),30*time.Second);defer cancel();err = caller.Call(callCtx,"http.do",`)
	helper := replace(direct, "context.WithTimeout(request.Context(),", "context.WithTimeout(identity(request.Context()),")
	helper = append(helper, []byte("\nfunc identity(c context.Context)context.Context{return c}\n")...)
	preserved := replace(saved, "\tif err != nil {\n\t\tresponse.Response = nil\n\t}\n", "")
	reordered := replace(direct, `out.Response = resp
	if err != nil {
		cancel()
		return err
	}
	if resp.Body == nil {
		cancel()
		return nil
	}
	resp.Body = &cancelBody{ReadCloser: resp.Body, cancel: cancel}
	return nil`, `out.Response = resp
 if err == nil {
  if resp.Body != nil { resp.Body = &cancelBody{ReadCloser: resp.Body, cancel: cancel} } else { cancel() }
  return nil
 }
 cancel()
 return err`)
	type testCase struct {
		name                    string
		source                  []byte
		verdict, behavior, code string
	}
	cases := []testCase{
		{"direct", direct, quality.ReadyForReview, quality.StatusPass, ""},
		{"saved", saved, quality.Rejected, quality.StatusFail, "response_missing_on_redirect_error"},
		{"helper", helper, quality.NeedsAttention, quality.StatusPass, ""},
		{"discard-error", replace(preserved, "return response.Response, err", "return response.Response, nil"), quality.Rejected, quality.StatusFail, "error_chain_changed"},
		{"early-close", replace(preserved, "resp.Body = &cancelBody", "_ = resp.Body.Close()\nresp.Body = &cancelBody"), quality.Rejected, quality.StatusFail, "body_closed_before_caller"},
		{"close-error", replace(preserved, "return body.ReadCloser.Close()", "_ = body.ReadCloser.Close()\nreturn nil"), quality.Rejected, quality.StatusFail, "close_error_changed"},
		{"renamed", []byte(strings.ReplaceAll(string(direct), "callCtx", "boundedCall")), quality.ReadyForReview, quality.StatusPass, ""},
		{"reordered", reordered, quality.NeedsAttention, quality.StatusPass, ""},
		{"wrong-timeout", replace(direct, "30*time.Second", "60*time.Second"), quality.Rejected, quality.StatusFail, "actual_expiry_changed"},
	}
	var results []map[string]any
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(output, tc.name)
			if err := gate.CopyFiles(filepath.Join(dir, "input/baseline"), fixtures); err != nil {
				t.Fatal(err)
			}
			candidate := map[string][]byte{}
			for name, data := range fixtures {
				candidate[name] = data
			}
			candidate["ghcli/"+boundaryFile] = tc.source
			candidate["ghcli/go.mod"] = migratedMod(baseline["go.mod"])
			if err := gate.CopyFiles(filepath.Join(dir, "input/candidate"), candidate); err != nil {
				t.Fatal(err)
			}
			spec := filepath.Join(dir, "input/spec.md")
			if err := gate.Write(spec, Spec()); err != nil {
				t.Fatal(err)
			}
			evidence := filepath.Join(dir, "evidence")
			v, err := Evaluate(ctx, checks.EvaluationRequest{BaselineDir: filepath.Join(dir, "input/baseline/ghcli"), CandidateDir: filepath.Join(dir, "input/candidate/ghcli"), SpecPath: spec, EvidenceDir: evidence, CacheDir: filepath.Join(dir, "cache"), Backend: backend})
			if saveErr := gate.WriteJSON(filepath.Join(dir, "evaluation.json"), v); saveErr != nil {
				t.Fatal(saveErr)
			}
			c := v.Checks["semantic_behavior"]
			if err != nil || v.Verdict != tc.verdict || c.Status != tc.behavior {
				t.Fatalf("verdict=%s behavior=%s want=%s/%s error=%v checks=%+v", v.Verdict, c.Status, tc.verdict, tc.behavior, err, v.Checks)
			}
			if tc.code != "" {
				found := false
				for _, f := range c.Findings {
					found = found || f.Code == tc.code
				}
				if !found {
					t.Fatalf("missing reason %s: %+v", tc.code, c.Findings)
				}
			}
			read := func(path string) ([]byte, error) { return os.ReadFile(filepath.Join(evidence, path)) }
			tree := func(path string) (map[string]string, error) {
				files, err := gate.ReadFiles(filepath.Join(evidence, path))
				return gate.Hashes(files), err
			}
			if err = ValidateBehaviorEvidence(v, evidence, read, tree); err != nil {
				t.Fatal(err)
			}
			if tc.name == "saved" {
				testBehaviorEvidenceTampering(t, v, evidence, read, tree)
			}
			results = append(results, map[string]any{"case": tc.name, "candidate_sha256": v.CandidateSHA256, "verdict": v.Verdict, "behavior": c, "commands": v.Commands, "observation_audit": "pass"})
		})
	}
	if !t.Failed() {
		if err := gate.WriteJSON(filepath.Join(output, "results.json"), results); err != nil {
			t.Fatal(err)
		}
	}
}

// 包括重算外层摘要后的内容篡改，避免只证明一般摘要校验生效。
func testBehaviorEvidenceTampering(t *testing.T, v checks.Evaluation, evidence string, read func(string) ([]byte, error), tree func(string) (map[string]string, error)) {
	t.Helper()
	for _, mode := range []string{"deleted-record", "deleted-reference", "changed-observation", "changed-log", "foreign-candidate", "changed-production", "changed-probe", "foreign-run", "changed-report", "changed-trusted-tree", "changed-sdk-tree"} {
		t.Run("audit-"+mode, func(t *testing.T) {
			originalEvidence := evidence
			cloneRaw, _ := json.Marshal(v)
			var altered checks.Evaluation
			_ = json.Unmarshal(cloneRaw, &altered)
			replacements := map[string][]byte{}
			var record behaviorEvidence
			raw, err := read("behavior-observations.json")
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(raw, &record); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "deleted-reference":
				for i, e := range altered.Evidence {
					if e.ID == behaviorEvidenceID {
						altered.Evidence = append(altered.Evidence[:i], altered.Evidence[i+1:]...)
						break
					}
				}
			case "changed-observation":
				record.Observation.Problem = "altered_observation"
			case "changed-log":
				log, err := read("logs/behavior.stdout.log")
				if err != nil {
					t.Fatal(err)
				}
				replacements["logs/behavior.stdout.log"] = bytes.ReplaceAll(log, []byte("response_missing_on_redirect_error"), []byte("response_properties_changed"))
				if bytes.Equal(replacements["logs/behavior.stdout.log"], log) {
					replacements["logs/behavior.stdout.log"] = append(append([]byte{}, log...), '{')
				}
				record.StdoutSHA256 = gate.Digest(replacements["logs/behavior.stdout.log"])
			case "foreign-candidate":
				record.Binding.CandidateSHA256 = fmt.Sprintf("%064d", 0)
			case "changed-production":
				record.Production[boundaryFile] = "different"
			case "foreign-run":
				originalEvidence = filepath.Join(evidence, "other-run")
			case "changed-probe":
				replacements["returns-probe.go.txt"] = []byte("different probe")
			case "changed-report":
				check := altered.Checks["semantic_behavior"]
				check.Detail = "altered reported check"
				altered.Checks["semantic_behavior"] = check
			}
			replacements["behavior-observations.json"], err = json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			for i, e := range altered.Evidence {
				if data, ok := replacements[e.Path]; ok {
					altered.Evidence[i].SHA256 = gate.Digest(data)
				}
			}
			alteredRead := func(path string) ([]byte, error) {
				if mode == "deleted-record" && path == "behavior-observations.json" {
					return nil, os.ErrNotExist
				}
				if data, ok := replacements[path]; ok {
					return data, nil
				}
				return read(path)
			}
			alteredTree := func(path string) (map[string]string, error) {
				files, err := tree(path)
				if mode == "changed-trusted-tree" && path == "trusted/ghcli" || mode == "changed-sdk-tree" && path == "trusted/rpcx-v2" {
					copy := make(map[string]string, len(files)+1)
					for name, sha := range files {
						copy[name] = sha
					}
					copy["unexpected.go"] = "different actual bytes"
					return copy, err
				}
				return files, err
			}
			if err := ValidateBehaviorEvidence(altered, originalEvidence, alteredRead, alteredTree); err == nil {
				t.Fatal("tampered behavior evidence accepted")
			}
			if reflect.DeepEqual(altered, v) {
				t.Log("read failure checked without changing report")
			}
		})
	}
}
