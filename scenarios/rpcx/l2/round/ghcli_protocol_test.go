package round

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/task"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
)

// 仅取参考发布元数据；输入及 provider 为合成记录，不执行业务或模型。
func fourTargetProtocolFixture(t *testing.T) (Enrollment, campaign.Plan) {
	t.Helper()
	e, plan := enrollmentFixture(t)
	identity, err := evaluation.PublishedIdentity(evaluation.GHCLIDataVersion)
	if err != nil {
		t.Fatal(err)
	}
	input := task.Target{ID: "ghcli", ProfileID: "rpcx-ghcli-t3-v1", BuildConfig: "default",
		Repository: task.Snapshot{SHA256: strings.Repeat("b", 64)}, Specification: task.Specification{SHA256: identity.SpecSHA256}}
	plan.Targets = append(plan.Targets, campaign.Target{Input: input})
	e.Targets = append(e.Targets, TargetPin{ID: input.ID, ProfileID: input.ProfileID, BuildConfig: input.BuildConfig,
		BaselineSHA256: input.Repository.SHA256, SpecSHA256: input.Specification.SHA256, ReferenceVersion: identity.Version,
		ReferenceSHA256: identity.ReferenceSHA256, ToolSHA256: identity.ToolSHA256})
	plan.Profile.Provider = "synthetic-provider-for-mechanism-only"
	e.SchemaVersion, e.Kind = 2, Formal
	e.Protocol = testProtocol(t, e.Targets, plan)
	return e, plan
}

func TestGHCLIProtocolBindsFourTargetsWithPublishedReferences(t *testing.T) {
	for _, kind := range []string{Calibration, Development, Formal} {
		t.Run(kind, func(t *testing.T) {
			e, plan := fourTargetProtocolFixture(t)
			e.Kind = kind
			raw, err := json.MarshalIndent(plan, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			raw = append(raw, '\n')
			manifest, err := e.Bind(raw)
			if err != nil {
				t.Fatal(err)
			}
			if len(manifest.Targets) != 4 || !reflect.DeepEqual(manifest.Targets, e.Targets) ||
				manifest.PlanSHA256 != gate.Digest(raw) || manifest.SchemaVersion != 2 || manifest.Kind != kind ||
				!reflect.DeepEqual(manifest.Protocol, e.Protocol) {
				t.Fatalf("four-target protocol binding changed: %+v", manifest)
			}
			manifest.Targets[3].ReferenceVersion = "changed"
			manifest.Protocol.Reviewers["ghcli"] = "changed"
			if e.Targets[3].ReferenceVersion != evaluation.GHCLIDataVersion || e.Protocol.Reviewers["ghcli"] != "synthetic-test-only" {
				t.Fatal("bound manifest shares mutable enrollment state")
			}
		})
	}
}

func TestGHCLIProtocolRejectsChangedEnrollmentAndPlan(t *testing.T) {
	for name, mutate := range map[string]func(*Enrollment, *campaign.Plan){
		"heldout":          func(e *Enrollment, _ *campaign.Plan) { e.Protocol.Split = "heldout" },
		"extra-attempt":    func(e *Enrollment, _ *campaign.Plan) { e.Protocol.AttemptsPerTarget = 2 },
		"missing-contract": func(e *Enrollment, _ *campaign.Plan) { e.Protocol = nil },
		"unknown-profile":  func(e *Enrollment, _ *campaign.Plan) { e.Targets[3].ProfileID = "rpcx-unpublished" },
		"wrong-profile":    func(e *Enrollment, _ *campaign.Plan) { e.Targets[3].ProfileID = "rpcx-goddd-t3-v1" },
		"baseline":         func(e *Enrollment, _ *campaign.Plan) { e.Targets[3].BaselineSHA256 = strings.Repeat("c", 64) },
		"spec":             func(e *Enrollment, _ *campaign.Plan) { e.Targets[3].SpecSHA256 = strings.Repeat("c", 64) },
		"old-reference": func(e *Enrollment, _ *campaign.Plan) {
			e.Targets[3].ReferenceVersion = "rpcx-l2-reference-ghcli-data-v1"
		},
		"reference-data":   func(e *Enrollment, _ *campaign.Plan) { e.Targets[3].ReferenceSHA256 = strings.Repeat("c", 64) },
		"reference-tool":   func(e *Enrollment, _ *campaign.Plan) { e.Targets[3].ToolSHA256 = strings.Repeat("c", 64) },
		"missing-reviewer": func(e *Enrollment, _ *campaign.Plan) { delete(e.Protocol.Reviewers, "ghcli") },
		"empty-reviewer":   func(e *Enrollment, _ *campaign.Plan) { e.Protocol.Reviewers["ghcli"] = " " },
		"missing-material": func(e *Enrollment, _ *campaign.Plan) { delete(e.Protocol.Materials, "dataset") },
		"ordering":         func(_ *Enrollment, p *campaign.Plan) { p.Targets[0], p.Targets[3] = p.Targets[3], p.Targets[0] },
		"plan-input":       func(_ *Enrollment, p *campaign.Plan) { p.Targets[3].Input.Repository.SHA256 = strings.Repeat("c", 64) },
		"budget":           func(_ *Enrollment, p *campaign.Plan) { p.Targets[3].Budget.MaxTokens++ },
	} {
		t.Run(name, func(t *testing.T) {
			e, plan := fourTargetProtocolFixture(t)
			mutate(&e, &plan)
			raw, err := json.Marshal(plan)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.Bind(raw); err == nil {
				t.Fatal("changed GHCLI enrollment or plan accepted")
			}
		})
	}
}

func TestGHCLIProtocolCannotRelabelControlledModelsOrEnableRecovery(t *testing.T) {
	for _, mode := range []string{"controlled-development", "controlled-formal", "recovery"} {
		t.Run(mode, func(t *testing.T) {
			e, plan := fourTargetProtocolFixture(t)
			want := "controlled model enrollment must remain development_calibration"
			if mode == "recovery" {
				plan.Recovery = &campaign.RecoveryPolicy{MaxAttempts: 2}
				want = "single-attempt evaluation does not allow a recovery policy"
			} else {
				plan.Profile.Provider = "controlled"
				if mode == "controlled-development" {
					e.Kind = Development
				}
			}
			// 同步设置摘要，避免仅因摘要漂移而误判约束已经生效。
			var err error
			e.Protocol.PlanSettingsSHA256, err = PlanSettingsSHA256(plan)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(plan)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.Bind(raw); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("expected policy rejection %q, got %v", want, err)
			}
		})
	}
}

