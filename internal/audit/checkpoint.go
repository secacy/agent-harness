package audit

import (
	"errors"
	"fmt"
	"os"
	"path"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/writer"
)

const RecoveryDirectory = "recoveries"

func recoverySource(p string) bool {
	return p == RecoveryDirectory || strings.HasPrefix(p, RecoveryDirectory+"/")
}

// RecoveryControlFile 只选择原控制记录和 Run 信封，不封存源码、缓存或任意路径。
func RecoveryControlFile(p string) bool {
	if p == "campaign.json" || p == "plan.json" || p == ledgerPath || p == "summary.json" || p == "summary.md" {
		return true
	}
	parts := strings.Split(p, "/")
	return len(parts) == 3 && parts[0] == "targets" && identifier.MatchString(parts[1]) && parts[2] == "target.json" ||
		len(parts) == 5 && parts[0] == "targets" && identifier.MatchString(parts[1]) && parts[2] == "runs" && identifier.MatchString(parts[3]) && parts[4] == "run.json"
}

// BuildRecoveryCheckpoint 只推导恢复状态；调用方另行保证完整审计和资源静止。
// Before 使用 base64 编码原始字节，避免 JSON 重排破坏原文身份。
func BuildRecoveryCheckpoint(sources []Source, files map[string][]byte, id string, at time.Time) (campaign.RecoveryCheckpoint, error) {
	var c campaign.RecoveryCheckpoint
	if !identifier.MatchString(id) || at.IsZero() {
		return c, errors.New("valid recovery ID and timestamp are required")
	}
	c = campaign.RecoveryCheckpoint{SchemaVersion: 1, ID: id, CreatedAt: at.UTC(), Sources: []campaign.RecoveryPin{}, Before: map[string][]byte{}}
	pins := map[string]Source{}
	for _, s := range sources {
		if recoverySource(s.Path) || execution.ResourceSource(s.Path) {
			continue
		}
		if !safeRelative(s.Path) || pins[s.Path].Path != "" || (s.Kind != "file" && s.Kind != "directory" && s.Kind != "missing") || s.Kind != "missing" && !digestPattern.MatchString(s.SHA256) {
			return c, errors.New("invalid source pin")
		}
		pins[s.Path] = s
		c.Sources = append(c.Sources, campaign.RecoveryPin{Path: s.Path, Kind: s.Kind, SHA256: s.SHA256})
		if s.Kind == "file" && RecoveryControlFile(s.Path) {
			data, ok := files[s.Path]
			if !ok || checksum(data) != s.SHA256 {
				return c, fmt.Errorf("control bytes differ from source: %s", s.Path)
			}
			c.Before[s.Path] = append([]byte{}, data...)
		}
	}
	if len(c.Before) != len(files) {
		return c, errors.New("unexpected archived control file")
	}
	sort.Slice(c.Sources, func(i, j int) bool { return c.Sources[i].Path < c.Sources[j].Path })
	total := 0
	for _, data := range c.Before {
		total += len(data)
		if total > 4<<20 {
			return c, errors.New("archived recovery controls exceed 4 MiB")
		}
	}
	var record campaign.Record
	var plan campaign.Plan
	var ledger budget.Snapshot
	if err := decode(c.Before["campaign.json"], &record, 2); err != nil {
		return c, err
	}
	if err := decode(c.Before["plan.json"], &plan, 0); err != nil {
		return c, err
	}
	if err := decode(c.Before[ledgerPath], &ledger, 1); err != nil {
		return c, err
	}
	if record.WriterProtocol != writer.Protocol || record.ID != plan.ID || ledger.CampaignID != record.ID || checksum(c.Before["plan.json"]) != record.PlanSHA256 {
		return c, errors.New("recovery input identity differs")
	}
	c.CampaignID, c.PlanSHA256 = record.ID, record.PlanSHA256
	var err error
	if c.After.Budget, err = budget.RecoverSnapshot(ledger, id, at); err != nil {
		return c, err
	}
	c.After.Targets = []campaign.RecoveredTarget{}
	seen := map[string]bool{}
	for _, target := range plan.Targets {
		id := target.Input.ID
		if !identifier.MatchString(id) || seen[id] {
			return c, errors.New("invalid or repeated target")
		}
		seen[id] = true
		var before campaign.TargetRecord
		if err := decode(c.Before[targetPath(id)], &before, 0); err != nil {
			return c, err
		}
		if before.ID != id {
			return c, errors.New("target identity differs")
		}
		after := campaign.RecoveredTarget{ID: id, Resolution: "Unstarted", Attempts: []campaign.HistoricalAttempt{}}
		var runPaths []string
		for p, s := range pins {
			parts := strings.Split(p, "/")
			if len(parts) == 5 && parts[0] == "targets" && parts[1] == id && parts[2] == "runs" && parts[4] == "run.json" && (s.Kind == "file" || s.Kind == "missing") {
				runPaths = append(runPaths, p)
			}
		}
		if len(runPaths) > 1 {
			return c, errors.New("source campaign does not support multiple runs")
		}
		if len(runPaths) == 0 {
			if before.Attempt != nil {
				if before.Attempt.RunID != "" {
					return c, errors.New("indexed run is absent")
				}
				after.Resolution, after.Current = "Recorded", before.Attempt
				after.Attempts = append(after.Attempts, campaign.HistoricalAttempt{ObservedStatus: string(before.Attempt.Status), Resolution: "Recorded", Result: before.Attempt})
			} else if before.State == campaign.Active {
				after.Resolution = "Interrupted"
			}
		} else {
			p := runPaths[0]
			runID := strings.Split(p, "/")[3]
			dir := path.Dir(p)
			h := campaign.HistoricalAttempt{RunID: runID, Directory: dir, RecordSHA256: pins[p].SHA256}
			if pins[p].Kind == "missing" {
				h.Resolution, after.Resolution = "Abandoned", "Interrupted"
			} else {
				var run RunRecord
				if err := decode(c.Before[p], &run, 2); err != nil {
					return c, err
				}
				if run.ID != runID || run.TargetID != id {
					return c, errors.New("run identity differs")
				}
				h.ObservedStatus = run.Status
				if run.Status == "Running" {
					h.Resolution, after.Resolution = "Interrupted", "Interrupted"
				} else if terminal(run.Status) {
					a := campaign.Attempt{RunID: runID, Directory: dir, Status: campaign.Status(run.Status), Verdict: run.Verdict, FailureCode: run.FailureCode}
					report := dir + "/artifacts/report.json"
					if s := pins[report]; s.Kind == "file" {
						a.Report = campaign.Artifact{Path: report, SHA256: s.SHA256}
					}
					if run.Status == "Completed" && (!validVerdict(run.Verdict) || a.Report.Path == "" || ledger.Targets[id].RunID != runID) {
						return c, errors.New("completed run lacks report or budget binding")
					}
					h.Resolution = "Adopted"
					if before.Attempt != nil {
						a = *before.Attempt
						h.Resolution = "Recorded"
					}
					h.Result, after.Current, after.Resolution = &a, &a, h.Resolution
				} else {
					return c, errors.New("unsupported run status")
				}
			}
			after.Attempts = append(after.Attempts, h)
		}
		c.After.Targets = append(c.After.Targets, after)
	}
	return c, nil
}

