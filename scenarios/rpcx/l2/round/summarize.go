package round

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"time"

	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/writer"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/runner"
)

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)

func validateManifest(m Manifest) error {
	if m.SchemaVersion != 1 || !identifier.MatchString(m.ID) || !identifier.MatchString(m.CampaignID) || !slices.Contains([]string{Calibration, Development}, m.Kind) || !digest(m.PlanSHA256) || len(m.Targets) == 0 {
		return errors.New("valid round identity, kind, plan digest and targets are required")
	}
	return validatePins(m.Targets)
}

func validatePins(targets []TargetPin) error {
	seen := map[string]bool{}
	for _, t := range targets {
		if !identifier.MatchString(t.ID) || seen[t.ID] || t.ProfileID == "" || t.BuildConfig == "" || t.ReferenceVersion == "" || !digest(t.BaselineSHA256) || !digest(t.SpecSHA256) || !digest(t.ReferenceSHA256) || !digest(t.ToolSHA256) {
			return errors.New("invalid or duplicate round target pin")
		}
		seen[t.ID] = true
	}
	return nil
}

// Summarize 只读既有记录并在独占目录发布新结果；缺材料保留未知，冲突禁止发布指标。
func Summarize(ctx context.Context, req Request) (out Report, err error) {
	if err = ctx.Err(); err != nil {
		return out, err
	}
	for _, path := range []string{req.ManifestPath, req.CampaignDir, req.EvidenceDir} {
		if !filepath.IsAbs(path) {
			return out, errors.New("round paths must be absolute")
		}
	}
	req.ManifestPath, err = gate.Canonical(req.ManifestPath)
	if err != nil {
		return out, err
	}
	req.CampaignDir, err = gate.Canonical(req.CampaignDir)
	if err != nil {
		return out, err
	}
	req.EvidenceDir, err = gate.Canonical(req.EvidenceDir)
	if err != nil {
		return out, err
	}
	raw, err := os.ReadFile(req.ManifestPath)
	if err != nil {
		return out, err
	}
	var manifest Manifest
	if err = Decode(raw, &manifest); err != nil {
		return out, err
	}
	if err = validateManifest(manifest); err != nil {
		return out, err
	}
	refs := map[string]Reference{}
	for _, ref := range req.References {
		if refs[ref.TargetID].TargetID != "" || !slices.ContainsFunc(manifest.Targets, func(t TargetPin) bool { return t.ID == ref.TargetID }) {
			return out, errors.New("unknown or duplicate reference target")
		}
		for _, a := range []*Artifact{&ref.Report, ref.Review} {
			if a == nil {
				continue
			}
			if !filepath.IsAbs(a.Path) || !digest(a.SHA256) {
				return out, errors.New("reference paths must be absolute with SHA-256 pins")
			}
			a.Path, err = gate.Canonical(a.Path)
			if err != nil {
				return out, err
			}
			if gate.Overlap(filepath.Dir(a.Path), req.EvidenceDir) {
				return out, errors.New("round output overlaps reference inputs")
			}
		}
		refs[ref.TargetID] = ref
	}
	if gate.Overlap(req.CampaignDir, req.EvidenceDir) || gate.Overlap(req.ManifestPath, req.EvidenceDir) {
		return out, errors.New("round output overlaps source inputs")
	}
	if err = os.Mkdir(req.EvidenceDir, 0755); err != nil {
		return out, err
	}
	out = Report{SchemaVersion: 1, Version: Version, ID: manifest.ID, Kind: manifest.Kind, CampaignID: manifest.CampaignID, ManifestSHA256: gate.Digest(raw), PlanSHA256: manifest.PlanSHA256, StartedAt: time.Now().UTC(), Valid: true, Sources: map[string]Source{}}
	for _, pin := range manifest.Targets {
		out.Targets = append(out.Targets, unknownResult(pin.ID))
	}
	reader := &reader{ctx: ctx, output: req.EvidenceDir, sources: out.Sources, trees: map[string]string{}}
	defer func() {
		err = errors.Join(err, ctx.Err())
		if drift := reader.unchanged(); drift != nil {
			err = errors.Join(err, drift)
		} else {
			out.InputsUnchanged = true
		}
		if err != nil {
			out.Valid = false
			out.ExecutionComplete = false
			out.Issues = append(out.Issues, Issue{Code: "invalid_evidence", Detail: err.Error()})
		}
		out.Counts, _ = count(out.Targets)
		if out.Valid {
			_, metrics := count(out.Targets)
			out.Metrics = &metrics
		}
		out.FinishedAt = time.Now().UTC()
		saveErr := gate.Write(filepath.Join(req.EvidenceDir, "summary.md"), markdown(out))
		if saveErr == nil {
			saveErr = gate.WriteJSON(filepath.Join(req.EvidenceDir, "round.json"), out)
		}
		if saveErr != nil {
			out.Valid = false
			out.Metrics = nil
		}
		err = errors.Join(err, saveErr)
	}()
	if err = gate.WriteJSON(filepath.Join(req.EvidenceDir, "request.json"), req); err != nil {
		return out, err
	}
	out.ToolSHA256, err = saveTools(filepath.Join(req.EvidenceDir, "tools"))
	if err != nil {
		return out, err
	}
	reader.trees[filepath.Join(req.EvidenceDir, "tools")] = out.ToolSHA256
	if _, _, err = reader.read(req.ManifestPath, out.ManifestSHA256); err != nil {
		return out, err
	}
	var plan campaign.Plan
	exists, err := reader.json(filepath.Join(req.CampaignDir, "plan.json"), manifest.PlanSHA256, &plan)
	if err != nil {
		return out, err
	}
	if !exists {
		return out, fmt.Errorf("%w: campaign plan is required", ErrInvalid)
	}
	if plan.ID != manifest.CampaignID || len(plan.Targets) != len(manifest.Targets) {
		return out, fmt.Errorf("%w: campaign plan membership mismatch", ErrInvalid)
	}
	if manifest.Kind == Development && plan.Profile.Provider == "controlled" {
		return out, fmt.Errorf("%w: controlled model campaign must remain development_calibration", ErrInvalid)
	}
	for i, pin := range manifest.Targets {
		t := plan.Targets[i].Input
		if t.ID != pin.ID || t.ProfileID != pin.ProfileID || t.BuildConfig != pin.BuildConfig || t.Repository.SHA256 != pin.BaselineSHA256 || t.Specification.SHA256 != pin.SpecSHA256 {
			return out, fmt.Errorf("%w: target input pin mismatch: %s", ErrInvalid, pin.ID)
		}
	}
	var record campaign.Record
	exists, err = reader.json(filepath.Join(req.CampaignDir, "campaign.json"), "", &record)
	if err != nil {
		return out, err
	}
	if !exists {
		out.Issues = append(out.Issues, Issue{Code: "missing_campaign", Detail: "campaign terminal record missing"})
		return out, nil
	}
	supportedRecord := record.SchemaVersion == 1 && record.WriterProtocol == "" || record.SchemaVersion == 2 && record.WriterProtocol == writer.Protocol
	if !supportedRecord || record.ID != plan.ID || record.PlanSHA256 != manifest.PlanSHA256 || !slices.Contains([]campaign.Status{campaign.Completed, campaign.Failed, campaign.Cancelled}, record.Status) || record.FinishedAt == nil {
		return out, fmt.Errorf("%w: campaign identity or terminal state mismatch", ErrInvalid)
	}
	out.CampaignStatus = record.Status
	summaryPath, err := child(req.CampaignDir, record.Summary.Path)
	if err != nil {
		return out, err
	}
	if !digest(record.Summary.SHA256) {
		return out, fmt.Errorf("%w: campaign summary digest required", ErrInvalid)
	}
	var summary campaign.Summary
	exists, err = reader.json(summaryPath, record.Summary.SHA256, &summary)
	if err != nil {
		return out, err
	}
	if !exists {
		out.Issues = append(out.Issues, Issue{Code: "missing_summary", Detail: "campaign summary missing"})
		return out, nil
	}
	if summary.CampaignID != plan.ID || summary.PlanSHA256 != manifest.PlanSHA256 || len(summary.Targets) != len(manifest.Targets) {
		return out, fmt.Errorf("%w: summary identity or membership mismatch", ErrInvalid)
	}
	out.ExecutionComplete = record.Status == campaign.Completed
	seenRuns := map[string]bool{}
	counts := campaign.Counts{Verdicts: map[string]int{}}
	for i, pin := range manifest.Targets {
		target := summary.Targets[i]
		if target.ID != pin.ID {
			return out, fmt.Errorf("%w: target ordering or identity mismatch", ErrInvalid)
		}
		targetPath := filepath.Join(req.CampaignDir, "targets", pin.ID, "target.json")
		var disk campaign.TargetRecord
		present, e := reader.json(targetPath, "", &disk)
		if e != nil {
			return out, e
		}
		if present && !reflect.DeepEqual(disk, target) {
			return out, fmt.Errorf("%w: target index mismatch", ErrInvalid)
		}
		if !present {
			out.Targets[i].Gaps = append(out.Targets[i].Gaps, "target_record_missing")
			out.ExecutionComplete = false
		}
		result, e := readTarget(reader, req.CampaignDir, pin, target, seenRuns, manifest.CampaignID)
		result.Gaps = append(result.Gaps, out.Targets[i].Gaps...)
		out.Targets[i] = result
		if e != nil {
			return out, e
		}
		if result.RunStatus == "Unknown" || target.State == campaign.Skipped || (target.Attempt != nil && target.Attempt.Status == campaign.Completed && !result.DeliveryVerified) {
			out.ExecutionComplete = false
		}
		if target.State == campaign.Skipped {
			counts.Skipped++
		} else if target.Attempt != nil {
			switch target.Attempt.Status {
			case campaign.Completed:
				counts.Completed++
				counts.Verdicts[target.Attempt.Verdict]++
			case campaign.Failed:
				counts.Failed++
			case campaign.Cancelled:
				counts.Cancelled++
			}
		}
		if ref, ok := refs[pin.ID]; ok {
			if !result.DeliveryVerified {
				if result.Verdict == "" {
					return out, fmt.Errorf("%w: reference supplied for task without candidate verdict", ErrInvalid)
				}
				out.Targets[i].Gaps = append(out.Targets[i].Gaps, "reference_unusable_without_delivery")
				continue
			}
			if e = attachReference(reader, &out.Targets[i], pin, ref, manifest.Kind); e != nil {
				return out, e
			}
		} else if result.CandidateSHA256 != "" {
			out.Targets[i].Gaps = append(out.Targets[i].Gaps, "reference_missing")
		}
	}
	if !reflect.DeepEqual(summary.Counts, counts) {
		return out, fmt.Errorf("%w: campaign count mismatch", ErrInvalid)
	}
	if err = attachBudget(reader, &out, req.CampaignDir, plan, summary.Budget); err != nil {
		return out, err
	}
	return out, nil
}

