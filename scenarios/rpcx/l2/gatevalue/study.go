// Package gatevalue 只读回顾固定历史候选的构建测试证据与原门禁，不生成新效果指标。
package gatevalue

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/secacy/agent-harness/internal/audit"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/internal/task"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
	"github.com/secacy/agent-harness/scenarios/rpcx/runner"
	"github.com/secacy/agent-harness/scenarios/rpcx/translation"
)

type Case struct {
	Cohort       string `json:"cohort"`
	Target       string `json:"target"`
	RunID        string `json:"run_id"`
	Candidate    string `json:"candidate_sha256"`
	Report       string `json:"report"`
	ReportSHA    string `json:"report_sha256"`
	Review       string `json:"review"`
	ReviewSHA    string `json:"review_sha256"`
	Reference    string `json:"reference"`
	ReferenceSHA string `json:"reference_sha256"`
	Spec         string `json:"spec_sha256"`
	Baseline     string `json:"baseline_sha256"`
	Materials    string `json:"materials_sha256"`
}
type Manifest struct {
	Schema     int    `json:"schema_version"`
	Status     string `json:"status"`
	Kind       string `json:"kind"`
	Generation bool   `json:"generation_allowed"`
	Cases      []Case `json:"cases"`
}
type Cost struct {
	Count        int   `json:"count"`
	Milliseconds int64 `json:"recorded_command_ms_sum"`
}
type Row struct {
	Case            Case                             `json:"case"`
	BuildTests      string                           `json:"build_tests"`
	OriginalVerdict string                           `json:"original_verdict,omitempty"`
	Checks          map[string]quality.Check         `json:"original_checks,omitempty"`
	Automatic       evaluation.Status                `json:"automatic,omitempty"`
	Assessment      evaluation.Status                `json:"assessment,omitempty"`
	Review          map[string]evaluation.ReviewItem `json:"review_items,omitempty"`
	Commands        []Command                        `json:"commands,omitempty"`
	Costs           map[string]Cost                  `json:"costs,omitempty"`
	ReferenceGap    string                           `json:"reference_gap,omitempty"`
	Error           string                           `json:"error,omitempty"`
}
type Command struct {
	Name         string `json:"name"`
	Category     string `json:"category"`
	Milliseconds int64  `json:"recorded_command_ms"`
}
type Source struct {
	Path string `json:"path"`
	SHA  string `json:"sha256"`
}
type Report struct {
	Kind            string   `json:"kind"`
	Rows            []Row    `json:"rows"`
	Sources         []Source `json:"sources"`
	InputsUnchanged bool     `json:"inputs_unchanged"`
}
type reader struct {
	trees        map[string]string
	root, output string
	sources      map[string]string
}

func (r *reader) read(path, sha string) ([]byte, error) {
	if !filepath.IsLocal(path) {
		return nil, errors.New("source path must be relative")
	}
	p := filepath.Join(r.root, path)
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(r.root, resolved)
	if err != nil || !filepath.IsLocal(rel) || gate.Overlap(resolved, r.output) {
		return nil, errors.New("source crosses input boundary")
	}
	info, err := os.Lstat(p)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return nil, errors.New("source must be a bounded regular file")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	actual := gate.Digest(data)
	if sha != "" && sha != actual {
		return nil, fmt.Errorf("source digest mismatch: %s", path)
	}
	if old, ok := r.sources[path]; ok && old != actual {
		return nil, errors.New("source changed between reads")
	}
	r.sources[path] = actual
	return data, nil
}
func (r *reader) tree(path, sha string) error {
	if !filepath.IsLocal(path) || gate.Overlap(filepath.Join(r.root, path), r.output) {
		return errors.New("invalid tree path")
	}
	files, err := gate.ReadFiles(filepath.Join(r.root, path))
	if err != nil {
		return err
	}
	if gate.TreeHash(files) != sha {
		return fmt.Errorf("tree digest mismatch: %s", path)
	}
	if r.trees == nil {
		r.trees = map[string]string{}
	}
	r.trees[path] = sha
	for name, data := range files {
		if _, err = r.read(filepath.Join(path, name), gate.Digest(data)); err != nil {
			return err
		}
	}
	return nil
}

