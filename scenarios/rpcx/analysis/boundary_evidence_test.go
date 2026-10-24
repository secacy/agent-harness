package analysis

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBoundaryEvidenceProtocol(t *testing.T) {
	req := scenario(t, "correct-timeout")
	result, err := Inspect(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string][]byte{}
	for _, call := range result.Report.Calls {
		sources[call.File], err = os.ReadFile(filepath.Join(req.ModuleDir, call.File))
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := ValidateBoundaryEvidence(result.Report, sources); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result.Report)
	for name, mutate := range map[string]func(*Report){
		"version":        func(r *Report) { r.Calls[0].Boundary.SchemaVersion++ },
		"missing-role":   func(r *Report) { r.Calls[0].Boundary.Nodes = r.Calls[0].Boundary.Nodes[1:] },
		"duplicate-role": func(r *Report) { r.Calls[0].Boundary.Nodes[1].Role = "function" },
		"range":          func(r *Report) { r.Calls[0].Boundary.Nodes[0].End++ },
		"hash":           func(r *Report) { r.Calls[0].Boundary.Nodes[0].SHA256 = "forged" },
		"foreign-file":   func(r *Report) { r.Calls[0].Boundary.Nodes[0].File = "../outside" },
		"role-swap": func(r *Report) {
			a, b := &r.Calls[0].Boundary.Nodes[1], &r.Calls[0].Boundary.Nodes[4]
			a.Role, b.Role = b.Role, a.Role
		},
		"non-covered": func(r *Report) { r.Calls[0].Status = "unknown" },
	} {
		t.Run(name, func(t *testing.T) {
			var r Report
			json.Unmarshal(raw, &r)
			mutate(&r)
			if ValidateBoundaryEvidence(r, sources) == nil {
				t.Fatal("corrupt boundary proof accepted")
			}
		})
	}
}
