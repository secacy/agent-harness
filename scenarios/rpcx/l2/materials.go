// Package l2 提供宿主使用的固定执行材料，不将候选预期交给 Agent。
package l2

import (
	"crypto/sha256"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

const GodddFreezeSHA256 = "3b00ca14b81d7de45fc8d72fe4dd7c01f3a83ff9d31722373782ea71d23cd9ce"

// 仅内嵌宿主材料和身份清单，业务模块由 Target 快照提供。
//
//go:embed data/rpcx-l2-goddd-v1/freeze.json data/rpcx-l2-goddd-v1/tests.json data/rpcx-l2-goddd-v1/dependencies
var material embed.FS

//go:embed data/rpcx-l2-translation-v1/freeze.json data/rpcx-l2-translation-v1/tests.json data/rpcx-l2-translation-v1/dependencies
var translationMaterial embed.FS

//go:embed modules.go materials.go baseline/data.go baseline/bundle.go baseline/ghcli.go baseline/dataset.go
var executionMaterial embed.FS

// ExecutionMaterials 保存实际装配与解包实现，供门禁绑定工具身份。
func ExecutionMaterials() (map[string][]byte, error) {
	files := map[string][]byte{}
	err := fs.WalkDir(executionMaterial, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := executionMaterial.ReadFile(name)
		files[name] = data
		return err
	})
	return files, err
}

const TranslationFreezeSHA256 = "d1e0e20608fed9e45212a6fb5010834028ab82228573a391c78a1500f7be8bdd"

func TranslationMaterials() (map[string][]byte, error) {
	return loadMaterials(translationMaterial, "data/rpcx-l2-translation-v1", TranslationFreezeSHA256)
}

// GodddMaterials 返回经过发布身份认证的依赖、测试身份和完整冻结清单。
func GodddMaterials() (map[string][]byte, error) {
	return loadMaterials(material, "data/rpcx-l2-goddd-v1", GodddFreezeSHA256)
}

func loadMaterials(material embed.FS, root, freeze string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := fs.WalkDir(material, root, func(name string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		data, err := material.ReadFile(name)
		if err == nil {
			files[strings.TrimPrefix(name, root+"/")] = data
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	hash := func(data []byte) string { return fmt.Sprintf("%x", sha256.Sum256(data)) }
	if hash(files["freeze.json"]) != freeze {
		return nil, errors.New("embedded release identity changed")
	}
	var release struct {
		Files map[string]string `json:"files"`
	}
	if err := json.Unmarshal(files["freeze.json"], &release); err != nil {
		return nil, err
	}
	for name, data := range files {
		if name != "freeze.json" && hash(data) != release.Files[name] {
			return nil, fmt.Errorf("host material differs from frozen release: %s", name)
		}
	}
	return files, nil
}

const WorkoutsFreezeSHA256 = "5efa3908ad6f80a8673a4893ef28bb6213a4a8d9a281bce4498726aa69282cb2"

//go:embed data/rpcx-l2-workouts-v1/freeze.json data/rpcx-l2-workouts-v1/tests.json data/rpcx-l2-workouts-v1/dependencies
var workoutsMaterial embed.FS

func WorkoutsMaterials() (map[string][]byte, error) {
	return loadMaterials(workoutsMaterial, "data/rpcx-l2-workouts-v1", WorkoutsFreezeSHA256)
}

const GHCLIFreezeSHA256 = "ec8a6be346dc043cf97298ca815cc40c573c001a37ab002eca6aa57328a3ae47"

//go:embed data/rpcx-l2-ghcli-v1/freeze.json data/rpcx-l2-ghcli-v1/tests.json data/rpcx-l2-ghcli-v1/identity.json data/rpcx-l2-ghcli-v1/dependencies
var ghcliMaterial embed.FS

// GHCLIMaterials 只返回专属宿主依赖及发布身份，不公开业务来源和纯规范以外的材料。
func GHCLIMaterials() (map[string][]byte, error) {
	return loadMaterials(ghcliMaterial, "data/rpcx-l2-ghcli-v1", GHCLIFreezeSHA256)
}
