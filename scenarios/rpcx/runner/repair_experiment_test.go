package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/deepseek"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/checks"
	"github.com/secacy/agent-harness/scenarios/rpcx/translation"
)

const repairProtocolDir = "docs/experiments/diagnostic-repair-dev-v0.3.0"

type repairProtocol struct {
	SchemaVersion      int    `json:"schema_version"`
	ID                 string `json:"id"`
	ExecutionOrder     string `json:"execution_order,omitempty"`
	ProfileID          string `json:"profile_id"`
	SourceRun          string `json:"source_run"`
	SeedSHA256         string `json:"seed_sha256"`
	SeedFileSHA256     string `json:"seed_file_sha256"`
	PromptSHA256       string `json:"prompt_sha256"`
	Model              string `json:"model"`
	Endpoint           string `json:"endpoint"`
	ContextTokens      int64  `json:"context_tokens"`
	OutputTokens       int    `json:"output_tokens"`
	MaxModelCalls      int    `json:"max_model_calls"`
	MaxToolCalls       int    `json:"max_tool_calls"`
	TokensPerAttempt   int64  `json:"tokens_per_attempt"`
	PreparationSeconds int    `json:"preparation_seconds"`
	GenerationSeconds  int    `json:"generation_seconds"`
	ValidationSeconds  int    `json:"validation_seconds"`
	HostSeconds        int    `json:"host_seconds"`
	CleanupSeconds     int    `json:"cleanup_seconds"`
	Image              string `json:"image"`
}

func repairDecode(raw []byte, value any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("exactly one JSON value is required")
	}
	return nil
}

func loadRepairProtocol(root string) (repairProtocol, tree, string, error) {
	var p repairProtocol
	dir := filepath.Join(root, repairProtocolDir)
	raw, err := os.ReadFile(filepath.Join(dir, "protocol.json"))
	if err != nil {
		return p, nil, "", err
	}
	if err = repairDecode(raw, &p); err != nil {
		return p, nil, "", err
	}
	if p.ID != "diagnostic-repair-dev-v0.3.0" || p.ExecutionOrder != "generate-then-validate" || p.SchemaVersion != 1 || p.ProfileID != ProfileTranslation || p.SourceRun != "rpcx-agent-1712004754" || p.SeedSHA256 != "19c4a36e7cdd83c0106fbdbbc3c221affe4dd471c0dd41df531491f48c4e6884" ||
		p.Model != "deepseek-flash" || p.Endpoint != "https://api.deepseek.com/chat/completions" || p.ContextTokens != 1048576 || p.OutputTokens != 4096 || p.MaxModelCalls != 12 || p.MaxToolCalls != 24 || p.TokensPerAttempt != 1500000 || p.PreparationSeconds != 180 || p.GenerationSeconds != 300 || p.ValidationSeconds != 360 || p.HostSeconds != 1680 || p.CleanupSeconds != 60 || p.Image != execution.DefaultImage {
		return p, nil, "", errors.New("repair protocol does not match the confirmed experiment")
	}
	seedRaw, err := os.ReadFile(filepath.Join(dir, "seed.json"))
	if err != nil {
		return p, nil, "", err
	}
	var seed tree
	if digest(seedRaw) != p.SeedFileSHA256 {
		return p, nil, "", errors.New("seed file digest mismatch")
	}
	if err = repairDecode(seedRaw, &seed); err != nil {
		return p, nil, "", err
	}
	// JSON 不是可信路径来源，写入前仍校验文件名及与普通输入相同的大小边界。
	total := 0
	for path, data := range seed {
		if path == "." || strings.Contains(path, "\\") || !filepath.IsLocal(path) || filepath.ToSlash(filepath.Clean(path)) != path || strings.Contains("/"+path+"/", "/.git/") || len(data) > 1<<20 {
			return p, nil, "", errors.New("invalid seed path or file size")
		}
		total += len(data)
	}
	if len(seed) == 0 || len(seed) > 10000 || total > 16<<20 || seed.hash() != p.SeedSHA256 {
		return p, nil, "", errors.New("seed tree identity mismatch")
	}
	prompt, err := os.ReadFile(filepath.Join(dir, "prompt.txt"))
	if err != nil || digest(prompt) != p.PromptSHA256 {
		return p, nil, "", errors.Join(err, errors.New("repair prompt digest mismatch"))
	}
	return p, seed, string(prompt), nil
}

