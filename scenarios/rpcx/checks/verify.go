// Package checks implements the public T1/T2 development-case verifier.
package checks

import (
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"example.com/agent-harness/internal/execution"
)

//go:embed probes/*.txt
var probes embed.FS

type Expected struct {
	Checks  map[string]string `json:"checks"`
	Verdict string            `json:"verdict"`
}
type Case struct {
	ID       string   `json:"id"`
	Patch    string   `json:"patch"`
	Expected Expected `json:"expected"`
}
type Manifest struct {
	SchemaVersion int    `json:"schema_version"`
	Spec          string `json:"spec"`
	Cases         []Case `json:"cases"`
}
type Check struct {
	Status string `json:"status"`
	Detail string `json:"detail"`
}
type Command struct {
	Args        []string       `json:"args"`
	Directory   string         `json:"directory"`
	Log         string         `json:"log"`
	ExitCode    int            `json:"exit_code"`
	TimedOut    bool           `json:"timed_out"`
	Cancelled   bool           `json:"cancelled,omitempty"`
	DurationMS  int64          `json:"duration_ms"`
	Backend     execution.Info `json:"backend"`
	ContainerID string         `json:"container_id,omitempty"`
	OOMKilled   bool           `json:"oom_killed,omitempty"`
	OutputLimit bool           `json:"output_limit,omitempty"`
}
type Result struct {
	ID                 string           `json:"id"`
	PatchSHA256        string           `json:"patch_sha256"`
	CandidateSHA256    string           `json:"candidate_sha256"`
	Checks             map[string]Check `json:"checks"`
	Verdict            string           `json:"verdict"`
	Commands           []Command        `json:"commands"`
	ExpectationMatched bool             `json:"expectation_matched"`
}
type Report struct {
	StartedAt         string    `json:"started_at"`
	GoVersion         string    `json:"go_version"`
	Spec              string    `json:"spec"`
	SpecSHA256        string    `json:"spec_sha256"`
	ManifestSHA256    string    `json:"manifest_sha256"`
	FixturesSHA256    string    `json:"fixtures_sha256"`
	ChecksSHA256      string    `json:"checks_sha256"`
	FixturesUnchanged bool      `json:"fixtures_unchanged"`
	PreflightCommands []Command `json:"preflight_commands"`
	Error             string    `json:"error,omitempty"`
	Results           []Result  `json:"results"`
}

var checkNames = []string{"dependencies", "protected_paths", "build", "baseline_tests", "test_preservation", "context_propagation"}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func readManifest(path string) (Manifest, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, nil, err
	}
	var m Manifest
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return m, nil, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return m, nil, fmt.Errorf("manifest must contain exactly one JSON document")
	}
	if m.SchemaVersion != 1 || len(m.Cases) == 0 {
		return m, nil, fmt.Errorf("invalid manifest version or empty cases")
	}
	seen := map[string]bool{}
	for _, c := range m.Cases {
		if c.ID == "" || strings.ContainsAny(c.ID, "/\\.") || seen[c.ID] {
			return m, nil, fmt.Errorf("invalid or duplicate case ID %q", c.ID)
		}
		seen[c.ID] = true
		if !safeRelative(c.Patch) {
			return m, nil, fmt.Errorf("unsafe patch path")
		}
		if len(c.Expected.Checks) != len(checkNames) {
			return m, nil, fmt.Errorf("case %s must declare every check", c.ID)
		}
		for _, name := range checkNames {
			switch c.Expected.Checks[name] {
			case "pass", "fail", "blocked", "not_run":
			default:
				return m, nil, fmt.Errorf("invalid expected check %s/%s", c.ID, name)
			}
		}
		if c.Expected.Verdict != "ReadyForReview" && c.Expected.Verdict != "Rejected" && c.Expected.Verdict != "NeedsAttention" {
			return m, nil, fmt.Errorf("invalid expected verdict")
		}
	}
	if !safeRelative(m.Spec) {
		return m, nil, fmt.Errorf("unsafe spec path")
	}
	return m, data, nil
}

func safeRelative(path string) bool {
	return path != "" && filepath.IsLocal(path)
}

// snapshot hashes every regular file, including its relative path. Symlinks are
// rejected so fixture copies cannot silently reach outside their source tree.
func snapshot(root string) (map[string]string, error) {
	return snapshotContext(context.Background(), root)
}

func snapshotContext(ctx context.Context, root string) (map[string]string, error) {
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
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
			return fmt.Errorf("non-regular fixture file: %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = digest(data)
		return nil
	})
	return files, err
}

