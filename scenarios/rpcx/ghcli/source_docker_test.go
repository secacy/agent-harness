package ghcli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

// TestSourceProofDocker 真实加载构造迁移的类型；不运行候选，也不读取参考答案。
func TestSourceProofDocker(t *testing.T) {
	output := os.Getenv("HARNESS_GHCLI_SOURCE_OUTPUT")
	if output == "" {
		t.Skip("set HARNESS_GHCLI_SOURCE_OUTPUT for isolated source-proof Docker checks")
	}
	output, err := gate.Canonical(output)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(output, 0755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	docker, err := execution.NewDocker(ctx, execution.DockerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := PrepareBackend(ctx, filepath.Join(output, "dependencies"), docker)
	if err != nil {
		t.Fatal(err)
	}
	fixtures := baselineFixture(t)
	material, err := materialFiles()
	if err != nil {
		t.Fatal(err)
	}
	toolSHA := gate.TreeHash(material)
	for _, tc := range []struct{ name, setup, parent, extra, status string }{
		{"direct", "", "request.Context()", "", "pass"},
		{"alias-fallback", "parent:=request.Context();if parent==nil{parent=context.Background()}", "parent", "", "pass"},
		{"detached", "parent:=context.Background()", "parent", "", "fail"},
		{"helper", "parent:=identity(request.Context())", "parent", "\nfunc identity(c context.Context)context.Context{return c}\n", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(output, tc.name)
			layout := filepath.Join(dir, "input")
			files := make(map[string][]byte, len(fixtures))
			for path, data := range fixtures {
				files[path] = data
			}
			key := "ghcli/" + boundaryFile
			data := bytes.Replace(files[key], []byte(`"example.com/rpcx"`), []byte(`"example.com/rpcx/v2"`), 1)
			data = bytes.Replace(data, []byte(`"sync/atomic"`), []byte("\"sync/atomic\"\n\"time\""), 1)
			data = bytes.Replace(data, []byte("Call(string, any, any)"), []byte("Call(context.Context, string, any, any)"), 1)
			data = bytes.Replace(data, []byte("rpcx.NewClient(address)"), []byte("rpcx.Dial(address)"), 1)
			before := []byte(`err = caller.Call("http.do",`)
			if bytes.Count(data, before) != 1 {
				t.Fatal("unexpected baseline call")
			}
			after := []byte(tc.setup + ";\ncallCtx,cancel:=context.WithTimeout(" + tc.parent + ",30*time.Second);defer cancel();\nerr = caller.Call(callCtx,\"http.do\",")
			data = bytes.Replace(data, before, after, 1)
			files[key] = append(data, []byte(tc.extra)...)
			files["ghcli/go.mod"] = migratedMod(files["ghcli/go.mod"])
			if err := gate.CopyFiles(layout, files); err != nil {
				t.Fatal(err)
			}
			candidate := gate.Module(files, "ghcli")
			root := filepath.Join(layout, "ghcli")
			e := evaluation{req: checks.EvaluationRequest{EvidenceDir: filepath.Join(dir, "evidence"), CacheDir: filepath.Join(dir, "cache")}, backend: backend,
				result: checks.Evaluation{SpecSHA256: SpecSHA256(), CandidateSHA256: gate.TreeHash(candidate), MaterialsSHA256: toolSHA}}
			typed, err := e.inspect(ctx, root, candidate, fixtures)
			if err := gate.WriteJSON(filepath.Join(dir, "inspection.json"), e.result); err != nil {
				t.Fatal(err)
			}
			if err != nil {
				t.Fatal(err)
			}
			if typed.Status != "complete" || len(typed.Calls) != 1 || typed.Calls[0].Status != tc.status {
				t.Fatalf("unexpected analysis: %+v", typed)
			}
			if err := gate.WriteJSON(filepath.Join(dir, "typed-analysis.json"), typed); err != nil {
				t.Fatal(err)
			}
			wire, err := os.ReadFile(filepath.Join(e.req.EvidenceDir, "boundary-proof.json"))
			if err != nil {
				t.Fatal(err)
			}
			var proof sourceEnvelope
			// 再走严格消费端，确认保存证据和实际 stdout 的身份一致。
			proof, err = decodeSourceEnvelope(wire, candidate, typed.InputSHA256, toolSHA, gate.Module(fixtures, "ghcli"))
			if err != nil || len(proof.Proofs) != 1 || proof.Proofs[0].Status != tc.status {
				t.Fatalf("unexpected proof: %+v / %v", proof, err)
			}
		})
	}
}