func repairOutputBoundary(project, output string) error {
	output, err := canonicalPath(output)
	if err != nil {
		return err
	}
	for _, input := range []string{"scenarios", "docs/experiments/diagnostic-repair-dev-v0.1.0", "docs/experiments/diagnostic-repair-dev-v0.2.0", repairProtocolDir, "runs/l2-current-dev-v0.1.0"} {
		path, err := canonicalPath(filepath.Join(project, input))
		if err != nil {
			return err
		}
		if within(output, path) || within(path, output) {
			return errors.New("repair output overlaps experiment inputs")
		}
	}
	parent, err := os.Stat(filepath.Dir(output))
	if err != nil {
		return err
	}
	if !parent.IsDir() {
		return errors.New("repair output parent must be a directory")
	}
	return nil
}

type repairServices struct {
	Backend      execution.Executor
	Profile      budget.Profile
	Capabilities *deepseek.Capabilities
	NewModel     func(string) (budget.ProfiledModel, error)
	// 只供普通机制测试注入；真实入口和 Docker 校准使用原 Evaluate。
	Evaluate func(context.Context, checks.EvaluationRequest) (checks.Evaluation, error)
}

// 共用执行器串行记录实际命令，包含 Agent 检查的容器身份和设施错误。
type repairRecorder struct {
	execution.Executor
	directory string
	sequence  int
	fault     error
}

func (r *repairRecorder) Run(ctx context.Context, req execution.Request) (execution.Result, error) {
	result, err := r.Executor.Run(ctx, req)
	r.sequence++
	record := struct {
		Request execution.Request `json:"request"`
		Result  execution.Result  `json:"result"`
		Error   string            `json:"error,omitempty"`
	}{Request: req, Result: result}
	if err != nil {
		record.Error = err.Error()
		r.fault = err
	}
	if saveErr := writeJSON(filepath.Join(r.directory, fmt.Sprintf("%04d.json", r.sequence)), record); saveErr != nil {
		r.fault = errors.Join(err, saveErr)
		return result, r.fault
	}
	return result, err
}

type repairAttempt struct {
	ID              string             `json:"id"`
	Status          string             `json:"status"`
	FeedbackShown   bool               `json:"feedback_shown"`
	SeedSHA256      string             `json:"seed_sha256"`
	CandidateSHA256 string             `json:"candidate_sha256,omitempty"`
	Budget          *budget.Reference  `json:"budget,omitempty"`
	Agent           *agent.Result      `json:"agent,omitempty"`
	Validation      *checks.Evaluation `json:"validation,omitempty"`
	Verdict         repairVerdict      `json:"repair"`
	Error           string             `json:"error,omitempty"`
	PreparationTime string             `json:"preparation_time,omitempty"`
	GenerationTime  string             `json:"generation_time,omitempty"`
	ValidationTime  string             `json:"validation_time,omitempty"`
}

type repairSummary struct {
	SchemaVersion int              `json:"schema_version"`
	ProtocolID    string           `json:"protocol_id"`
	Status        string           `json:"status"`
	Error         string           `json:"error,omitempty"`
	Attempts      []repairAttempt  `json:"attempts"`
	Budget        *budget.Snapshot `json:"budget,omitempty"`
}

func repairMessages(r *runState, prompt string, feedback agent.ToolResult, shown bool, roots []string) []agent.Message {
	prefix := "No pre-run diagnostic is provided."
	if shown {
		prefix = "The following is the host's test-structure diagnostic for the starting candidate. Later edits may invalidate it.\n" + feedback.Content
	}
	return []agent.Message{
		{Role: agent.RoleSystem, Content: strings.TrimSpace(prompt) + "\nAvailable roots: workspace (writable), " + strings.Join(roots, ", ") + " (read-only).\nProtected paths: " + strings.Join(r.profile.ProtectedPaths, ", ") + "."},
		{Role: agent.RoleUser, Content: "Specification " + r.input.SpecVersion + ":\n\n" + string(r.spec)},
		{Role: agent.RoleUser, Content: prefix},
	}
}

