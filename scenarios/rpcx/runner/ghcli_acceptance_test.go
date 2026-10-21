package runner_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/audit"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/task"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/round"
	"github.com/secacy/agent-harness/scenarios/rpcx/runner"
)

type closureCase struct{ ID, Input, SHA256, Runtime, Reference string }
type closureMatrix struct {
	Cases    []closureCase
	TargetID string `json:"target_id"`
}
type closureModel struct {
	profile budget.Profile
	step    int
	edits   []agent.ToolCall
}

func (m *closureModel) BudgetProfile() (budget.Profile, error) { return m.profile, nil }
func closureCall(id, name string, args any) agent.ToolCall {
	raw, _ := json.Marshal(args)
	return agent.ToolCall{ID: id, Name: name, Arguments: string(raw)}
}
func (m *closureModel) Complete(_ context.Context, messages []agent.Message, tools []agent.ToolSpec) (agent.Reply, error) {
	m.step++
	for _, v := range messages {
		if v.Role == agent.RoleTool {
			denied := strings.HasPrefix(v.ToolCallID, "deny-")
			if v.IsError != denied {
				return agent.Reply{}, fmt.Errorf("unexpected tool result for %s: %s", v.ToolCallID, v.Content)
			}
		}
	}
	r := agent.Reply{Usage: &agent.Usage{InputTokens: 15, OutputTokens: 5, TotalTokens: 20}}
	switch m.step {
	case 1:
		for _, tool := range tools {
			if tool.Name == "read_file" {
				var schema struct {
					Properties map[string]struct{ Enum []string }
				}
				if json.Unmarshal(tool.Parameters, &schema) != nil {
					return r, fmt.Errorf("invalid tool schema")
				}
				roots := schema.Properties["root"].Enum
				sort.Strings(roots)
				if strings.Join(roots, ",") != "rpcx-v1,rpcx-v2,spec,workspace" {
					return r, fmt.Errorf("unexpected visible roots: %v", roots)
				}
			}
		}
		r.Finish = agent.FinishToolCalls
		r.ToolCalls = append(r.ToolCalls,
			closureCall("deny-list", "list_files", map[string]string{"root": "workspace", "path": "../../../../private"}),
			closureCall("deny-read", "read_file", map[string]string{"root": "reference", "path": "freeze.json"}),
			closureCall("deny-search", "search", map[string]string{"root": "workspace", "path": "../..", "text": "sentinel"}))
		r.ToolCalls = append(r.ToolCalls, m.edits...)
	case 2:
		r.Finish = agent.FinishToolCalls
		r.ToolCalls = []agent.ToolCall{closureCall("build", "go_check", map[string]string{"kind": "build"}), closureCall("test", "go_check", map[string]string{"kind": "test"})}
	case 3:
		r.Finish = agent.FinishStop
		r.Content = "Controlled calibration candidate submitted."
	default:
		return r, fmt.Errorf("controlled model exceeded fixed calls")
	}
	return r, nil
}

type closureCommand struct {
	OriginalArgs, ActualArgs, Mounts []string
	Directory, ContainerID, Error    string
	ExitCode                         int
	DurationMS                       int64
	Sentinel, Removed                bool
}
type closureObserver struct {
	backend  execution.Executor
	dir      string
	mu       sync.Mutex
	commands []closureCommand
	hidden   []string
}

