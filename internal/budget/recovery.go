package budget

import (
	"fmt"
	"math"
	"slices"
	"time"
)

func addTotal(a, b Totals) (Totals, bool) {
	out := a
	for _, pair := range [][2]*int64{{&out.Calls, &b.Calls}, {&out.KnownTokens, &b.KnownTokens}, {&out.ReservedTokens, &b.ReservedTokens}, {&out.UnknownTokens, &b.UnknownTokens}} {
		if *pair[0] < 0 || *pair[1] < 0 || *pair[0] > math.MaxInt64-*pair[1] {
			return Totals{}, false
		}
		*pair[0] += *pair[1]
	}
	return out, true
}

func withinLimits(t Totals, l Limits) bool {
	if !validLimits(l) || t.Calls < 0 || t.Calls > l.MaxCalls || t.KnownTokens < 0 || t.ReservedTokens < 0 || t.UnknownTokens < 0 {
		return false
	}
	free := l.MaxTokens - t.KnownTokens
	return free >= 0 && t.ReservedTokens <= free && t.UnknownTokens <= free-t.ReservedTokens
}

func hasRun(s Snapshot, a Account, id string) bool {
	if s.SchemaVersion == 1 {
		return a.RunID == id
	}
	return slices.Contains(a.RunIDs, id)
}

// ValidateSnapshot 只校验已解码的数据与逐调用账目，不读取磁盘或恢复准入。
func ValidateSnapshot(s Snapshot) error {
	invalid := func(reason string) error { return fmt.Errorf("%w: %s", ErrIntegrity, reason) }
	if (s.SchemaVersion != 1 && s.SchemaVersion != 2) || !identifier.MatchString(s.CampaignID) || s.Revision < 1 || s.Profile.Validate() != nil || !withinLimits(s.Totals, s.Limits) || len(s.Targets) == 0 || s.Calls == nil || s.Rejections == nil {
		return invalid("invalid snapshot identity, shape or limits")
	}
	if s.SchemaVersion == 1 && (len(s.RecoveryIDs) != 0 || s.RecoveryRevision != 0) || s.SchemaVersion == 2 && (len(s.RecoveryIDs) == 0 || s.RecoveryRevision < 1 || s.RecoveryRevision > s.Revision) {
		return invalid("recovery history does not match the snapshot version")
	}
	seenRecovery := map[string]bool{}
	for _, id := range s.RecoveryIDs {
		if !identifier.MatchString(id) || seenRecovery[id] {
			return invalid("invalid or repeated recovery ID")
		}
		seenRecovery[id] = true
	}
	for id, a := range s.Targets {
		if !identifier.MatchString(id) || !withinLimits(a.Totals, a.Limits) || a.RunID != "" && !identifier.MatchString(a.RunID) {
			return invalid("invalid target account")
		}
		seen := map[string]bool{}
		for _, run := range a.RunIDs {
			if !identifier.MatchString(run) || seen[run] {
				return invalid("invalid or repeated run binding")
			}
			seen[run] = true
		}
		if s.SchemaVersion == 1 && len(a.RunIDs) != 0 || s.SchemaVersion == 2 && a.RunID != "" && !seen[a.RunID] {
			return invalid("run bindings do not match the snapshot version")
		}
	}
	totals, accounts, calls := Totals{}, map[string]Totals{}, map[string]bool{}
	q := s.Profile.ContextTokens + s.Profile.MaxOutputTokens
	for _, c := range s.Calls {
		a, ok := s.Targets[c.TargetID]
		if !ok || !identifier.MatchString(c.ID) || calls[c.ID] || !identifier.MatchString(c.RunID) || !hasRun(s, a, c.RunID) || !validDigest(c.InputSHA256) || c.ReservedTokens != q || c.CreatedAt.IsZero() || c.UpdatedAt.IsZero() {
			return invalid("invalid call identity or reservation")
		}
		calls[c.ID] = true
		n := Totals{Calls: 1}
		switch c.State {
		case StateReserved:
			if c.Usage != nil || c.Outcome != "" || c.RecoveryID != "" {
				return invalid("reserved call has settlement fields")
			}
			n.ReservedTokens = q
		case StateSettled:
			if c.Usage == nil || !validUsage(c.Usage, s.Profile) {
				return invalid("settled call lacks valid usage")
			}
			n.KnownTokens = c.Usage.TotalTokens
		case StateUnknown:
			if c.Usage != nil {
				return invalid("unknown call carries usage")
			}
			n.UnknownTokens = q
		case StateInvalid:
			if c.Usage == nil || validUsage(c.Usage, s.Profile) || s.HaltReason == "" {
				return invalid("invalid usage lacks a shared halt")
			}
			n.UnknownTokens = q
		default:
			return invalid("unsupported call state")
		}
		if c.State != StateReserved {
			if c.Outcome == "interrupted" {
				if c.State != StateUnknown || s.SchemaVersion != 2 || !seenRecovery[c.RecoveryID] {
					return invalid("interrupted call lacks recovery provenance")
				}
			} else if (c.Outcome != "reply" && c.Outcome != "model_error") || c.RecoveryID != "" {
				return invalid("invalid settlement outcome")
			}
		}
		var added bool
		if totals, added = addTotal(totals, n); !added {
			return invalid("campaign recomputation overflows")
		}
		if accounts[c.TargetID], added = addTotal(accounts[c.TargetID], n); !added {
			return invalid("target recomputation overflows")
		}
	}
	if totals != s.Totals {
		return invalid("campaign totals differ from calls")
	}
	for id, a := range s.Targets {
		if a.Totals != accounts[id] {
			return invalid("target totals differ from calls")
		}
	}
	for _, r := range s.Rejections {
		a, ok := s.Targets[r.TargetID]
		if !ok || !identifier.MatchString(r.RunID) || !hasRun(s, a, r.RunID) || !validDigest(r.InputSHA256) || r.At.IsZero() || (r.Scope != "campaign" && r.Scope != "target") || (r.Resource != "calls" && r.Resource != "tokens") {
			return invalid("invalid rejected request")
		}
	}
	return nil
}

