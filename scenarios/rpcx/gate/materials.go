package gate

import (
	"embed"
	"io/fs"
)

//go:embed *.go
var materials embed.FS

// Materials 返回共用工具实际源码，不包含测试、候选或预期。
func Materials() (map[string][]byte, error) {
	out := map[string][]byte{}
	err := fs.WalkDir(materials, ".", func(name string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		if len(name) >= 8 && name[len(name)-8:] == "_test.go" {
			return nil
		}
		data, err := materials.ReadFile(name)
		out[name] = data
		return err
	})
	return out, err
}
