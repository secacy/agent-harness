package task

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTargetStructureRejectsAmbiguousIdentity(t *testing.T) {
	root := t.TempDir()
	hash := strings.Repeat("a", 64)
	base := Target{ID: "orders", ProfileID: "rpcx-profile-v1", BuildConfig: "default", Repository: Snapshot{root, hash}, Specification: Specification{filepath.Join(root, "spec.md"), hash}, Dependencies: map[string]Snapshot{}}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*Target){"id": func(t *Target) { t.ID = "../orders" }, "profile": func(t *Target) { t.ProfileID = "" }, "config": func(t *Target) { t.BuildConfig = "extra tag" }, "relative": func(t *Target) { t.Repository.Directory = "relative" }, "hash": func(t *Target) { t.Repository.SHA256 = "short" }, "reserved-root": func(t *Target) { t.Dependencies = map[string]Snapshot{"workspace": {root, hash}} }} {
		t.Run(name, func(t *testing.T) {
			target := base
			change(&target)
			if err := target.Validate(); err == nil {
				t.Fatal("invalid target accepted")
			}
		})
	}
}