func prepareRepair(ctx context.Context, project, dir, id string, p repairProtocol, seed tree, backend execution.Executor) (*runState, agent.ToolResult, error) {
	target, err := TargetFromTranslation(id, filepath.Join(project, "scenarios/rpcx/l2/data/rpcx-l2-translation-v1/fixtures"), filepath.Join(project, "scenarios/rpcx/translation/specs/v0.4.0.md"))
	if err != nil {
		return nil, agent.ToolResult{}, err
	}
	profile, err := lookupProfile(p.ProfileID)
	if err != nil {
		return nil, agent.ToolResult{}, err
	}
	if err = os.Mkdir(dir, 0700); err != nil {
		return nil, agent.ToolResult{}, err
	}
	r := &runState{profile: profile,
		config: Config{Target: &target, ModelName: p.Model, Backend: backend, DiagnosticLimit: 2, CompletionPolicy: agent.FinalResponseV1},
		record: Record{ID: "repair-" + id, TargetID: id, Directory: dir},
		input:  Input{Target: &target, ProfileSHA256: profile.hash(), RepositoryName: profile.RepositoryName, SpecVersion: profile.SpecVersion, ModelName: p.Model, Backend: backend.Info(), ExecutionMode: backend.Info().Kind, CompletionPolicy: agent.FinalResponseV1, DiagnosticLimit: 2, MaxModelCalls: p.MaxModelCalls, MaxToolCalls: p.MaxToolCalls, Timeout: (time.Duration(p.GenerationSeconds) * time.Second).String()},
	}
	if err = profile.validate(target); err != nil {
		return r, agent.ToolResult{}, err
	}
	if err = r.prepare(ctx); err != nil {
		return r, agent.ToolResult{}, err
	}
	if err = r.checkBaseline(ctx); err != nil {
		return r, agent.ToolResult{}, err
	}
	workspace := r.path("workspace/translation")
	if err = os.RemoveAll(workspace); err != nil {
		return r, agent.ToolResult{}, err
	}
	if err = seed.write(ctx, workspace); err != nil {
		return r, agent.ToolResult{}, err
	}
	actual, err := readTree(ctx, workspace)
	if err != nil || actual.hash() != p.SeedSHA256 {
		return r, agent.ToolResult{}, errors.Join(err, errors.New("copied seed digest mismatch"))
	}
	if err = seed.write(ctx, r.path("starting-candidate")); err != nil {
		return r, agent.ToolResult{}, err
	}
	feedback, err := (&diagnosticTools{run: r}).Execute(ctx, agent.ToolCall{ID: "host-starting-diagnostic", Name: diagnosticTool, Arguments: `{}`})
	var parsed diagnosticFeedback
	if err == nil && (feedback.IsError || json.Unmarshal([]byte(feedback.Content), &parsed) != nil || parsed.Status != "unknown" || !strings.Contains(feedback.Content, "TestTranslationRoundTrip")) {
		err = errors.New("starting diagnostic did not locate the expected unknown declaration")
	}
	return r, feedback, err
}

