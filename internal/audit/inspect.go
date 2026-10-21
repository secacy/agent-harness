package audit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/writer"
)

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

const ledgerPath = "budget/budget.json"

type inspection struct {
	view      *View
	report    Report
	findings  []Finding
	plan      *campaign.Plan
	record    *campaign.Record
	ledger    *budget.Snapshot
	targets   map[string]*campaign.TargetRecord
	runs      map[string]map[string]RunRecord
	inspector RunInspector
	started   bool
}

// Inspect 独占发布一份新报告；来源违规通过 Report 返回，配置或发布失败返回 error。
// 调用方负责先停止写者；本函数不检测进程活性，也不赋予恢复许可。
func Inspect(ctx context.Context, request Request, inspectRun RunInspector) (Report, error) {
	root, err := canonical(request.CampaignDir)
	if err != nil {
		return Report{}, fmt.Errorf("%w: source: %w", ErrConfig, err)
	}
	output, err := canonical(request.OutputDir)
	if err != nil {
		return Report{}, fmt.Errorf("%w: output: %w", ErrConfig, err)
	}
	if within(root, output) || within(output, root) {
		return Report{}, fmt.Errorf("%w: source and output overlap", ErrConfig)
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return Report{}, fmt.Errorf("%w: source must be a directory", ErrConfig)
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		return Report{}, fmt.Errorf("%w: output must not exist", ErrConfig)
	}
	if err = os.Mkdir(output, 0700); err != nil {
		return Report{}, fmt.Errorf("%w: create output: %w", ErrConfig, err)
	}
	i := &inspection{
		view: &View{root: root, ctx: ctx, sources: map[string]*Source{}, data: map[string][]byte{}},
		report: Report{
			SchemaVersion: 1, ToolVersion: Version, CampaignDir: root,
			WriterState: "not_checked", ExecutionState: "incomplete",
			Violations: []Finding{}, Gaps: []Finding{}, Observations: []Finding{},
			Coverage: []Coverage{{Area: "campaign_target_run"}, {Area: "budget"}},
		},
		targets: map[string]*campaign.TargetRecord{}, runs: map[string]map[string]RunRecord{}, inspector: inspectRun,
	}
	i.inspect()
	i.checkRecoveryCheckpoint()
	i.checkResources()
	i.report.InputsUnchanged = i.view.verify()
	if ctx.Err() != nil {
		i.add(Gap, "audit_cancelled", ".", "", ctx.Err().Error())
	}
	i.finish()
	publishErr := publish(output, i.report)
	return i.report, errors.Join(ctx.Err(), publishErr)
}

func (i *inspection) add(severity Severity, code, p, field, reason string, dependencies ...string) {
	sources := append([]string{p}, dependencies...)
	// 无独立版本的记录依赖父契约；目录归属和 Target 清单依赖 plan。
	if p == "plan.json" || p == "summary.json" || strings.HasPrefix(p, "targets/") || p == "targets" {
		sources = append(sources, "campaign.json")
	}
	sort.Strings(sources)
	sources = compact(sources)
	i.findings = append(i.findings, Finding{Severity: severity, Code: code, Path: p, Field: field, Reason: reason, Sources: sources})
}
func compact(in []string) []string {
	out := []string{}
	for _, s := range in {
		if s != "" && (len(out) == 0 || s != out[len(out)-1]) {
			out = append(out, s)
		}
	}
	return out
}
func (i *inspection) read(p string, out any, version int) bool {
	data, err := i.view.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		severity, code := Gap, "source_unreadable"
		if errors.Is(err, ErrUnsafePath) {
			severity, code = Violation, "unsafe_path"
		}
		i.add(severity, code, p, "", err.Error())
		return false
	}
	err = decode(data, out, version)
	// Campaign v2 仅增加创建时写锁协议，其他记录仍沿用单 Run 契约。
	if record, ok := out.(*campaign.Record); ok && UnsupportedFormat(err) {
		var f *formatError
		if errors.As(err, &f) && f.code == "unsupported_schema" {
			err = decode(data, record, 2)
		}
	}
	if err != nil {
		var f *formatError
		errors.As(err, &f)
		severity := Violation
		if f.code == "unsupported_schema" || f.code == "unsupported_format" {
			severity = Gap
		}
		i.add(severity, f.code, p, "", f.reason)
		if severity == Violation {
			i.add(Gap, "format_checks_blocked", p, "", "dependent checks require a supported, valid record")
		}
		return false
	}
	return true
}
func (i *inspection) missing(p string) bool {
	s := i.view.sources[p]
	return s != nil && s.Kind == "missing"
}
func (i *inspection) list(p string) []string {
	entries, err := i.view.ReadDir(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		severity, code := Gap, "source_unreadable"
		if errors.Is(err, ErrUnsafePath) {
			severity, code = Violation, "unsafe_path"
		}
		i.add(severity, code, p, "", err.Error())
		return nil
	}
	names := []string{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".publish-") {
			continue
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !identifier.MatchString(entry.Name()) {
			i.add(Violation, "unexpected_entry", p, "", fmt.Sprintf("unexpected directory entry %q", entry.Name()))
			continue
		}
		names = append(names, entry.Name())
	}
	return names
}

