package round

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/writer"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
)

func testProtocol(t *testing.T, targets []TargetPin, plan campaign.Plan) *Protocol {
	t.Helper()
	hash, err := PlanSettingsSHA256(plan)
	if err != nil {
		t.Fatal(err)
	}
	p := &Protocol{ID: "mechanism-only-v1", Split: DevelopmentSplit, AttemptsPerTarget: 1,
		PlanSettingsSHA256: hash, Execution: execution.Info{Kind: execution.KindLocal}, Reviewers: map[string]string{}, Materials: map[string]Artifact{}}
	for _, target := range targets {
		p.Reviewers[target.ID] = "synthetic-test-only"
	}
	for _, name := range protocolMaterialNames {
		path := filepath.Join(t.TempDir(), name)
		raw := []byte("synthetic mechanism material, not a frozen M5 protocol: " + name + "\n")
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		p.Materials[name] = Artifact{Path: path, SHA256: gate.Digest(raw)}
	}
	return p
}

func TestProtocolBindsSettingsAndCopiesContract(t *testing.T) {
	e, p := enrollmentFixture(t)
	e.SchemaVersion = 2
	e.Protocol = testProtocol(t, e.Targets, p)
	raw, _ := json.Marshal(p)
	m, err := e.Bind(raw)
	if err != nil || m.SchemaVersion != 2 || !reflect.DeepEqual(m.Protocol, e.Protocol) {
		t.Fatalf("v2 binding failed: %+v %v", m, err)
	}
	m.Protocol.Reviewers["goddd"] = "changed"
	delete(m.Protocol.Materials, "protocol")
	if e.Protocol.Reviewers["goddd"] != "synthetic-test-only" || len(e.Protocol.Materials) != 4 {
		t.Fatal("bound manifest shares mutable protocol maps")
	}
	first, _ := PlanSettingsSHA256(p)
	p.ID = "another-runtime-id"
	p.Profile.VerifiedAt = p.Profile.VerifiedAt.Add(time.Minute)
	second, _ := PlanSettingsSHA256(p)
	if first != second {
		t.Fatal("runtime identity or query time entered frozen settings")
	}
	e.Kind = Formal
	if _, err := e.Bind(raw); err == nil {
		t.Fatal("controlled model relabeled as formal")
	}
}

func TestProtocolRejectsIncompleteOrMisclassifiedEnrollment(t *testing.T) {
	for name, mutate := range map[string]func(*Enrollment){
		"old-schema":       func(e *Enrollment) { e.SchemaVersion = 1 },
		"no-contract":      func(e *Enrollment) { e.Protocol = nil },
		"unknown-kind":     func(e *Enrollment) { e.Kind = "unknown" },
		"unknown-schema":   func(e *Enrollment) { e.SchemaVersion = 3 },
		"heldout-relabel":  func(e *Enrollment) { e.Protocol.Split = "heldout" },
		"extra-attempt":    func(e *Enrollment) { e.Protocol.AttemptsPerTarget = 2 },
		"missing-settings": func(e *Enrollment) { e.Protocol.PlanSettingsSHA256 = "" },
		"missing-executor": func(e *Enrollment) { e.Protocol.Execution = execution.Info{} },
		"mutable-image": func(e *Enrollment) {
			e.Protocol.Execution = execution.Info{Kind: execution.KindDocker, Image: "golang:latest", CPUs: 2, MemoryBytes: 2 << 30, Pids: 128}
		},
		"missing-reviewer":   func(e *Enrollment) { delete(e.Protocol.Reviewers, "goddd") },
		"empty-reviewer":     func(e *Enrollment) { e.Protocol.Reviewers["goddd"] = " " },
		"extra-reviewer":     func(e *Enrollment) { e.Protocol.Reviewers["extra"] = "someone" },
		"unprepared-profile": func(e *Enrollment) { e.Targets[0].ProfileID = "rpcx-heldout-unprepared" },
		"missing-material":   func(e *Enrollment) { delete(e.Protocol.Materials, "dataset") },
		"extra-material":     func(e *Enrollment) { e.Protocol.Materials["unexpected"] = e.Protocol.Materials["dataset"] },
		"relative-material": func(e *Enrollment) {
			a := e.Protocol.Materials["dataset"]
			a.Path = "relative"
			e.Protocol.Materials["dataset"] = a
		},
	} {
		t.Run(name, func(t *testing.T) {
			e, p := enrollmentFixture(t)
			e.SchemaVersion = 2
			e.Protocol = testProtocol(t, e.Targets, p)
			mutate(&e)
			if err := e.Validate(p.Targets); err == nil {
				t.Fatal("invalid protocol accepted")
			}
		})
	}
	e, p := enrollmentFixture(t)
	e.Kind = Formal
	if err := e.Validate(p.Targets); err == nil {
		t.Fatal("old enrollment relabeled as formal")
	}
}