// runRepairComparison 只组织实验，不使用标准 Run/Campaign 成功记录。
func runRepairComparison(parent context.Context, project, output string, p repairProtocol, seed tree, prompt string, services repairServices) (summary repairSummary, err error) {
	summary = repairSummary{SchemaVersion: 1, ProtocolID: p.ID, Status: "Running", Attempts: []repairAttempt{{ID: "A", Status: "Pending", SeedSHA256: p.SeedSHA256}, {ID: "B", Status: "Pending", FeedbackShown: true, SeedSHA256: p.SeedSHA256}}}
	output, err = canonicalPath(output)
	if err != nil {
		return summary, err
	}
	if err = repairOutputBoundary(project, output); err != nil {
		return summary, err
	}
	if services.Backend == nil || services.NewModel == nil || seed.hash() != p.SeedSHA256 || services.Profile.Model != p.Model {
		return summary, errors.New("repair services or seed identity mismatch")
	}
	if err = os.Mkdir(output, 0700); err != nil {
		return summary, err
	}
	if err = os.Mkdir(filepath.Join(output, "commands"), 0700); err != nil {
		return summary, err
	}
	recorder := &repairRecorder{Executor: services.Backend, directory: filepath.Join(output, "commands")}
	services.Backend = recorder
	ctx, cancel := context.WithTimeout(parent, time.Duration(p.HostSeconds)*time.Second)
	defer cancel()
	var ledger *budget.Ledger
	states := make([]*runState, 2)
	defer func() {
		if ledger != nil {
			value := ledger.Snapshot()
			summary.Budget = &value
		}
		// 未启动项不伪造失败尝试；独立保存后续人工核对所需的入口。
		for i := range summary.Attempts {
			a := &summary.Attempts[i]
			if a.Status == "Prepared" {
				a.Status = "NotStarted"
				err = errors.Join(err, writeJSON(filepath.Join(output, a.ID, "attempt.json"), a))
			} else if a.Status == "Pending" {
				a.Status = "NotStarted"
			} else if a.Status == "PreparationFailed" && states[i] != nil {
				err = errors.Join(err, writeJSON(filepath.Join(output, a.ID, "attempt.json"), a))
			}
		}
		summary.Status = "Completed"
		if err != nil {
			summary.Status, summary.Error = "Aborted", err.Error()
		}
		if saveErr := writeAtomic(filepath.Join(output, "summary.md"), renderRepairSummary(summary)); saveErr != nil {
			err = errors.Join(err, saveErr)
			summary.Status, summary.Error = "Aborted", err.Error()
		}
		// JSON 最后发布；只有材料入口和终态均保存成功，才算实验交付完成。
		if saveErr := writeJSON(filepath.Join(output, "experiment.json"), summary); saveErr != nil {
			err = errors.Join(err, saveErr)
			summary.Status, summary.Error = "Aborted", err.Error()
		}
	}()
	if err = writeJSON(filepath.Join(output, "protocol.json"), p); err != nil {
		return summary, err
	}
	if err = writeAtomic(filepath.Join(output, "prompt.txt"), []byte(prompt)); err != nil {
		return summary, err
	}
	if services.Capabilities != nil {
		if err = writeAtomic(filepath.Join(output, "models-response.json"), services.Capabilities.Response); err != nil {
			return summary, err
		}
		if err = writeJSON(filepath.Join(output, "capabilities.json"), services.Capabilities); err != nil {
			return summary, err
		}
	}

	feedback := make([]agent.ToolResult, 2)
	for i := range summary.Attempts {
		a := &summary.Attempts[i]
		start := time.Now()
		prepCtx, stop := context.WithTimeout(ctx, time.Duration(p.PreparationSeconds)*time.Second)
		states[i], feedback[i], err = prepareRepair(prepCtx, project, filepath.Join(output, a.ID), a.ID, p, seed, services.Backend)
		stop()
		a.PreparationTime = time.Since(start).String()
		if err != nil {
			a.Status, a.Error = "PreparationFailed", err.Error()
			return summary, err
		}
		a.Status = "Prepared"
		if err = writeJSON(filepath.Join(output, a.ID, "attempt.json"), a); err != nil {
			return summary, err
		}
	}
	if !reflect.DeepEqual(feedback[0], feedback[1]) {
		return summary, errors.New("starting feedback differs between conditions")
	}
	limits := budget.Limits{MaxCalls: int64(p.MaxModelCalls), MaxTokens: p.TokensPerAttempt}
	ledger, err = budget.Create(budget.Config{Directory: filepath.Join(output, "budget"), CampaignID: strings.ReplaceAll(p.ID, ".", "_"), Limits: budget.Limits{MaxCalls: 2 * limits.MaxCalls, MaxTokens: 2 * limits.MaxTokens}, Targets: map[string]budget.Limits{"A": limits, "B": limits}, Profile: services.Profile})
	if err != nil {
		return summary, err
	}
	for i, r := range states {
		if err = errors.Join(ctx.Err(), ledger.Err()); err != nil {
			return summary, err
		}
		model, modelErr := services.NewModel(summary.Attempts[i].ID)
		if modelErr != nil {
			return summary, modelErr
		}
		a := &summary.Attempts[i]
		if err = generateRepair(ctx, r, p, seed, prompt, feedback[i], a, ledger, model); err != nil {
			return summary, err
		}
		if recorder.fault != nil {
			return summary, recorder.fault
		}
	}
	// 两份生成快照均发布后才进入重型验证；验证故障仍按原边界停止。
	for i, r := range states {
		if err = errors.Join(ctx.Err(), ledger.Err()); err != nil {
			return summary, err
		}
		if err = validateRepair(ctx, r, p, &summary.Attempts[i], services.Evaluate); err != nil {
			return summary, err
		}
		if recorder.fault != nil {
			return summary, recorder.fault
		}
	}
	return summary, nil
}

