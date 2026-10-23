package round

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

func TestModelContractKeepsLegacyProtocolIdentity(t *testing.T) {
	// 读取已提交的公开计划，核对历史设置算法和缺省 JSON 形状没有变化。
	root := "../../../../docs/experiments/ghcli-dev-eval-v0.1.0"
	var plan campaign.Plan
	var enrollment Enrollment
	readJSON(t, filepath.Join(root, "expected-plan.json"), &plan)
	readJSON(t, filepath.Join(root, "enrollment.json"), &enrollment)
	hash, err := enrollment.Protocol.SettingsSHA256(plan)
	if err != nil || hash != enrollment.Protocol.PlanSettingsSHA256 {
		t.Fatalf("legacy settings identity changed: %s %v", hash, err)
	}
	raw, err := json.Marshal(enrollment.Protocol)
	if err != nil || strings.Contains(string(raw), "capability_binding") {
		t.Fatalf("legacy serialization changed: %s %v", raw, err)
	}
	plan.Profile.SourceSHA256 = strings.Repeat("c", 64)
	if err := enrollment.Protocol.validatePlan(plan); err == nil {
		t.Fatal("legacy protocol accepted a different response")
	}
}

func TestModelContractBindsAllRemainingSettings(t *testing.T) {
	changes := map[string]func(*campaign.Plan){
		"response":      func(p *campaign.Plan) { p.Profile.SourceSHA256 = strings.Repeat("c", 64) },
		"runtime":       func(p *campaign.Plan) { p.ID = "new-id"; p.Profile.VerifiedAt = p.Profile.VerifiedAt.Add(time.Minute) },
		"provider":      func(p *campaign.Plan) { p.Profile.Provider = "changed" },
		"endpoint":      func(p *campaign.Plan) { p.Profile.Endpoint = "changed" },
		"model":         func(p *campaign.Plan) { p.Profile.Model = "changed" },
		"context-up":    func(p *campaign.Plan) { p.Profile.ContextTokens++ },
		"context-down":  func(p *campaign.Plan) { p.Profile.ContextTokens-- },
		"output":        func(p *campaign.Plan) { p.Profile.MaxOutputTokens++ },
		"budget":        func(p *campaign.Plan) { p.Budget.MaxTokens++ },
		"calls":         func(p *campaign.Plan) { p.Budget.MaxCalls++ },
		"target-budget": func(p *campaign.Plan) { p.Targets[0].Budget.MaxTokens++ },
		"target-calls":  func(p *campaign.Plan) { p.Targets[0].Budget.MaxCalls++ },
		"model-limit":   func(p *campaign.Plan) { p.Targets[0].Limits.MaxModelCalls++ },
		"tool-limit":    func(p *campaign.Plan) { p.Targets[0].Limits.MaxToolCalls++ },
		"timeout":       func(p *campaign.Plan) { p.Targets[0].Timeout += time.Second },
		"target-slots":  func(p *campaign.Plan) { p.Concurrency++ },
		"command-slots": func(p *campaign.Plan) { p.ExecutionConcurrency++ },
		"input":         func(p *campaign.Plan) { p.Targets[0].Input.Repository.Directory = "/changed-input" },
		"resources": func(p *campaign.Plan) {
			p.Resources = &execution.ResourcePolicy{Protocol: execution.ResourceProtocol, Context: "test", EngineID: "changed-engine"}
		},
		"recovery":       func(p *campaign.Plan) { p.Recovery = &campaign.RecoveryPolicy{MaxAttempts: 2} },
		"invalid-source": func(p *campaign.Plan) { p.Profile.SourceSHA256 = "" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			e, plan := enrollmentFixture(t)
			e.SchemaVersion = 2
			e.Protocol = testProtocol(t, e.Targets, plan)
			e.Protocol.CapabilityBinding = ModelContractBinding
			hash, err := e.Protocol.SettingsSHA256(plan)
			if err != nil {
				t.Fatal(err)
			}
			e.Protocol.PlanSettingsSHA256 = hash
			before, _ := json.Marshal(plan)
			change(&plan)
			raw, _ := json.Marshal(plan)
			manifest, err := e.Bind(raw)
			if name == "response" || name == "runtime" {
				if err != nil || manifest.PlanSHA256 != gate.Digest(raw) || manifest.PlanSHA256 == gate.Digest(before) {
					t.Fatalf("actual plan identity was not retained: %+v %v", manifest, err)
				}
			} else if err == nil {
				t.Fatal("changed critical settings or invalid source accepted")
			}
		})
	}
	protocol := &Protocol{CapabilityBinding: "unknown"}
	if _, err := protocol.SettingsSHA256(campaign.Plan{}); err == nil {
		t.Fatal("unknown binding accepted")
	}
}