func (b *closureObserver) Info() execution.Info { return b.backend.Info() }
func (b *closureObserver) Run(ctx context.Context, req execution.Request) (execution.Result, error) {
	c := closureCommand{OriginalArgs: append([]string{}, req.Args...), Mounts: append([]string{}, req.ReadOnlyDirs...), Directory: req.Directory}
	// 测试观察器明确记录插入动作；仍执行原命令并原样传播其退出状态。
	if strings.Contains(filepath.ToSlash(req.Directory), "/workspace/ghcli") {
		c.Sentinel = true
		workspace := filepath.Dir(req.Directory)
		runRoot := filepath.Dir(workspace)
		allowed := map[string]bool{req.Directory: true, filepath.Join(workspace, "rpcx-v1"): true, filepath.Join(workspace, "rpcx-v2"): true, filepath.Join(runRoot, "host-materials/rpcx-ghcli-modules"): true}
		if len(req.ReadOnlyDirs) != len(allowed) {
			return execution.Result{}, fmt.Errorf("unexpected generation mount count")
		}
		for _, p := range req.ReadOnlyDirs {
			if !allowed[p] {
				return execution.Result{}, fmt.Errorf("unexpected generation mount: %s", p)
			}
		}
		script := `count=$1; shift; while [ "$count" -gt 0 ]; do test ! -e "$1" || exit 91; shift; count=$((count-1)); done; printf 'HARNESS_VISIBILITY_SENTINEL_PASS\n'; exec "$@"`
		prefix := []string{"sh", "-c", script, "closure-observer", strconv.Itoa(len(b.hidden))}
		prefix = append(prefix, b.hidden...)
		req.Args = append(prefix, req.Args...)
	}
	c.ActualArgs = append([]string{}, req.Args...)
	start := time.Now()
	r, err := b.backend.Run(ctx, req)
	c.DurationMS = time.Since(start).Milliseconds()
	c.ContainerID = r.ContainerID
	c.ExitCode = r.ExitCode
	if err != nil {
		c.Error = err.Error()
	}
	if r.ContainerID != "" {
		raw, e := exec.Command("docker", "inspect", r.ContainerID).CombinedOutput()
		c.Removed = e != nil && strings.Contains(strings.ToLower(string(raw)), "no such")
		if !c.Removed && err == nil {
			err = fmt.Errorf("execution container cleanup not confirmed")
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(b.commands)
	b.commands = append(b.commands, c)
	prefix := filepath.Join(b.dir, fmt.Sprintf("%03d", n))
	_ = closureSave(prefix+".json", c)
	_ = os.WriteFile(prefix+".stdout", r.Stdout, 0600)
	_ = os.WriteFile(prefix+".stderr", r.Stderr, 0600)
	if c.Sentinel && !bytes.Contains(r.Stdout, []byte("HARNESS_VISIBILITY_SENTINEL_PASS")) && err == nil {
		err = fmt.Errorf("visibility sentinel evidence missing")
	}
	return r, err
}
func closureSave(path string, v any) error {
	raw, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0755); e != nil {
		return e
	}
	return os.WriteFile(path, append(raw, '\n'), 0600)
}

// 校准包只使用相对输入；执行时由固定矩阵所在目录解析，不能依赖旧runs位置。
func closureLoadMatrix(path string) (closureMatrix, error) {
	var m closureMatrix
	err := closureRead(path, &m)
	if err != nil {
		return m, err
	}
	for i := range m.Cases {
		if !filepath.IsAbs(m.Cases[i].Input) {
			if !filepath.IsLocal(m.Cases[i].Input) {
				return m, fmt.Errorf("unsafe relative calibration input")
			}
			m.Cases[i].Input = filepath.Join(filepath.Dir(path), m.Cases[i].Input)
		}
	}
	return m, nil
}
func closureScenario() (string, error) {
	if p := os.Getenv("HARNESS_GHCLI_SCENARIO"); p != "" {
		if !filepath.IsAbs(p) {
			return "", fmt.Errorf("absolute scenario path required")
		}
		return p, nil
	}
	return filepath.Abs("..")
}

func closureRead(path string, v any) error {
	raw, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	return json.Unmarshal(raw, v)
}

// 仅显式启用时运行真实禁网容器；矩阵必须在调用前固定，生成中不直接写候选。
func TestGHCLIControlledAcceptance(t *testing.T) {
	matrixPath := os.Getenv("HARNESS_GHCLI_MATRIX")
	if matrixPath == "" {
		t.Skip("set HARNESS_GHCLI_MATRIX and HARNESS_GHCLI_OUTPUT for controlled Docker calibration")
	}
	output := os.Getenv("HARNESS_GHCLI_OUTPUT")
	if !filepath.IsAbs(matrixPath) || !filepath.IsAbs(output) {
		t.Fatal("absolute matrix and output paths required")
	}
	if err := os.Mkdir(output, 0755); err != nil {
		t.Fatal(err)
	}
	matrix, matrixErr := closureLoadMatrix(matrixPath)
	if err := matrixErr; err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(matrixPath)
	if err := closureSave(filepath.Join(output, "matrix-pin.json"), map[string]string{"sha256": gate.Digest(raw)}); err != nil {
		t.Fatal(err)
	}
	scenario, err := closureScenario()
	if err != nil {
		t.Fatal(err)
	}
	fixtures := filepath.Join(scenario, "l2/data/rpcx-l2-ghcli-v1/fixtures")
	spec := filepath.Join(scenario, "ghcli/specs/v0.6.0.md")
	input, err := runner.TargetFromGHCLI(matrix.TargetID, fixtures, spec)
	if err != nil {
		t.Fatal(err)
	}
	before, err := gate.ReadFiles(input.Repository.Directory)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Minute)
	defer cancel()
	docker, err := execution.NewDocker(ctx, execution.DockerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	limit := len(matrix.Cases)
	if s := os.Getenv("HARNESS_GHCLI_LIMIT"); s != "" {
		v, e := strconv.Atoi(s)
		if e != nil || v < 1 || v > limit {
			t.Fatal("invalid limit")
		}
		limit = v
	}
	for _, item := range matrix.Cases[:limit] {
		candidate, e := gate.ReadFiles(item.Input)
		if e != nil || gate.TreeHash(candidate) != item.SHA256 {
			t.Fatal("fixed complete candidate identity mismatch", e)
		}
		var paths []string
		for p, b := range candidate {
			if !bytes.Equal(b, before[p]) {
				paths = append(paths, p)
			}
		}
		sort.Strings(paths)
		var edits []agent.ToolCall
		for i, p := range paths {
			if len(before[p]) == 0 {
				t.Fatal("controlled edit requires existing file")
			}
			edits = append(edits, closureCall(fmt.Sprintf("edit-%d", i), "replace_text", map[string]string{"path": p, "old": string(before[p]), "new": string(candidate[p])}))
		}
		caseRoot := filepath.Join(output, item.ID)
		if e = os.Mkdir(caseRoot, 0755); e != nil {
			t.Fatal(e)
		}
		// 宿主正向哨兵先落盘并实读；候选和工具根不包含该私有父目录。
		markerHashes := map[string]string{}
		for _, kind := range []string{"runtime", "reference", "expected", "patches"} {
			marker := filepath.Join(caseRoot, "sentinel-scope", kind, "marker")
			if e = os.MkdirAll(filepath.Dir(marker), 0755); e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(marker, []byte("host-only generic visibility marker\n"), 0644); e != nil {
				t.Fatal(e)
			}
			raw, readErr := os.ReadFile(marker)
			if readErr != nil {
				t.Fatal(readErr)
			}
			markerHashes[marker] = gate.Digest(raw)
		}
		if e = closureSave(filepath.Join(caseRoot, "host-sentinels.json"), markerHashes); e != nil {
			t.Fatal(e)
		}
		hidden := []string{"/workspace/sentinel-scope", "/workspace/" + filepath.Base(caseRoot) + "/sentinel-scope", "/workspace/" + filepath.Base(output) + "/" + item.ID + "/sentinel-scope"}
		observer := &closureObserver{backend: docker, dir: filepath.Join(caseRoot, "execution"), hidden: hidden}
		if e = os.Mkdir(observer.dir, 0755); e != nil {
			t.Fatal(e)
		}
		shared, e := execution.NewLimited(observer, 1)
		if e != nil {
			t.Fatal(e)
		}
		profile := budget.Profile{Provider: "controlled", Endpoint: "in-process", Model: "scripted-test-model", ContextTokens: 90, MaxOutputTokens: 10, SourceSHA256: strings.Repeat("e", 64), VerifiedAt: time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)}
		config := campaign.Config{Directory: filepath.Join(caseRoot, "campaign"), Backend: shared, Execute: runner.CampaignExecutor(false), Plan: campaign.Plan{ID: item.ID, Concurrency: 1, ExecutionConcurrency: 1, Budget: budget.Limits{MaxCalls: 3, MaxTokens: 300}, Profile: profile, Targets: []campaign.Target{{Input: input, Budget: budget.Limits{MaxCalls: 3, MaxTokens: 300}, Limits: agent.Limits{MaxModelCalls: 3, MaxToolCalls: 40}, Timeout: 15 * time.Minute}}}, NewModel: func(task.Target) (budget.ProfiledModel, error) {
			return &closureModel{profile: profile, edits: edits}, nil
		}}
		record, summary, e := campaign.Run(ctx, config)
		_ = closureSave(filepath.Join(caseRoot, "execution-index.json"), observer.commands)
		if e != nil || record.Status != campaign.Completed || len(summary.Targets) != 1 || summary.Targets[0].Attempt == nil || summary.Targets[0].Attempt.Status != campaign.Completed {
			t.Fatalf("controlled campaign incomplete: %v %+v", e, summary.Counts)
		}
		a := summary.Targets[0].Attempt
		run := filepath.Join(record.Directory, filepath.FromSlash(a.Directory))
		var report runner.Report
		if e = closureRead(filepath.Join(record.Directory, filepath.FromSlash(a.Report.Path)), &report); e != nil {
			t.Fatal(e)
		}
		if report.CandidateSHA256 != item.SHA256 || summary.Budget.Totals.Calls != 3 || summary.Budget.Totals.KnownTokens != 60 {
			t.Fatal("candidate or shared budget mismatch")
		}
		// 原始补丁在新的完整基线上重放，不使用 Git 或修改已生成候选。
		replay := filepath.Join(caseRoot, "replay")
		if e = gate.CopyFiles(replay, before); e != nil {
			t.Fatal(e)
		}
		patch := exec.Command("patch", "-p1", "--batch", "--forward", "-i", filepath.Join(run, "artifacts/candidate.patch"))
		patch.Dir = replay
		patchOut, e := patch.CombinedOutput()
		_ = os.WriteFile(filepath.Join(caseRoot, "patch-replay.log"), patchOut, 0600)
		if e != nil {
			t.Fatal("patch replay failed", e)
		}
		replayFiles, e := gate.ReadFiles(replay)
		if e != nil || gate.TreeHash(replayFiles) != item.SHA256 {
			t.Fatal("patch replay identity mismatch", e)
		}
		auditReport, e := audit.Inspect(ctx, audit.Request{CampaignDir: record.Directory, OutputDir: filepath.Join(caseRoot, "audit")}, runner.InspectRun)
		if e != nil || auditReport.Consistency != "pass" || !auditReport.ScanComplete {
			_ = closureSave(filepath.Join(caseRoot, "audit-result.json"), auditReport)
			t.Fatalf("audit incomplete or inconsistent: %s %v", auditReport.Consistency, e)
		}
		reviewRaw, reviewErr := os.ReadFile(filepath.Join(run, "artifacts/review.md"))
		if reviewErr != nil {
			t.Fatal(reviewErr)
		}
		reviewLinks := regexp.MustCompile(`\]\(([^)]+)\)`).FindAllSubmatch(reviewRaw, -1)
		if len(reviewLinks) < 3 {
			t.Fatal("review delivery links missing")
		}
		for _, link := range reviewLinks {
			name := strings.SplitN(string(link[1]), "#", 2)[0]
			if name == "" {
				continue
			}
			resolved, linkErr := gate.Canonical(filepath.Join(run, "artifacts", filepath.FromSlash(name)))
			relative, relErr := filepath.Rel(run, resolved)
			if linkErr != nil || relErr != nil || !filepath.IsLocal(relative) {
				t.Fatal("review link escapes run")
			}
			if _, statErr := os.Stat(resolved); statErr != nil {
				t.Fatal("review link target missing", statErr)
			}
		}
		result := map[string]any{"candidate_sha256": report.CandidateSHA256, "runtime": a.Verdict, "expected_runtime": item.Runtime, "patch_replay": true, "review_links_verified": len(reviewLinks), "audit_consistency": auditReport.Consistency, "audit_complete": auditReport.ScanComplete, "campaign": record.Directory, "run": a.RunID}
		if item.ID == "control-1" && os.Getenv("HARNESS_GHCLI_REFERENCES") == "1" {
			tamper, tamperErr := closureAuditTamper(ctx, caseRoot, record.Directory, auditReport)
			if tamperErr != nil {
				t.Fatal(tamperErr)
			}
			result["audit_tamper"] = tamper
		}
		if os.Getenv("HARNESS_GHCLI_REFERENCES") == "1" {
			refRoot := filepath.Join(caseRoot, "reference")
			ref, e := evaluation.EvaluateGHCLI(ctx, evaluation.Request{TargetID: input.ID, RunID: a.RunID, FixturesDir: fixtures, CandidateDir: filepath.Join(run, "evidence/candidate/ghcli"), CandidateSHA256: item.SHA256, SpecPath: spec, EvidenceDir: refRoot, Backend: shared})
			_ = closureSave(filepath.Join(caseRoot, "execution-index.json"), observer.commands)
			if e != nil {
				t.Fatal("independent reference infrastructure failure", e)
			}
			result["reference"] = ref.Automatic
			result["expected_reference"] = item.Reference
			planRaw, e := os.ReadFile(filepath.Join(record.Directory, "plan.json"))
			if e != nil {
				t.Fatal(e)
			}
			manifest := round.Manifest{SchemaVersion: 1, ID: item.ID, Kind: round.Calibration, CampaignID: record.ID, PlanSHA256: gate.Digest(planRaw), Targets: []round.TargetPin{{ID: input.ID, ProfileID: input.ProfileID, BuildConfig: input.BuildConfig, BaselineSHA256: input.Repository.SHA256, SpecSHA256: input.Specification.SHA256, ReferenceVersion: ref.ReferenceVersion, ReferenceSHA256: ref.ReferenceSHA256, ToolSHA256: ref.ToolSHA256}}}
			manifestPath := filepath.Join(caseRoot, "round-manifest.json")
			if e = closureSave(manifestPath, manifest); e != nil {
				t.Fatal(e)
			}
			referencePath := filepath.Join(refRoot, "reference.json")
			referenceRaw, e := os.ReadFile(referencePath)
			if e != nil {
				t.Fatal(e)
			}
			r, e := round.Summarize(ctx, round.Request{ManifestPath: manifestPath, CampaignDir: record.Directory, EvidenceDir: filepath.Join(caseRoot, "round"), References: []round.Reference{{TargetID: input.ID, Report: round.Artifact{Path: referencePath, SHA256: gate.Digest(referenceRaw)}}}})
			result["round_valid"] = r.Valid
			result["round_complete"] = r.ExecutionComplete
			result["round_counts"] = r.Counts
			_ = closureSave(filepath.Join(caseRoot, "result.json"), result)
			pending := evaluation.Inconclusive
			if ref.Automatic == evaluation.Fail {
				pending = evaluation.Fail
			}
			if e != nil || !r.Valid || !r.ExecutionComplete || len(r.Targets) != 1 || r.Counts.Success != 0 || r.Targets[0].Automatic != ref.Automatic || r.Targets[0].Reference != pending || r.Targets[0].Verdict != a.Verdict {
				t.Fatal("round failed or changed original verdict/pending review", e)
			}
			if string(ref.Automatic) != item.Reference {
				t.Fatalf("independent reference calibration mismatch: %s", ref.Automatic)
			}
		}
		_ = closureSave(filepath.Join(caseRoot, "result.json"), result)
		if a.Verdict != item.Runtime {
			t.Fatalf("runtime calibration mismatch: got %s expected %s", a.Verdict, item.Runtime)
		}
	}
}