func generateRepair(ctx context.Context, r *runState, p repairProtocol, seed tree, prompt string, feedback agent.ToolResult, a *repairAttempt, ledger *budget.Ledger, model budget.ProfiledModel) (err error) {
	a.Status = "Running"
	defer func() {
		if err != nil {
			a.Status, a.Error = "Error", err.Error()
			a.Verdict.Checks = quality.StatusUnknown
		}
		if saveErr := writeJSON(r.path("attempt.json"), a); saveErr != nil {
			a.Status, a.Error = "Error", errors.Join(err, saveErr).Error()
			a.Verdict.Checks = quality.StatusUnknown
			err = errors.Join(err, saveErr)
		}
	}()
	if err = r.checkInputs(ctx); err != nil {
		return err
	}
	starting, err := readTree(ctx, r.path("workspace/translation"))
	if err != nil || starting.hash() != p.SeedSHA256 {
		return errors.Join(err, errors.New("repair seed changed before generation"))
	}
	wrapped, err := budget.NewModel(ledger, a.ID, r.record.ID, model)
	if err != nil {
		a.Error = err.Error()
		return err
	}
	ref := wrapped.Reference()
	a.Budget, r.input.Budget = &ref, &ref
	if err = writeJSON(r.path("inputs/task.json"), r.input); err != nil {
		return err
	}
	tools, roots, err := r.repositoryTools()
	if err != nil {
		return err
	}
	initial := repairMessages(r, prompt, feedback, a.FeedbackShown, roots)
	if err = writeJSON(r.path("attempt.json"), a); err != nil {
		return err
	}
	if err = writeJSON(r.path("initial-messages.json"), initial); err != nil {
		return err
	}
	start := time.Now()
	genCtx, stop := context.WithTimeout(ctx, time.Duration(p.GenerationSeconds)*time.Second)
	result, generationErr := (agent.Loop{CompletionPolicy: agent.FinalResponseV1, Model: wrapped, Executor: tools, Tools: tools.Specs(), Limits: agent.Limits{MaxModelCalls: p.MaxModelCalls, MaxToolCalls: p.MaxToolCalls}}).Run(genCtx, initial)
	stop()
	a.GenerationTime, a.Agent = time.Since(start).String(), &result
	if generationErr != nil {
		a.Error = generationErr.Error()
	}
	if err = writeJSON(r.path("artifacts/agent.json"), result); err != nil {
		return err
	}
	// 取消不丢弃已有候选，但不在父取消后继续执行耗时命令。
	cleanup, cleanStop := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(p.CleanupSeconds)*time.Second)
	defer cleanStop()
	candidate, err := readTree(cleanup, r.path("workspace/translation"))
	if err != nil {
		return err
	}
	a.CandidateSHA256 = candidate.hash()
	a.Verdict = repairContent(r.fixtures.module("translation"), seed, candidate)
	if result.Reason == agent.ExitCompleted && generationErr == nil {
		a.Verdict.Submitted = quality.StatusPass
	}
	if err = r.checkInputs(cleanup); err != nil {
		return err
	}
	for name := range r.profile.Dependencies {
		if err = r.fixtures.module(name).write(cleanup, r.path("submitted/"+name)); err != nil {
			return err
		}
	}
	if err = candidate.write(cleanup, r.path("submitted/translation")); err != nil {
		return err
	}
	a.Status = "Generated"
	// 只写一次的生成记录不随独立验证更新；部分候选也保留实际提交状态。
	if err = writeJSON(r.path("artifacts/generation.json"), a); err != nil {
		return err
	}
	if recorder, ok := r.config.Backend.(*repairRecorder); ok && recorder.fault != nil {
		return recorder.fault
	}
	if err = errors.Join(ctx.Err(), ledger.Err()); err != nil {
		return err
	}
	return nil
}

