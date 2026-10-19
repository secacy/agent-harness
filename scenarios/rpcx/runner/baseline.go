package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/translation"
)

type BaselineCommand struct {
	Name         string         `json:"name"`
	Args         []string       `json:"args"`
	ExitCode     int            `json:"exit_code"`
	Backend      execution.Info `json:"backend"`
	ContainerID  string         `json:"container_id,omitempty"`
	Stdout       string         `json:"stdout"`
	StdoutSHA256 string         `json:"stdout_sha256,omitempty"`
	Stderr       string         `json:"stderr"`
	StderrSHA256 string         `json:"stderr_sha256,omitempty"`
	Error        string         `json:"error,omitempty"`
}

// BaselineReport 只证明生成前的基线检查，不是候选的质量证据。
type BaselineReport struct {
	SchemaVersion      int                          `json:"schema_version"`
	Status             string                       `json:"status"`
	ProfileSHA256      string                       `json:"profile_sha256"`
	BaselineSHA256     string                       `json:"baseline_sha256"`
	LayoutSHA256       string                       `json:"layout_sha256"`
	SpecSHA256         string                       `json:"spec_sha256"`
	BuildConfig        string                       `json:"build_config"`
	Commands           []BaselineCommand            `json:"commands"`
	Tests              map[string]string            `json:"tests"`
	Error              string                       `json:"error,omitempty"`
	ConfigurationTests map[string]map[string]string `json:"configuration_tests,omitempty"`
	ModuleTests        map[string]map[string]string `json:"module_tests,omitempty"`
}

func (r *runState) checkBaseline(ctx context.Context) (err error) {
	report := &BaselineReport{SchemaVersion: 1, Status: "running", ProfileSHA256: r.input.ProfileSHA256, BaselineSHA256: r.input.BaselineSHA256, LayoutSHA256: r.fixtures.hash(), SpecSHA256: r.input.SpecSHA256, BuildConfig: r.profile.BuildConfig, Commands: []BaselineCommand{}, Tests: map[string]string{}}
	r.baseline = report
	if err = os.MkdirAll(r.path("preflight"), 0700); err != nil {
		report.Status = "error"
		report.Error = err.Error()
		return fmt.Errorf("%w: %w", ErrBaseline, err)
	}
	defer func() {
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrInputChanged) {
				report.Status = "error"
			}
			if report.Status != "error" {
				report.Status = "fail"
			}
			report.Error = err.Error()
		}
		if writeErr := writeJSON(r.path("preflight/baseline.json"), report); writeErr != nil {
			report.Status = "error"
			err = errors.Join(err, writeErr)
			report.Error = err.Error()
		}
		if err != nil {
			err = fmt.Errorf("%w: %w", ErrBaseline, err)
		}
	}()
	if err = writeJSON(r.path("preflight/baseline.json"), report); err != nil {
		return err
	}
	if err = r.checkInputs(ctx); err != nil {
		return err
	}
	if err = r.checkInitialWorkspace(ctx); err != nil {
		return err
	}
	binary := "go"
	if r.config.Backend.Info().Kind == execution.KindLocal {
		binary, err = exec.LookPath("go")
		if err != nil {
			return err
		}
	}

	// 宿主声明的只读本地模块先验收，失败时不进入业务或模型。
	for _, name := range sortedKeys(r.profile.PrerequisiteTests) {
		if _, err = r.baselineModuleCommand(ctx, name, name+"-build", []string{binary, "build", "-o", "/dev/null", "./..."}); err != nil {
			return err
		}
		data, runErr := r.baselineModuleCommand(ctx, name, name+"-test", []string{binary, "test", "-json", "-count=1", "-timeout=30s", "./..."})
		if runErr != nil {
			return runErr
		}
		states, runErr := baselineEvents(data, r.profile.PrerequisiteTests[name])
		if runErr != nil {
			return runErr
		}
		if report.ModuleTests == nil {
			report.ModuleTests = map[string]map[string]string{}
		}
		report.ModuleTests[name] = states
	}

	for _, config := range r.profile.configurations() {
		prefix := ""
		if len(r.profile.BuildConfigurations) > 0 {
			prefix = config.ID + "-"
		}
		if r.profile.Verifier == translation.VerifierVersion {
			data, runErr := r.baselineCommand(ctx, prefix+"list", config.Arguments([]string{binary, "list", "-json", "./..."}))
			if runErr != nil {
				return runErr
			}
			if err = translation.ValidateConfiguration(data, config.ID); err != nil {
				return err
			}
		}
		if _, err = r.baselineCommand(ctx, prefix+"build", config.Arguments([]string{binary, "build", "-o", "/dev/null", "./..."})); err != nil {
			return err
		}
		data, runErr := r.baselineCommand(ctx, prefix+"test", config.Arguments([]string{binary, "test", "-json", "-count=1", "-timeout=30s", "./..."}))
		if runErr != nil {
			return runErr
		}
		states, runErr := baselineEvents(data, r.profile.RequiredTests)
		if runErr != nil {
			return runErr
		}
		if len(r.profile.BuildConfigurations) > 0 {
			if report.ConfigurationTests == nil {
				report.ConfigurationTests = map[string]map[string]string{}
			}
			report.ConfigurationTests[config.ID] = states
		} else {
			report.Tests = states
		}
	}
	if err = r.checkInputs(ctx); err != nil {
		return err
	}
	if err = r.checkInitialWorkspace(ctx); err != nil {
		return err
	}
	report.Status = "pass"
	return nil
}

