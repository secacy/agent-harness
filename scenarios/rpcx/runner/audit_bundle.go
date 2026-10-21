package runner

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path"

	"github.com/secacy/agent-harness/internal/audit"
)

// checkGHCLIBundle 逐片读取实际字节并按固定序列累计身份，沿用 View 的全部读取上限。
func (a *runAudit) checkGHCLIBundle(manifest, bundle string) {
	metadata, haveMetadata := a.bytes(manifest, true)
	index, haveIndex := a.bytes(bundle+"/bundle.json", true)
	if !haveMetadata || !haveIndex {
		return
	}
	var v struct {
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
	d := json.Unmarshal(index, &v)
	if d != nil || v.SchemaVersion != 1 || v.Version != "rpcx-l2-ghcli-v1" || v.ManifestSHA256 != digest(metadata) || len(v.Chunks) == 0 || len(v.Chunks) > 8 || v.LogicalSize <= 0 || v.LogicalSize > 64<<20 {
		a.issue(audit.Violation, "host_material_mismatch", bundle+"/bundle.json", "", "invalid dependency bundle index", a.file(manifest))
		return
	}
	hashes := map[string]string{"modules.json": digest(metadata), "bundle.json": digest(index)}
	logical := sha256.New()
	var total int64
	for i, c := range v.Chunks {
		expected := fmt.Sprintf("dependencies/modules-%03d.tar.gz.part", i)
		if c.Name != expected || c.Size <= 0 || c.Size > 8<<20 || !isDigest(c.SHA256) {
			a.issue(audit.Violation, "host_material_mismatch", bundle+"/bundle.json", "chunks", "invalid dependency chunk identity/order")
			return
		}
		name := bundle + "/" + path.Base(expected)
		data, ok := a.bytes(name, true)
		if !ok {
			return
		}
		if int64(len(data)) != c.Size || digest(data) != c.SHA256 {
			a.issue(audit.Violation, "host_material_mismatch", name, "", "dependency chunk length/digest mismatch")
			return
		}
		total += int64(len(data))
		if total > 64<<20 {
			a.issue(audit.Violation, "host_material_mismatch", name, "", "dependency bundle exceeds fixed limit")
			return
		}
		logical.Write(data)
		hashes[path.Base(expected)] = digest(data)
	}
	if total != v.LogicalSize || fmt.Sprintf("%x", logical.Sum(nil)) != v.LogicalSHA256 {
		a.issue(audit.Violation, "host_material_mismatch", bundle+"/bundle.json", "", "ordered logical dependency bytes mismatch")
	}
	raw, _ := json.Marshal(hashes)
	if digest(raw) != a.profile.HostMaterialsSHA256 {
		a.issue(audit.Violation, "host_material_mismatch", bundle+"/bundle.json", "", "dependency bundle differs from fixed profile")
	}
}
