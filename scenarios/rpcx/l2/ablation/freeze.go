package ablation

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/deepseek"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
	"github.com/secacy/agent-harness/scenarios/rpcx/runner"
)

// FreezeRequest contains observed capabilities, not credentials. Freeze does
// not probe the provider, build the binary, run a candidate, or grant spending.
// The host supplies the executable it intends to launch; its bytes are pinned,
// not a claim that this API has reproduced its build from the source inventory.
type FreezeRequest struct {
	ProjectDir   string
	BinaryPath   string
	Protocol     Protocol
	ModelsJSON   []byte
	Capabilities deepseek.Capabilities
}

type FreezeManifest struct {
	SchemaVersion  int                      `json:"schema_version"`
	CreatedAt      time.Time                `json:"created_at"`
	ProjectDir     string                   `json:"project_dir"`
	GitHEAD        string                   `json:"git_head"`
	BinarySHA256   string                   `json:"binary_sha256"`
	ProtocolSHA256 string                   `json:"protocol_sha256"`
	Model          budget.Profile           `json:"model"`
	Execution      execution.Info           `json:"execution"`
	Resources      execution.ResourcePolicy `json:"resources"`
	Materials      map[string]string        `json:"materials"`
	FreezeSHA256   string                   `json:"-"`
}

var freezeMaterialNames = []string{"protocol.json", "models.json", "capabilities.json", "generation-definitions.json", "source-files.json"}

// Freeze authenticates the executable protocol and actual task inputs, then
// writes a new, exclusive freeze directory. Runtime source changes must first
// be committed; unrelated documentation and human-review edits do not block it.
func Freeze(ctx context.Context, output string, req FreezeRequest) (manifest FreezeManifest, err error) {
	if err = ctx.Err(); err != nil {
		return manifest, err
	}
	project, err := gate.Canonical(req.ProjectDir)
	if err != nil {
		return manifest, err
	}
	output, err = gate.Canonical(output)
	if err != nil {
		return manifest, err
	}
	if err = req.Protocol.Validate(); err != nil {
		return manifest, err
	}
	if err = validateCapabilities(req.Protocol.Model, req.ModelsJSON, req.Capabilities); err != nil {
		return manifest, err
	}
	definitions, err := freezeDefinitions(ctx, req.Protocol, output)
	if err != nil {
		return manifest, err
	}
	head, sources, err := sourceInventory(ctx, project)
	if err != nil {
		return manifest, err
	}
	binary, err := executableBytes(req.BinaryPath)
	if err != nil {
		return manifest, err
	}
	if gate.Overlap(project, output) {
		rel, e := filepath.Rel(project, output)
		if e != nil {
			return manifest, e
		}
		if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return manifest, errors.New("freeze output cannot contain the project")
		}
		// An ignored run directory can live beside the source tree; new runtime
		// files there cannot silently become part of the frozen build inputs.
		if _, e = gitOutput(ctx, project, "check-ignore", "--quiet", "--", filepath.Join(rel, "freeze.json")); e != nil {
			return manifest, errors.New("freeze output within project must be Git-ignored")
		}
	}
	protocolRaw, err := canonicalJSON(req.Protocol)
	if err != nil {
		return manifest, err
	}
	capabilitiesRaw, err := canonicalJSON(req.Capabilities)
	if err != nil {
		return manifest, err
	}
	definitionsRaw, err := canonicalJSON(definitions)
	if err != nil {
		return manifest, err
	}
	sourcesRaw, err := canonicalJSON(sources)
	if err != nil {
		return manifest, err
	}
	materials := map[string][]byte{"protocol.json": protocolRaw, "models.json": bytes.Clone(req.ModelsJSON), "capabilities.json": capabilitiesRaw, "generation-definitions.json": definitionsRaw, "source-files.json": sourcesRaw}
	manifest = FreezeManifest{SchemaVersion: 1, CreatedAt: time.Now().UTC(), ProjectDir: project, GitHEAD: head, BinarySHA256: gate.Digest(binary), ProtocolSHA256: gate.Digest(protocolRaw), Model: req.Protocol.Model, Execution: req.Protocol.Execution, Resources: req.Protocol.Resources, Materials: map[string]string{}}
	for name, raw := range materials {
		manifest.Materials[name] = gate.Digest(raw)
	}
	raw, err := canonicalJSON(manifest)
	if err != nil {
		return manifest, err
	}
	if err = ctx.Err(); err != nil {
		return manifest, err
	}
	if err = os.Mkdir(output, 0700); err != nil {
		return manifest, err
	}
	// Publish the manifest last. A partial directory is never reusable or valid.
	for _, name := range freezeMaterialNames {
		if err = writeFreezeFile(filepath.Join(output, name), materials[name]); err != nil {
			return manifest, err
		}
	}
	if err = writeFreezeFile(filepath.Join(output, "freeze.json"), raw); err != nil {
		return manifest, err
	}
	if err = syncFreezeDir(output); err != nil {
		return manifest, err
	}
	if err = syncFreezeDir(filepath.Dir(output)); err != nil {
		return manifest, err
	}
	manifest.FreezeSHA256 = gate.Digest(raw)
	return manifest, nil
}