func unknownResult(id string) Result {
	return Result{TargetID: id, RunStatus: "Unknown", Automatic: "inconclusive", Reference: "inconclusive"}
}

func readTarget(reader *reader, root string, pin TargetPin, target campaign.TargetRecord, seen map[string]bool, campaignID string) (Result, error) {
	out := unknownResult(pin.ID)
	a := target.Attempt
	if target.State == campaign.Skipped {
		if a != nil {
			return out, fmt.Errorf("%w: skipped target has an attempt", ErrInvalid)
		}
		out.RunStatus = "Skipped"
		out.FailureCode = target.Reason
		return out, nil
	}
	if target.State != campaign.Finished || a == nil {
		return out, fmt.Errorf("%w: target is not terminal", ErrInvalid)
	}
	if !slices.Contains([]campaign.Status{campaign.Completed, campaign.Failed, campaign.Cancelled}, a.Status) {
		return out, fmt.Errorf("%w: attempt is not terminal", ErrInvalid)
	}
	if a.RunID == "" {
		if a.Status == campaign.Completed || a.Directory != "" || a.Verdict != "" || a.Report.Path != "" || a.Report.SHA256 != "" {
			return out, fmt.Errorf("%w: invalid attempt without run", ErrInvalid)
		}
		out.RunStatus = string(a.Status)
		out.FailureCode = a.FailureCode
		return out, nil
	}
	if !identifier.MatchString(a.RunID) || seen[a.RunID] {
		return out, fmt.Errorf("%w: duplicate or invalid run ID", ErrInvalid)
	}
	seen[a.RunID] = true
	out.RunID = a.RunID
	if a.Status == campaign.Completed {
		if !slices.Contains([]string{"ReadyForReview", "Rejected", "NeedsAttention"}, a.Verdict) {
			return out, fmt.Errorf("%w: invalid original verdict", ErrInvalid)
		}
		out.Verdict = a.Verdict
	}
	expected := filepath.ToSlash(filepath.Join("targets", pin.ID, "runs", a.RunID))
	if a.Directory != expected {
		return out, fmt.Errorf("%w: cross-target run directory", ErrInvalid)
	}
	dir, err := child(root, a.Directory)
	if err != nil {
		return out, err
	}
	var run runner.Record
	exists, err := reader.json(filepath.Join(dir, "run.json"), "", &run)
	if err != nil {
		return out, err
	}
	if !exists {
		out.Gaps = append(out.Gaps, "run_record_missing")
		return out, nil
	}
	if run.SchemaVersion != 2 || run.ID != a.RunID || run.TargetID != pin.ID || string(run.Status) != string(a.Status) || run.Verdict != a.Verdict || run.FailureCode != a.FailureCode || run.FinishedAt == nil {
		return out, fmt.Errorf("%w: run terminal identity mismatch", ErrInvalid)
	}
	out.RunStatus = string(run.Status)
	out.FailureCode = run.FailureCode
	duration := run.FinishedAt.Sub(run.StartedAt).Milliseconds()
	if duration < 0 {
		return out, fmt.Errorf("%w: invalid run times", ErrInvalid)
	}
	out.DurationMS = &duration
	if a.Status != campaign.Completed && a.Verdict != "" {
		return out, fmt.Errorf("%w: failed attempt carries verdict", ErrInvalid)
	}
	if a.Report.Path == "" {
		if a.Status == campaign.Completed || a.Report.SHA256 != "" {
			return out, fmt.Errorf("%w: completed attempt lacks report identity", ErrInvalid)
		}
		out.Gaps = append(out.Gaps, "run_report_missing")
		return out, nil
	}
	path, err := child(root, a.Report.Path)
	if err != nil {
		return out, err
	}
	if !contained(dir, path) || !digest(a.Report.SHA256) {
		return out, fmt.Errorf("%w: cross-run report path or invalid digest", ErrInvalid)
	}
	var report runner.Report
	exists, err = reader.json(path, a.Report.SHA256, &report)
	if err != nil {
		return out, err
	}
	if !exists {
		out.Gaps = append(out.Gaps, "run_report_missing")
		return out, nil
	}
	if report.RunID != run.ID || report.Status != run.Status || report.FailureCode != run.FailureCode || report.Input.Target == nil || report.Input.Target.ID != pin.ID || report.Input.Target.ProfileID != pin.ProfileID || report.Input.Target.Repository.SHA256 != pin.BaselineSHA256 || report.Input.Target.Specification.SHA256 != pin.SpecSHA256 {
		return out, fmt.Errorf("%w: run report input identity mismatch", ErrInvalid)
	}
	if report.AgentRecord != "" {
		agentPath, e := child(filepath.Dir(path), report.AgentRecord)
		if e != nil {
			return out, e
		}
		var usage struct{ ModelCalls, ToolCalls *int }
		present, e := reader.json(agentPath, "", &usage)
		if e != nil {
			return out, e
		}
		if !present {
			out.Gaps = append(out.Gaps, "agent_record_missing")
		} else {
			if usage.ModelCalls == nil || usage.ToolCalls == nil || *usage.ModelCalls < 0 || *usage.ToolCalls < 0 {
				return out, fmt.Errorf("%w: invalid agent attempt counts", ErrInvalid)
			}
			out.LoopModelCalls = usage.ModelCalls
			out.ToolCalls = usage.ToolCalls
		}
	}
	if run.Status != runner.StatusCompleted {
		return out, nil
	}
	v := report.Validation
	if !slices.Contains([]string{"ReadyForReview", "Rejected", "NeedsAttention"}, run.Verdict) || v == nil || report.CandidateSHA256 != run.CandidateSHA256 || v.CandidateSHA256 != run.CandidateSHA256 || v.Verdict != run.Verdict || v.SpecSHA256 != pin.SpecSHA256 || v.BaselineSHA256 != pin.BaselineSHA256 || !digest(run.CandidateSHA256) {
		return out, fmt.Errorf("%w: completed report validation mismatch", ErrInvalid)
	}
	if report.Input.SpecSHA256 != pin.SpecSHA256 || report.Input.BaselineSHA256 != pin.BaselineSHA256 || report.Input.Target.BuildConfig != pin.BuildConfig {
		return out, fmt.Errorf("%w: run input binding mismatch", ErrInvalid)
	}
	binding := report.Input.Budget
	if binding == nil || binding.TargetID != pin.ID || binding.RunID != run.ID || binding.CampaignID != campaignID {
		return out, fmt.Errorf("%w: run budget identity missing", ErrInvalid)
	}
	module := map[string]string{"rpcx-goddd-t3-v1": "goddd", "rpcx-translation-t3-v1": "translation", "rpcx-workouts-t3-v1": "trainings"}[pin.ProfileID]
	if module == "" {
		return out, fmt.Errorf("%w: unsupported L2 profile", ErrInvalid)
	}
	candidate, err := child(dir, "evidence/candidate/"+module)
	if err != nil {
		return out, err
	}
	if _, err = os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
		out.Gaps = append(out.Gaps, "candidate_snapshot_missing")
		return out, nil
	}
	if err = reader.tree(candidate, run.CandidateSHA256); err != nil {
		return out, err
	}
	out.Verdict = run.Verdict
	out.CandidateSHA256 = run.CandidateSHA256
	out.DeliveryVerified = true
	return out, nil
}
