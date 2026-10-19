package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"time"

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/execution"
)

var identifier = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)

func validate(config Config) (Config, []budget.ProfiledModel, error) {
	// 固定清单及嵌套 map；宿主在调用期间也不得修改原配置。
	data, err := json.Marshal(config.Plan)
	if err != nil {
		return config, nil, err
	}
	var plan Plan
	if err = json.Unmarshal(data, &plan); err != nil {
		return config, nil, err
	}
	config.Plan = plan
	p := &config.Plan
	if !identifier.MatchString(p.ID) || p.Concurrency < 1 || p.ExecutionConcurrency < 1 || len(p.Targets) == 0 || p.Budget.MaxCalls < 1 || p.Budget.MaxTokens < 1 || config.Directory == "" || config.Backend == nil || config.Execute == nil || config.NewModel == nil {
		return config, nil, errors.New("ID, positive limits, targets, directory, backend, model factory and executor are required")
	}
	if err = p.Profile.Validate(); err != nil {
		return config, nil, err
	}
	p.Profile.VerifiedAt = p.Profile.VerifiedAt.UTC()
	config.Directory, err = canonical(config.Directory)
	if err != nil {
		return config, nil, err
	}
	if _, err = os.Stat(config.Directory); !errors.Is(err, os.ErrNotExist) {
		return config, nil, errors.New("campaign directory must not exist")
	}
	if info, err := os.Stat(filepath.Dir(config.Directory)); err != nil || !info.IsDir() {
		return config, nil, errors.New("campaign parent directory must exist")
	}
	ids := map[string]bool{}
	for _, target := range p.Targets {
		if err = target.Input.Validate(); err != nil {
			return config, nil, err
		}
		if ids[target.Input.ID] || target.Budget.MaxCalls < 1 || target.Budget.MaxTokens < 1 || target.Limits.MaxModelCalls < 1 || target.Limits.MaxToolCalls < 1 || target.Timeout <= 0 {
			return config, nil, errors.New("unique targets and positive target limits are required")
		}
		ids[target.Input.ID] = true
		inputs := []string{target.Input.Repository.Directory, target.Input.Specification.Path}
		for _, dependency := range target.Input.Dependencies {
			inputs = append(inputs, dependency.Directory)
		}
		for _, input := range inputs {
			path, err := canonical(input)
			if err != nil {
				return config, nil, err
			}
			if within(path, config.Directory) || within(config.Directory, path) {
				return config, nil, errors.New("campaign output overlaps task inputs")
			}
		}
	}
	models := make([]budget.ProfiledModel, len(p.Targets))
	for i, target := range p.Targets {
		// 工厂是可信宿主代码，不能在此发起生成请求。
		models[i], err = config.NewModel(target.Input)
		if err != nil {
			return config, nil, err
		}
		if models[i] == nil {
			return config, nil, errors.New("model factory returned nil")
		}
		profile, err := models[i].BudgetProfile()
		if err != nil {
			return config, nil, err
		}
		profile.VerifiedAt = profile.VerifiedAt.UTC()
		if !reflect.DeepEqual(profile, p.Profile) {
			return config, nil, errors.New("target model profile does not match the campaign")
		}
	}
	return config, models, nil
}

type result struct {
	index   int
	attempt Attempt
	err     error
}