// CheckFreeze is a read-only launch check. It rereads the actual source member
// set, HEAD, binary, provider response and all twelve model-visible policies.
// Checksums bind evidence; they do not authenticate a supplier or human identity.
func CheckFreeze(ctx context.Context, directory, projectDir, binaryPath string) (manifest FreezeManifest, err error) {
	if err = ctx.Err(); err != nil {
		return manifest, err
	}
	directory, err = gate.Canonical(directory)
	if err != nil {
		return manifest, err
	}
	raw, err := readLocal(directory, "freeze.json")
	if err != nil {
		return manifest, err
	}
	if err = Decode(raw, &manifest); err != nil {
		return manifest, err
	}
	if manifest.SchemaVersion != 1 || manifest.CreatedAt.IsZero() || !gitOIDOK(manifest.GitHEAD) || !digestOK(manifest.BinarySHA256) || !digestOK(manifest.ProtocolSHA256) || len(manifest.Materials) != len(freezeMaterialNames) {
		return manifest, errors.New("invalid freeze manifest")
	}
	materials := map[string][]byte{}
	for _, name := range freezeMaterialNames {
		expected, exists := manifest.Materials[name]
		if !exists || !digestOK(expected) {
			return manifest, errors.New("freeze material identity missing")
		}
		b, e := readLocal(directory, name)
		if e != nil {
			return manifest, e
		}
		if gate.Digest(b) != expected {
			return manifest, fmt.Errorf("frozen material changed: %s", name)
		}
		materials[name] = b
	}
	if manifest.ProtocolSHA256 != manifest.Materials["protocol.json"] {
		return manifest, errors.New("protocol identity differs from freeze")
	}
	var p Protocol
	if err = Decode(materials["protocol.json"], &p); err != nil {
		return manifest, err
	}
	if err = p.Validate(); err != nil {
		return manifest, err
	}
	if !reflect.DeepEqual(manifest.Model, p.Model) || manifest.Execution != p.Execution || manifest.Resources != p.Resources {
		return manifest, errors.New("frozen model or resources disagree with protocol")
	}
	var capabilities deepseek.Capabilities
	if err = Decode(materials["capabilities.json"], &capabilities); err != nil {
		return manifest, err
	}
	if err = validateCapabilities(p.Model, materials["models.json"], capabilities); err != nil {
		return manifest, err
	}
	definitions, err := freezeDefinitions(ctx, p, directory)
	if err != nil {
		return manifest, err
	}
	var frozenDefinitions map[string]runner.ExperimentDefinition
	if err = Decode(materials["generation-definitions.json"], &frozenDefinitions); err != nil {
		return manifest, err
	}
	actualDefinitionJSON, _ := json.Marshal(definitions)
	frozenDefinitionJSON, _ := json.Marshal(frozenDefinitions)
	if !bytes.Equal(actualDefinitionJSON, frozenDefinitionJSON) {
		return manifest, errors.New("actual generation definitions differ from freeze")
	}
	project, err := gate.Canonical(projectDir)
	if err != nil {
		return manifest, err
	}
	head, sources, err := sourceInventory(ctx, project)
	if err != nil {
		return manifest, err
	}
	var frozenSources map[string]string
	if err = Decode(materials["source-files.json"], &frozenSources); err != nil {
		return manifest, err
	}
	if head != manifest.GitHEAD || !reflect.DeepEqual(sources, frozenSources) {
		return manifest, errors.New("runtime source membership, bytes or HEAD changed")
	}
	binary, err := executableBytes(binaryPath)
	if err != nil {
		return manifest, err
	}
	if gate.Digest(binary) != manifest.BinarySHA256 {
		return manifest, errors.New("execution binary changed")
	}
	manifest.FreezeSHA256 = gate.Digest(raw)
	return manifest, nil
}

