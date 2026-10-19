package runner

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/secacy/agent-harness/internal/task"
	"github.com/secacy/agent-harness/scenarios/rpcx/goddd"
	"github.com/secacy/agent-harness/scenarios/rpcx/translation"
	"github.com/secacy/agent-harness/scenarios/rpcx/workouts"
)

const (
	ProfileT1T2        = "rpcx-demo-orders-t1t2-v1"
	ProfileT3          = "rpcx-demo-orders-t3-v1"
	ProfileGoddd       = "rpcx-goddd-t3-v1"
	ProfileTranslation = "rpcx-translation-t3-v1"
	ProfileWorkouts    = "rpcx-workouts-t3-v1"
)

//go:embed profiles.json
var profileData []byte

type profile struct {
	ID                  string                    `json:"id"`
	RepositoryName      string                    `json:"repository_name"`
	SpecVersion         string                    `json:"spec_version"`
	SpecSHA256          string                    `json:"spec_sha256"`
	RepositorySHA256    string                    `json:"repository_sha256"`
	Dependencies        map[string]string         `json:"dependencies"`
	ProtectedPaths      []string                  `json:"protected_paths"`
	BuildConfig         string                    `json:"build_config"`
	RequiredTests       []string                  `json:"required_tests"`
	Verifier            string                    `json:"verifier"`
	HostMaterialsSHA256 string                    `json:"host_materials_sha256,omitempty"`
	BuildConfigurations []task.BuildConfiguration `json:"build_configurations,omitempty"`
	PrerequisiteTests   map[string][]string       `json:"prerequisite_tests,omitempty"`
}

func lookupProfile(id string) (profile, error) {
	var profiles []profile
	if err := json.Unmarshal(profileData, &profiles); err != nil {
		return profile{}, err
	}
	for _, p := range profiles {
		if p.ID == id {
			return p, nil
		}
	}
	return profile{}, fmt.Errorf("unsupported rpcx profile: %s", id)
}

// ValidateTarget 校验宿主 Profile 和路径边界，不读取业务输入或执行基线。
func ValidateTarget(t task.Target, output string) error {
	if err := t.Validate(); err != nil {
		return err
	}
	p, err := lookupProfile(t.ProfileID)
	if err != nil {
		return err
	}
	if err := p.validate(t); err != nil {
		return err
	}
	_, err = normalizeTarget(t, output)
	return err
}
func (p profile) hash() string {
	data, _ := json.MarshalIndent(p, "", "  ")
	return digest(append(data, '\n'))
}
func (p profile) validate(t task.Target) error {
	if t.BuildConfig != p.BuildConfig || t.Repository.SHA256 != p.RepositorySHA256 || t.Specification.SHA256 != p.SpecSHA256 || len(t.Dependencies) != len(p.Dependencies) {
		return errors.New("target does not match the supported profile")
	}
	for name, hash := range p.Dependencies {
		if t.Dependencies[name].SHA256 != hash {
			return fmt.Errorf("unsupported dependency snapshot: %s", name)
		}
	}
	if !slices.Contains([]string{"rpcx-t1t2-evidence-v1", "rpcx-t3-evidence-v1", goddd.VerifierVersion, translation.VerifierVersion, workouts.VerifierVersion}, p.Verifier) {
		return errors.New("unsupported rpcx verifier")
	}
	if p.Verifier == goddd.VerifierVersion || p.Verifier == translation.VerifierVersion || p.Verifier == workouts.VerifierVersion {
		materials := goddd.HostMaterialsSHA256
		if p.Verifier == translation.VerifierVersion {
			materials = translation.HostMaterialsSHA256
		}
		if p.Verifier == workouts.VerifierVersion {
			materials = workouts.HostMaterialsSHA256
		}
		hash, err := materials()
		if err != nil {
			return err
		}
		if hash != p.HostMaterialsSHA256 {
			return errors.New("unsupported host materials")
		}
	}
	for name, tests := range p.PrerequisiteTests {
		if _, ok := p.Dependencies[name]; !ok || len(tests) == 0 {
			return errors.New("invalid prerequisite module test inventory")
		}
	}
	return task.ValidateBuildConfigurations(p.BuildConfigurations)
}

func (p profile) configurations() []task.BuildConfiguration {
	if len(p.BuildConfigurations) > 0 {
		return p.BuildConfigurations
	}
	return []task.BuildConfiguration{{ID: "default"}}
}

// TargetFromTranslation 使用宿主认证的双配置集合，不允许选择单一配置。
func TargetFromTranslation(id, fixtures, spec string) (task.Target, error) {
	return targetFromFixtures(id, fixtures, spec, ProfileTranslation)
}

// TargetFromGoddd 描述已发布的 Goddd 输入；不读取候选或评测预期。
func TargetFromGoddd(id, fixtures, spec string) (task.Target, error) {
	return targetFromFixtures(id, fixtures, spec, ProfileGoddd)
}