func treeDigest(files map[string]string) string {
	data, _ := json.Marshal(files) // encoding/json sorts map keys.
	return digest(data)
}

func copyTree(source, dest string) error {
	return copyTreeContext(context.Background(), source, dest)
}

func copyTreeContext(ctx context.Context, source, dest string) error {
	files, err := snapshotContext(ctx, source)
	if err != nil {
		return err
	}
	for rel := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		path := filepath.Join(dest, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			return err
		}
	}
	return nil
}

// command 保留可信本地入口，供公开夹具与命令机制测试使用。
func command(parent context.Context, cacheDir, dir, log string, args ...string) (Command, []byte, error) {
	return commandOn(parent, execution.Local{}, cacheDir, dir, log, args...)
}

func commandOn(parent context.Context, backend execution.Executor, cacheDir, dir, log string, args ...string) (Command, []byte, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	started := time.Now()
	mounts := []string{dir}
	if backend.Info().Kind == execution.KindDocker {
		for _, sdk := range []string{"rpcx-v1", "rpcx-v2"} {
			mounts = append(mounts, filepath.Join(filepath.Dir(dir), sdk))
		}
	}
	output, err := backend.Run(ctx, execution.Request{Args: args, Directory: dir, ReadOnlyDirs: mounts, CacheDir: cacheDir})
	meta := Command{Args: args, Directory: dir, Log: log, ExitCode: output.ExitCode,
		DurationMS: time.Since(started).Milliseconds(), TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded), Cancelled: errors.Is(ctx.Err(), context.Canceled),
		Backend: backend.Info(), ContainerID: output.ContainerID, OOMKilled: output.OOMKilled, OutputLimit: output.OutputLimit}
	data := append([]byte(nil), output.Stdout...)
	if len(output.Stderr) != 0 {
		data = append(append(data, []byte("\n--- stderr ---\n")...), output.Stderr...)
	}
	if output.OutputLimit {
		data = append(data, []byte("\n[output limit exceeded]\n")...)
	}
	return meta, output.Stdout, errors.Join(err, os.WriteFile(log, data, 0644))
}

type testEvent struct{ Action, Package, Test string }

func testEvents(data []byte) (map[string]string, error) {
	states := map[string]string{}
	d := json.NewDecoder(bytes.NewReader(data))
	for {
		var event testEvent
		if err := d.Decode(&event); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		if event.Test != "" {
			states[event.Package+"/"+event.Test] = event.Action
		}
	}
	return states, nil
}

func preservationCheck(baselineNames map[string]bool, baselineEvents map[string]string, names map[string]bool, states map[string]string) Check {
	missing := []string{}
	for name := range baselineNames {
		if !names[name] || states[name] != "pass" {
			missing = append(missing, name)
		}
	}
	for name, state := range baselineEvents {
		if state == "pass" && states[name] != "pass" && !baselineNames[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) != 0 {
		return Check{"fail", "TEST-001: missing, skipped or failed: " + strings.Join(missing, ", ")}
	}
	return Check{"pass", fmt.Sprintf("all %d original tests executed and passed", len(baselineNames))}
}

func probeCheck(data []byte, meta Command) Check {
	if meta.TimedOut {
		return Check{"blocked", "context behavior probe timed out"}
	}
	if meta.ExitCode != 0 {
		return Check{"fail", "context behavior probe failed; see context-probes.log"}
	}
	states, err := testEvents(data)
	if err != nil {
		return Check{"blocked", "context probe output could not be decoded"}
	}
	prefix := "example.com/demo-orders/cmd/server/TestHarnessRequestContext"
	for _, operation := range []string{"Place", "Cancel"} {
		for _, mode := range []string{"metadata", "already-cancelled", "cancel-in-backend"} {
			name := prefix + "/" + operation + "/" + mode
			if states[name] != "pass" {
				return Check{"fail", "required probe did not execute and pass: " + name}
			}
		}
	}
	return Check{"pass", "CTX-001/002/003: both request chains preserve metadata, deadline and cancellation"}
}

func testNames(root string) (map[string]bool, error) {
	names := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		pkg := "example.com/demo-orders"
		if rel != "." {
			pkg += "/" + filepath.ToSlash(rel)
		}
		for _, decl := range file.Decls {
			if f, ok := decl.(*ast.FuncDecl); ok && f.Recv == nil && strings.HasPrefix(f.Name.Name, "Test") {
				names[pkg+"/"+f.Name.Name] = true
			}
		}
		return nil
	})
	return names, err
}