// Run 独占创建新批次，等待所有已启动任务完成回收后才发布汇总。
// 局部任务错误保存在 Summary，不作为批次控制错误返回。
func Run(ctx context.Context, config Config) (record Record, summary Summary, err error) {
	if err = ctx.Err(); err != nil {
		return
	}
	config, models, err := validate(config)
	if err != nil {
		return record, summary, fmt.Errorf("%w: %w", ErrConfig, err)
	}
	p, root := config.Plan, config.Directory
	if err = mkdir(root); err != nil {
		return record, summary, fmt.Errorf("%w: %w", ErrControl, err)
	}
	record = Record{SchemaVersion: 1, ID: p.ID, Directory: root, Status: Running, StartedAt: time.Now().UTC()}
	planRef, err := writeJSON(filepath.Join(root, "plan.json"), p)
	if err != nil {
		return record, summary, fmt.Errorf("%w: %w", ErrControl, err)
	}
	record.PlanSHA256 = planRef.SHA256
	saveRecord := func() error { _, err := writeJSON(filepath.Join(root, "campaign.json"), record); return err }
	if err = saveRecord(); err != nil {
		return record, summary, fmt.Errorf("%w: %w", ErrControl, err)
	}
	// 初始化中途的失败或父取消也保存终态；绝不删除目录或覆盖已发布的 Run。
	defer func() {
		if record.Status == Running && err != nil {
			record.Status, record.Error = Failed, err.Error()
			if ctx.Err() != nil {
				record.Status = Cancelled
			}
			now := time.Now().UTC()
			record.FinishedAt = &now
			err = errors.Join(err, saveRecord())
		}
	}()
	if config.Prepare != nil {
		planRef.Path = filepath.Join(root, "plan.json")
		if err = config.Prepare(ctx, planRef); err != nil {
			return record, summary, fmt.Errorf("%w: prepare campaign materials: %w", ErrControl, err)
		}
		data, readErr := os.ReadFile(planRef.Path)
		if readErr != nil || checksum(data) != planRef.SHA256 {
			return record, summary, fmt.Errorf("%w: campaign plan changed during preparation", ErrControl)
		}
		if err = ctx.Err(); err != nil {
			return record, summary, err
		}
	}
	if err = mkdir(filepath.Join(root, "targets")); err != nil {
		return record, summary, fmt.Errorf("%w: %w", ErrControl, err)
	}
	records := make([]TargetRecord, len(p.Targets))
	targetDir := func(i int) string { return filepath.Join(root, "targets", p.Targets[i].Input.ID) }
	saveTarget := func(i int) error {
		_, err := writeJSON(filepath.Join(targetDir(i), "target.json"), records[i])
		return err
	}
	quotas := make(map[string]budget.Limits, len(p.Targets))
	for i, target := range p.Targets {
		if err = mkdir(targetDir(i)); err != nil {
			return record, summary, fmt.Errorf("%w: %w", ErrControl, err)
		}
		if err = mkdir(filepath.Join(targetDir(i), "runs")); err != nil {
			return record, summary, fmt.Errorf("%w: %w", ErrControl, err)
		}
		records[i] = TargetRecord{ID: target.Input.ID, State: Pending}
		if err = saveTarget(i); err != nil {
			return record, summary, fmt.Errorf("%w: %w", ErrControl, err)
		}
		quotas[target.Input.ID] = target.Budget
	}
	ledger, err := budget.Create(budget.Config{Directory: filepath.Join(root, "budget"), CampaignID: p.ID, Limits: p.Budget, Targets: quotas, Profile: p.Profile})
	if err != nil {
		return record, summary, fmt.Errorf("%w: %w", ErrControl, err)
	}
	backend, _ := execution.NewLimited(config.Backend, p.ExecutionConcurrency)
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan result, min(p.Concurrency, len(p.Targets)))
	active, next := 0, 0
	var controlErr error
	checkStop := func() {
		if fault := ledger.Err(); fault != nil && !errors.Is(controlErr, fault) {
			controlErr = errors.Join(controlErr, fault)
		}
		if controlErr != nil || ctx.Err() != nil {
			cancel()
		}
	}
	for next < len(p.Targets) || active > 0 {
		checkStop()
		for next < len(p.Targets) && active < p.Concurrency && workCtx.Err() == nil {
			i := next
			records[i].State = Active
			if saveErr := saveTarget(i); saveErr != nil {
				controlErr = errors.Join(controlErr, saveErr)
				cancel()
				break
			}
			next++
			active++
			go func() {
				targetCtx, stop := context.WithTimeout(workCtx, p.Targets[i].Timeout)
				defer stop()
				services := Services{RunsDir: filepath.Join(targetDir(i), "runs"), Model: models[i], Budget: budget.Target{Ledger: ledger, ID: p.Targets[i].Input.ID}, Backend: backend}
				attempt, runErr := config.Execute(targetCtx, p.Targets[i], services)
				results <- result{i, attempt, runErr}
			}()
			checkStop()
		}
		if workCtx.Err() != nil {
			reason := "campaign_cancelled"
			if controlErr != nil {
				reason = "campaign_failed"
			}
			for next < len(p.Targets) {
				records[next] = TargetRecord{ID: p.Targets[next].Input.ID, State: Skipped, Reason: reason}
				if saveErr := saveTarget(next); saveErr != nil {
					controlErr = errors.Join(controlErr, saveErr)
				}
				next++
			}
		}
		if active == 0 {
			break
		}
		// 已取消后只等结果，避免一直选中关闭的 Done 通道。
		var done, halted <-chan struct{}
		if workCtx.Err() == nil {
			done, halted = ctx.Done(), ledger.Done()
		}
		select {
		case <-done:
			cancel()
		case <-halted:
			checkStop()
		case result := <-results:
			active--
			checked, checkErr := checkAttempt(root, filepath.Join(targetDir(result.index), "runs"), records[result.index].ID, result.attempt, result.err)
			if checkErr == nil && checked.Status == Completed {
				account := ledger.Snapshot().Targets[records[result.index].ID]
				if account.RunID != checked.RunID {
					checked = Attempt{Status: Failed, FailureCode: "invalid_result"}
					checkErr = fmt.Errorf("%w: completed run lacks its budget binding", ErrResult)
				}
			}
			result.err = errors.Join(result.err, checkErr)
			records[result.index].State, records[result.index].Attempt = Finished, &checked
			if result.err != nil {
				records[result.index].Error = result.err.Error()
			}
			if saveErr := saveTarget(result.index); saveErr != nil {
				controlErr = errors.Join(controlErr, saveErr)
				cancel()
			}
		}
	}
	checkStop()
	summary = summarize(p.ID, record.PlanSHA256, records, ledger.Snapshot())
	summaryRef, summaryErr := writeJSON(filepath.Join(root, "summary.json"), summary)
	if summaryErr == nil {
		summaryRef.Path = "summary.json"
		summaryErr = writeFile(filepath.Join(root, "summary.md"), markdown(summary))
	}
	controlErr = errors.Join(controlErr, summaryErr)
	record.Status = Completed
	if ctx.Err() != nil {
		record.Status = Cancelled
		err = ctx.Err()
	}
	if controlErr != nil {
		record.Status = Failed
		err = errors.Join(err, ErrControl, controlErr)
	}
	if summaryErr == nil {
		record.Summary = summaryRef
	}
	now := time.Now().UTC()
	record.FinishedAt = &now
	if err != nil {
		record.Error = err.Error()
	}
	if saveErr := saveRecord(); saveErr != nil {
		record.Status, record.Summary = Failed, Artifact{}
		err = errors.Join(err, ErrControl, saveErr)
		record.Error = err.Error()
		// 最后一次发布可能已完成 rename，尽力覆盖成失败；仍返回原始错误。
		err = errors.Join(err, saveRecord())
	}
	return record, summary, err
}