func TestProtocolDetectsActualPlanDrift(t *testing.T) {
	for name, mutate := range map[string]func(*campaign.Plan){
		"model":               func(p *campaign.Plan) { p.Profile.Model = "different" },
		"endpoint":            func(p *campaign.Plan) { p.Profile.Endpoint = "another-endpoint" },
		"capability-response": func(p *campaign.Plan) { p.Profile.SourceSHA256 = strings.Repeat("c", 64) },
		"context":             func(p *campaign.Plan) { p.Profile.ContextTokens++ },
		"output":              func(p *campaign.Plan) { p.Profile.MaxOutputTokens++ },
		"batch-budget":        func(p *campaign.Plan) { p.Budget.MaxTokens++ },
		"batch-calls":         func(p *campaign.Plan) { p.Budget.MaxCalls++ },
		"target-budget":       func(p *campaign.Plan) { p.Targets[0].Budget.MaxTokens++ },
		"agent-limit":         func(p *campaign.Plan) { p.Targets[0].Limits.MaxToolCalls++ },
		"timeout":             func(p *campaign.Plan) { p.Targets[0].Timeout += time.Second },
		"target-slots":        func(p *campaign.Plan) { p.Concurrency++ },
		"command-slots":       func(p *campaign.Plan) { p.ExecutionConcurrency++ },
		"repository-path":     func(p *campaign.Plan) { p.Targets[0].Input.Repository.Directory = "/changed-input" },
		"resources": func(p *campaign.Plan) {
			p.Resources = &execution.ResourcePolicy{Protocol: execution.ResourceProtocol, Context: "test", EngineID: "changed-engine"}
		},
		"recovery": func(p *campaign.Plan) { p.Recovery = &campaign.RecoveryPolicy{MaxAttempts: 2} },
	} {
		t.Run(name, func(t *testing.T) {
			e, p := enrollmentFixture(t)
			e.SchemaVersion = 2
			e.Protocol = testProtocol(t, e.Targets, p)
			mutate(&p)
			raw, _ := json.Marshal(p)
			if _, err := e.Bind(raw); err == nil {
				t.Fatal("changed plan accepted by frozen protocol")
			}
		})
	}
}