func protectedCheck(baseline, candidate string) Check {
	before, err := snapshot(filepath.Join(baseline, "internal/pb"))
	if err != nil {
		return Check{"fail", err.Error()}
	}
	after, err := snapshot(candidate)
	if err != nil {
		return Check{"fail", err.Error()}
	}
	filtered := map[string]string{}
	for path, hash := range after {
		if strings.HasPrefix(path, "internal/pb/") {
			filtered[strings.TrimPrefix(path, "internal/pb/")] = hash
		}
	}
	if treeDigest(before) != treeDigest(filtered) {
		return Check{"fail", "PATH-001: protected file list or contents changed"}
	}
	return Check{"pass", "PATH-001: protected file list and SHA-256 match baseline"}
}

func dependencyCheck(candidate string, run func(string, string, ...string) ([]byte, Command, error)) (Check, Command, error) {
	data, meta, err := run(candidate, "dependencies", "go", "mod", "edit", "-json")
	if err != nil {
		return Check{}, meta, err
	}
	if meta.TimedOut {
		return Check{"blocked", "dependency inspection timed out"}, meta, nil
	}
	if meta.ExitCode != 0 {
		return Check{"fail", "DEP-001: invalid go.mod"}, meta, nil
	}
	var mod struct {
		Module  struct{ Path string }
		Require []struct{ Path, Version string }
		Replace []struct {
			Old, New struct{ Path, Version string }
		}
	}
	if err := json.Unmarshal(data, &mod); err != nil {
		return Check{}, meta, err
	}
	if mod.Module.Path != "example.com/demo-orders" || len(mod.Require) != 1 || mod.Require[0].Path != "example.com/rpcx/v2" || mod.Require[0].Version != "v2.0.0" || len(mod.Replace) != 1 || mod.Replace[0].Old.Path != "example.com/rpcx/v2" || mod.Replace[0].Old.Version != "" || mod.Replace[0].New.Path != "../rpcx-v2" || mod.Replace[0].New.Version != "" {
		return Check{"fail", "DEP-001: expected only rpcx/v2 v2.0.0 with local ../rpcx-v2 replacement"}, meta, nil
	}
	err = filepath.WalkDir(candidate, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			value, _ := strconv.Unquote(imp.Path.Value)
			if value == "example.com/rpcx" {
				return fmt.Errorf("DEP-001: v1 import in %s", path)
			}
		}
		return nil
	})
	if err != nil {
		return Check{"fail", err.Error()}, meta, nil
	}
	return Check{"pass", "DEP-001: module, local replacement and imports migrated"}, meta, nil
}

// contextPolicy supplements behavioral probes with a narrow syntax check.
// It is not a general Go data-flow analysis or sandbox.
func contextPolicy(candidate string) error {
	return filepath.WalkDir(candidate, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		aliases := map[string]bool{}
		for _, imp := range file.Imports {
			if imp.Path.Value == `"context"` {
				name := "context"
				if imp.Name != nil {
					name = imp.Name.Name
				}
				if name == "." {
					return fmt.Errorf("CTX-003: context dot import cannot be verified")
				}
				aliases[name] = true
			}
		}
		var violation error
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := selector.X.(*ast.Ident)
			if ok && aliases[id.Name] && (selector.Sel.Name == "Background" || selector.Sel.Name == "TODO" || selector.Sel.Name == "WithoutCancel") {
				violation = fmt.Errorf("CTX-003: %s in %s", selector.Sel.Name, path)
			}
			return true
		})
		return violation
	})
}

func verdict(checks map[string]Check) string {
	unknown := false
	for _, name := range checkNames {
		c := checks[name]
		if c.Status == "fail" {
			return "Rejected"
		}
		if c.Status != "pass" {
			unknown = true
		}
	}
	if unknown {
		return "NeedsAttention"
	}
	return "ReadyForReview"
}