func TestGHCLIProtocolSummaryPreservesUnknownAndRejectsRebinding(t *testing.T) {
	for _, mode := range []string{"valid", "formal", "material", "execution", "reviewer"} {
		t.Run(mode, func(t *testing.T) {
			req, manifest := ghcliRoundFixture(t)
			var plan campaign.Plan
			readJSON(t, filepath.Join(req.CampaignDir, "plan.json"), &plan)
			kind := Calibration
			if mode == "formal" {
				// 仅用合成 provider 检验正式格式，不模拟真实供应商的迁移效果。
				kind = Formal
				plan.Profile.Provider = "synthetic-provider-for-mechanism-only"
				manifest.PlanSHA256 = write(t, filepath.Join(req.CampaignDir, "plan.json"), plan)
				var summary campaign.Summary
				readJSON(t, filepath.Join(req.CampaignDir, "summary.json"), &summary)
				summary.PlanSHA256, summary.Budget.Profile = manifest.PlanSHA256, plan.Profile
				write(t, filepath.Join(req.CampaignDir, "budget/budget.json"), summary.Budget)
				publishSummary(t, req, summary, manifest.PlanSHA256)
			}
			publishProtocolRound(t, req, &manifest, plan, kind)
			root := filepath.Dir(req.CampaignDir)
			switch mode {
			case "valid", "formal":
				// 只读已发布副本；原材料消失不应阻碍离线重算。
				for _, material := range manifest.Protocol.Materials {
					if err := os.Remove(material.Path); err != nil {
						t.Fatal(err)
					}
				}
			case "material":
				if err := os.WriteFile(filepath.Join(root, "protocol-materials/dataset"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "execution":
				write(t, filepath.Join(root, "execution.json"), execution.Info{Kind: execution.KindDocker})
			case "reviewer":
				manifest.Protocol.Reviewers["one"] = "changed"
				write(t, req.ManifestPath, manifest)
			}
			out, err := Summarize(context.Background(), req)
			if mode == "valid" || mode == "formal" {
				if err != nil || !out.Valid || !out.InputsUnchanged || out.SchemaVersion != 2 ||
					out.Kind != kind || out.Counts.Targets != 3 || out.Counts.Success != 0 || out.Counts.UnknownReleases != 1 || out.Metrics.Success.Denominator != 3 {
					t.Fatalf("missing GHCLI references did not remain unknown: counts=%+v err=%v", out.Counts, err)
				}
			} else if err == nil || out.Valid || out.Metrics != nil {
				t.Fatalf("changed GHCLI protocol evidence accepted: valid=%t err=%v", out.Valid, err)
			}
		})
	}
}
