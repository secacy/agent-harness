package evaluation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

var reviewSections = []string{"test_preservation", "change_coverage", "delivery_evidence"}
var automaticSections = []string{"dependencies", "protected_paths", "build", "candidate_tests", "behavior", "expiry"}

func reviewTemplate(report Report, hash string) Review {
	review := Review{SchemaVersion: 1, Kind: "candidate_review", ReportSHA256: hash, TargetID: report.TargetID, RunID: report.RunID, CandidateSHA256: report.CandidateSHA256, Items: map[string]ReviewItem{}}
	for _, name := range reviewSections {
		review.Items[name] = ReviewItem{Status: Inconclusive, Reason: "review pending", Files: []string{}, Evidence: []string{}}
	}
	return review
}

func strictJSON(data []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("trailing JSON data")
	}
	return nil
}

// Assess 只关联已发布参考与另行填写的复核，返回新结论，不修改原 Run 或参考文件。
// 内容定位和人员信息由可信宿主提供；结构校验不等于人工审查真实性认证。
func Assess(referenceJSON, reviewJSON []byte) (Assessment, error) {
	result := Assessment{Status: Inconclusive, ReportSHA256: gate.Digest(referenceJSON), ReviewSHA256: gate.Digest(reviewJSON)}
	var report Report
	var review Review
	if err := strictJSON(referenceJSON, &report); err != nil {
		return result, err
	}
	if err := strictJSON(reviewJSON, &review); err != nil {
		return result, err
	}
	result.TargetID, result.RunID, result.CandidateSHA256, result.Kind = report.TargetID, report.RunID, report.CandidateSHA256, review.Kind
	if report.SchemaVersion != 1 || report.Version != Version || review.SchemaVersion != 1 || !slices.Contains([]string{"candidate_review", "development_calibration"}, review.Kind) {
		return result, errors.New("unsupported reference or review schema")
	}
	if review.ReportSHA256 != result.ReportSHA256 || review.TargetID != report.TargetID || review.RunID != report.RunID || review.CandidateSHA256 != report.CandidateSHA256 {
		return result, errors.New("review does not match reference identity")
	}
	if len(report.Checks) != len(automaticSections) || len(review.Items) != len(reviewSections) {
		return result, errors.New("incomplete reference or review sections")
	}
	for _, name := range automaticSections {
		if !validStatus(report.Checks[name].Status) {
			return result, fmt.Errorf("invalid reference section: %s", name)
		}
	}
	status := report.Automatic
	if !report.InputsUnchanged {
		status = Inconclusive
	} else if report.Automatic != aggregate(report.Checks) {
		return result, errors.New("automatic reference status is inconsistent")
	}
	for _, name := range reviewSections {
		item, ok := review.Items[name]
		if !ok || !validStatus(item.Status) {
			return result, fmt.Errorf("invalid review section: %s", name)
		}
		if item.Status != Inconclusive {
			if strings.TrimSpace(review.Reviewer) == "" || strings.TrimSpace(item.Reason) == "" || len(item.Files) == 0 || len(item.Evidence) == 0 {
				return result, fmt.Errorf("review section lacks author, reason or evidence: %s", name)
			}
			if _, err := time.Parse(time.RFC3339, review.RecordedAt); err != nil {
				return result, errors.New("review timestamp must be RFC3339")
			}
			for _, location := range append(append([]string{}, item.Files...), item.Evidence...) {
				if strings.TrimSpace(location) == "" {
					return result, errors.New("empty review evidence location")
				}
			}
		}
		if item.Status == Fail {
			status = Fail
		} else if item.Status == Inconclusive && status == Pass {
			status = Inconclusive
		}
	}
	if !report.InputsUnchanged {
		status = Inconclusive
	}
	result.Status = status
	return result, nil
}

func validStatus(status Status) bool {
	return status == Pass || status == Fail || status == Inconclusive
}