func freezeDefinitions(ctx context.Context, p Protocol, output string) (map[string]runner.ExperimentDefinition, error) {
	definitions := map[string]runner.ExperimentDefinition{}
	for _, t := range p.Tasks {
		if err := runner.ValidateTarget(t.Input, output); err != nil {
			return nil, err
		}
		paths := []string{t.Input.Repository.Directory, t.Input.Specification.Path}
		for _, dependency := range t.Input.Dependencies {
			paths = append(paths, dependency.Directory)
		}
		for _, path := range paths {
			canonical, err := gate.Canonical(path)
			if err != nil {
				return nil, err
			}
			if canonical != path {
				return nil, errors.New("frozen task paths must be canonical")
			}
		}
		identity, err := evaluation.PublishedIdentity(t.ReferenceVersion)
		if err != nil {
			return nil, err
		}
		if identity.SpecSHA256 != t.Input.Specification.SHA256 || identity.ReferenceSHA256 != t.ReferenceSHA256 || identity.ToolSHA256 != t.ReferenceToolSHA256 {
			return nil, fmt.Errorf("%s reference differs from published identity", t.ID)
		}
		for _, g := range []Group{A, B, C} {
			limit := t.DiagnosticLimit
			if g == A {
				limit = 0
			}
			definition, err := runner.DescribeExperiment(ctx, t.Input, runner.ExperimentGroup(g), limit)
			if err != nil {
				return nil, fmt.Errorf("%s/%s definition: %w", t.ID, g, err)
			}
			definitions[t.ID+"/"+string(g)] = definition
		}
	}
	return definitions, nil
}

func validateCapabilities(profile budget.Profile, raw []byte, c deepseek.Capabilities) error {
	if err := profile.Validate(); err != nil {
		return err
	}
	if len(raw) == 0 || len(raw) > 4<<20 || gate.Digest(raw) != profile.SourceSHA256 || !reflect.DeepEqual(c.Profile, profile) {
		return errors.New("model capability raw response or profile identity mismatch")
	}
	if len(c.Response) > 0 && !bytes.Equal(c.Response, raw) {
		return errors.New("capability response differs from supplied raw bytes")
	}
	endpoint, err := deepseek.ModelsEndpoint(profile.Endpoint)
	if err != nil || profile.Provider != "deepseek" || c.URL != endpoint {
		return errors.New("capability source does not match the configured provider endpoint")
	}
	var response struct {
		Object string `json:"object"`
		Data   []struct {
			ID               string   `json:"id"`
			Context          int64    `json:"context_window"`
			Output           int64    `json:"max_output_tokens"`
			InputModalities  []string `json:"input_modalities"`
			OutputModalities []string `json:"output_modalities"`
		} `json:"data"`
	}
	if err = json.Unmarshal(raw, &response); err != nil {
		return errors.New("invalid raw model capabilities")
	}
	matches := 0
	for _, model := range response.Data {
		if model.ID == profile.Model {
			matches++
			if model.Context != profile.ContextTokens || model.Output <= 0 || model.Output > model.Context || model.Output != c.ModelOutputLimit || profile.MaxOutputTokens > model.Output || !slices.Contains(model.InputModalities, "text") || !slices.Contains(model.OutputModalities, "text") {
				return errors.New("declared model capabilities differ from frozen limits")
			}
		}
	}
	if response.Object != "list" || matches != 1 {
		return errors.New("raw capabilities require one matching model")
	}
	return nil
}

