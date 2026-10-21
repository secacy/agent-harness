package execution

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultImage 固定工具链版本与镜像内容；任务运行时禁止隐式拉取。
const DefaultImage = "golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd"

type DockerConfig struct {
	Image       string
	CPUs        int
	MemoryBytes int64
	Pids        int
}

type Docker struct {
	binary  string
	info    Info
	context string
}

func NewDocker(ctx context.Context, config DockerConfig) (*Docker, error) {
	if config.Image == "" {
		config.Image = DefaultImage
	}
	if config.CPUs == 0 {
		config.CPUs = 2
	}
	if config.MemoryBytes == 0 {
		config.MemoryBytes = 2 * 1024 * 1024 * 1024
	}
	if config.Pids == 0 {
		config.Pids = 128
	}
	if config.CPUs < 1 || config.MemoryBytes < 32*1024*1024 || config.Pids < 8 || strings.HasPrefix(config.Image, "-") {
		return nil, errors.New("invalid Docker image or resource limits")
	}
	binary, err := exec.LookPath("docker")
	if err != nil {
		return nil, err
	}
	d := &Docker{binary: binary}
	data, err := d.control(ctx, "image", "inspect", config.Image)
	if err != nil {
		return nil, fmt.Errorf("inspect local Docker image (pull it before running): %w", err)
	}
	var images []struct {
		ID     string
		OS     string `json:"Os"`
		Config struct{ Volumes map[string]any }
	}
	if err := json.Unmarshal(data, &images); err != nil {
		return nil, err
	}
	if len(images) != 1 || !strings.HasPrefix(images[0].ID, "sha256:") || images[0].OS != "linux" || len(images[0].Config.Volumes) != 0 {
		return nil, errors.New("expected one local Linux image without declared volumes")
	}
	d.info = Info{Kind: KindDocker, Image: images[0].ID, CPUs: config.CPUs, MemoryBytes: config.MemoryBytes, Pids: config.Pids}
	return d, nil
}

func (d *Docker) Info() Info { return d.info }

func (d *Docker) Run(ctx context.Context, req Request) (result Result, err error) {
	result.ExitCode = -1
	if err := ctx.Err(); err != nil {
		return result, err
	}
	args, err := d.createArgs(req)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return result, err
	}
	name := "harness-" + hex.EncodeToString(random[:])
	var intent *ResourceIntent
	if req.Owner != nil {
		owned, prepared, prepareErr := d.prepareResource(ctx, req, args, hex.EncodeToString(random[:]))
		if prepareErr != nil {
			return result, prepareErr
		}
		d, intent, name = owned, &prepared, prepared.Name
	}
	// 即使 create 请求被取消，也使用预先生成的名称回收可能已创建的容器。
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if intent != nil {
			if cleanupErr := d.finishResource(cleanupCtx, *intent); cleanupErr != nil {
				err = errors.Join(err, ErrResourcesUnknown, fmt.Errorf("reconcile owned container: %w", cleanupErr))
			}
			return
		}
		_, removeErr := d.control(cleanupCtx, "rm", "--force", "--volumes", name)
		if removeErr != nil && !missingContainer(removeErr) {
			err = errors.Join(err, fmt.Errorf("remove container %s: %w", name, removeErr))
		}
	}()
	create := []string{"create", "--name", name, "--label", "agent-harness.execution=true"}
	if intent != nil {
		if err := publishResource(filepath.Join(intent.Owner.Root, "resources", intent.CommandID, "dispatch.json"), ResourceDispatch{1, ResourceDigest(*intent)}); err != nil {
			return result, err
		}
		for _, key := range []string{"agent-harness.intent", "agent-harness.owner", "agent-harness.command", "agent-harness.registry"} {
			create = append(create, "--label", key+"="+intentLabels(*intent)[key])
		}
	}
	data, err := d.control(ctx, append(create, args...)...)
	if err != nil {
		return result, err
	}
	result.ContainerID = strings.TrimSpace(string(data))
	if intent != nil {
		if !containerID.MatchString(result.ContainerID) {
			return result, errors.New("Docker create did not return a full container ID")
		}
		if err := publishResource(filepath.Join(intent.Owner.Root, "resources", intent.CommandID, "created.json"), ResourceCreated{1, ResourceDigest(*intent), result.ContainerID}); err != nil {
			return result, err
		}
		// 启动前也认证服务返回的资源，不以随机名称代替归属验证。
		if c, found, err := d.inspectResource(ctx, *intent); err != nil || !found || c.ID != result.ContainerID {
			return result, errors.Join(errors.New("created container failed ownership verification"), err)
		}
	}
	commandCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout, stderr := outputs(req.MaxBytes, cancel)
	command := exec.CommandContext(commandCtx, d.binary, "start", "--attach", name)
	if d.context != "" {
		command = exec.CommandContext(commandCtx, d.binary, "--context", d.context, "start", "--attach", name)
	}
	command.Stdout, command.Stderr, command.WaitDelay = stdout, stderr, time.Second
	startErr := command.Run()
	result.Stdout, result.Stderr = stdout.bytes(), stderr.bytes()
	result.OutputLimit = stdout.exceeded() || stderr.exceeded()
	if ctx.Err() != nil || result.OutputLimit {
		if result.OutputLimit {
			err = ErrOutputLimit
		}
		return result, errors.Join(err, ctx.Err())
	}
	stateData, err := d.control(ctx, "inspect", "--format", "{{json .State}}", name)
	if err != nil {
		return result, err
	}
	var state struct {
		Status             string
		Running, OOMKilled bool
		ExitCode           int
		Error              string
	}
	if err := json.Unmarshal(stateData, &state); err != nil {
		return result, err
	}
	result.ExitCode, result.OOMKilled = state.ExitCode, state.OOMKilled
	if state.OOMKilled {
		return result, ErrOOM
	}
	if state.ExitCode == 126 || state.ExitCode == 127 {
		return result, fmt.Errorf("container command unavailable: exit %d", state.ExitCode)
	}
	if state.Status != "exited" || state.Running || state.Error != "" || (startErr != nil && state.ExitCode == 0) {
		return result, fmt.Errorf("container did not complete normally: %s; attach error: %v", state.Error, startErr)
	}
	return result, nil
}

