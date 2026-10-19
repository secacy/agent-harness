package evaluation

import (
	"testing"

	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

func TestPublishedIdentityMatchesReferenceMaterials(t *testing.T) {
	tools, err := toolData()
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []referenceScenario{godddScenario(), translationScenario(), workoutsScenario()} {
		got, err := PublishedIdentity(scenario.dataVersion)
		data, dataErr := scenario.data()
		if err != nil || dataErr != nil || got.Version != scenario.dataVersion || got.SpecSHA256 != scenario.specSHA256 ||
			got.ReferenceSHA256 != gate.Digest(data["freeze.json"]) || got.ToolSHA256 != gate.TreeHash(tools) {
			t.Fatalf("published identity differs from report materials: %+v %v %v", got, err, dataErr)
		}
	}
	if _, err := PublishedIdentity("unknown"); err == nil {
		t.Fatal("unregistered reference identity accepted")
	}
}
