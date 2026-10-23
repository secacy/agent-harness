package round

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/secacy/agent-harness/internal/budget"
	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

const Formal = "formal_evaluation"
const DevelopmentSplit = "development"

// ModelContractBinding 只固定运行所用设置，实际查询原文由实际计划另行绑定。
const ModelContractBinding = "model-contract-v1"

var protocolMaterialNames = []string{"protocol", "dataset", "implementation", "environment"}

// Protocol 绑定宿主发布的评测约定；材料摘要不认证说明内容或人员身份的真实性。
type Protocol struct {
	ID                 string              `json:"id"`
	Split              string              `json:"split"`
	AttemptsPerTarget  int                 `json:"attempts_per_target"`
	PlanSettingsSHA256 string              `json:"plan_settings_sha256"`
	CapabilityBinding  string              `json:"capability_binding,omitempty"`
	Execution          execution.Info      `json:"execution"`
	Reviewers          map[string]string   `json:"reviewers"`
	Materials          map[string]Artifact `json:"materials"`
}

func validateProtocol(schema int, kind string, targets []TargetPin, p *Protocol) error {
	if schema == 1 {
		if p != nil || kind != Calibration && kind != Development {
			return errors.New("schema 1 only supports development rounds without a protocol contract")
		}
		// 旧格式的新应用入口仍限校准；效果评测必须绑定 schema 2 协议。
		for _, pin := range targets {
			if pin.ProfileID == "rpcx-ghcli-t3-v1" && kind != Calibration {
				return errors.New("schema 1 GHCLI rounds must remain development_calibration")
			}
		}
		return nil
	}
	if schema != 2 || p == nil || kind != Calibration && kind != Development && kind != Formal {
		return errors.New("schema 2 requires a protocol contract and a supported round kind")
	}
	if !identifier.MatchString(p.ID) || p.Split != DevelopmentSplit || p.AttemptsPerTarget != 1 || !digest(p.PlanSettingsSHA256) {
		return errors.New("a versioned protocol ID, supported development split, one attempt and settings digest are required")
	}
	if p.CapabilityBinding != "" && p.CapabilityBinding != ModelContractBinding {
		return errors.New("unsupported protocol capability binding")
	}
	if len(p.Reviewers) != len(targets) || len(p.Materials) != len(protocolMaterialNames) {
		return errors.New("protocol requires exact target reviewers and four frozen material identities")
	}
	switch p.Execution.Kind {
	case execution.KindLocal:
		if p.Execution != (execution.Info{Kind: execution.KindLocal}) {
			return errors.New("local execution cannot carry container settings")
		}
	case execution.KindDocker:
		if !strings.HasPrefix(p.Execution.Image, "sha256:") || !digest(strings.TrimPrefix(p.Execution.Image, "sha256:")) || p.Execution.CPUs < 1 || p.Execution.MemoryBytes < 32<<20 || p.Execution.Pids < 8 {
			return errors.New("Docker evaluation requires a fixed image identity and positive resource limits")
		}
	default:
		return errors.New("a supported frozen execution backend is required")
	}
	for _, pin := range targets {
		// 已见 Profile 不能通过换 split 名称认证成留出；新增数据须另行发布和接入。
		switch pin.ProfileID {
		case "rpcx-goddd-t3-v1", "rpcx-translation-t3-v1", "rpcx-workouts-t3-v1", "rpcx-ghcli-t3-v1":
		default:
			return fmt.Errorf("unsupported protocol profile: %s", pin.ProfileID)
		}
		reviewer := p.Reviewers[pin.ID]
		if reviewer == "" || strings.TrimSpace(reviewer) != reviewer {
			return fmt.Errorf("a fixed reviewer is required for target: %s", pin.ID)
		}
	}
	for _, name := range protocolMaterialNames {
		a := p.Materials[name]
		if !filepath.IsAbs(a.Path) || !digest(a.SHA256) {
			return fmt.Errorf("absolute material path and digest are required: %s", name)
		}
	}
	return nil
}

// PlanSettingsSHA256 仅排除运行分配的 ID 和查询时间，保留能力原文身份及 Plan 中全部设置。
func PlanSettingsSHA256(plan campaign.Plan) (string, error) {
	plan.ID = ""
	plan.Profile.VerifiedAt = time.Time{}
	raw, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	return gate.Digest(raw), nil
}

// SettingsSHA256 按协议的显式策略计算设置身份；缺省保留历史原文匹配语义。
func (p *Protocol) SettingsSHA256(plan campaign.Plan) (string, error) {
	switch p.CapabilityBinding {
	case "":
	case ModelContractBinding:
		plan.Profile.SourceSHA256 = ""
	default:
		return "", errors.New("unsupported protocol capability binding")
	}
	return PlanSettingsSHA256(plan)
}