// 认证失败必须先于任何实际命令；使用未执行后端统计，不伪造执行通过。
type closureNoExecution struct {
	calls int
	kind  string
}

func (b *closureNoExecution) Info() execution.Info { return execution.Info{Kind: b.kind} }
func (b *closureNoExecution) Run(context.Context, execution.Request) (execution.Result, error) {
	b.calls++
	return execution.Result{}, fmt.Errorf("unexpected execution after invalid reference binding")
}
func TestGHCLIReferenceBindingFailures(t *testing.T) {
	matrixPath := os.Getenv("HARNESS_GHCLI_MATRIX")
	output := os.Getenv("HARNESS_GHCLI_BINDINGS_OUTPUT")
	if matrixPath == "" || output == "" {
		t.Skip("set fixed matrix and bindings output for reference rejection checks")
	}
	if !filepath.IsAbs(output) {
		t.Fatal("absolute bindings output required")
	}
	if err := os.Mkdir(output, 0755); err != nil {
		t.Fatal(err)
	}
	matrix, matrixErr := closureLoadMatrix(matrixPath)
	if err := matrixErr; err != nil || len(matrix.Cases) != 5 {
		t.Fatal("fixed matrix unavailable", err)
	}
	scenario, err := closureScenario()
	if err != nil {
		t.Fatal(err)
	}
	fixtures := filepath.Join(scenario, "l2/data/rpcx-l2-ghcli-v1/fixtures")
	spec := filepath.Join(scenario, "ghcli/specs/v0.6.0.md")
	results := map[string]any{}
	for _, mode := range []string{"empty-target", "empty-run", "candidate-digest", "sdk-digest", "spec-digest", "overlap", "existing-output", "missing-backend", "local-without-trust"} {
		t.Run(mode, func(t *testing.T) {
			backend := &closureNoExecution{kind: execution.KindDocker}
			root := filepath.Join(output, mode)
			if err := os.Mkdir(root, 0755); err != nil {
				t.Fatal(err)
			}
			req := evaluation.Request{TargetID: matrix.TargetID, RunID: "binding-negative-only", FixturesDir: fixtures, CandidateDir: matrix.Cases[0].Input, CandidateSHA256: matrix.Cases[0].SHA256, SpecPath: spec, EvidenceDir: filepath.Join(root, "reference"), Backend: backend}
			switch mode {
			case "empty-target":
				req.TargetID = ""
			case "empty-run":
				req.RunID = ""
			case "candidate-digest":
				req.CandidateSHA256 = strings.Repeat("b", 64)
			case "sdk-digest":
				files, e := gate.ReadFiles(fixtures)
				if e != nil {
					t.Fatal(e)
				}
				files["rpcx-v2/client.go"] = append(files["rpcx-v2/client.go"], '\n')
				req.FixturesDir = filepath.Join(root, "fixtures")
				if e = gate.CopyFiles(req.FixturesDir, files); e != nil {
					t.Fatal(e)
				}
			case "spec-digest":
				raw, e := os.ReadFile(spec)
				if e != nil {
					t.Fatal(e)
				}
				req.SpecPath = filepath.Join(root, "spec.md")
				if e = os.WriteFile(req.SpecPath, append(raw, '\n'), 0600); e != nil {
					t.Fatal(e)
				}
			case "overlap":
				req.EvidenceDir = filepath.Join(req.CandidateDir, "invalid-reference-overlap")
			case "existing-output":
				if e := os.Mkdir(req.EvidenceDir, 0755); e != nil {
					t.Fatal(e)
				}
			case "missing-backend":
				req.Backend = nil
			case "local-without-trust":
				backend.kind = execution.KindLocal
			}
			_, e := evaluation.EvaluateGHCLI(context.Background(), req)
			results[mode] = map[string]any{"rejected": e != nil, "backend_calls": backend.calls}
			_ = closureSave(filepath.Join(output, "results.json"), results)
			if e == nil || backend.calls != 0 {
				t.Fatalf("invalid request reached execution or was accepted: calls=%d err=%v", backend.calls, e)
			}
		})
	}
}