func targetFromFixtures(id, fixtures, spec, profileID string) (task.Target, error) {
	p, err := lookupProfile(profileID)
	if err != nil {
		return task.Target{}, err
	}
	fixtures, err = canonicalPath(fixtures)
	if err != nil {
		return task.Target{}, err
	}
	spec, err = canonicalPath(spec)
	if err != nil {
		return task.Target{}, err
	}
	t := task.Target{ID: id, ProfileID: p.ID, BuildConfig: p.BuildConfig,
		Repository:    task.Snapshot{Directory: filepath.Join(fixtures, p.RepositoryName), SHA256: p.RepositorySHA256},
		Specification: task.Specification{Path: spec, SHA256: p.SpecSHA256}, Dependencies: map[string]task.Snapshot{}}
	for name, hash := range p.Dependencies {
		t.Dependencies[name] = task.Snapshot{Directory: filepath.Join(fixtures, name), SHA256: hash}
	}
	return t, t.Validate()
}

// TargetFromScenario 只构造固定输入描述；实际读取与认证由 Run 完成。
func TargetFromScenario(id, scenario, specVersion string) (task.Target, error) {
	profileID := ProfileT1T2
	switch specVersion {
	case "", "v0.1.0":
	case "v0.2.0":
		profileID = ProfileT3
	default:
		return task.Target{}, errors.New("unsupported rpcx specification version")
	}
	p, err := lookupProfile(profileID)
	if err != nil {
		return task.Target{}, err
	}
	scenario, err = canonicalPath(scenario)
	if err != nil {
		return task.Target{}, err
	}
	t := task.Target{ID: id, ProfileID: p.ID, BuildConfig: p.BuildConfig, Repository: task.Snapshot{Directory: filepath.Join(scenario, "fixtures", p.RepositoryName), SHA256: p.RepositorySHA256}, Dependencies: map[string]task.Snapshot{}, Specification: task.Specification{Path: filepath.Join(scenario, "specs", p.SpecVersion+".md"), SHA256: p.SpecSHA256}}
	for name, hash := range p.Dependencies {
		t.Dependencies[name] = task.Snapshot{Directory: filepath.Join(scenario, "fixtures", name), SHA256: hash}
	}
	return t, t.Validate()
}

func readTarget(ctx context.Context, t task.Target, p profile) (tree, []byte, error) {
	files := tree{}
	snapshots := map[string]task.Snapshot{p.RepositoryName: t.Repository}
	for name, snapshot := range t.Dependencies {
		snapshots[name] = snapshot
	}
	for _, name := range sortedKeys(snapshots) {
		snapshot := snapshots[name]
		data, err := readTree(ctx, snapshot.Directory)
		if err != nil {
			return nil, nil, err
		}
		if data.hash() != snapshot.SHA256 {
			return nil, nil, fmt.Errorf("input snapshot changed: %s", name)
		}
		if _, ok := data["go.mod"]; !ok {
			return nil, nil, fmt.Errorf("module %s has no go.mod", name)
		}
		for path, content := range data {
			files[name+"/"+path] = content
		}
	}
	info, err := os.Lstat(t.Specification.Path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
		return nil, nil, errors.New("specification must be a regular file of at most 1 MiB")
	}
	spec, err := os.ReadFile(t.Specification.Path)
	if err != nil {
		return nil, nil, err
	}
	if digest(spec) != t.Specification.SHA256 {
		return nil, nil, errors.New("specification snapshot changed")
	}
	return files, spec, ctx.Err()
}

func normalizeTarget(t task.Target, output string) (task.Target, error) {
	var err error
	t.Repository.Directory, err = canonicalPath(t.Repository.Directory)
	if err != nil {
		return t, err
	}
	t.Specification.Path, err = canonicalPath(t.Specification.Path)
	if err != nil {
		return t, err
	}
	dependencies := make(map[string]task.Snapshot, len(t.Dependencies))
	inputs := []string{t.Repository.Directory, t.Specification.Path}
	for _, name := range sortedKeys(t.Dependencies) {
		snapshot := t.Dependencies[name]
		snapshot.Directory, err = canonicalPath(snapshot.Directory)
		if err != nil {
			return t, err
		}
		dependencies[name] = snapshot
		inputs = append(inputs, snapshot.Directory)
	}
	t.Dependencies = dependencies
	for i, input := range inputs {
		if within(output, input) || within(input, output) {
			return t, errors.New("runs directory overlaps a task input")
		}
		for _, other := range inputs[:i] {
			if within(input, other) || within(other, input) {
				return t, errors.New("task inputs must not overlap")
			}
		}
	}
	return t, nil
}

// TargetFromWorkouts 描述包含只读 common 模块的固定取消切片。
func TargetFromWorkouts(id, fixtures, spec string) (task.Target, error) {
	return targetFromFixtures(id, fixtures, spec, ProfileWorkouts)
}
