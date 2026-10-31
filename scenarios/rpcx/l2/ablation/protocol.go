package ablation

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"regexp"
	"time"
)

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)

// Decode rejects ambiguous documents, misspelled options and trailing values.
func Decode(data []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("expected exactly one JSON value")
	}
	return nil
}

func digestOK(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && s == fmt.Sprintf("%x", b)
}

// Schedule rotates group order between tasks and repetitions, then executes
// serially. Repetition is a block label, not a claim of paired model sampling.
func Schedule(tasks []Task, repetitions int) []Slot {
	var slots []Slot
	groups := []Group{A, B, C}
	for r := 1; r <= repetitions; r++ {
		for i, task := range tasks {
			for j := 0; j < len(groups); j++ {
				g := groups[(i+r-1+j)%len(groups)]
				slots = append(slots, Slot{ID: fmt.Sprintf("%s-%s-%02d", task.ID, g, r), TaskID: task.ID, Group: g, Repetition: r})
			}
		}
	}
	return slots
}

// Validate checks a protocol that is ready to freeze. Human review, source
// inventory and spending authorization are verified separately at launch.
func (p Protocol) Validate() error {
	if p.SchemaVersion != 1 || !identifier.MatchString(p.ID) || (p.Kind != "formal" && p.Kind != "development_calibration") {
		return errors.New("unsupported P1 protocol identity or kind")
	}
	if p.Analysis != "independent-generations" || p.FailurePolicy != "retain-all-no-retry-stop-on-shared-integrity-failure" {
		return errors.New("explicit independent analysis and no-retry failure policy required")
	}
	if p.Repetitions < 3 || p.Repetitions > 100 || len(p.Tasks) != 4 {
		return errors.New("P1 requires four tasks and at least three repetitions")
	}
	if p.Model.Provider == "" || p.Model.Model == "" || p.Model.Endpoint == "" || p.Model.ContextTokens < 1 || p.Model.MaxOutputTokens < 1 || !digestOK(p.Model.SourceSHA256) || p.Model.VerifiedAt.IsZero() || p.Model.ContextTokens > math.MaxInt64-p.Model.MaxOutputTokens {
		return errors.New("verified model identity and token upper bounds required")
	}
	if p.Execution.Kind != "docker" || !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(p.Execution.Image) || p.Execution.CPUs < 1 || p.Execution.MemoryBytes < 32<<20 || p.Execution.Pids < 8 {
		return errors.New("a pinned Docker image and positive resource limits are required")
	}
	if err := p.Resources.Validate(); err != nil {
		return err
	}
	if p.Limits.MaxModelCalls < 1 || p.Limits.MaxToolCalls < 1 || p.AttemptBudget.MaxCalls != int64(p.Limits.MaxModelCalls) || p.AttemptBudget.MaxTokens < p.Model.ContextTokens+p.Model.MaxOutputTokens {
		return errors.New("attempt budget must cover one conservative reservation and match call limits")
	}
	n := int64(12 * p.Repetitions)
	if p.AttemptBudget.MaxCalls > math.MaxInt64/n || p.AttemptBudget.MaxTokens > math.MaxInt64/n || p.TotalBudget.MaxCalls != n*p.AttemptBudget.MaxCalls || p.TotalBudget.MaxTokens != n*p.AttemptBudget.MaxTokens {
		return errors.New("total hard budget must equal all scheduled attempt allowances")
	}
	for _, d := range []time.Duration{p.PreparationTimeout, p.GenerationTimeout, p.ValidationTimeout, p.ReferenceTimeout, p.HostTimeout} {
		if d <= 0 || d > 7*24*time.Hour {
			return errors.New("all stage and host time limits must be positive and at most seven days")
		}
	}
	wantProfiles := map[string]string{"goddd": "rpcx-goddd-t3-v1", "translation": "rpcx-translation-t3-v2", "workouts": "rpcx-workouts-t3-v1", "ghcli": "rpcx-ghcli-t3-v1"}
	for _, t := range p.Tasks {
		profile, ok := wantProfiles[t.ID]
		if !ok || t.Input.ProfileID != profile || t.Input.ID != t.ID {
			return errors.New("four unique seen tasks with current profiles required")
		}
		delete(wantProfiles, t.ID)
		if err := t.Input.Validate(); err != nil {
			return fmt.Errorf("%s: %w", t.ID, err)
		}
		if !digestOK(t.ReferenceSHA256) || !digestOK(t.ReferenceToolSHA256) || t.ReferenceVersion == "" {
			return errors.New("reference data and tool pins required")
		}
		limit := 0
		if t.ID == "translation" || t.ID == "workouts" {
			limit = 2
		}
		if t.DiagnosticLimit != limit {
			return errors.New("full generation diagnostics must match supported task policy")
		}
	}
	if len(p.Schedule) != int(n) {
		return errors.New("schedule must contain every planned attempt")
	}
	seen := map[string]bool{}
	cells := map[string]bool{}
	for _, s := range p.Schedule {
		if !identifier.MatchString(s.ID) || seen[s.ID] || (s.Group != A && s.Group != B && s.Group != C) || s.Repetition < 1 || s.Repetition > p.Repetitions {
			return errors.New("invalid or duplicate scheduled slot")
		}
		found := false
		for _, t := range p.Tasks {
			found = found || s.TaskID == t.ID
		}
		cell := fmt.Sprintf("%s/%s/%d", s.TaskID, s.Group, s.Repetition)
		if !found || cells[cell] {
			return errors.New("invalid or duplicate task/group/repetition")
		}
		seen[s.ID], cells[cell] = true, true
	}
	if p.Price != nil {
		return p.Price.Validate()
	}
	return nil
}

func (p Price) Validate() error {
	u, err := url.Parse(p.Source)
	if len(p.Currency) != 3 || err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("currency and HTTPS price source required")
	}
	if _, err = time.Parse(time.RFC3339, p.RecordedAt); err != nil {
		return err
	}
	for _, v := range []float64{p.InputPerMillion, p.CachedInputPerMillion, p.OutputPerMillion} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return errors.New("invalid price")
		}
	}
	return nil
}

func (p Price) ConservativeAmount(tokens int64) (float64, error) {
	if err := p.Validate(); err != nil {
		return 0, err
	}
	if tokens < 0 {
		return 0, errors.New("negative token allowance")
	}
	return float64(tokens) * math.Max(p.InputPerMillion, math.Max(p.CachedInputPerMillion, p.OutputPerMillion)) / 1e6, nil
}