func (r *runState) checkInitialWorkspace(ctx context.Context) error {
	files, err := readTree(ctx, r.path("workspace/"+r.profile.RepositoryName))
	if err != nil {
		return err
	}
	if files.hash() != r.input.BaselineSHA256 {
		return fmt.Errorf("%w: workspace changed before generation", ErrInputChanged)
	}
	return nil
}

func (r *runState) baselineCommand(parent context.Context, name string, args []string) ([]byte, error) {
	return r.baselineModuleCommand(parent, r.profile.RepositoryName, name, args)
}

func (r *runState) baselineModuleCommand(parent context.Context, module, name string, args []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	mounts := []string{r.path("baseline/" + module)}
	for _, sdk := range sortedKeys(r.profile.Dependencies) {
		if sdk != module {
			mounts = append(mounts, r.path("baseline/"+sdk))
		}
	}
	actual := args
	if r.hostBackend != nil {
		actual = append([]string{"env", "GOMODCACHE=" + r.hostBackend.Environment()["GOMODCACHE"]}, args...)
	}
	command := BaselineCommand{Name: name, Args: actual, ExitCode: -1, Backend: r.backend().Info(), Stdout: "preflight/" + name + ".stdout.log", Stderr: "preflight/" + name + ".stderr.log"}
	index := len(r.baseline.Commands)
	r.baseline.Commands = append(r.baseline.Commands, command)
	if err := writeJSON(r.path("preflight/baseline.json"), r.baseline); err != nil {
		return nil, err
	}
	result, runErr := r.backend().Run(ctx, execution.Request{Args: args, Directory: mounts[0], ReadOnlyDirs: mounts, CacheDir: r.path("cache/agent"), MaxBytes: 4 * 1024 * 1024})
	command.ExitCode, command.ContainerID = result.ExitCode, result.ContainerID
	if runErr != nil || result.ExitCode < 0 || result.OOMKilled || result.OutputLimit || ctx.Err() != nil {
		r.baseline.Status = "error"
	}
	if runErr == nil && (result.OOMKilled || result.OutputLimit) {
		runErr = errors.New("baseline command exceeded execution limits")
	}
	if runErr == nil && result.ExitCode != 0 {
		runErr = fmt.Errorf("baseline %s exited with code %d", name, result.ExitCode)
	}
	if ctx.Err() != nil {
		runErr = errors.Join(runErr, ctx.Err())
	}
	if runErr != nil {
		command.Error = runErr.Error()
	}
	command.StdoutSHA256, command.StderrSHA256 = digest(result.Stdout), digest(result.Stderr)
	r.baseline.Commands[index] = command
	saveErr := errors.Join(writeAtomic(r.path(command.Stdout), result.Stdout), writeAtomic(r.path(command.Stderr), result.Stderr), writeJSON(r.path("preflight/baseline.json"), r.baseline))
	if saveErr != nil {
		r.baseline.Status = "error"
	}
	return result.Stdout, errors.Join(runErr, saveErr)
}

func baselineEvents(data []byte, required []string) (map[string]string, error) {
	states := map[string]string{}
	packages := map[string]string{}
	owners := map[string]string{}
	runs := map[string]int{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var event struct{ Action, Package, Test string }
		if err := decoder.Decode(&event); err == io.EOF {
			break
		} else if err != nil {
			return states, fmt.Errorf("decode baseline test events: %w", err)
		}
		if event.Test != "" {
			id := event.Package + "/" + event.Test
			states[id] = event.Action
			owners[id] = event.Package
			if event.Action == "run" {
				runs[id]++
			}
		} else if event.Action == "pass" || event.Action == "fail" || event.Action == "skip" {
			packages[event.Package] = event.Action
		}
	}
	for _, test := range required {
		if states[test] != "pass" || packages[owners[test]] != "pass" || runs[test] != 1 {
			return states, fmt.Errorf("required baseline test or package did not pass: %s", test)
		}
	}
	for pkg, state := range packages {
		if state == "fail" {
			return states, fmt.Errorf("baseline package failed: %s", pkg)
		}
	}
	return states, nil
}