func (p *Protocol) validatePlan(plan campaign.Plan) error {
	if p == nil {
		return nil
	}
	if plan.Recovery != nil {
		return errors.New("single-attempt evaluation does not allow a recovery policy")
	}
	hash, err := p.SettingsSHA256(plan)
	if err != nil {
		return err
	}
	if hash != p.PlanSettingsSHA256 {
		return errors.New("actual campaign settings differ from the frozen protocol")
	}
	return nil
}

func cloneProtocol(p *Protocol) *Protocol {
	if p == nil {
		return nil
	}
	copy := *p
	copy.Reviewers = maps.Clone(p.Reviewers)
	copy.Materials = maps.Clone(p.Materials)
	return &copy
}

// ReadProtocolMaterials 在任何外部访问前固定有界说明文件；output 不得与材料重叠。
// 返回的副本由宿主在准备屏障发布，后续不再依赖可变的原路径。
func ReadProtocolMaterials(p *Protocol, output string) (map[string][]byte, error) {
	if p == nil {
		return nil, nil
	}
	output, err := gate.Canonical(output)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]byte, len(protocolMaterialNames))
	for _, name := range protocolMaterialNames {
		a := p.Materials[name]
		path, err := gate.Canonical(a.Path)
		if err != nil {
			return nil, err
		}
		if gate.Overlap(path, output) {
			return nil, errors.New("protocol material overlaps batch output")
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			f.Close()
			return nil, errors.New("protocol material must be a regular file of at most 1 MiB")
		}
		raw, readErr := io.ReadAll(io.LimitReader(f, (1<<20)+1))
		if err := errors.Join(readErr, f.Close()); err != nil {
			return nil, err
		}
		if len(raw) == 0 || len(raw) > 1<<20 || gate.Digest(raw) != a.SHA256 {
			return nil, fmt.Errorf("protocol material is empty, oversized or has a digest mismatch: %s", name)
		}
		result[name] = raw
	}
	return result, nil
}

func verifyProtocol(r *reader, manifest Manifest, planJSON []byte, campaignDir string, manifestSHA256 string) error {
	if manifest.Protocol == nil {
		return nil
	}
	root := filepath.Dir(campaignDir)
	var enrollment Enrollment
	enrollmentJSON, exists, err := r.read(filepath.Join(root, "enrollment.json"), "")
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: pre-generation enrollment is required", ErrInvalid)
	}
	if err := Decode(enrollmentJSON, &enrollment); err != nil {
		return fmt.Errorf("%w: invalid pre-generation enrollment: %v", ErrInvalid, err)
	}
	// 归档材料与实际参考另行核对；离线重算不强制它等于今天的参考工具发布。
	bound, err := enrollment.bind(planJSON, false)
	if err != nil || !reflect.DeepEqual(bound, manifest) {
		return fmt.Errorf("%w: protocol enrollment binding mismatch: %v", ErrInvalid, err)
	}
	if manifest.Protocol.CapabilityBinding == ModelContractBinding {
		var plan campaign.Plan
		if err := Decode(planJSON, &plan); err != nil {
			return err
		}
		if err := verifyCapabilitySources(r, root, plan.Profile); err != nil {
			return err
		}
	}
	if _, exists, err = r.read(filepath.Join(root, "round-manifest.json"), manifestSHA256); err != nil {
		return err
	} else if !exists {
		return fmt.Errorf("%w: pre-generation manifest is required", ErrInvalid)
	}
	var backend execution.Info
	if exists, err = r.json(filepath.Join(root, "execution.json"), "", &backend); err != nil {
		return err
	} else if !exists || backend != manifest.Protocol.Execution {
		return fmt.Errorf("%w: execution backend differs from the frozen protocol", ErrInvalid)
	}
	for _, name := range protocolMaterialNames {
		if _, exists, err = r.read(filepath.Join(root, "protocol-materials", name), manifest.Protocol.Materials[name].SHA256); err != nil {
			return err
		} else if !exists {
			return fmt.Errorf("%w: protocol material is required: %s", ErrInvalid, name)
		}
	}
	return nil
}

// verifyCapabilitySources 核对本次原文归属；宿主对供应商声明的解释仍是可信边界。
func verifyCapabilitySources(r *reader, root string, profile budget.Profile) error {
	if _, exists, err := r.read(filepath.Join(root, "models.json"), profile.SourceSHA256); err != nil {
		return err
	} else if !exists {
		return fmt.Errorf("%w: current model response is required", ErrInvalid)
	}
	var capabilities struct {
		Profile budget.Profile `json:"profile"`
	}
	if exists, err := r.json(filepath.Join(root, "capabilities.json"), "", &capabilities); err != nil {
		return err
	} else if !exists || capabilities.Profile != profile {
		return fmt.Errorf("%w: discovered capability profile differs from the actual plan", ErrInvalid)
	}
	return nil
}
