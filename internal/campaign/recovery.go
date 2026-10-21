package campaign

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/secacy/agent-harness/internal/budget"
)

const RecoveryExecutionPath = "recoveries/execution"

type RunReceipt struct {
	TargetID      string `json:"target_id"`
	RunID         string `json:"run_id"`
	Ordinal       int    `json:"ordinal"`
	PreviousRunID string `json:"previous_run_id,omitempty"`
}

// Activation 固定本代所有尝试收据；发布后不得追加选择或再次执行。
type Activation struct {
	SchemaVersion int          `json:"schema_version"`
	CampaignID    string       `json:"campaign_id"`
	PlanSHA256    string       `json:"plan_sha256"`
	Checkpoint    Artifact     `json:"checkpoint"`
	CreatedAt     time.Time    `json:"created_at"`
	Receipts      []RunReceipt `json:"receipts"`
}

type RecoveryStarted struct {
	SchemaVersion    int       `json:"schema_version"`
	ActivationSHA256 string    `json:"activation_sha256"`
	StartedAt        time.Time `json:"started_at"`
}

// RecoveryResult 的 Targets 包含继承的旧终态及本代新结果，不重判质量。
type RecoveryResult struct {
	SchemaVersion    int             `json:"schema_version"`
	ActivationSHA256 string          `json:"activation_sha256"`
	StartedAt        time.Time       `json:"started_at"`
	FinishedAt       time.Time       `json:"finished_at"`
	Status           Status          `json:"status"`
	Targets          []TargetRecord  `json:"targets"`
	Budget           budget.Snapshot `json:"budget"`
	Error            string          `json:"error,omitempty"`
}

// RecoveryReceipts 从原计划及检查点确定性分配身份；不创建目录或执行任务。
func RecoveryReceipts(p Plan, c RecoveryCheckpoint, selected []string, at time.Time) ([]RunReceipt, error) {
	if selected == nil || at.IsZero() || c.CampaignID != p.ID || len(c.After.Targets) != len(p.Targets) {
		return nil, errors.New("explicit selection and matching recovery context are required")
	}
	for n, t := range p.Targets {
		if c.After.Targets[n].ID != t.Input.ID {
			return nil, errors.New("checkpoint target order differs from plan")
		}
	}
	if len(selected) > 0 && (p.Recovery == nil || p.Recovery.MaxAttempts < 1 || !at.Before(p.Recovery.GenerationDeadline)) {
		return nil, errors.New("original plan does not permit another attempt at this time")
	}
	out := []RunReceipt{}
	seen := map[string]bool{}
	for _, id := range selected {
		index := slices.IndexFunc(p.Targets, func(t Target) bool { return t.Input.ID == id })
		if index < 0 || seen[id] || c.After.Targets[index].ID != id {
			return nil, errors.New("unknown or repeated recovery target")
		}
		seen[id] = true
		t := c.After.Targets[index]
		if t.Current != nil && t.Current.Status == Completed {
			return nil, errors.New("completed deliveries cannot be retried")
		}
		if len(t.Attempts) >= p.Recovery.MaxAttempts {
			return nil, errors.New("original attempt limit exhausted")
		}
		r := RunReceipt{TargetID: id, RunID: "retry-1", Ordinal: len(t.Attempts) + 1}
		for _, old := range t.Attempts {
			if old.RunID == r.RunID {
				return nil, errors.New("allocated run ID conflicts with historical identity")
			}
		}
		if len(t.Attempts) > 0 {
			r.PreviousRunID = t.Attempts[len(t.Attempts)-1].RunID
		}
		out = append(out, r)
	}
	return out, nil
}

// ValidateRecoveryBudget 校验继承前缀与收据归属，不允许旧费用改写或释放。
func ValidateRecoveryBudget(seed, current budget.Snapshot, receipts []RunReceipt) error {
	if err := budget.ValidateSnapshot(current); err != nil {
		return err
	}
	if current.SchemaVersion != 2 || current.CampaignID != seed.CampaignID || current.Limits != seed.Limits || !reflect.DeepEqual(current.Profile, seed.Profile) || !reflect.DeepEqual(current.RecoveryIDs, seed.RecoveryIDs) || current.RecoveryRevision != seed.RecoveryRevision || current.Revision < seed.Revision || len(current.Calls) < len(seed.Calls) || len(current.Rejections) < len(seed.Rejections) || len(current.Targets) != len(seed.Targets) {
		return errors.New("recovery ledger does not inherit the checkpoint")
	}
	if !reflect.DeepEqual(current.Calls[:len(seed.Calls)], seed.Calls) || !reflect.DeepEqual(current.Rejections[:len(seed.Rejections)], seed.Rejections) {
		return errors.New("historical calls or rejections changed")
	}
	bindings := map[string]string{}
	for _, r := range receipts {
		bindings[r.TargetID] = r.RunID
	}
	for id, a := range current.Targets {
		old, ok := seed.Targets[id]
		if !ok || a.Limits != old.Limits || len(a.RunIDs) < len(old.RunIDs) || !slices.Equal(a.RunIDs[:len(old.RunIDs)], old.RunIDs) {
			return errors.New("historical target binding changed")
		}
		extra := a.RunIDs[len(old.RunIDs):]
		if len(extra) > 1 || len(extra) == 1 && (extra[0] != bindings[id] || a.RunID != extra[0]) || len(extra) == 0 && a.RunID != "" {
			return fmt.Errorf("active binding lacks its recovery receipt: target=%s active=%s history=%v inherited=%v allocated=%s", id, a.RunID, a.RunIDs, old.RunIDs, bindings[id])
		}
	}
	for _, c := range current.Calls[len(seed.Calls):] {
		if bindings[c.TargetID] != c.RunID || c.RecoveryID != "" {
			return errors.New("new call lacks its recovery receipt")
		}
	}
	for _, r := range current.Rejections[len(seed.Rejections):] {
		if bindings[r.TargetID] != r.RunID {
			return errors.New("new rejection lacks its recovery receipt")
		}
	}
	return nil
}