// RecoverSnapshot 生成不可变恢复检查点中的账目，不打开账本或授权模型调用。
// 同一恢复 ID 只转换一次；所有旧 Run 归属和累计占用保留，当前准入绑定关闭。
func RecoverSnapshot(s Snapshot, id string, at time.Time) (Snapshot, error) {
	if err := ValidateSnapshot(s); err != nil {
		return Snapshot{}, err
	}
	if !identifier.MatchString(id) || at.IsZero() || s.HaltReason != "" {
		return Snapshot{}, fmt.Errorf("%w: recovery identity, timestamp and non-halted ledger are required", ErrIntegrity)
	}
	if slices.Contains(s.RecoveryIDs, id) {
		if s.RecoveryIDs[len(s.RecoveryIDs)-1] != id || s.Revision != s.RecoveryRevision || s.Totals.ReservedTokens != 0 {
			return Snapshot{}, fmt.Errorf("%w: recovery ID belongs to an older checkpoint", ErrIntegrity)
		}
		return cloneSnapshot(s), nil
	}
	if s.Revision == math.MaxInt64 {
		return Snapshot{}, fmt.Errorf("%w: ledger revision overflows", ErrIntegrity)
	}
	next := cloneSnapshot(s)
	next.SchemaVersion, next.Revision = 2, s.Revision+1
	next.RecoveryRevision = next.Revision
	next.RecoveryIDs = append(next.RecoveryIDs, id)
	for target, a := range next.Targets {
		if s.SchemaVersion == 1 && a.RunID != "" {
			a.RunIDs = []string{a.RunID}
		}
		a.RunID = ""
		next.Targets[target] = a
	}
	for n := range next.Calls {
		c := &next.Calls[n]
		if c.State != StateReserved {
			continue
		}
		c.State, c.Outcome, c.RecoveryID, c.UpdatedAt = StateUnknown, "interrupted", id, at.UTC()
		a := next.Targets[c.TargetID]
		a.Totals.ReservedTokens -= c.ReservedTokens
		a.Totals.UnknownTokens += c.ReservedTokens
		next.Targets[c.TargetID] = a
		next.Totals.ReservedTokens -= c.ReservedTokens
		next.Totals.UnknownTokens += c.ReservedTokens
	}
	return next, ValidateSnapshot(next)
}
