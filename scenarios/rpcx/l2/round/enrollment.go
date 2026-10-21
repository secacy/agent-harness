package round

import (
	"errors"
	"fmt"

	"github.com/secacy/agent-harness/internal/campaign"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
)

// Enrollment 在模型调用前固定任务与参考身份，动态计划身份在启动时补入 Manifest。
type Enrollment struct {
	SchemaVersion int         `json:"schema_version"`
	ID            string      `json:"id"`
	Kind          string      `json:"kind"`
	Targets       []TargetPin `json:"targets"`
	Protocol      *Protocol   `json:"protocol,omitempty"`
}

// Validate 核对输入顺序和宿主参考发布；不接受候选或既有门禁结论。
func (e Enrollment) Validate(targets []campaign.Target) error {
	return e.validate(targets, true)
}

func (e Enrollment) validate(targets []campaign.Target, published bool) error {
	if !identifier.MatchString(e.ID) || len(e.Targets) == 0 {
		return errors.New("valid enrollment identity, kind and targets are required")
	}
	if err := validatePins(e.Targets); err != nil {
		return err
	}
	if err := validateProtocol(e.SchemaVersion, e.Kind, e.Targets, e.Protocol); err != nil {
		return err
	}
	if len(targets) != len(e.Targets) {
		return errors.New("enrollment target count does not match the plan")
	}
	for i, pin := range e.Targets {
		t := targets[i].Input
		if pin.ID != t.ID || pin.ProfileID != t.ProfileID || pin.BuildConfig != t.BuildConfig || pin.BaselineSHA256 != t.Repository.SHA256 || pin.SpecSHA256 != t.Specification.SHA256 {
			return fmt.Errorf("enrollment input or ordering mismatch: %s", pin.ID)
		}
		if published {
			identity, err := evaluation.PublishedIdentity(pin.ReferenceVersion)
			if err != nil {
				return err
			}
			if pin.SpecSHA256 != identity.SpecSHA256 || pin.ReferenceSHA256 != identity.ReferenceSHA256 || pin.ToolSHA256 != identity.ToolSHA256 {
				return fmt.Errorf("enrollment reference identity mismatch: %s", pin.ID)
			}
		}
	}
	return nil
}

// Bind 只对实际落盘计划的原文取摘要，不预测其序列化或查询时间。
func (e Enrollment) Bind(planJSON []byte) (Manifest, error) {
	return e.bind(planJSON, true)
}

func (e Enrollment) bind(planJSON []byte, published bool) (Manifest, error) {
	var plan campaign.Plan
	if err := Decode(planJSON, &plan); err != nil {
		return Manifest{}, err
	}
	if err := plan.Profile.Validate(); err != nil {
		return Manifest{}, err
	}
	if err := e.validate(plan.Targets, published); err != nil {
		return Manifest{}, err
	}
	if e.Kind != Calibration && plan.Profile.Provider == "controlled" {
		return Manifest{}, errors.New("controlled model enrollment must remain development_calibration")
	}
	if err := e.Protocol.validatePlan(plan); err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{SchemaVersion: e.SchemaVersion, ID: e.ID, Kind: e.Kind,
		CampaignID: plan.ID, PlanSHA256: gate.Digest(planJSON), Targets: append([]TargetPin(nil), e.Targets...), Protocol: cloneProtocol(e.Protocol)}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}
