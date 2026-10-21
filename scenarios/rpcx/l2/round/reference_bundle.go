package round

import (
	"crypto/sha256"
	"fmt"
	"path"
	"strings"

	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/baseline"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/evaluation"
)

// 新参考必须逐件声明完整依赖表示；删除索引引用不能把缺证据伪装成通过。
func readGHCLIReferenceBundle(r *reader, root string, report evaluation.Report) (bool, error) {
	files := map[string][]byte{}
	read := func(artifact, original string) (bool, error) {
		h := report.Artifacts[artifact]
		if !digest(h) {
			return false, fmt.Errorf("%w: required reference dependency artifact missing", ErrInvalid)
		}
		p, err := child(root, artifact)
		if err != nil {
			return false, err
		}
		data, present, err := r.read(p, h)
		if err == nil && present {
			files[original] = data
		}
		return present, err
	}
	for _, entry := range [][2]string{{"modules.json", "dependencies/modules.json"}, {"modules.bundle/bundle.json", "dependencies/bundle.json"}} {
		if present, err := read(entry[0], entry[1]); err != nil || !present {
			return false, err
		}
	}
	var index struct {
		SchemaVersion  int    `json:"schema_version"`
		Version        string `json:"version"`
		ManifestSHA256 string `json:"manifest_sha256"`
		LogicalSize    int64  `json:"logical_size"`
		LogicalSHA256  string `json:"logical_sha256"`
		Chunks         []struct {
			Name   string `json:"name"`
			Size   int64  `json:"size"`
			SHA256 string `json:"sha256"`
		} `json:"chunks"`
	}
	if len(files["dependencies/bundle.json"]) > 1<<20 || Decode(files["dependencies/bundle.json"], &index) != nil || index.SchemaVersion != 1 || index.Version != baseline.GHCLIVersion || index.ManifestSHA256 != gate.Digest(files["dependencies/modules.json"]) || len(index.Chunks) == 0 || len(index.Chunks) > 8 || index.LogicalSize <= 0 || index.LogicalSize > 64<<20 || !digest(index.LogicalSHA256) {
		return false, fmt.Errorf("%w: invalid reference dependency index", ErrInvalid)
	}
	expected := map[string]bool{"modules.bundle/bundle.json": true}
	logical := sha256.New()
	var size int64
	for i, chunk := range index.Chunks {
		name := fmt.Sprintf("dependencies/modules-%03d.tar.gz.part", i)
		if chunk.Name != name || chunk.Size <= 0 || chunk.Size > 8<<20 || !digest(chunk.SHA256) {
			return false, fmt.Errorf("%w: invalid reference dependency chunk order or identity", ErrInvalid)
		}
		artifact := "modules.bundle/" + path.Base(name)
		expected[artifact] = true
		if present, err := read(artifact, name); err != nil || !present {
			return false, err
		}
		data := files[name]
		if int64(len(data)) != chunk.Size || gate.Digest(data) != chunk.SHA256 {
			return false, fmt.Errorf("%w: reference dependency chunk length or digest mismatch", ErrInvalid)
		}
		size += chunk.Size
		if size > 64<<20 {
			return false, fmt.Errorf("%w: reference dependency bundle exceeds limit", ErrInvalid)
		}
		logical.Write(data)
	}
	for name := range report.Artifacts {
		if strings.HasPrefix(name, "modules.bundle/") && !expected[name] {
			return false, fmt.Errorf("%w: unexpected reference dependency artifact", ErrInvalid)
		}
	}
	actual, err := l2.HostMaterialsSHA256Version(files, baseline.GHCLIVersion)
	if err != nil || actual != report.HostMaterialsSHA256 || size != index.LogicalSize || fmt.Sprintf("%x", logical.Sum(nil)) != index.LogicalSHA256 {
		return false, fmt.Errorf("%w: reference dependency bundle identity mismatch", ErrInvalid)
	}
	return true, nil
}
