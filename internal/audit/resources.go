package audit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/internal/writer"
)

func (i *inspection) resourceRecord(p string, v any) bool {
	b, err := i.view.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return false
	}
	if err != nil {
		i.add(Gap, "resource_unreadable", p, "", err.Error())
		return false
	}
	if err = execution.DecodeResource(b, v); err != nil {
		i.add(Violation, "resource_format_invalid", p, "", err.Error())
		return false
	}
	return true
}

// 这里只核对原始记录，不连接 Docker，也不把 removed 声明当作当前静止证明。
func (i *inspection) checkResources() {
	entries, err := i.view.ReadDir("resources")
	if i.plan == nil || i.record == nil {
		if err == nil {
			i.add(Gap, "resource_context_missing", "resources", "", "resource records require a supported plan")
		}
		return
	}
	if i.plan.Resources == nil {
		if err == nil {
			i.add(Violation, "resource_protocol_missing", "resources", "", "original plan does not declare resource ownership", "plan.json")
		}
		return
	}
	i.report.Coverage = append(i.report.Coverage, Coverage{Area: "resource_records", Complete: true})
	i.report.ResourceState = "recorded_pending"
	if i.record.SchemaVersion != 2 || i.record.WriterProtocol != writer.Protocol {
		i.add(Violation, "resource_protocol_missing", "plan.json", "resources", "resource ownership requires creation-time writer protocol")
		return
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			i.add(Gap, "resource_registry_missing", "resources", "", "resource initialization did not complete", "plan.json")
		} else {
			i.add(Gap, "resource_unreadable", "resources", "", err.Error())
		}
		return
	}
	expected := execution.ResourceRegistry{SchemaVersion: 1, CampaignID: i.record.ID, PlanSHA256: i.record.PlanSHA256, Policy: *i.plan.Resources}
	var registry execution.ResourceRegistry
	if !i.resourceRecord("resources/registry.json", &registry) {
		if i.missing("resources/registry.json") {
			i.add(Gap, "resource_registry_missing", "resources/registry.json", "", "resource initialization did not complete")
		}
		return
	}
	if registry != expected || registry.Policy.Validate() != nil {
		i.add(Violation, "resource_registry_mismatch", "resources/registry.json", "", "registry differs from the original plan", "plan.json")
		return
	}
	allRemoved := true
	for _, dir := range entries {
		if dir.Name() == "registry.json" {
			continue
		}
		if strings.HasPrefix(dir.Name(), ".resource-") && dir.Type().IsRegular() {
			continue
		}
		if !dir.IsDir() || !resourceCommandPattern(dir.Name()) {
			i.add(Violation, "resource_entry_invalid", "resources", "", "unexpected registry member")
			continue
		}
		p := "resources/" + dir.Name()
		members, listErr := i.view.ReadDir(p)
		if listErr != nil {
			i.add(Gap, "resource_unreadable", p, "", listErr.Error())
			continue
		}
		var e execution.ResourceEntry
		if !i.resourceRecord(p+"/intent.json", &e.Intent) {
			unpublished := i.missing(p + "/intent.json")
			for _, m := range members {
				if !m.Type().IsRegular() || !strings.HasPrefix(m.Name(), ".resource-") {
					unpublished = false
				}
			}
			if !unpublished {
				i.add(Gap, "resource_intent_missing", p, "", "command material lacks its pre-create intent")
			}
			continue
		}
		for _, f := range []struct {
			name  string
			value any
		}{{"dispatch.json", &e.Dispatch}, {"created.json", &e.Created}, {"removed.json", &e.Removed}} {
			i.resourceRecord(p+"/"+f.name, f.value)
		}
		for _, m := range members {
			if m.Name() != "intent.json" && m.Name() != "dispatch.json" && m.Name() != "created.json" && m.Name() != "removed.json" && !(m.Type().IsRegular() && strings.HasPrefix(m.Name(), ".resource-")) {
				i.add(Violation, "resource_entry_invalid", p, "", "unexpected command member")
			}
		}
		if e.Intent.CommandID != dir.Name() || execution.ValidateResourceEntry(e, registry) != nil {
			i.add(Violation, "resource_record_invalid", p+"/intent.json", "", "resource ownership or publication provenance is invalid", p+"/dispatch.json", p+"/created.json", p+"/removed.json", "resources/registry.json")
			continue
		}
		owner := e.Intent.Owner
		rp := owner.RunDirectory + "/run.json"
		var run RunRecord
		if !i.read(rp, &run, 2) {
			if i.missing(rp) {
				i.add(Violation, "resource_run_missing", p+"/intent.json", "", "resource lacks its owning Run", rp)
			}
			continue
		}
		known := false
		for _, t := range i.plan.Targets {
			if t.Input.ID == owner.TargetID {
				known = true
			}
		}
		if !known || run.ID != owner.RunID || run.TargetID != owner.TargetID || run.Directory != filepath.Join(i.record.Directory, filepath.FromSlash(owner.RunDirectory)) {
			i.add(Violation, "resource_run_mismatch", p+"/intent.json", "", "resource owner differs from the fixed Run", rp, "plan.json")
		}
		if e.Removed == nil {
			allRemoved = false
			i.add(Observation, "resource_pending", p+"/intent.json", "", "resource requires live reconciliation; absence is not inferred from records", p+"/dispatch.json", p+"/created.json")
		}
	}
	if allRemoved {
		i.report.ResourceState = "recorded_removed"
	}
}

func resourceCommandPattern(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'f' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
