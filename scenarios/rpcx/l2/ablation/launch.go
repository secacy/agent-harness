package ablation

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

// SpendingAuthorization is supplied by the host from an actual user decision.
// These fields authenticate structure and bindings, not the person's identity
// or whether the claimed authorization really occurred. No API fills it in.
type SpendingAuthorization struct {
	SchemaVersion      int            `json:"schema_version"`
	Decision           string         `json:"decision"` // authorized; templates remain pending.
	FreezeSHA256       string         `json:"freeze_sha256"`
	ProtocolSHA256     string         `json:"protocol_sha256"`
	Model              budget.Profile `json:"model"`
	MaxCalls           int64          `json:"max_calls"`
	MaxTokens          int64          `json:"max_tokens"`
	Currency           string         `json:"currency"`
	MaxAmount          *float64       `json:"max_amount"`
	AllowUnknownAmount bool           `json:"allow_unknown_amount"`
	Authorizer         string         `json:"authorizer"`
	RecordedAt         string         `json:"recorded_at"`
	Evidence           []string       `json:"evidence"`
	Reason             string         `json:"reason"`
}

// ValidateTaskReviews requires one accepted, fully attributed record per task.
// The caller remains responsible for obtaining genuine human review rather
// than substituting an assistant's observations or fabricated attestations.
func ValidateTaskReviews(p Protocol, reviews []TaskReview) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if len(reviews) != len(p.Tasks) {
		return errors.New("every frozen task requires one actual accepted human review")
	}
	seen := map[string]bool{}
	for _, review := range reviews {
		task := taskFor(p, review.Task.ID)
		if task.ID == "" || seen[task.ID] || !reflect.DeepEqual(task, review.Task) {
			return errors.New("task review differs from frozen task pins or repeats a task")
		}
		if review.Decision != "accepted" || !attributed(review.Reviewer, review.Relationship, review.Reason) || !evidencePresent(review.Evidence) {
			return fmt.Errorf("%s requires an accepted human review with attribution, evidence and reason", task.ID)
		}
		if err := actualRecordTime(review.RecordedAt); err != nil {
			return fmt.Errorf("%s review: %w", task.ID, err)
		}
		seen[task.ID] = true
	}
	return nil
}

// ValidateSpendingAuthorization checks the supplied claim against a specific
// freeze and the entire planned allowance, never just an average expected run.
// A missing price can only proceed with explicit permission for unknown money;
// it cannot produce or validate a monetary ceiling.
func ValidateSpendingAuthorization(p Protocol, freeze FreezeManifest, a SpendingAuthorization) error {
	if err := p.Validate(); err != nil {
		return err
	}
	protocolRaw, err := canonicalJSON(p)
	if err != nil {
		return err
	}
	if gate.Digest(protocolRaw) != freeze.ProtocolSHA256 {
		return errors.New("spending protocol differs from freeze")
	}
	if a.SchemaVersion != 1 || a.Decision != "authorized" || !digestOK(freeze.FreezeSHA256) || a.FreezeSHA256 != freeze.FreezeSHA256 || a.ProtocolSHA256 != freeze.ProtocolSHA256 || !reflect.DeepEqual(a.Model, p.Model) || !reflect.DeepEqual(freeze.Model, p.Model) {
		return errors.New("spending authorization does not bind this freeze, protocol and model")
	}
	if a.MaxCalls < p.TotalBudget.MaxCalls || a.MaxTokens < p.TotalBudget.MaxTokens {
		return errors.New("planned hard allowance exceeds authorized calls or tokens")
	}
	if !attributed(a.Authorizer, a.Reason) || !evidencePresent(a.Evidence) {
		return errors.New("spending authorization requires actual attribution, evidence and reason")
	}
	if err := actualRecordTime(a.RecordedAt); err != nil {
		return err
	}
	if p.Price == nil {
		if !a.AllowUnknownAmount || a.MaxAmount != nil || a.Currency != "" {
			return errors.New("missing prices cannot support a monetary promise; explicit unknown-amount authorization is required")
		}
		return nil
	}
	if a.Currency != p.Price.Currency || a.MaxAmount == nil || !finiteAmount(*a.MaxAmount) {
		return errors.New("authorization requires a finite ceiling in the frozen price currency")
	}
	bound, err := p.Price.ConservativeAmount(p.TotalBudget.MaxTokens)
	if err != nil {
		return err
	}
	if !finiteAmount(bound) || bound > *a.MaxAmount {
		return errors.New("conservative total monetary allowance exceeds authorization")
	}
	return nil
}

// CheckLaunch completes all local read-only checks before the host may start a
// paid experiment. It does not consume generation tokens, write an authorized
// record, or prove the authenticity of human statements supplied by the host.
func CheckLaunch(ctx context.Context, freezeDir, projectDir, binaryPath string, reviews []TaskReview, a SpendingAuthorization) (FreezeManifest, error) {
	manifest, err := CheckFreeze(ctx, freezeDir, projectDir, binaryPath)
	if err != nil {
		return manifest, err
	}
	raw, err := readLocal(freezeDir, "protocol.json")
	if err != nil {
		return manifest, err
	}
	var p Protocol
	if err = Decode(raw, &p); err != nil {
		return manifest, err
	}
	if err = ValidateTaskReviews(p, reviews); err != nil {
		return manifest, err
	}
	if err = ValidateSpendingAuthorization(p, manifest, a); err != nil {
		return manifest, err
	}
	return manifest, nil
}
func attributed(fields ...string) bool {
	for _, field := range fields {
		if strings.TrimSpace(field) == "" || field != strings.TrimSpace(field) {
			return false
		}
	}
	return true
}
func evidencePresent(evidence []string) bool {
	if len(evidence) == 0 {
		return false
	}
	for _, item := range evidence {
		if !attributed(item) {
			return false
		}
	}
	return true
}
func actualRecordTime(value string) error {
	at, err := time.Parse(time.RFC3339, value)
	if err != nil || at.IsZero() {
		return errors.New("actual RFC3339 record time required")
	}
	return nil
}
