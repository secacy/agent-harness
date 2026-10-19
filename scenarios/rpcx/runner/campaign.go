package runner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/secacy/agent-harness/internal/campaign"
)

// CampaignExecutor 将通用批次服务接到现有 Runner，不改变质量门禁。
// 本地执行仍须由宿主明确授权；原模型仅交给 Runner 的预算绑定步骤。
func CampaignExecutor(trustedLocal bool) campaign.Execute {
	return func(ctx context.Context, target campaign.Target, services campaign.Services) (campaign.Attempt, error) {
		record, runErr := Run(ctx, Config{
			Target: &target.Input, RunsDir: services.RunsDir,
			Model: services.Model, ModelName: services.Budget.Ledger.Snapshot().Profile.Model,
			Budget: &services.Budget, Limits: target.Limits, Timeout: target.Timeout,
			Backend: services.Backend, TrustedLocal: trustedLocal,
		})
		status := campaign.Status(record.Status)
		if status == "" {
			status = campaign.Failed
		}
		attempt := campaign.Attempt{RunID: record.ID, Directory: record.Directory, Status: status, Verdict: record.Verdict, FailureCode: record.FailureCode}
		if record.Directory == "" {
			return attempt, runErr
		}
		reportPath := filepath.Join(record.Directory, "artifacts/report.json")
		data, err := os.ReadFile(reportPath)
		if err == nil {
			attempt.Report = campaign.Artifact{Path: reportPath, SHA256: digest(data)}
			var report Report
			if err = json.Unmarshal(data, &report); err == nil {
				if report.RunID != record.ID || report.Status != record.Status || report.Input.Target == nil || report.Input.Target.ID != target.Input.ID {
					err = errors.New("report does not match the target run")
				} else if record.Status == StatusCompleted {
					binding := report.Input.Budget
					if report.Validation == nil || report.CandidateSHA256 != record.CandidateSHA256 || report.PatchSHA256 != record.PatchSHA256 || binding == nil || binding.TargetID != target.Input.ID || binding.RunID != record.ID || binding.CampaignID != services.Budget.Ledger.Snapshot().CampaignID {
						err = errors.New("completed report lacks bound validation or budget evidence")
					} else if report.Validation.Verdict != record.Verdict || report.Validation.CandidateSHA256 != record.CandidateSHA256 || report.Validation.SpecSHA256 != target.Input.Specification.SHA256 || report.Validation.BaselineSHA256 != target.Input.Repository.SHA256 {
						err = errors.New("validation does not match the declared inputs and verdict")
					}
				}
			}
		}
		if err != nil {
			attempt.Status, attempt.Verdict = campaign.Failed, ""
			runErr = errors.Join(runErr, err)
		}
		return attempt, runErr
	}
}
