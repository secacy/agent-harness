package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/deepseek"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/ablation"
)

func runAblationLaunch(ctx context.Context, mode string, args []string, stdout, stderr io.Writer) int {
	f := flag.NewFlagSet("harness ablation "+mode, flag.ContinueOnError)
	f.SetOutput(stderr)
	project := f.String("project", ".", "frozen source project root")
	probe := f.String("probe", "", "saved model/engine probe directory")
	freeze := f.String("freeze", "", "frozen launch directory")
	reviews := f.String("reviews", "", "completed actual human task reviews JSON")
	authorization := f.String("authorization", "", "actual spending authorization JSON")
	root := f.String("experiment", "", "saved experiment root for reference evaluation")
	output := f.String("output", "", "new freeze or experiment directory")
	if err := f.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if f.NArg() != 0 || (mode == "freeze" && (*probe == "" || *output == "")) || (mode == "run" && (*freeze == "" || *reviews == "" || *authorization == "" || *output == "")) || (mode == "reference" && (*freeze == "" || *root == "")) {
		fmt.Fprintln(stderr, "missing mode-specific inputs; use --help")
		return 2
	}
	binary, err := os.Executable()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	switch mode {
	case "freeze":
		var p ablation.Protocol
		var capabilities deepseek.Capabilities
		if err = readAblationJSON(filepath.Join(*probe, "protocol.probed.json"), &p); err != nil {
			break
		}
		if err = readAblationJSON(filepath.Join(*probe, "capabilities.json"), &capabilities); err != nil {
			break
		}
		var models []byte
		models, err = os.ReadFile(filepath.Join(*probe, "models.json"))
		if err != nil {
			break
		}
		var manifest ablation.FreezeManifest
		manifest, err = ablation.Freeze(ctx, *output, ablation.FreezeRequest{ProjectDir: *project, BinaryPath: binary, Protocol: p, ModelsJSON: models, Capabilities: capabilities})
		if err == nil {
			fmt.Fprintf(stdout, "Freeze: %s\nProtocol: %s\n", manifest.FreezeSHA256, manifest.ProtocolSHA256)
			var reviews []ablation.TaskReview
			for _, task := range p.Tasks {
				reviews = append(reviews, ablation.TaskReview{Task: task, Decision: "pending", Evidence: []string{}})
			}
			if err = gate.WriteJSON(filepath.Join(*output, "task-reviews.template.json"), reviews); err != nil {
				break
			}
			auth := ablation.SpendingAuthorization{SchemaVersion: 1, Decision: "pending", FreezeSHA256: manifest.FreezeSHA256, ProtocolSHA256: manifest.ProtocolSHA256, Model: p.Model, MaxCalls: p.TotalBudget.MaxCalls, MaxTokens: p.TotalBudget.MaxTokens, Evidence: []string{}}
			if p.Price != nil {
				bound, e := p.Price.ConservativeAmount(p.TotalBudget.MaxTokens)
				if e != nil {
					err = e
					break
				}
				auth.Currency = p.Price.Currency
				auth.MaxAmount = &bound
			}
			err = gate.WriteJSON(filepath.Join(*output, "authorization.template.json"), auth)
		}
	case "run":
		var rs []ablation.TaskReview
		var auth ablation.SpendingAuthorization
		if err = readAblationJSON(*reviews, &rs); err != nil {
			break
		}
		if err = readAblationJSON(*authorization, &auth); err != nil {
			break
		}
		var manifest ablation.FreezeManifest
		manifest, err = ablation.CheckLaunch(ctx, *freeze, *project, binary, rs, auth)
		if err != nil {
			break
		}
		var p ablation.Protocol
		if err = readAblationJSON(filepath.Join(*freeze, "protocol.json"), &p); err != nil {
			break
		}
		var backend *execution.Docker
		backend, err = ablationBackend(ctx, p)
		if err != nil {
			break
		}
		// GET metadata immediately before dispatch; a changed provider declaration
		// invalidates this freeze rather than silently changing admission limits.
		var model *deepseek.Client
		model, err = deepseek.New(deepseek.Config{APIKey: os.Getenv("DEEPSEEK_API_KEY"), Model: p.Model.Model, Endpoint: p.Model.Endpoint, MaxTokens: int(p.Model.MaxOutputTokens), BudgetProfile: &p.Model})
		if err != nil {
			break
		}
		var capabilities deepseek.Capabilities
		capabilities, err = model.Discover(ctx)
		if err != nil {
			break
		}
		live := capabilities.Profile
		live.VerifiedAt = p.Model.VerifiedAt
		if !reflect.DeepEqual(live, p.Model) {
			err = errors.New("provider capability declaration changed; freeze a new protocol")
			break
		}
		_, err = ablation.Run(ctx, *output, p, ablation.Services{Backend: backend, NewModel: func(ablation.Slot) (budget.ProfiledModel, error) {
			return deepseek.New(deepseek.Config{APIKey: os.Getenv("DEEPSEEK_API_KEY"), Model: p.Model.Model, Endpoint: p.Model.Endpoint, MaxTokens: int(p.Model.MaxOutputTokens), BudgetProfile: &p.Model})
		}, Prepare: func(_ context.Context, dir, protocolSHA string) error {
			if protocolSHA != manifest.ProtocolSHA256 {
				return errors.New("launch protocol mismatch")
			}
			return writeAblationLaunch(filepath.Join(dir, "launch.json"), struct {
				Freeze        ablation.FreezeManifest        `json:"freeze"`
				FreezeSHA256  string                         `json:"freeze_sha256"`
				Reviews       []ablation.TaskReview          `json:"task_reviews"`
				Authorization ablation.SpendingAuthorization `json:"authorization"`
			}{manifest, manifest.FreezeSHA256, rs, auth})
		}})
	case "reference":
		var manifest ablation.FreezeManifest
		manifest, err = ablation.CheckFreeze(ctx, *freeze, *project, binary)
		if err != nil {
			break
		}
		var p ablation.Protocol
		if err = readAblationJSON(filepath.Join(*root, "protocol.json"), &p); err != nil {
			break
		}
		var raw []byte
		raw, err = os.ReadFile(filepath.Join(*root, "protocol.json"))
		if err != nil {
			break
		}
		if gate.Digest(raw) != manifest.ProtocolSHA256 {
			err = errors.New("experiment differs from frozen reference protocol")
			break
		}
		var backend *execution.Docker
		backend, err = ablationBackend(ctx, p)
		if err != nil {
			break
		}
		_, err = ablation.EvaluateAll(ctx, *root, backend)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "P1", mode, "completed")
	return 0
}

func readAblationJSON(path string, out any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return ablation.Decode(b, out)
}
func ablationBackend(ctx context.Context, p ablation.Protocol) (*execution.Docker, error) {
	backend, err := execution.NewDocker(ctx, execution.DockerConfig{Image: p.Execution.Image, CPUs: p.Execution.CPUs, MemoryBytes: p.Execution.MemoryBytes, Pids: p.Execution.Pids})
	if err != nil {
		return nil, err
	}
	policy, err := backend.ResourcePolicy(ctx)
	if err != nil {
		return nil, err
	}
	if backend.Info() != p.Execution || policy != p.Resources {
		return nil, errors.New("Docker image, resource limits or engine differ from frozen protocol")
	}
	return backend, nil
}

func writeAblationLaunch(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(append(data, '\n'))
	if err == nil {
		err = file.Sync()
	}
	if err = errors.Join(err, file.Close()); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
