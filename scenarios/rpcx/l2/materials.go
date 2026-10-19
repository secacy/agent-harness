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

// GodddMaterials 返回经过发布身份认证的依赖、测试身份和完整冻结清单。
func GodddMaterials() (map[string][]byte, error) {
	const root = "data/rpcx-l2-goddd-v1"
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
	if hash(files["freeze.json"]) != GodddFreezeSHA256 {
		return nil, errors.New("embedded Goddd release identity changed")
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
