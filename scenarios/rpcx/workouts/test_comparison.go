package workouts

import (
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/testintegrity"
)

// CompareTests 复用门禁的原测试适配与比较；可信调用方须先认证完整基线和材料身份。
func CompareTests(base, candidate map[string][]byte, material string) (testintegrity.Report, map[string][]byte, error) {
	adapted, err := adapt(gate.TestFiles(base))
	if err != nil {
		return testintegrity.Report{}, nil, err
	}
	report, err := testintegrity.CompareAdapted(gate.TestFiles(base), adapted, gate.TestFiles(candidate), testintegrity.Plan{Version: "rpcx-workouts-test-structure-v1", SpecSHA256: SpecSHA256(), MaterialSHA256: material, BaselineFiles: gate.Hashes(gate.TestFiles(base)), AdditionalTestImports: []string{"errors", "time"}})
	return report, adapted, err
}

// TestMaterials 保存测试比较实际依赖的宿主源码，不含参考答案。
func TestMaterials() (map[string][]byte, error) {
	out, err := testintegrity.Materials()
	if err != nil {
		return nil, err
	}
	result := map[string][]byte{}
	for name, data := range out {
		result["testintegrity/"+name] = data
	}
	for _, name := range []string{"data.go", "structure.go", "test_comparison.go"} {
		data, err := materials.ReadFile(name)
		if err != nil {
			return nil, err
		}
		result["workouts/"+name] = data
	}
	shared, err := gate.Materials()
	if err != nil {
		return nil, err
	}
	for name, data := range shared {
		result["gate/"+name] = data
	}
	return result, nil
}