func validateRepair(ctx context.Context, r *runState, p repairProtocol, a *repairAttempt, evaluate func(context.Context, checks.EvaluationRequest) (checks.Evaluation, error)) (err error) {
	a.Status = "Validating"
	defer func() {
		if err != nil {
			a.Status = "Error"
			if a.Error == "" {
				a.Error = err.Error()
			} else {
				a.Error = errors.Join(errors.New(a.Error), err).Error()
			}
			a.Verdict.Checks = quality.StatusUnknown
		}
		if saveErr := writeJSON(r.path("attempt.json"), a); saveErr != nil {
			a.Status, a.Error = "Error", errors.Join(err, saveErr).Error()
			a.Verdict.Checks = quality.StatusUnknown
			err = errors.Join(err, saveErr)
		}
	}()
	if err = writeJSON(r.path("attempt.json"), a); err != nil {
		return err
	}
	if err = r.checkInputs(ctx); err != nil {
		return err
	}
	before, err := readTree(ctx, r.path("submitted/translation"))
	if err != nil || before.hash() != a.CandidateSHA256 {
		return errors.Join(err, errors.New("repair candidate changed before validation"))
	}
	for name := range r.profile.Dependencies {
		actual, err := readTree(ctx, r.path("submitted/"+name))
		if err != nil || actual.hash() != r.fixtures.module(name).hash() {
			return errors.Join(err, errors.New("repair dependency changed before validation"))
		}
	}
	if evaluate == nil {
		evaluate = translation.Evaluate
	}
	start := time.Now()
	validationCtx, validationStop := context.WithTimeout(ctx, time.Duration(p.ValidationSeconds)*time.Second)
	value, validationErr := evaluate(validationCtx, checks.EvaluationRequest{BaselineDir: r.path("baseline/translation"), CandidateDir: r.path("submitted/translation"), SpecPath: r.path("inputs/spec/spec.md"), EvidenceDir: r.path("evidence"), CacheDir: r.path("cache/validation"), Backend: r.config.Backend})
	validationErr = errors.Join(validationErr, validationCtx.Err())
	validationStop()
	a.ValidationTime, a.Validation = time.Since(start).String(), &value
	if validationErr != nil {
		return validationErr
	}
	if value.CandidateSHA256 != a.CandidateSHA256 || value.BaselineSHA256 != r.input.BaselineSHA256 || value.SpecSHA256 != r.input.SpecSHA256 || value.VerifierVersion != r.profile.Verifier {
		return errors.New("repair validation identity mismatch")
	}
	a.Verdict.Checks = quality.StatusPass
	for _, id := range []string{"build_configurations", "build", "baseline_tests"} {
		if value.Checks[id].Status != quality.StatusPass {
			a.Verdict.Checks = quality.StatusFail
		}
	}
	postCtx, postStop := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(p.CleanupSeconds)*time.Second)
	defer postStop()
	after, err := readTree(postCtx, r.path("submitted/translation"))
	if err != nil || after.hash() != a.CandidateSHA256 {
		return errors.Join(err, errors.New("repair candidate drifted during validation"))
	}
	if err = r.checkInputs(postCtx); err != nil {
		return err
	}
	a.Status = "Finished"
	return nil
}

func renderRepairSummary(s repairSummary) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# 定点修复对照\n\n状态：%s。此为开发修复实验，不是完整迁移成功率。\n\n", s.Status)
	b.WriteString("| 条件 | 尝试状态 | 断言恢复 | 其它测试 | 范围 | 实际检查 | 提交 | 交付复核 | 原门禁 |\n| --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, a := range s.Attempts {
		verdict := ""
		if a.Validation != nil {
			verdict = a.Validation.Verdict
		}
		label := a.ID
		if a.Status != "NotStarted" {
			label = fmt.Sprintf("[%s](%s/attempt.json)", a.ID, a.ID)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %s | %s |\n", label, a.Status, a.Verdict.Assertion, a.Verdict.Tests, a.Verdict.Scope, a.Verdict.Checks, a.Verdict.Submitted, a.Verdict.Delivery, verdict)
	}
	b.WriteString("\nGenerated 表示候选和生成记录已保存、独立验证未开始；Finished 只表示验证流程结束，不等于修复成功。各组 artifacts/generation.json 独立保留生成阶段结果。\n\n交付说明需按对话、实际命令和修改逐项复核；缺少复核时保持 unknown，不自动认定完整定点修复成功。原始记录见 [experiment.json](experiment.json)。\n")
	return []byte(b.String())
}
