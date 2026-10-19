package evaluation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
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
	if report.SchemaVersion != 1 || !slices.Contains([]string{Version, TranslationVersion}, report.Version) || review.SchemaVersion != 1 || !slices.Contains([]string{"candidate_review", "development_calibration"}, review.Kind) {
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
	if err := validateConfigurations(report); err != nil {
		return result, err
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

func validateConfigurations(report Report) error {
	if report.Version == Version {
		if len(report.Configurations) != 0 {
			return errors.New("unexpected Goddd configuration records")
		}
		return nil
	}
	if len(report.Configurations) != 2 {
		return errors.New("both Translation configurations are required")
	}
	for _, config := range translationScenario().configurations {
		record, ok := report.Configurations[config.id]
		if !ok || record.Tags != config.tags || len(record.Checks) != len(configurationSections) {
			return errors.New("invalid Translation configuration identity or sections")
		}
		for _, name := range configurationSections {
			if !validStatus(record.Checks[name].Status) {
				return fmt.Errorf("invalid configuration section: %s/%s", config.id, name)
			}
		}
		if record.Checks["selection"].Status == Pass {
			files := record.SelectedFiles
			if !slices.Contains(files["webapi/go"], config.adapter) || slices.Contains(files["webapi/go"], config.ignored) || !slices.Contains(files["webapi/ignored"], config.ignored) || !slices.Contains(files["usecase/tests"], "mocks_repo_test.go") {
				return errors.New("configuration selection lacks required file evidence")
			}
		}
	}
	for name, expected := range mergedConfigurationChecks(report.Configurations) {
		if !reflect.DeepEqual(report.Checks[name], expected) {
			return fmt.Errorf("configuration aggregate is inconsistent: %s", name)
		}
	}
	return nil
}

func validStatus(status Status) bool {
	return status == Pass || status == Fail || status == Inconclusive
}