// Run 保存新报告并保留所有纳入行；单项缺失或冲突降为 insufficient，源漂移使整份结果失效。
func Run(ctx context.Context, root, manifest, output string) (Report, error) {
	out := Report{Kind: "seen_historical_evidence_projection"}
	var err error
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return out, err
	}
	output, err = gate.Canonical(output)
	if err != nil {
		return out, err
	}
	r := &reader{root: root, output: output, sources: map[string]string{}}
	raw, err := r.read(manifest, "")
	if err != nil {
		return out, err
	}
	var m Manifest
	if err = json.Unmarshal(raw, &m); err != nil {
		return out, err
	}
	if m.Schema != 1 || m.Generation || m.Kind != "historical_candidate_review" || len(m.Cases) == 0 {
		return out, errors.New("invalid historical manifest")
	}
	// 所有输出边界在创建目录之前检查，避免错误配置污染历史输入树。
	for _, c := range m.Cases {
		campaign := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(c.Report))))))
		for _, path := range []string{campaign, filepath.Dir(c.Reference), filepath.Dir(c.Review), filepath.Dir(manifest)} {
			if !filepath.IsLocal(path) {
				return out, errors.New("invalid input path")
			}
			resolved, e := gate.Canonical(filepath.Join(root, path))
			if e != nil {
				return out, e
			}
			if gate.Overlap(resolved, output) {
				return out, errors.New("output overlaps historical inputs")
			}
		}
	}
	if err = os.Mkdir(output, 0755); err != nil {
		return out, err
	}
	audits := map[string]error{}
	seen := map[string]bool{}
	for _, c := range m.Cases {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		for _, sha := range []string{c.Candidate, c.ReportSHA, c.ReviewSHA, c.ReferenceSHA, c.Spec, c.Baseline, c.Materials} {
			if b, e := hex.DecodeString(sha); e != nil || len(b) != 32 {
				return out, errors.New("invalid case digest")
			}
		}
		key := c.Cohort + "/" + c.RunID
		if seen[key] {
			return out, errors.New("duplicate historical case")
		}
		seen[key] = true
		row := Row{Case: c, BuildTests: "insufficient"}
		campaign := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(c.Report))))))
		if _, ok := audits[campaign]; !ok {
			if !filepath.IsLocal(campaign) || gate.Overlap(filepath.Join(root, campaign), output) {
				return out, errors.New("invalid campaign boundary")
			}
			a, e := audit.Inspect(ctx, audit.Request{CampaignDir: filepath.Join(root, campaign), OutputDir: filepath.Join(output, fmt.Sprintf("audit-%d", len(audits)))}, runner.InspectRun)
			if e == nil && (a.Consistency != "pass" || !a.ScanComplete || a.InputsUnchanged == nil || !*a.InputsUnchanged) {
				e = errors.New("historical campaign audit incomplete")
			}
			for _, s := range a.Sources {
				if s.Kind == "file" && s.SHA256 != "" {
					rel, re := filepath.Rel(root, filepath.Join(root, campaign, s.Path))
					if re == nil {
						_, re = r.read(rel, s.SHA256)
					}
					e = errors.Join(e, re)
				}
			}
			audits[campaign] = e
		}
		e := r.analyze(c, &row)
		if audits[campaign] != nil {
			e = errors.Join(e, audits[campaign])
		}
		if e != nil {
			row.BuildTests = "insufficient"
			row.Error = e.Error()
		}
		out.Rows = append(out.Rows, row)
	}
	out.InputsUnchanged = true
	for path, sha := range r.trees {
		files, e := gate.ReadFiles(filepath.Join(root, path))
		if e != nil || gate.TreeHash(files) != sha {
			out.InputsUnchanged = false
			err = errors.Join(err, errors.New("source tree changed during study"))
		}
	}
	for path, sha := range r.sources {
		if _, e := r.read(path, sha); e != nil {
			out.InputsUnchanged = false
			err = errors.Join(err, e)
		}
		out.Sources = append(out.Sources, Source{path, sha})
	}
	// 输出使用固定顺序，便于复核同一材料。
	sortSources(out.Sources)
	if !out.InputsUnchanged {
		for i := range out.Rows {
			out.Rows[i].BuildTests = "insufficient"
		}
	}
	err = errors.Join(err, gate.WriteJSON(filepath.Join(output, "report.json"), out), gate.Write(filepath.Join(output, "summary.md"), markdown(out)))
	return out, err
}
func (r *reader) analyze(c Case, row *Row) error {
	raw, err := r.read(c.Report, c.ReportSHA)
	if err != nil {
		return err
	}
	var report runner.Report
	if err = json.Unmarshal(raw, &report); err != nil {
		return err
	}
	v := report.Validation
	if report.RunID != c.RunID || report.Input.Target == nil || report.Input.Target.ID != c.Target || v == nil || v.CandidateSHA256 != c.Candidate || v.BaselineSHA256 != c.Baseline || v.SpecSHA256 != c.Spec || v.MaterialsSHA256 != c.Materials || report.CandidateSHA256 != c.Candidate || report.Status != runner.StatusCompleted {
		return errors.New("historical report identity mismatch")
	}
	row.OriginalVerdict = v.Verdict
	row.Checks = v.Checks
	run := filepath.Dir(filepath.Dir(c.Report))
	repo := report.Input.RepositoryName
	for path, sha := range map[string]string{"workspace/" + repo: c.Candidate, "baseline/" + repo: c.Baseline} {
		if err = r.tree(filepath.Join(run, path), sha); err != nil {
			return err
		}
	}
	evidence := map[string]string{}
	for _, e := range v.Evidence {
		if !filepath.IsLocal(e.Path) {
			return errors.New("unsafe evidence path")
		}
		if old, ok := evidence[e.Path]; ok && old != e.SHA256 {
			return errors.New("conflicting evidence path")
		}
		evidence[e.Path] = e.SHA256
		if _, err = r.read(filepath.Join(run, "evidence", e.Path), e.SHA256); err != nil {
			return err
		}
	}
	profileRaw, err := r.read(filepath.Join(run, "inputs/profile.json"), report.Input.ProfileSHA256)
	if err != nil {
		return err
	}
	var profile struct {
		Required       []string                  `json:"required_tests"`
		Configurations []task.BuildConfiguration `json:"build_configurations"`
		Prerequisites  map[string][]string       `json:"prerequisite_tests"`
	}
	if err = json.Unmarshal(profileRaw, &profile); err != nil {
		return err
	}
	if len(profile.Required) == 0 {
		return errors.New("required tests missing from profile")
	}
	required := requiredCommands(c.Target, profile.Required, profile.Prerequisites)
	if len(required) == 0 {
		return errors.New("unsupported historical target")
	}
	costs := map[string]Cost{}
	found := map[string]bool{}
	status := "supported"
	for _, cmd := range v.Commands {
		name := strings.TrimSuffix(strings.TrimPrefix(cmd.Log, "logs/"), ".stdout.log")
		if found[name] || cmd.DurationMS < 0 {
			return errors.New("invalid command inventory")
		}
		found[name] = true
		category := "unclassified"
		switch name {
		case "trusted-tests", "typed-command", "behavior", "behavior-quick", "behavior-expiry", "default-trusted-tests", "default-behavior-quick", "default-behavior-expiry", "rpcx_alternate-trusted-tests", "rpcx_alternate-behavior-quick", "rpcx_alternate-behavior-expiry":
			category = "additional_validation"
		}
		tests, needed := required[name]
		if needed {
			category = "build_test_evidence"
		}
		cost := costs[category]
		cost.Count++
		if cmd.DurationMS > math.MaxInt64-cost.Milliseconds {
			return errors.New("command duration sum overflow")
		}
		cost.Milliseconds += cmd.DurationMS
		costs[category] = cost
		row.Commands = append(row.Commands, Command{name, category, cmd.DurationMS})
		if !needed {
			continue
		}
		if cmd.TimedOut || cmd.Cancelled || cmd.OOMKilled || cmd.OutputLimit || cmd.ExitCode < 0 {
			return errors.New("required command has infrastructure failure")
		}
		data, err := r.read(filepath.Join(run, "evidence", cmd.Log), evidence[cmd.Log])
		if err != nil {
			return err
		}
		if evidence[cmd.Log] == "" {
			return errors.New("command log lacks evidence binding")
		}
		if len(tests) > 0 {
			state := testEvents(data, tests, cmd.ExitCode)
			if state == "insufficient" {
				return fmt.Errorf("required test events incomplete: %s", name)
			}
			if state == "failed" {
				status = "failed"
			}
		} else if cmd.ExitCode != 0 {
			status = "failed"
		}
		if c.Target == "translation" && strings.HasSuffix(name, "-list") {
			config := strings.Split(name, "-")[0]
			if err = translation.ValidateConfiguration(data, config); err != nil {
				return err
			}
		}
	}
	for name := range required {
		if !found[name] {
			return fmt.Errorf("required command missing: %s", name)
		}
	}
	row.Costs = costs
	reference, err := r.read(c.Reference, c.ReferenceSHA)
	if err != nil {
		return err
	}
	review, err := r.read(c.Review, c.ReviewSHA)
	if err != nil {
		return err
	}
	var ref evaluation.Report
	var rev evaluation.Review
	if err = json.Unmarshal(reference, &ref); err != nil {
		return err
	}
	if err = json.Unmarshal(review, &rev); err != nil {
		return err
	}
	if ref.RunID != c.RunID || ref.TargetID != c.Target || ref.CandidateSHA256 != c.Candidate || ref.BaselineSHA256 != c.Baseline || ref.SpecSHA256 != c.Spec {
		return errors.New("reference identity mismatch")
	}
	for path, sha := range ref.Artifacts {
		if _, err = r.read(filepath.Join(filepath.Dir(c.Reference), path), sha); err != nil {
			return err
		}
	}
	if rev.ReportSHA256 != c.ReferenceSHA || rev.RunID != c.RunID || rev.TargetID != c.Target || rev.CandidateSHA256 != c.Candidate {
		return errors.New("review identity mismatch")
	}
	row.Automatic = ref.Automatic
	row.Review = rev.Items
	row.BuildTests = status
	if ref.Version == "rpcx-l2-reference-ghcli-v2" {
		row.Assessment = evaluation.Inconclusive
		row.ReferenceGap = "legacy reference v2: recorded review and artifact identities preserved; current v3 assessor not applicable"
		return nil
	}
	assessment, err := evaluation.Assess(reference, review)
	if err != nil {
		return err
	}
	row.Automatic = ref.Automatic
	row.Assessment = assessment.Status
	row.Review = rev.Items
	row.BuildTests = status
	return nil
}
func requiredCommands(target string, tests []string, modules map[string][]string) map[string][]string {
	out := map[string][]string{}
	add := func(prefix, testName string) { out[prefix+"build"] = nil; out[prefix+testName] = tests }
	switch target {
	case "goddd":
		add("baseline-", "test")
		add("candidate-", "tests")
	case "ghcli":
		add("baseline-", "test")
		add("candidate-", "test")
	case "workouts":
		add("baseline-", "test")
		add("candidate-", "tests")
		out["common-build"] = nil
		out["common-test"] = modules["common"]
	case "translation":
		for _, config := range []string{"default", "rpcx_alternate"} {
			for _, side := range []string{"baseline", "candidate"} {
				prefix := config + "-" + side + "-"
				out[prefix+"list"] = nil
				add(prefix, "tests")
			}
		}
	default:
		return nil
	}
	return out
}
