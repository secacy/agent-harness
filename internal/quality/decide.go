package quality

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Decide 校验材料后聚合。错误表示记录不可信，返回空判定；缺少证据则保留缺口。
// 调用方仍须在执行失败时停止评估，不能拿部分结果替代一次完成的验证。
func Decide(root string, policy Policy, binding Binding, checks map[string]Check, evidence []Evidence) (Decision, error) {
	if !validDigest(binding.SpecSHA256) || !validDigest(binding.BaselineSHA256) || !validDigest(binding.CandidateSHA256) || binding.SpecSHA256 != policy.SpecSHA256 {
		return Decision{}, errors.New("invalid evaluation binding")
	}
	if len(policy.Requirements) == 0 {
		return Decision{}, errors.New("quality policy must require checks")
	}
	requirements := map[string]Requirement{}
	for _, r := range policy.Requirements {
		if r.ID == "" || len(r.RuleIDs) == 0 {
			return Decision{}, errors.New("invalid quality requirement")
		}
		if _, exists := requirements[r.ID]; exists {
			return Decision{}, fmt.Errorf("duplicate requirement: %s", r.ID)
		}
		for _, rule := range r.RuleIDs {
			if rule == "" {
				return Decision{}, errors.New("empty policy rule")
			}
		}
		requirements[r.ID] = r
	}
	for id := range checks {
		if _, ok := requirements[id]; !ok {
			return Decision{}, fmt.Errorf("unexpected check: %s", id)
		}
	}
	known := map[string]bool{}
	for _, e := range evidence {
		if e.ID == "" || e.Kind == "" || known[e.ID] {
			return Decision{}, fmt.Errorf("invalid or duplicate evidence: %s", e.ID)
		}
		if e.Binding != binding {
			return Decision{}, fmt.Errorf("evidence binding mismatch: %s", e.ID)
		}
		if err := verifyFile(root, e); err != nil {
			return Decision{}, err
		}
		known[e.ID] = true
	}
	decision := Decision{Verdict: ReadyForReview, Gaps: []Gap{}}
	failed := false
	for _, req := range policy.Requirements {
		check, ok := checks[req.ID]
		if !ok {
			decision.Gaps = append(decision.Gaps, Gap{req.ID, "check_missing", "required check was not returned"})
			continue
		}
		if err := validateCheck(req, check); err != nil {
			return Decision{}, err
		}
		gap := func(code, detail string) { decision.Gaps = append(decision.Gaps, Gap{req.ID, code, detail}) }
		explained := check.Detail != "" && len(check.Findings) != 0
		if check.Detail == "" || len(check.Findings) == 0 {
			gap("finding_missing", "check has no explanation")
		}
		for _, rule := range req.RuleIDs {
			if !slices.Contains(check.RuleIDs, rule) {
				gap("rule_missing", "required clause is not associated: "+rule)
			}
		}
		referenced := map[string]bool{}
		for _, id := range check.EvidenceIDs {
			referenced[id] = true
		}
		for _, f := range check.Findings {
			for _, id := range f.EvidenceIDs {
				referenced[id] = true
			}
		}
		if len(referenced) == 0 {
			gap("evidence_missing", "check has no evidence reference")
		}
		materialComplete := len(referenced) != 0
		for _, id := range sortedKeys(referenced) {
			if !known[id] {
				materialComplete = false
				gap("evidence_missing", "unresolved evidence reference: "+id)
			}
		}
		if check.Applicability == NotApplicable {
			if !req.AllowNotApplicable {
				gap("not_applicable_disallowed", "host policy requires this check")
			}
			if check.Status != StatusSkipped {
				return Decision{}, fmt.Errorf("not-applicable check must be skipped: %s", req.ID)
			}
		} else {
			switch check.Status {
			case StatusPass:
			case StatusFail:
				if explained && materialComplete {
					failed = true
				}
			default:
				gap("check_incomplete", "check did not establish a result: "+check.Status)
			}
		}
	}
	if failed {
		decision.Verdict = Rejected
	} else if len(decision.Gaps) != 0 {
		decision.Verdict = NeedsAttention
	}
	return decision, nil
}

func validateCheck(req Requirement, check Check) error {
	switch check.Status {
	case StatusPass, StatusFail, StatusUnknown, StatusBlocked, StatusSkipped, StatusNotRun:
	default:
		return fmt.Errorf("invalid check status: %s/%s", req.ID, check.Status)
	}
	if check.Applicability != Applicable && check.Applicability != NotApplicable {
		return fmt.Errorf("invalid applicability: %s", req.ID)
	}
	for _, rule := range check.RuleIDs {
		if !slices.Contains(req.RuleIDs, rule) {
			return fmt.Errorf("unexpected clause %s in check %s", rule, req.ID)
		}
	}
	for _, f := range check.Findings {
		if f.Code == "" || f.Detail == "" || len(f.RuleIDs) == 0 {
			return fmt.Errorf("invalid finding in check: %s", req.ID)
		}
		for _, rule := range f.RuleIDs {
			if !slices.Contains(req.RuleIDs, rule) {
				return fmt.Errorf("unexpected finding clause %s in check %s", rule, req.ID)
			}
		}
		for _, loc := range f.Locations {
			if (loc.Tree != "baseline" && loc.Tree != "candidate") || !safePath(loc.Path) || loc.Line < 0 {
				return fmt.Errorf("invalid finding location in check: %s", req.ID)
			}
		}
	}
	return nil
}

func verifyFile(root string, e Evidence) error {
	if !safePath(e.Path) || !validDigest(e.SHA256) {
		return fmt.Errorf("invalid evidence path or hash: %s", e.ID)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return err
	}
	path := resolved
	for _, part := range strings.Split(e.Path, "/") {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("evidence %s: %w", e.ID, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("evidence traverses symlink: %s", e.ID)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("evidence is not a regular file: %s", e.ID)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != e.SHA256 {
		return fmt.Errorf("evidence content changed: %s", e.ID)
	}
	return nil
}

func validDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && value == strings.ToLower(value)
}
func safePath(path string) bool {
	return path != "" && path != "." && filepath.IsLocal(path) && filepath.ToSlash(filepath.Clean(path)) == path && !strings.Contains(path, "\\")
}
func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
