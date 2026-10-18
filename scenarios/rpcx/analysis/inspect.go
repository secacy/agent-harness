package analysis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/secacy/agent-harness/internal/execution"
)

//go:embed worker/main.go worker/rules.go
var material embed.FS

var validTag = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.]*$`)

// Inspect 在同一后端命令内完成加载与分析，返回有输入绑定的有限覆盖事实。
// load_failed 表示没有分析结论；后端或协议错误通过 error 返回，保留原始日志。
func Inspect(ctx context.Context, req Request) (result Result, err error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if req.Backend == nil || req.ModuleDir == "" || req.EvidenceDir == "" || len(req.SDKDirs) == 0 {
		return result, errors.New("backend, module, SDK roots and evidence directory are required")
	}
	for _, tag := range req.Tags {
		if !validTag.MatchString(tag) {
			return result, errors.New("invalid analysis build tag")
		}
	}
	tags := append([]string{}, req.Tags...)
	sort.Strings(tags)
	roots := append([]string{req.ModuleDir}, req.SDKDirs...)
	names := map[string]bool{"rpcx-analysis-tool": true}
	for i, root := range roots {
		root, err = filepath.Abs(root)
		if err != nil {
			return result, err
		}
		stat, statErr := os.Lstat(root)
		if statErr != nil || !stat.IsDir() {
			return result, errors.New("analysis roots must be regular directories")
		}
		// 所有绑定和挂载都使用解析祖先符号链接后的同一目录。
		resolved, resolveErr := filepath.EvalSymlinks(root)
		if resolveErr != nil {
			return result, resolveErr
		}
		root = resolved
		name := filepath.Base(root)
		if names[name] {
			return result, errors.New("analysis mount names must be distinct")
		}
		for _, previous := range roots[:i] {
			if overlaps(root, previous) {
				return result, errors.New("analysis input roots must not overlap")
			}
		}
		names[name], roots[i] = true, root
	}
	before, err := snapshot(ctx, roots)
	if err != nil {
		return result, err
	}
	inputSHA := manifestDigest(before)
	published := ""
	defer func() {
		after, checkErr := snapshot(context.WithoutCancel(ctx), roots)
		if checkErr != nil || manifestDigest(after) != inputSHA {
			err = errors.Join(err, errors.New("analysis inputs changed during inspection"), checkErr)
			result.Report = Report{}
			if published != "" {
				err = errors.Join(err, os.Remove(published))
			}
		}
	}()
	evidence, err := resolvePath(req.EvidenceDir)
	if err != nil {
		return result, err
	}
	for _, root := range roots {
		if overlaps(root, evidence) {
			return result, errors.New("analysis evidence and input roots must not overlap")
		}
	}
	if err := os.MkdirAll(evidence, 0755); err != nil {
		return result, err
	}
	entries, err := os.ReadDir(evidence)
	if err != nil || len(entries) != 0 {
		return result, errors.New("analysis evidence directory must be empty")
	}
	toolDir := filepath.Join(evidence, "rpcx-analysis-tool")
	if err := os.Mkdir(toolDir, 0755); err != nil {
		return result, err
	}
	toolFiles := map[string]string{}
	files := []string{"main.go", "rules.go"}
	for _, name := range files {
		data, err := material.ReadFile("worker/" + name)
		if err != nil {
			return result, err
		}
		if err := os.WriteFile(filepath.Join(toolDir, name), data, 0644); err != nil {
			return result, err
		}
		toolFiles[name] = digest(data)
	}
	toolSHA := manifestDigest(toolFiles)
	inputsJSON, err := json.MarshalIndent(struct {
		Inputs map[string]string `json:"inputs"`
		Tool   map[string]string `json:"tool"`
	}{before, toolFiles}, "", "  ")
	if err != nil {
		return result, err
	}
	if err := os.WriteFile(filepath.Join(evidence, "inputs.json"), append(inputsJSON, '\n'), 0644); err != nil {
		return result, err
	}
	if req.CacheDir == "" {
		req.CacheDir = filepath.Join(evidence, "go-cache")
	}
	req.CacheDir, err = resolvePath(req.CacheDir)
	if err != nil {
		return result, err
	}
	for _, root := range roots {
		if overlaps(root, req.CacheDir) {
			return result, errors.New("analysis cache and input roots must not overlap")
		}
	}
	backendPath := func(path string) string {
		if req.Backend.Info().Kind == execution.KindDocker {
			return "/workspace/" + filepath.Base(path)
		}
		return path
	}
	args := []string{"go", "run"}
	for _, name := range files {
		args = append(args, filepath.Join(backendPath(toolDir), name))
	}
	args = append(args, "-input", inputSHA, "-tool", toolSHA, "-tags", strings.Join(tags, ","))
	for _, root := range roots {
		args = append(args, backendPath(root))
	}
	result.Command, err = req.Backend.Run(ctx, execution.Request{
		Directory: roots[0], ReadOnlyDirs: append(append([]string{}, roots...), toolDir),
		CacheDir: req.CacheDir, Args: args,
	})
	for name, data := range map[string][]byte{"stdout.json": result.Command.Stdout, "stderr.log": result.Command.Stderr} {
		err = errors.Join(err, os.WriteFile(filepath.Join(evidence, name), data, 0644))
	}
	commandJSON, encodeErr := json.MarshalIndent(struct {
		Backend     execution.Info `json:"backend"`
		Args        []string       `json:"args"`
		ContainerID string         `json:"container_id,omitempty"`
		ExitCode    int            `json:"exit_code"`
	}{req.Backend.Info(), args, result.Command.ContainerID, result.Command.ExitCode}, "", "  ")
	err = errors.Join(err, encodeErr)
	if encodeErr == nil {
		err = errors.Join(err, os.WriteFile(filepath.Join(evidence, "execution.json"), append(commandJSON, '\n'), 0644))
	}
	if err != nil {
		return result, err
	}
	if result.Command.ExitCode != 0 {
		return result, fmt.Errorf("analysis worker exited with status %d", result.Command.ExitCode)
	}
	var report Report
	decoder := json.NewDecoder(bytes.NewReader(result.Command.Stdout))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return result, fmt.Errorf("decode analysis protocol: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return result, errors.New("analysis protocol has trailing content")
	}
	if err := validate(report, before, filepath.Base(roots[0]), inputSHA, toolSHA, tags, req.Backend.Info().Kind); err != nil {
		return result, err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err == nil {
		err = os.WriteFile(filepath.Join(evidence, "analysis.json"), append(data, '\n'), 0644)
	}
	if err != nil {
		return result, err
	}
	result.Report = report
	published = filepath.Join(evidence, "analysis.json")
	return result, nil
}

func resolvePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for ancestor := absolute; ; ancestor = filepath.Dir(ancestor) {
		if _, err := os.Lstat(ancestor); err == nil {
			resolved, err := filepath.EvalSymlinks(ancestor)
			if err != nil {
				return "", err
			}
			tail, _ := filepath.Rel(ancestor, absolute)
			return filepath.Join(resolved, tail), nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
}

func overlaps(a, b string) bool {
	forward, _ := filepath.Rel(a, b)
	backward, _ := filepath.Rel(b, a)
	return forward == "." || filepath.IsLocal(forward) || backward == "." || filepath.IsLocal(backward)
}

func snapshot(ctx context.Context, roots []string) (map[string]string, error) {
	files := map[string]string{}
	total := int64(0)
	for _, root := range roots {
		if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err := errors.Join(err, ctx.Err()); err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			stat, err := entry.Info()
			if err != nil || !stat.Mode().IsRegular() || stat.Size() > 1<<20 {
				return errors.New("analysis inputs must be regular files of at most 1 MiB")
			}
			total += stat.Size()
			if len(files) >= 10000 || total > 16<<20 {
				return errors.New("analysis input limit exceeded")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			files[filepath.Base(root)+"/"+filepath.ToSlash(rel)] = digest(data)
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func digest(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }

func manifestDigest(files map[string]string) string {
	data, _ := json.Marshal(files)
	return digest(data)
}

func validate(r Report, inputs map[string]string, module, inputSHA, toolSHA string, tags []string, backend string) error {
	wantOS := runtime.GOOS
	if backend == execution.KindDocker {
		wantOS = "linux"
	}
	if r.SchemaVersion != 2 || r.InputSHA256 != inputSHA || r.ToolSHA256 != toolSHA || r.Config.GOOS != wantOS || r.Config.GOARCH == "" || r.Config.GoVersion == "" || r.Config.CGO != "0" || !reflect.DeepEqual(r.Config.Tags, tags) || r.Packages == nil || r.Calls == nil || r.Gaps == nil {
		return errors.New("analysis protocol configuration or binding mismatch")
	}
	if r.Status == "load_failed" {
		if r.LoadExitCode == 0 || r.Diagnostic == "" || len(r.Packages)+len(r.Calls)+len(r.Gaps) != 0 {
			return errors.New("invalid failed-load analysis record")
		}
		return nil
	}
	if r.Status != "complete" || r.LoadExitCode != 0 || len(r.Packages) == 0 || r.Diagnostic != "" {
		return errors.New("analysis did not provide target packages")
	}
	active := map[string]string{}
	known := func(path string) bool {
		return filepath.IsLocal(path) && filepath.ToSlash(filepath.Clean(path)) == path && inputs[module+"/"+path] != ""
	}
	for _, pkg := range r.Packages {
		if pkg.Path == "" || len(pkg.Files) == 0 {
			return errors.New("invalid analysis package")
		}
		for path, hash := range pkg.Files {
			if !known(path) || hash != inputs[module+"/"+path] || active[path] != "" {
				return errors.New("analysis source hash or path mismatch")
			}
			active[path] = pkg.Path
		}
		for _, path := range pkg.Ignored {
			if !known(path) {
				return errors.New("invalid ignored source path")
			}
		}
	}
	if err := validateFacts(r, active, known); err != nil {
		return err
	}
	accounted := map[string]bool{}
	for path := range active {
		accounted[path] = true
	}
	for _, gap := range r.Gaps {
		accounted[gap.File] = true
	}
	for path := range inputs {
		if rel, ok := strings.CutPrefix(path, module+"/"); ok && strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go") && !accounted[rel] {
			return errors.New("production source omitted from analysis record")
		}
	}
	return nil
}

func validateFacts(r Report, active map[string]string, known func(string) bool) error {
	for _, call := range r.Calls {
		if !known(call.File) || active[call.File] != call.Package || call.Line < 1 || call.Reason == "" {
			return errors.New("invalid analysis call location")
		}
		if call.Kind != "direct" && call.Kind != "indirect" && call.Kind != "interface" {
			return errors.New("invalid analysis call kind")
		}
		if call.Kind != "interface" && call.SDK != "example.com/rpcx" && call.SDK != "example.com/rpcx/v2" {
			return errors.New("invalid analysis SDK identity")
		}
		if call.Kind == "interface" && call.SDK != "" {
			return errors.New("interface dispatch cannot claim a concrete SDK")
		}
		switch call.Status {
		case "covered":
			if call.SDK != "example.com/rpcx/v2" || call.Kind != "direct" || call.Reason != "direct_timeout_boundary" || call.TimeoutNS == nil || *call.TimeoutNS != 30_000_000_000 {
				return errors.New("inconsistent covered RPC boundary")
			}
		case "violation":
			if call.SDK != "example.com/rpcx/v2" || call.Kind != "direct" || call.Reason != "timeout_duration_mismatch" || call.TimeoutNS == nil || *call.TimeoutNS == 30_000_000_000 {
				return errors.New("inconsistent timeout violation")
			}
		case "legacy":
			if call.SDK != "example.com/rpcx" || call.Kind != "direct" || call.Reason != "v1_call" {
				return errors.New("inconsistent legacy RPC boundary")
			}
		case "unknown":
			found := false
			for _, gap := range r.Gaps {
				if gap.File == call.File && gap.Line == call.Line && gap.Reason == call.Reason {
					found = true
				}
			}
			if !found {
				return errors.New("unknown RPC boundary has no coverage gap")
			}
		default:
			return errors.New("invalid analysis call status")
		}
	}
	for _, gap := range r.Gaps {
		if !known(gap.File) || gap.Line < 0 || gap.Reason == "" {
			return errors.New("invalid analysis gap")
		}
	}
	return nil
}
