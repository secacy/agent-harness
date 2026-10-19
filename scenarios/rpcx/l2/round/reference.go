package round

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
)

func attachReference(reader *reader, out *Result, pin TargetPin, ref Reference, kind string) error {
	raw, exists, err := reader.read(ref.Report.Path, ref.Report.SHA256)
	if err != nil {
		return err
	}
	if !exists {
		out.Gaps = append(out.Gaps, "reference_missing")
		return nil
	}
	var report evaluation.Report
	if err = Decode(raw, &report); err != nil {
		return fmt.Errorf("%w: invalid reference: %v", ErrInvalid, err)
	}
	if report.TargetID != out.TargetID || report.RunID != out.RunID || report.CandidateSHA256 != out.CandidateSHA256 || report.BaselineSHA256 != pin.BaselineSHA256 || report.SpecSHA256 != pin.SpecSHA256 || report.ReferenceVersion != pin.ReferenceVersion || report.ReferenceSHA256 != pin.ReferenceSHA256 || report.ToolSHA256 != pin.ToolSHA256 {
		return fmt.Errorf("%w: reference identity mismatch", ErrInvalid)
	}
	expectedVersion := map[string]string{"rpcx-goddd-t3-v1": evaluation.Version, "rpcx-translation-t3-v1": evaluation.TranslationVersion, "rpcx-workouts-t3-v1": evaluation.WorkoutsVersion}[pin.ProfileID]
	if expectedVersion == "" || report.Version != expectedVersion {
		return fmt.Errorf("%w: reference scenario mismatch", ErrInvalid)
	}
	review := evaluation.Review{SchemaVersion: 1, Kind: "candidate_review", ReportSHA256: gate.Digest(raw), TargetID: out.TargetID, RunID: out.RunID, CandidateSHA256: out.CandidateSHA256, Items: map[string]evaluation.ReviewItem{}}
	for _, name := range []string{"test_preservation", "change_coverage", "delivery_evidence"} {
		review.Items[name] = evaluation.ReviewItem{Status: evaluation.Inconclusive, Reason: "review pending"}
	}
	reviewRaw, _ := json.Marshal(review)
	if ref.Review != nil {
		data, present, e := reader.read(ref.Review.Path, ref.Review.SHA256)
		if e != nil {
			return e
		}
		if present {
			if e = Decode(data, &review); e != nil {
				return fmt.Errorf("%w: invalid review: %v", ErrInvalid, e)
			}
			if kind == Development && review.Kind != "candidate_review" {
				return fmt.Errorf("%w: calibration review cannot enter development evaluation", ErrInvalid)
			}
			reviewRaw = data
			out.ReviewSHA256 = gate.Digest(data)
		} else {
			out.Gaps = append(out.Gaps, "review_missing")
		}
	} else {
		out.Gaps = append(out.Gaps, "review_missing")
	}
	assessment, err := evaluation.Assess(raw, reviewRaw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	out.ReferenceReportSHA256 = gate.Digest(raw)
	if !report.InputsUnchanged {
		out.Gaps = append(out.Gaps, "reference_inputs_invalid")
		return nil
	}
	root := filepath.Dir(ref.Report.Path)
	// 所有引用证据必须仍在，并匹配报告；不凭 reference.json 的 pass 字符串计成绩。
	for _, entry := range []struct {
		path, hash string
		tree       bool
	}{
		{"materials/tools", pin.ToolSHA256, true}, {"materials/reference/freeze.json", pin.ReferenceSHA256, false}, {"materials/spec.md", pin.SpecSHA256, false},
	} {
		path, e := child(root, entry.path)
		if e != nil {
			return e
		}
		if _, e = os.Stat(path); os.IsNotExist(e) {
			out.Gaps = append(out.Gaps, "reference_material_missing")
			return nil
		}
		if entry.tree {
			if e = reader.tree(path, entry.hash); e != nil {
				return e
			}
		} else {
			if _, present, e := reader.read(path, entry.hash); e != nil {
				return e
			} else if !present {
				out.Gaps = append(out.Gaps, "reference_material_missing")
				return nil
			}
		}
	}
	for name, hash := range report.Artifacts {
		if !digest(hash) {
			return fmt.Errorf("%w: invalid reference artifact digest", ErrInvalid)
		}
		path, e := child(root, name)
		if e != nil {
			return e
		}
		if _, present, e := reader.read(path, hash); e != nil {
			return e
		} else if !present {
			out.Gaps = append(out.Gaps, "reference_log_missing")
			return nil
		}
	}
	// 每条实际命令的双日志都必须列入摘要表，不能靠删除索引掩盖缺证据。
	for _, command := range report.Commands {
		for _, path := range []string{command.Stdout, command.Stderr} {
			if !digest(report.Artifacts[path]) {
				return fmt.Errorf("%w: command log identity missing", ErrInvalid)
			}
		}
	}
	if len(report.Commands) == 0 {
		return fmt.Errorf("%w: reference command evidence missing", ErrInvalid)
	}
	out.Automatic = report.Automatic
	out.Reference = assessment.Status
	if report.Automatic == evaluation.Fail {
		out.FailureCategories = append(out.FailureCategories, "migration")
	}
	for name, item := range review.Items {
		if item.Status != evaluation.Fail {
			continue
		}
		category := "migration"
		if name == "delivery_evidence" {
			category = "delivery"
		}
		if !slices.Contains(out.FailureCategories, category) {
			out.FailureCategories = append(out.FailureCategories, category)
		}
	}
	slices.Sort(out.FailureCategories)
	return nil
}
