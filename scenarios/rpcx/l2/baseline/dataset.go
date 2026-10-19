package baseline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
)

const TranslationVersion = "rpcx-l2-translation-v1"
const WorkoutsVersion = "rpcx-l2-workouts-v1"

type buildConfiguration struct {
	ID          string
	Tags        []string
	ActiveFile  string
	IgnoredFile string
}
type dataset struct {
	Version        string
	Business       string
	FreezeSHA256   string
	Configurations []buildConfiguration
	LocalModules   []string
}

// modules 先验证只读本地依赖，再验证业务模块；每项都独立执行完整基线。
func (d dataset) modules() []string {
	out := append([]string{"rpcx-v1", "rpcx-v2"}, d.LocalModules...)
	return append(out, d.Business)
}

// 发布版本只选择宿主已知的准备流程，不接受任意测试命令或候选配置。
func lookupDataset(version string) (dataset, error) {
	switch version {
	case Version:
		return dataset{Version: version, Business: "goddd", FreezeSHA256: "3b00ca14b81d7de45fc8d72fe4dd7c01f3a83ff9d31722373782ea71d23cd9ce", Configurations: []buildConfiguration{{ID: "default"}}}, nil
	case TranslationVersion:
		return dataset{Version: version, Business: "translation", FreezeSHA256: "d1e0e20608fed9e45212a6fb5010834028ab82228573a391c78a1500f7be8bdd", Configurations: []buildConfiguration{
			{ID: "default", ActiveFile: "translation_google.go", IgnoredFile: "translation_rpcx_alternate.go"},
			{ID: "rpcx_alternate", Tags: []string{"rpcx_alternate"}, ActiveFile: "translation_rpcx_alternate.go", IgnoredFile: "translation_google.go"},
		}}, nil
	case WorkoutsVersion:
		return dataset{Version: version, Business: "trainings", FreezeSHA256: "5efa3908ad6f80a8673a4893ef28bb6213a4a8d9a281bce4498726aa69282cb2", LocalModules: []string{"common"}, Configurations: []buildConfiguration{{ID: "default"}}}, nil
	default:
		return dataset{}, fmt.Errorf("unsupported baseline dataset: %s", version)
	}
}

// configurationFiles 核对真实 go list 结果，防止只换标签却仍编译同一 RPC 文件。
func configurationFiles(data []byte, config buildConfiguration) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	adapter, mocks := false, false
	for {
		var p struct {
			ImportPath                            string
			GoFiles, IgnoredGoFiles, XTestGoFiles []string
		}
		if err := decoder.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			return fmt.Errorf("decode build configuration: %w", err)
		}
		switch p.ImportPath {
		case "github.com/evrone/go-clean-template/internal/repo/webapi":
			if adapter || !slices.Contains(p.GoFiles, config.ActiveFile) || slices.Contains(p.GoFiles, config.IgnoredFile) || !slices.Contains(p.IgnoredGoFiles, config.IgnoredFile) {
				return fmt.Errorf("unexpected RPC source selection for %s", config.ID)
			}
			adapter = true
		case "github.com/evrone/go-clean-template/internal/usecase":
			if mocks || !slices.Contains(p.XTestGoFiles, "mocks_repo_test.go") || !slices.Contains(p.XTestGoFiles, "translation_test.go") {
				return fmt.Errorf("original tests or generated mock not selected for %s", config.ID)
			}
			mocks = true
		}
	}
	if !adapter || !mocks {
		return fmt.Errorf("missing package evidence for %s", config.ID)
	}
	return nil
}

// ValidateTranslationConfiguration 复用已发布双配置的实际源文件与 mock 选择检查。
func ValidateTranslationConfiguration(data []byte, id string) error {
	ds, _ := lookupDataset(TranslationVersion)
	for _, config := range ds.Configurations {
		if config.ID == id {
			return configurationFiles(data, config)
		}
	}
	return fmt.Errorf("unsupported Translation configuration: %s", id)
}
