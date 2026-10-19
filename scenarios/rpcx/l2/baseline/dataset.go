package baseline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
)

const TranslationVersion = "rpcx-l2-translation-v1"

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