func (d *Docker) createArgs(req Request) ([]string, error) {
	if len(req.Args) == 0 || req.Directory == "" || len(req.ReadOnlyDirs) == 0 {
		return nil, errors.New("command, directory and explicit read-only mounts are required")
	}
	absoluteDir, err := filepath.Abs(req.Directory)
	if err != nil {
		return nil, err
	}
	workingDir, err := filepath.EvalSymlinks(absoluteDir)
	if err != nil {
		return nil, err
	}
	args := []string{
		"--pull=never", "--network=none", "--read-only", "--cap-drop=ALL",
		"--security-opt=no-new-privileges", "--user=65534:65534", "--init",
		"--pids-limit=" + strconv.Itoa(d.info.Pids), "--cpus=" + strconv.Itoa(d.info.CPUs),
		"--memory=" + strconv.FormatInt(d.info.MemoryBytes, 10), "--memory-swap=" + strconv.FormatInt(d.info.MemoryBytes, 10),
		"--tmpfs=/tmp:rw,exec,nosuid,nodev,size=1073741824,mode=1777",
		"--log-driver=none", "--no-healthcheck", "--entrypoint=/usr/bin/env",
	}
	names := map[string]bool{}
	work := ""
	for _, source := range req.ReadOnlyDirs {
		abs, err := filepath.Abs(source)
		if err != nil {
			return nil, err
		}
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, err
		}
		name := filepath.Base(resolved)
		if names[name] || name == "." || name == string(filepath.Separator) || strings.ContainsAny(resolved, ",\n\r") {
			return nil, errors.New("mount names must be distinct and paths must not contain commas or newlines")
		}
		names[name] = true
		if err := regularTree(resolved); err != nil {
			return nil, err
		}
		target := "/workspace/" + name
		args = append(args, "--mount", "type=bind,source="+resolved+",target="+target+",readonly")
		if workingDir == resolved {
			work = target
		}
	}
	if work == "" {
		return nil, errors.New("working directory must be an explicit mounted root")
	}
	args = append(args, "--workdir", work, d.info.Image, "-i",
		"PATH=/usr/local/go/bin:/usr/bin:/bin", "HOME=/tmp", "TMPDIR=/tmp", "GOCACHE=/tmp/go-cache",
		"GOMODCACHE=/tmp/modules", "GOPATH=/tmp/gopath", "GOPROXY=off", "GOSUMDB=off", "GOWORK=off",
		"GOENV=off", "GOTOOLCHAIN=local", "GOFLAGS=-mod=readonly", "GOTELEMETRY=off", "CGO_ENABLED=0",
		"GOMAXPROCS="+strconv.Itoa(d.info.CPUs))
	return append(args, req.Args...), nil
}

func regularTree(root string) error {
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("mount must be a directory: %s", root)
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular mounted file: %s", path)
		}
		return nil
	})
}

func (d *Docker) control(parent context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	stdout, stderr := outputs(1024*1024, cancel)
	commandArgs := args
	if d.context != "" {
		commandArgs = append([]string{"--context", d.context}, args...)
	}
	command := exec.CommandContext(ctx, d.binary, commandArgs...)
	command.Stdout, command.Stderr, command.WaitDelay = stdout, stderr, time.Second
	err := command.Run()
	if stdout.exceeded() || stderr.exceeded() {
		return stdout.bytes(), errors.Join(ErrOutputLimit, ctx.Err())
	}
	if err != nil {
		return stdout.bytes(), fmt.Errorf("docker %s: %w: %s", args[0], errors.Join(err, ctx.Err()), stderr.bytes())
	}
	return stdout.bytes(), nil
}

func missingContainer(err error) bool {
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "no such container") || strings.Contains(text, "no such object")
}
