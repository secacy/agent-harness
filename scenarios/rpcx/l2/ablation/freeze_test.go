package ablation

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/deepseek"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

func freezeTestGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git fixture: %v %s", err, out)
	}
}
func freezeTestSource(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	freezeTestGit(t, root, "init", "--quiet")
	for name, contents := range map[string]string{"go.mod": "module example.com/freeze-fixture\n\ngo 1.23.0\n", "internal/runtime.go": "package runtime\n", "docs/README.md": "fixture documentation\n", ".gitignore": "runs/\n"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	freezeTestGit(t, root, "add", "--all")
	freezeTestGit(t, root, "-c", "user.name=Freeze Test", "-c", "user.email=freeze-test@example.invalid", "commit", "--quiet", "-m", "freeze test source fixture")
	return root
}
func freezeTestRequest(t *testing.T) FreezeRequest {
	t.Helper()
	p := runnableProtocol(t)
	raw := []byte(`{"object":"list","data":[{"id":"frozen-model","context_window":90,"max_output_tokens":20,"input_modalities":["text"],"output_modalities":["text"]}]}`)
	p.Model = budget.Profile{Provider: "deepseek", Endpoint: "https://provider.example/chat/completions", Model: "frozen-model", ContextTokens: 90, MaxOutputTokens: 10, SourceSHA256: gate.Digest(raw), VerifiedAt: time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)}
	p.Price = &Price{Currency: "USD", Source: "https://provider.example/pricing", RecordedAt: "2026-10-31T00:00:00Z", InputPerMillion: 1, CachedInputPerMillion: .1, OutputPerMillion: 3}
	binary := filepath.Join(t.TempDir(), "fixture-executable")
	// A checksum fixture, never executed and never represented as a real harness.
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return FreezeRequest{ProjectDir: freezeTestSource(t), BinaryPath: binary, Protocol: p, ModelsJSON: raw, Capabilities: deepseek.Capabilities{URL: "https://provider.example/models", Profile: p.Model, ModelOutputLimit: 20, Response: raw}}
}
func freezeTestReviews(p Protocol) []TaskReview {
	var reviews []TaskReview
	// These records exist only in unit-test memory; no person or real experiment
	// is claimed to have been reviewed or authorized by these fixtures.
	for _, task := range p.Tasks {
		reviews = append(reviews, TaskReview{Task: task, Reviewer: "unit test fixture", Relationship: "test data only", RecordedAt: "2026-10-31T00:00:00Z", Decision: "accepted", Evidence: []string{"unit-test://review-fixture"}, Reason: "exercise validation of structural fields"})
	}
	return reviews
}
func freezeTestAuthorization(p Protocol, m FreezeManifest) SpendingAuthorization {
	amount, _ := p.Price.ConservativeAmount(p.TotalBudget.MaxTokens)
	return SpendingAuthorization{SchemaVersion: 1, Decision: "authorized", FreezeSHA256: m.FreezeSHA256, ProtocolSHA256: m.ProtocolSHA256, Model: p.Model, MaxCalls: p.TotalBudget.MaxCalls, MaxTokens: p.TotalBudget.MaxTokens, Currency: p.Price.Currency, MaxAmount: &amount, Authorizer: "unit test fixture", RecordedAt: "2026-10-31T00:00:00Z", Evidence: []string{"unit-test://authorization-fixture"}, Reason: "exercise structural authorization checks only"}
}