func canonicalJSON(v any) ([]byte, error) {
	b, e := json.MarshalIndent(v, "", "  ")
	return append(b, '\n'), e
}
func gitOutput(ctx context.Context, project string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", project}, args...)...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	raw, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("read Git metadata: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return raw, nil
}
func sourceScopes(project string) ([]string, error) {
	scopes := []string{"cmd", "internal", "scenarios", "scripts", "vendor", "go.mod", "go.sum", "go.work", "go.work.sum", "Makefile", "Dockerfile", ".dockerignore", ".gitmodules"}
	entries, err := os.ReadDir(project)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() && (strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), ".sh") || strings.HasPrefix(entry.Name(), "Dockerfile.")) {
			scopes = append(scopes, entry.Name())
		}
	}
	sort.Strings(scopes)
	return slices.Compact(scopes), nil
}
func sourceInventory(ctx context.Context, project string) (string, map[string]string, error) {
	var err error
	project, err = gate.Canonical(project)
	if err != nil {
		return "", nil, err
	}
	top, err := gitOutput(ctx, project, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", nil, err
	}
	canonical, err := gate.Canonical(strings.TrimSpace(string(top)))
	if err != nil || canonical != project {
		return "", nil, errors.New("project must be the Git worktree root")
	}
	head, err := gitOutput(ctx, project, "rev-parse", "HEAD")
	if err != nil {
		return "", nil, err
	}
	scopes, err := sourceScopes(project)
	if err != nil {
		return "", nil, err
	}
	dirty, err := gitOutput(ctx, project, append([]string{"status", "--porcelain=v1", "-z", "--untracked-files=no", "--"}, scopes...)...)
	if err != nil {
		return "", nil, err
	}
	if len(dirty) != 0 {
		return "", nil, errors.New("runtime sources have uncommitted tracked changes")
	}
	untracked, err := gitOutput(ctx, project, append([]string{"ls-files", "--others", "-z", "--"}, scopes...)...)
	if err != nil {
		return "", nil, err
	}
	if len(untracked) != 0 {
		return "", nil, errors.New("runtime source scope contains untracked files")
	}
	tracked, err := gitOutput(ctx, project, append([]string{"ls-files", "--cached", "-z", "--"}, scopes...)...)
	if err != nil {
		return "", nil, err
	}
	inventory := map[string]string{}
	for _, name := range bytes.Split(tracked, []byte{0}) {
		if len(name) > 0 {
			relative := string(name)
			b, e := readLocal(project, relative)
			if e != nil {
				return "", nil, e
			}
			inventory[relative] = gate.Digest(b)
		}
	}
	if len(inventory) == 0 {
		return "", nil, errors.New("runtime source inventory is empty")
	}
	return strings.TrimSpace(string(head)), inventory, nil
}
func executableBytes(path string) ([]byte, error) {
	path, err := gate.Canonical(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || info.Size() > 256<<20 {
		return nil, errors.New("binary must be a bounded executable regular file")
	}
	return os.ReadFile(path)
}
func writeFreezeFile(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(raw)
	if err == nil {
		err = file.Sync()
	}
	return errors.Join(err, file.Close())
}
func syncFreezeDir(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

// finiteAmount is shared by spending checks without treating NaN as a budget.
func finiteAmount(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }

func gitOIDOK(value string) bool {
	b, e := hex.DecodeString(value)
	return e == nil && (len(b) == 20 || len(b) == 32) && value == fmt.Sprintf("%x", b)
}