func TestProtocolMaterialSnapshots(t *testing.T) {
	for _, mode := range []string{"valid", "missing", "changed", "empty", "oversized", "directory", "overlap"} {
		t.Run(mode, func(t *testing.T) {
			e, p := enrollmentFixture(t)
			protocol := testProtocol(t, e.Targets, p)
			material := protocol.Materials["protocol"]
			output := filepath.Join(t.TempDir(), "new-batch")
			switch mode {
			case "missing":
				if err := os.Remove(material.Path); err != nil {
					t.Fatal(err)
				}
			case "changed", "empty", "oversized":
				data := []byte("changed")
				if mode == "empty" {
					data = nil
				} else if mode == "oversized" {
					data = make([]byte, (1<<20)+1)
				}
				if err := os.WriteFile(material.Path, data, 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				material.Path = t.TempDir()
				protocol.Materials["protocol"] = material
			case "overlap":
				output = filepath.Dir(material.Path)
			}
			files, err := ReadProtocolMaterials(protocol, output)
			if mode == "valid" {
				if err != nil || len(files) != 4 || gate.Digest(files["protocol"]) != material.SHA256 {
					t.Fatalf("snapshot failed: %v", err)
				}
			} else if err == nil {
				t.Fatal("invalid material snapshot accepted")
			}
		})
	}
}

// 此构造仅验证格式与关联；合成 provider/复核人不是实际模型或人员证据。
func protocolRound(t *testing.T, kind string) (Request, Manifest) {
	t.Helper()
	req, m := fakeRound(t)
	attachFakeReference(t, &req, &m, "one", evaluation.Pass, true)
	var plan campaign.Plan
	readJSON(t, filepath.Join(req.CampaignDir, "plan.json"), &plan)
	if kind == Formal {
		plan.Profile.Provider = "synthetic-provider-for-mechanism-only"
		var summary campaign.Summary
		readJSON(t, filepath.Join(req.CampaignDir, "summary.json"), &summary)
		summary.Budget.Profile = plan.Profile
		m.PlanSHA256 = write(t, filepath.Join(req.CampaignDir, "plan.json"), plan)
		summary.PlanSHA256 = m.PlanSHA256
		write(t, filepath.Join(req.CampaignDir, "budget/budget.json"), summary.Budget)
		publishSummary(t, req, summary, m.PlanSHA256)
		var review evaluation.Review
		readJSON(t, req.References[0].Review.Path, &review)
		review.Kind = "candidate_review"
		req.References[0].Review.SHA256 = write(t, req.References[0].Review.Path, review)
	}
	publishProtocolRound(t, req, &m, plan, kind)
	return req, m
}

func publishProtocolRound(t *testing.T, req Request, m *Manifest, plan campaign.Plan, kind string) {
	t.Helper()
	m.SchemaVersion, m.Kind = 2, kind
	m.Protocol = testProtocol(t, m.Targets, plan)
	root := filepath.Dir(req.CampaignDir)
	materials, err := ReadProtocolMaterials(m.Protocol, filepath.Join(root, "unused-output"))
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range materials {
		if err := gate.Write(filepath.Join(root, "protocol-materials", name), raw); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(root, "enrollment.json"), Enrollment{SchemaVersion: 2, ID: m.ID, Kind: m.Kind, Targets: m.Targets, Protocol: m.Protocol})
	write(t, filepath.Join(root, "round-manifest.json"), m)
	write(t, filepath.Join(root, "execution.json"), m.Protocol.Execution)
	write(t, req.ManifestPath, m)
}

func TestProtocolRoundConsumesPublishedSnapshots(t *testing.T) {
	for _, kind := range []string{Calibration, Formal} {
		t.Run(kind, func(t *testing.T) {
			req, m := protocolRound(t, kind)
			// 离线读取发布副本，不依赖原宿主材料路径。
			for _, material := range m.Protocol.Materials {
				if err := os.Remove(material.Path); err != nil {
					t.Fatal(err)
				}
			}
			out, err := Summarize(context.Background(), req)
			if err != nil || !out.Valid || out.SchemaVersion != 2 || out.Protocol == nil || out.Protocol.Split != DevelopmentSplit || out.Counts.Targets != 3 || out.Counts.Success != 1 || out.Counts.NeedsAttention != 1 || out.Counts.Execution["Failed"] != 1 || out.Metrics.Success.Denominator != 3 {
				t.Fatalf("v2 summary failed: %+v %v", out, err)
			}
			if out.Cost == nil || out.Cost.Totals != (budget.Totals{}) || !out.InputsUnchanged {
				t.Fatal("existing cost or input identity behavior changed")
			}
		})
	}
}

func TestProtocolRoundRejectsRebindingAndPreservesUnknown(t *testing.T) {
	for _, mode := range []string{"missing-enrollment", "missing-published-manifest", "missing-material", "changed-material", "changed-execution", "relabeled-request", "changed-enrollment", "wrong-reviewer", "calibration-review", "missing-review"} {
		t.Run(mode, func(t *testing.T) {
			req, m := protocolRound(t, Formal)
			root := filepath.Dir(req.CampaignDir)
			switch mode {
			case "missing-enrollment", "missing-published-manifest", "missing-material":
				name := map[string]string{"missing-enrollment": "enrollment.json", "missing-published-manifest": "round-manifest.json", "missing-material": "protocol-materials/dataset"}[mode]
				if err := os.Remove(filepath.Join(root, name)); err != nil {
					t.Fatal(err)
				}
			case "changed-material":
				if err := os.WriteFile(filepath.Join(root, "protocol-materials/dataset"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "changed-execution":
				write(t, filepath.Join(root, "execution.json"), execution.Info{Kind: execution.KindDocker, CPUs: 5})
			case "relabeled-request":
				m.Protocol.ID = "another-protocol"
				write(t, req.ManifestPath, m)
			case "changed-enrollment":
				var enrollment Enrollment
				readJSON(t, filepath.Join(root, "enrollment.json"), &enrollment)
				enrollment.Protocol.Reviewers["one"] = "another-reviewer"
				write(t, filepath.Join(root, "enrollment.json"), enrollment)
			case "wrong-reviewer", "calibration-review":
				var review evaluation.Review
				readJSON(t, req.References[0].Review.Path, &review)
				if mode == "wrong-reviewer" {
					review.Reviewer = "unplanned-reviewer"
				} else {
					review.Kind = Calibration
				}
				req.References[0].Review.SHA256 = write(t, req.References[0].Review.Path, review)
			case "missing-review":
				req.References[0].Review = nil
			}
			out, err := Summarize(context.Background(), req)
			if mode == "missing-review" {
				if err != nil || !out.Valid || out.Counts.Success != 0 || out.Counts.UnknownReleases != 1 || out.Metrics.WrongRelease.Upper != 1 {
					t.Fatalf("missing review did not remain unknown: %+v %v", out, err)
				}
			} else if err == nil || out.Valid || out.Metrics != nil {
				t.Fatalf("conflicting protocol accepted: %+v %v", out, err)
			}
		})
	}
}

func TestRoundAcceptsKnownCampaignWriterSchemas(t *testing.T) {
	for _, mode := range []string{"legacy", "current", "unknown-writer", "legacy-with-writer", "unknown-schema"} {
		t.Run(mode, func(t *testing.T) {
			req, _ := fakeRound(t)
			path := filepath.Join(req.CampaignDir, "campaign.json")
			var record campaign.Record
			readJSON(t, path, &record)
			switch mode {
			case "current":
				record.SchemaVersion, record.WriterProtocol = 2, writer.Protocol
			case "unknown-writer":
				record.SchemaVersion, record.WriterProtocol = 2, "unknown-writer"
			case "legacy-with-writer":
				record.WriterProtocol = writer.Protocol
			case "unknown-schema":
				record.SchemaVersion = 3
			}
			write(t, path, record)
			out, err := Summarize(context.Background(), req)
			if mode == "legacy" || mode == "current" {
				if err != nil || !out.Valid {
					t.Fatalf("supported campaign record rejected: %v", err)
				}
			} else if err == nil || out.Metrics != nil {
				t.Fatal("unsupported writer schema accepted")
			}
		})
	}
}