func (i *inspection) inspect() {
	var record campaign.Record
	if i.read("campaign.json", &record, 1) {
		i.record = &record
		if record.SchemaVersion == 1 && record.WriterProtocol != "" || record.SchemaVersion == 2 && record.WriterProtocol != writer.Protocol {
			i.add(Violation, "writer_protocol_mismatch", "campaign.json", "writer_protocol", "writer protocol does not match the campaign format")
		}
		if record.SchemaVersion == 2 {
			data, err := i.view.ReadFile(writer.FileName)
			switch {
			case err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, ErrUnsafePath):
				i.add(Gap, "source_unreadable", writer.FileName, "", err.Error(), "campaign.json")
			case err != nil || len(data) != 0:
				i.add(Violation, "writer_lock_invalid", writer.FileName, "", "campaign v2 requires its original empty regular lock file", "campaign.json")
			}
		}
		if !identifier.MatchString(record.ID) || (!filepath.IsAbs(record.Directory) || filepath.Clean(record.Directory) != record.Directory) || !digestPattern.MatchString(record.PlanSHA256) {
			i.add(Violation, "identity_mismatch", "campaign.json", "id/directory/plan_sha256", "invalid campaign identity")
		}
		i.lifecycle("campaign.json", string(record.Status), record.FinishedAt != nil, record.Error)
		if record.StartedAt.IsZero() {
			i.add(Violation, "invalid_state", "campaign.json", "started_at", "campaign start timestamp is missing")
		}
		if record.Status == campaign.Failed || record.Status == campaign.Cancelled {
			i.report.ExecutionState = "terminal_with_failures"
		}
	} else {
		if i.missing("campaign.json") {
			i.add(Gap, "initialization_incomplete", "campaign.json", "", "campaign identity has not been published")
		}
		// 不支持父版本时，不猜测无独立版本的 plan/Target/summary 格式。
		var ledger budget.Snapshot
		if i.read(ledgerPath, &ledger, 1) {
			i.ledger = &ledger
			i.checkBudget()
			if i.missing("campaign.json") {
				i.add(Violation, "prerequisite_missing", "campaign.json", "", "ledger initialization requires a published campaign", ledgerPath)
			}
		}
		i.report.ExecutionState = "unknown"
		return
	}
	var plan campaign.Plan
	if i.read("plan.json", &plan, 0) {
		i.plan = &plan
		if checksum(i.view.data["plan.json"]) != record.PlanSHA256 {
			i.add(Violation, "plan_digest_mismatch", "plan.json", "", "plan bytes do not match campaign reference", "campaign.json")
		}
		if plan.ID != record.ID || plan.Concurrency < 1 || plan.ExecutionConcurrency < 1 || len(plan.Targets) == 0 || plan.Profile.Validate() != nil || !limitsValid(plan.Budget) {
			i.add(Violation, "identity_mismatch", "plan.json", "", "invalid plan identity or limits", "campaign.json")
		}
		if plan.Recovery != nil && (plan.Recovery.MaxAttempts < 1 || plan.Recovery.GenerationDeadline.IsZero()) {
			i.add(Violation, "recovery_policy_invalid", "plan.json", "recovery", "attempt limit and absolute deadline must be frozen before execution")
		}
	} else {
		if i.missing("plan.json") {
			i.add(Violation, "prerequisite_missing", "plan.json", "", "published campaign requires its plan", "campaign.json")
		}
		i.report.ExecutionState = "unknown"
	}
	var ledger budget.Snapshot
	if i.read(ledgerPath, &ledger, 1) {
		i.ledger = &ledger
	}
	if i.plan == nil {
		if i.ledger != nil {
			i.checkBudget()
		}
		return
	}
	expected := map[string]bool{}
	for _, target := range plan.Targets {
		id := target.Input.ID
		if !identifier.MatchString(id) || expected[id] || target.Input.Validate() != nil || !limitsValid(target.Budget) || target.Limits.MaxModelCalls < 1 || target.Limits.MaxToolCalls < 1 || target.Timeout <= 0 {
			i.add(Violation, "identity_mismatch", "plan.json", "targets", "invalid or duplicate target")
			continue
		}
		expected[id] = true
		i.inspectTarget(target)
	}
	for _, id := range i.list("targets") {
		if !expected[id] {
			i.add(Violation, "identity_mismatch", "targets", "", "target directory is not in plan", "plan.json")
		}
	}
	active := i.started
	activitySources := []string{"campaign.json"}
	for id, t := range i.targets {
		if t.State != campaign.Pending || len(i.runs[id]) > 0 {
			active = true
			activitySources = append(activitySources, targetPath(id))
		}
	}
	for id := range i.runs {
		activitySources = append(activitySources, "targets/"+id+"/runs")
	}
	if i.ledger != nil {
		for _, account := range i.ledger.Targets {
			if account.RunID != "" {
				active = true
			}
		}
		if len(i.ledger.Calls) > 0 {
			active = true
		}
		i.checkBudget()
	} else if i.missing(ledgerPath) {
		severity, code := Observation, "initialization_incomplete"
		if active || record.Status == campaign.Completed {
			severity, code = Violation, "prerequisite_missing"
		}
		i.add(severity, code, ledgerPath, "", "budget has not been published", activitySources...)
	}
	for _, target := range plan.Targets {
		id := target.Input.ID
		if !identifier.MatchString(id) {
			continue
		}
		if i.targets[id] == nil && i.missing(targetPath(id)) {
			severity, code := Observation, "initialization_incomplete"
			if active || record.Status == campaign.Completed {
				severity, code = Violation, "prerequisite_missing"
			}
			deps := append([]string{"plan.json", ledgerPath}, activitySources...)
			i.add(severity, code, targetPath(id), "", "target record has not been published", deps...)
		}
	}
	i.checkSummary()
	i.report.Coverage[0].Complete = true
	i.report.Coverage[1].Complete = i.ledger != nil || i.missing(ledgerPath) && !active
}
