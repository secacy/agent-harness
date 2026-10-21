package ghcli

import (
	"reflect"

	"github.com/secacy/agent-harness/internal/quality"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

// dependencyStatus 可信基线解析失败是宿主错误，不能被候选的 unknown 掩盖。
func dependencyStatus(baseline, candidate []byte) (string, error) {
	expected, err := gate.ModEntries(migratedMod(baseline))
	if err != nil {
		return "", err
	}
	actual, err := gate.ModEntries(candidate)
	if err != nil {
		return quality.StatusUnknown, nil
	}
	if !reflect.DeepEqual(expected, actual) {
		return quality.StatusFail, nil
	}
	return quality.StatusPass, nil
}
