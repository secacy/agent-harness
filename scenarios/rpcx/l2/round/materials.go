package round

import (
	"embed"
	"io/fs"
	"strings"

	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

//go:embed *.go
var source embed.FS

// 只归档本汇总包的实际生产源码，不声称是整个 Harness 或执行环境的身份。
func toolMaterials() (map[string][]byte, error) {
	files := map[string][]byte{}
	names, err := fs.Glob(source, "*.go")
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		raw, err := source.ReadFile(name)
		if err != nil {
			return nil, err
		}
		files[name] = raw
	}
	return files, nil
}

func saveTools(output string) (string, error) {
	files, err := toolMaterials()
	if err != nil {
		return "", err
	}
	if err = gate.CopyFiles(output, files); err != nil {
		return "", err
	}
	return gate.TreeHash(files), nil
}