func TestP1FreezeBindsActualDefinitionsSourcesBinaryAndLaunch(t *testing.T) {
	req := freezeTestRequest(t)
	output := filepath.Join(t.TempDir(), "freeze")
	manifest, err := Freeze(context.Background(), output, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Materials) != 5 || !digestOK(manifest.FreezeSHA256) || !digestOK(manifest.BinarySHA256) || !gitOIDOK(manifest.GitHEAD) {
		t.Fatalf("missing identities %+v", manifest)
	}
	checked, err := CheckFreeze(context.Background(), output, req.ProjectDir, req.BinaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(manifest, checked) {
		t.Fatal("freeze changed after round trip")
	}
	reviews := freezeTestReviews(req.Protocol)
	authorization := freezeTestAuthorization(req.Protocol, manifest)
	if _, err = CheckLaunch(context.Background(), output, req.ProjectDir, req.BinaryPath, reviews, authorization); err != nil {
		t.Fatal(err)
	}
	raw, err := readLocal(output, "generation-definitions.json")
	if err != nil {
		t.Fatal(err)
	}
	var definitions map[string]any
	if err = Decode(raw, &definitions); err != nil || len(definitions) != 12 {
		t.Fatal("freeze lacks twelve actual policies", err)
	}
	// Human-review/documentation work can continue after runtime source freezing.
	if err = os.WriteFile(filepath.Join(req.ProjectDir, "docs/README.md"), []byte("updated notes\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = CheckFreeze(context.Background(), output, req.ProjectDir, req.BinaryPath); err != nil {
		t.Fatal("documentation edit blocked launch", err)
	}
	if _, err = Freeze(context.Background(), output, req); err == nil {
		t.Fatal("reused freeze directory")
	}
	raw, err = os.ReadFile(req.BinaryPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(req.BinaryPath, append(raw, '\n'), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err = CheckFreeze(context.Background(), output, req.ProjectDir, req.BinaryPath); err == nil {
		t.Fatal("changed executable passed freeze")
	}
}

func TestP1FreezeSourceScopeRejectsRuntimeDriftAndNewMembers(t *testing.T) {
	root := freezeTestSource(t)
	head, initial, err := sourceInventory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(initial) != 2 || !gitOIDOK(head) {
		t.Fatalf("unexpected scope %v", initial)
	}
	path := filepath.Join(root, "internal/runtime.go")
	if err = os.WriteFile(path, []byte("package changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = sourceInventory(context.Background(), root); err == nil {
		t.Fatal("uncommitted source passed freeze")
	}
	freezeTestGit(t, root, "checkout", "--", "internal/runtime.go")
	added := filepath.Join(root, "internal/new.go")
	if err = os.WriteFile(added, []byte("package runtime\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = sourceInventory(context.Background(), root); err == nil {
		t.Fatal("untracked source passed freeze")
	}
	freezeTestGit(t, root, "add", "internal/new.go")
	freezeTestGit(t, root, "-c", "user.name=Freeze Test", "-c", "user.email=freeze-test@example.invalid", "commit", "--quiet", "-m", "add fixture runtime source")
	newHead, current, err := sourceInventory(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if newHead == head || reflect.DeepEqual(initial, current) || len(current) != 3 {
		t.Fatal("source membership change was invisible")
	}
}

func TestP1FreezeRejectsRawCapabilitiesAndPublishedPinDrift(t *testing.T) {
	req := freezeTestRequest(t)
	bad := req
	bad.ModelsJSON = append(append([]byte(nil), req.ModelsJSON...), '\n')
	if _, err := Freeze(context.Background(), filepath.Join(t.TempDir(), "freeze"), bad); err == nil {
		t.Fatal("raw capabilities mismatch accepted")
	}
	bad = req
	bad.Capabilities.URL = "https://other.example/models"
	if _, err := Freeze(context.Background(), filepath.Join(t.TempDir(), "freeze"), bad); err == nil {
		t.Fatal("capability source mismatch accepted")
	}
	bad = req
	bad.Protocol.Tasks = append([]Task(nil), req.Protocol.Tasks...)
	bad.Protocol.Tasks[0].ReferenceSHA256 = strings.Repeat("0", 64)
	if _, err := Freeze(context.Background(), filepath.Join(t.TempDir(), "freeze"), bad); err == nil {
		t.Fatal("unpublished reference identity accepted")
	}
}

func TestP1TaskReviewAndSpendingClaimsCannotDefaultToAccepted(t *testing.T) {
	req := freezeTestRequest(t)
	p := req.Protocol
	protocolRaw, _ := canonicalJSON(p)
	manifest := FreezeManifest{FreezeSHA256: strings.Repeat("a", 64), ProtocolSHA256: gate.Digest(protocolRaw), Model: p.Model}
	reviews := freezeTestReviews(p)
	if err := ValidateTaskReviews(p, reviews); err != nil {
		t.Fatal(err)
	}
	reviews[0].Decision = "pending"
	if err := ValidateTaskReviews(p, reviews); err == nil {
		t.Fatal("pending review authorized launch")
	}
	reviews = freezeTestReviews(p)
	reviews[0].Relationship = ""
	if err := ValidateTaskReviews(p, reviews); err == nil {
		t.Fatal("unattributed review authorized launch")
	}
	reviews = freezeTestReviews(p)
	reviews[0].Task.ReferenceToolSHA256 = strings.Repeat("0", 64)
	if err := ValidateTaskReviews(p, reviews); err == nil {
		t.Fatal("review for different reference authorized launch")
	}
	authorization := freezeTestAuthorization(p, manifest)
	if err := ValidateSpendingAuthorization(p, manifest, authorization); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSpendingAuthorization(p, manifest, SpendingAuthorization{}); err == nil {
		t.Fatal("empty authorization accepted")
	}
	authorization.MaxTokens--
	if err := ValidateSpendingAuthorization(p, manifest, authorization); err == nil {
		t.Fatal("token ceiling exceeded")
	}
	authorization = freezeTestAuthorization(p, manifest)
	amount := *authorization.MaxAmount - .01
	authorization.MaxAmount = &amount
	if err := ValidateSpendingAuthorization(p, manifest, authorization); err == nil {
		t.Fatal("monetary ceiling exceeded")
	}
	authorization = freezeTestAuthorization(p, manifest)
	authorization.FreezeSHA256 = strings.Repeat("b", 64)
	if err := ValidateSpendingAuthorization(p, manifest, authorization); err == nil {
		t.Fatal("authorization for another freeze accepted")
	}
	p.Price = nil
	protocolRaw, _ = canonicalJSON(p)
	manifest.ProtocolSHA256 = gate.Digest(protocolRaw)
	authorization.ProtocolSHA256 = manifest.ProtocolSHA256
	authorization.FreezeSHA256 = manifest.FreezeSHA256
	if err := ValidateSpendingAuthorization(p, manifest, authorization); err == nil {
		t.Fatal("missing price supported monetary promise")
	}
	authorization.Currency = ""
	authorization.MaxAmount = nil
	authorization.AllowUnknownAmount = true
	if err := ValidateSpendingAuthorization(p, manifest, authorization); err != nil {
		t.Fatal("explicit token-only authorization rejected", err)
	}
}
