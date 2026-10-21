package baseline

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

const GHCLIVersion = "rpcx-l2-ghcli-v1"
const GHCLIFreezeSHA256 = "ec8a6be346dc043cf97298ca815cc40c573c001a37ab002eca6aa57328a3ae47"

// validateGHCLIIdentity 核对宿主算法的模块与完整布局身份，旧 B 来源摘要单列保存。
func validateGHCLIIdentity(files map[string][]byte) error {
	var identity struct {
		LayoutSHA256 string `json:"layout_sha256"`
		Modules      map[string]struct {
			TreeSHA256 string `json:"tree_sha256"`
			FileCount  int    `json:"file_count"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(files["identity.json"], &identity); err != nil {
		return err
	}
	layout := gate.Module(files, "fixtures")
	if gate.TreeHash(layout) != identity.LayoutSHA256 || len(identity.Modules) != 3 {
		return errors.New("invalid published module layout identity")
	}
	count := 0
	for _, name := range []string{"ghcli", "rpcx-v1", "rpcx-v2"} {
		m := gate.Module(layout, name)
		expected, ok := identity.Modules[name]
		var total int
		for n, b := range m {
			if strings.HasPrefix(n, "fixtures/") || len(b) > 1<<20 {
				return errors.New("invalid business or SDK snapshot")
			}
			total += len(b)
		}
		if !ok || total > 16<<20 || len(m) != expected.FileCount || gate.TreeHash(m) != expected.TreeSHA256 {
			return errors.New("published module snapshot identity mismatch")
		}
		count += len(m)
	}
	if count != len(layout) {
		return errors.New("unexpected published module")
	}
	return nil
}