func (i *inspection) checkRecoveryCheckpoint() {
	entries, err := i.view.ReadDir(RecoveryDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	i.report.Coverage = append(i.report.Coverage, Coverage{Area: "recovery_checkpoint", Complete: true})
	if err != nil {
		i.add(Gap, "recovery_unreadable", RecoveryDirectory, "", err.Error())
		return
	}
	if i.record == nil || i.plan == nil || i.ledger == nil {
		i.add(Gap, "recovery_context_missing", RecoveryDirectory, "", "recovery checks require supported source controls and ledger")
		return
	}
	paths := []string{}
	headExists := false
	executionExists := false
	for _, e := range entries {
		p := RecoveryDirectory + "/" + e.Name()
		if e.Type().IsRegular() && strings.HasPrefix(e.Name(), ".checkpoint-") {
			continue
		}
		switch {
		case e.IsDir() && e.Name() == "execution":
			// 子树在检查点及 head 认证后独立检查。
			executionExists = true
		case e.Type().IsRegular() && e.Name() == "head.json":
			headExists = true
		case e.Type().IsRegular() && strings.HasPrefix(e.Name(), "checkpoint-") && strings.HasSuffix(e.Name(), ".json"):
			id := strings.TrimSuffix(strings.TrimPrefix(e.Name(), "checkpoint-"), ".json")
			if identifier.MatchString(id) {
				paths = append(paths, p)
			} else {
				i.add(Violation, "recovery_entry_invalid", RecoveryDirectory, "", "invalid recovery ID: "+e.Name())
			}
		default:
			i.add(Violation, "recovery_entry_invalid", RecoveryDirectory, "", "unexpected recovery entry: "+e.Name())
		}
	}
	if executionExists && !headExists {
		i.add(Violation, "recovery_execution_before_commit", RecoveryDirectory, "", "execution requires a committed checkpoint")
	}
	if len(paths) > 1 || headExists && len(paths) != 1 {
		i.add(Violation, "recovery_conflict", RecoveryDirectory, "", "expected at most one checkpoint and its matching head")
		return
	}
	if len(paths) == 0 {
		i.add(Observation, "recovery_pending", RecoveryDirectory, "", "recovery directory exists before checkpoint publication")
		return
	}
	p := paths[0]
	var c campaign.RecoveryCheckpoint
	if !i.read(p, &c, 1) {
		return
	}
	var sources []Source
	for _, s := range i.view.sources {
		if !recoverySource(s.Path) && !execution.ResourceSource(s.Path) {
			sources = append(sources, *s)
		}
	}
	expected, buildErr := BuildRecoveryCheckpoint(sources, c.Before, c.ID, c.CreatedAt)
	if buildErr != nil || !reflect.DeepEqual(expected, c) || p != RecoveryDirectory+"/checkpoint-"+c.ID+".json" {
		i.add(Violation, "recovery_checkpoint_invalid", p, "", "checkpoint differs from source controls or deterministic recovery", recoveryDependencies(sources)...)
		return
	}
	if !headExists {
		i.add(Observation, "recovery_pending", p, "", "checkpoint published; head not committed")
		return
	}
	var head campaign.RecoveryHead
	headPath := RecoveryDirectory + "/head.json"
	if !i.read(headPath, &head, 1) {
		return
	}
	if head.CampaignID != c.CampaignID || head.PlanSHA256 != c.PlanSHA256 || head.Checkpoint.Path != p || head.Checkpoint.SHA256 != checksum(i.view.data[p]) {
		i.add(Violation, "recovery_head_mismatch", headPath, "", "head does not bind this checkpoint", p)
		return
	}
	i.add(Observation, "recovery_prepared", headPath, "", "recovery control checkpoint committed; execution state is checked separately", append(recoveryDependencies(sources), p)...)
	i.checkRecoveryExecution(c, head)
}

func recoveryDependencies(sources []Source) []string {
	out := make([]string, 0, len(sources))
	for _, s := range sources {
		out = append(out, s.Path)
	}
	return out
}