// 只改审计实际消费材料的私有副本；原Campaign始终保持只读。
func closureAuditTamper(ctx context.Context, caseRoot, campaignRoot string, original audit.Report) (map[string]any, error) {
	clone := filepath.Join(caseRoot, "audit-tamper-input")
	if err := os.Mkdir(clone, 0755); err != nil {
		return nil, err
	}
	chosen := ""
	for _, source := range original.Sources {
		if !filepath.IsLocal(source.Path) {
			return nil, fmt.Errorf("unsafe audit source path")
		}
		target := filepath.Join(clone, filepath.FromSlash(source.Path))
		switch source.Kind {
		case "directory":
			if err := os.MkdirAll(target, 0755); err != nil {
				return nil, err
			}
		case "file":
			raw, err := os.ReadFile(filepath.Join(campaignRoot, filepath.FromSlash(source.Path)))
			if err != nil || gate.Digest(raw) != source.SHA256 {
				return nil, fmt.Errorf("original audit input drift")
			}
			if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return nil, err
			}
			if err = os.WriteFile(target, raw, 0600); err != nil {
				return nil, err
			}
			if strings.HasSuffix(source.Path, "modules-000.tar.gz.part") {
				chosen = target
			}
		}
	}
	baseline, e := audit.Inspect(ctx, audit.Request{CampaignDir: clone, OutputDir: filepath.Join(caseRoot, "audit-clone-baseline")}, runner.InspectRun)
	if e != nil || baseline.Consistency != "pass" || !baseline.ScanComplete {
		return nil, fmt.Errorf("audit clone baseline incomplete: %s %v", baseline.Consistency, e)
	}
	if chosen == "" {
		return nil, fmt.Errorf("no real dependency chunk consumed by audit")
	}
	raw, e := os.ReadFile(chosen)
	if e != nil || len(raw) == 0 {
		return nil, fmt.Errorf("chunk unavailable")
	}
	before := gate.Digest(raw)
	raw[0] ^= 1
	if e = os.WriteFile(chosen, raw, 0600); e != nil {
		return nil, e
	}
	altered, e := audit.Inspect(ctx, audit.Request{CampaignDir: clone, OutputDir: filepath.Join(caseRoot, "audit-tamper")}, runner.InspectRun)
	if e != nil || altered.Consistency != "violated" || len(altered.Violations) == 0 {
		return nil, fmt.Errorf("real chunk corruption was not rejected: %s %v", altered.Consistency, e)
	}
	result := map[string]any{"clone_baseline": "pass", "tamper_consistency": altered.Consistency, "violations": len(altered.Violations), "original_sha256": before, "altered_sha256": gate.Digest(raw), "original_campaign_changed": false}
	return result, closureSave(filepath.Join(caseRoot, "audit-tamper-result.json"), result)
}

func TestGHCLIAuditTamperCopy(t *testing.T) {
	source := os.Getenv("HARNESS_GHCLI_AUDIT_SOURCE")
	output := os.Getenv("HARNESS_GHCLI_AUDIT_OUTPUT")
	if source == "" || output == "" {
		t.Skip("set archived campaign and new output for read-only audit tamper copy")
	}
	if !filepath.IsAbs(source) || !filepath.IsAbs(output) {
		t.Fatal("absolute audit diagnostic paths required")
	}
	if err := os.Mkdir(output, 0755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	r, e := audit.Inspect(ctx, audit.Request{CampaignDir: source, OutputDir: filepath.Join(output, "source-audit")}, runner.InspectRun)
	if e != nil || r.Consistency != "pass" || !r.ScanComplete {
		t.Fatalf("source audit unavailable: %s %v", r.Consistency, e)
	}
	if _, e = closureAuditTamper(ctx, output, source, r); e != nil {
		t.Fatal(e)
	}
}
