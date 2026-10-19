package round

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/task"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
)

func enrollmentFixture(t *testing.T) (Enrollment, campaign.Plan) {
	t.Helper()
	e := Enrollment{SchemaVersion: 1, ID: "before-generation", Kind: Calibration}
	p := campaign.Plan{ID: "future-campaign", Profile: budget.Profile{Provider: "controlled", Endpoint: "in-process", Model: "fixed",
		ContextTokens: 90, MaxOutputTokens: 10, SourceSHA256: strings.Repeat("a", 64), VerifiedAt: time.Now().UTC()}}
	for _, def := range []struct{ id, profile, config, version string }{
		{"goddd", "rpcx-goddd-t3-v1", "default", "rpcx-l2-reference-goddd-data-v2"},
		{"translation", "rpcx-translation-t3-v1", "all-required", "rpcx-l2-reference-translation-data-v2"},
		{"workouts", "rpcx-workouts-t3-v1", "default", "rpcx-l2-reference-workouts-data-v1"},
	} {
		identity, err := evaluation.PublishedIdentity(def.version)
		if err != nil {
			t.Fatal(err)
		}
		input := task.Target{ID: def.id, ProfileID: def.profile, BuildConfig: def.config,
			Repository: task.Snapshot{SHA256: strings.Repeat("b", 64)}, Specification: task.Specification{SHA256: identity.SpecSHA256}}
		p.Targets = append(p.Targets, campaign.Target{Input: input})
		e.Targets = append(e.Targets, TargetPin{ID: input.ID, ProfileID: input.ProfileID, BuildConfig: input.BuildConfig,
			BaselineSHA256: input.Repository.SHA256, SpecSHA256: identity.SpecSHA256, ReferenceVersion: identity.Version,
			ReferenceSHA256: identity.ReferenceSHA256, ToolSHA256: identity.ToolSHA256})
	}
	return e, p
}

func TestEnrollmentBindsActualPlanBytes(t *testing.T) {
	e, p := enrollmentFixture(t)
	for _, indent := range []string{"", "    "} {
		raw, err := json.MarshalIndent(p, "", indent)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, '\n')
		manifest, err := e.Bind(raw)
		if err != nil || manifest.PlanSHA256 != gate.Digest(raw) || manifest.CampaignID != p.ID || len(manifest.Targets) != 3 {
			t.Fatalf("manifest not bound to exact plan bytes: %+v %v", manifest, err)
		}
		manifest.Targets[0].ID = "modified"
		if e.Targets[0].ID != "goddd" {
			t.Fatal("bound manifest shares mutable pins")
		}
	}
	// 时间变化产生另一份真实计划身份，无需伪造提前冻结的摘要。
	first, _ := json.Marshal(p)
	p.Profile.VerifiedAt = p.Profile.VerifiedAt.Add(time.Second)
	second, _ := json.Marshal(p)
	a, _ := e.Bind(first)
	b, _ := e.Bind(second)
	if a.PlanSHA256 == b.PlanSHA256 {
		t.Fatal("dynamic plan identity ignored")
	}
}

func TestEnrollmentRejectsDriftAndInvalidMembership(t *testing.T) {
	cases := map[string]func(*Enrollment){
		"duplicate": func(e *Enrollment) { e.Targets[1] = e.Targets[0] },
		"missing":   func(e *Enrollment) { e.Targets = e.Targets[:2] },
		"extra":     func(e *Enrollment) { e.Targets = append(e.Targets, e.Targets[0]) },
		"reorder":   func(e *Enrollment) { e.Targets[0], e.Targets[1] = e.Targets[1], e.Targets[0] },
		"baseline":  func(e *Enrollment) { e.Targets[0].BaselineSHA256 = strings.Repeat("c", 64) },
		"spec":      func(e *Enrollment) { e.Targets[0].SpecSHA256 = strings.Repeat("c", 64) },
		"data":      func(e *Enrollment) { e.Targets[0].ReferenceSHA256 = strings.Repeat("c", 64) },
		"tool":      func(e *Enrollment) { e.Targets[0].ToolSHA256 = strings.Repeat("c", 64) },
		"version":   func(e *Enrollment) { e.Targets[0].ReferenceVersion = "unpublished" },
		"kind":      func(e *Enrollment) { e.Kind = "unknown" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			e, p := enrollmentFixture(t)
			change(&e)
			if err := e.Validate(p.Targets); err == nil {
				t.Fatal("invalid enrollment accepted")
			}
		})
	}
	e, p := enrollmentFixture(t)
	e.Kind = Development
	raw, _ := json.Marshal(p)
	if _, err := e.Bind(raw); err == nil {
		t.Fatal("controlled model relabeled as real development")
	}
}