// Verify runs every public case. Expected results are consulted only after all
// actual checks and the actual verdict have been computed.
func Verify(root, runDir string) (*Report, error) {
	scenario := filepath.Join(root, "scenarios/rpcx")
	manifest, rawManifest, err := readManifest(filepath.Join(scenario, "cases/manifest.yaml"))
	if err != nil {
		return nil, err
	}
	spec, err := os.ReadFile(filepath.Join(scenario, manifest.Spec))
	if err != nil {
		return nil, err
	}
	fixtures := filepath.Join(scenario, "fixtures")
	before, err := snapshot(fixtures)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(runDir, 0755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "runs/go-cache"), 0755); err != nil {
		return nil, err
	}
	report := &Report{StartedAt: time.Now().UTC().Format(time.RFC3339), Spec: manifest.Spec, SpecSHA256: digest(spec), ManifestSHA256: digest(rawManifest), FixturesSHA256: treeDigest(before)}
	checkFiles, err := snapshot(filepath.Join(scenario, "checks"))
	if err != nil {
		return report, err
	}
	report.ChecksSHA256 = treeDigest(checkFiles)
	for name, data := range map[string][]byte{"spec.md": spec, "manifest.yaml": rawManifest} {
		if err := os.WriteFile(filepath.Join(runDir, name), data, 0644); err != nil {
			return report, err
		}
	}
	// SDK and baseline tests are preconditions, not candidate verdicts.
	preflight := filepath.Join(runDir, "preflight")
	if err := copyTree(fixtures, preflight); err != nil {
		return report, err
	}
	version, data, err := command(context.Background(), filepath.Join(root, "runs/go-cache"), preflight, filepath.Join(runDir, "go-version.log"), "go", "version")
	report.PreflightCommands = append(report.PreflightCommands, version)
	if err != nil || version.ExitCode != 0 {
		return report, fmt.Errorf("go version: %v", err)
	}
	report.GoVersion = strings.TrimSpace(string(data))
	for _, module := range []string{"rpcx-v1", "rpcx-v2", "demo-orders"} {
		meta, _, err := command(context.Background(), filepath.Join(root, "runs/go-cache"), filepath.Join(preflight, module), filepath.Join(runDir, "baseline-"+module+".log"), "go", "test", "-json", "-count=1", "-timeout=30s", "./...")
		report.PreflightCommands = append(report.PreflightCommands, meta)
		if err != nil {
			return report, err
		}
		if meta.ExitCode != 0 || meta.TimedOut {
			return report, fmt.Errorf("baseline %s failed: see %s", module, meta.Log)
		}
	}
	baseline := filepath.Join(fixtures, "demo-orders")
	for _, c := range manifest.Cases {
		result, err := verifyCase(root, scenario, fixtures, baseline, runDir, manifest.Spec, c)
		if err != nil {
			return report, fmt.Errorf("case %s: %w", c.ID, err)
		}
		report.Results = append(report.Results, result)
	}
	after, err := snapshot(fixtures)
	if err != nil {
		return report, err
	}
	report.FixturesUnchanged = treeDigest(before) == treeDigest(after)
	return report, nil
}

func verifyCase(root, scenario, fixtures, baseline, runDir, spec string, c Case) (Result, error) {
	result := Result{ID: c.ID, Checks: map[string]Check{}}
	for _, name := range checkNames {
		result.Checks[name] = Check{"not_run", "not executed"}
	}
	dir := filepath.Join(runDir, c.ID)
	workspace := filepath.Join(dir, "workspace")
	if err := copyTree(fixtures, workspace); err != nil {
		return result, err
	}
	candidate := filepath.Join(workspace, "demo-orders")
	patchPath := filepath.Join(scenario, "cases", c.Patch)
	patch, err := os.ReadFile(patchPath)
	if err != nil {
		return result, err
	}
	result.PatchSHA256 = digest(patch)
	if err := os.WriteFile(filepath.Join(dir, "candidate.patch"), patch, 0644); err != nil {
		return result, err
	}
	run := func(label string, args ...string) ([]byte, Command, error) {
		meta, data, err := command(context.Background(), filepath.Join(root, "runs/go-cache"), candidate, filepath.Join(dir, label+".log"), args...)
		result.Commands = append(result.Commands, meta)
		return data, meta, err
	}
	for i, args := range [][]string{{"git", "apply", "--check", patchPath}, {"git", "apply", patchPath}} {
		_, meta, err := run(fmt.Sprintf("patch-%d", i), args...)
		if err != nil {
			return result, err
		}
		if meta.ExitCode != 0 || meta.TimedOut {
			return result, fmt.Errorf("patch application failed: see %s", meta.Log)
		}
	}
	evaluation, err := Evaluate(context.Background(), EvaluationRequest{
		BaselineDir:  baseline,
		CandidateDir: candidate,
		SpecPath:     filepath.Join(scenario, spec),
		EvidenceDir:  filepath.Join(dir, "evaluation"),
		CacheDir:     filepath.Join(root, "runs/go-cache"),
	})
	result.CandidateSHA256 = evaluation.CandidateSHA256
	result.Checks = evaluation.Checks
	result.Verdict = evaluation.Verdict
	result.Commands = append(result.Commands, evaluation.Commands...)
	if err != nil {
		return result, err
	}
	result.ExpectationMatched = result.Verdict == c.Expected.Verdict
	for _, name := range checkNames {
		if result.Checks[name].Status != c.Expected.Checks[name] {
			result.ExpectationMatched = false
		}
	}
	return result, nil
}
