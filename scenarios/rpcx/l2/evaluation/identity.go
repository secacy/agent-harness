package evaluation

import (
	"errors"

	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

// Identity 仅包含已发布参考材料的身份，不包含测试正文或候选结论。
type Identity struct {
	Version         string `json:"version"`
	SpecSHA256      string `json:"spec_sha256"`
	ReferenceSHA256 string `json:"reference_sha256"`
	ToolSHA256      string `json:"tool_sha256"`
}

// PublishedIdentity 在生成前认证参考发布；只读取宿主材料，不执行仓库命令。
func PublishedIdentity(version string) (Identity, error) {
	var scenario referenceScenario
	switch version {
	case referenceDataVersion:
		scenario = godddScenario()
	case translationDataVersion:
		scenario = translationScenario()
	case GHCLIDataVersion:
		scenario = referenceScenario{dataVersion: GHCLIDataVersion, specSHA256: ghcliSpecSHA256, data: ghcliData}
	case workoutsDataVersion:
		scenario = workoutsScenario()
	default:
		return Identity{}, errors.New("unsupported reference data version")
	}
	data, err := scenario.data()
	if err != nil {
		return Identity{}, err
	}
	tools, err := toolData()
	if err != nil {
		return Identity{}, err
	}
	return Identity{Version: scenario.dataVersion, SpecSHA256: scenario.specSHA256,
		ReferenceSHA256: gate.Digest(data["freeze.json"]), ToolSHA256: gate.TreeHash(tools)}, nil
}