// 仅构造宿主发布记录，不执行模型或仓库；原文内容不冒称真实供应商声明。
func modelContractRound(t *testing.T) (Request, campaign.Plan) {
	t.Helper()
	req, manifest := protocolRound(t, Formal)
	root := filepath.Dir(req.CampaignDir)
	response := []byte(`{"object":"list","data":[],"synthetic_mechanism_only":true}`)
	if err := os.WriteFile(filepath.Join(root, "models.json"), response, 0600); err != nil {
		t.Fatal(err)
	}
	var plan campaign.Plan
	readJSON(t, filepath.Join(req.CampaignDir, "plan.json"), &plan)
	plan.Profile.SourceSHA256 = gate.Digest(response)
	manifest.PlanSHA256 = write(t, filepath.Join(req.CampaignDir, "plan.json"), plan)
	var summary campaign.Summary
	readJSON(t, filepath.Join(req.CampaignDir, "summary.json"), &summary)
	summary.PlanSHA256, summary.Budget.Profile = manifest.PlanSHA256, plan.Profile
	write(t, filepath.Join(req.CampaignDir, "budget/budget.json"), summary.Budget)
	publishSummary(t, req, summary, manifest.PlanSHA256)
	manifest.Protocol.CapabilityBinding = ModelContractBinding
	var err error
	manifest.Protocol.PlanSettingsSHA256, err = manifest.Protocol.SettingsSHA256(plan)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "capabilities.json"), struct {
		Profile budget.Profile `json:"profile"`
	}{plan.Profile})
	write(t, filepath.Join(root, "enrollment.json"), Enrollment{SchemaVersion: 2, ID: manifest.ID, Kind: manifest.Kind, Targets: manifest.Targets, Protocol: manifest.Protocol})
	write(t, filepath.Join(root, "round-manifest.json"), manifest)
	write(t, req.ManifestPath, manifest)
	return req, plan
}

func TestModelContractRequiresCurrentResponseEvidence(t *testing.T) {
	for _, mode := range []string{"valid", "missing-response", "changed-response", "missing-capabilities", "changed-profile"} {
		t.Run(mode, func(t *testing.T) {
			req, plan := modelContractRound(t)
			root := filepath.Dir(req.CampaignDir)
			switch mode {
			case "missing-response", "missing-capabilities":
				name := "models.json"
				if mode == "missing-capabilities" {
					name = "capabilities.json"
				}
				if err := os.Remove(filepath.Join(root, name)); err != nil {
					t.Fatal(err)
				}
			case "changed-response":
				if err := os.WriteFile(filepath.Join(root, "models.json"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "changed-profile":
				plan.Profile.SourceSHA256 = strings.Repeat("f", 64)
				write(t, filepath.Join(root, "capabilities.json"), struct {
					Profile budget.Profile `json:"profile"`
				}{plan.Profile})
			}
			out, err := Summarize(context.Background(), req)
			if mode != "valid" {
				if err == nil || out.Valid || out.Metrics != nil {
					t.Fatalf("unbound response evidence accepted: valid=%t err=%v", out.Valid, err)
				}
				return
			}
			if err != nil || !out.Valid || !out.InputsUnchanged {
				t.Fatalf("current response evidence rejected: valid=%t err=%v", out.Valid, err)
			}
			for _, name := range []string{"models.json", "capabilities.json"} {
				path, err := gate.Canonical(filepath.Join(root, name))
				if err != nil {
					t.Fatal(err)
				}
				source, ok := out.Sources[path]
				if !ok || source.Missing || source.SHA256 == "" || source.Copy == "" {
					t.Fatalf("response evidence not preserved: %s %+v", name, source)
				}
			}
		})
	}
}
