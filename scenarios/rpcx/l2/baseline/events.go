package baseline

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
)

type testInventory struct {
	SchemaVersion int                        `json:"schema_version"`
	Modules       map[string]moduleInventory `json:"modules"`
}

type moduleInventory struct {
	Packages             []string `json:"packages"`
	PackagesWithoutTests []string `json:"packages_without_tests"`
	OriginalTests        []string `json:"original_tests"`
	AddedTests           []string `json:"added_tests"`
}

func testEvents(data []byte, inventory moduleInventory) (int, error) {
	runs, passes, packages := map[string]int{}, map[string]int{}, map[string]int{}
	expectedPackages := map[string]string{}
	for _, name := range inventory.Packages {
		expectedPackages[name] = "pass"
	}
	for _, name := range inventory.PackagesWithoutTests {
		expectedPackages[name] = "skip"
	}
	expectedTests := map[string]int{}
	for _, names := range [][]string{inventory.OriginalTests, inventory.AddedTests} {
		for _, name := range names {
			if expectedTests[name] != 0 {
				return 0, fmt.Errorf("duplicate required test: %s", name)
			}
			expectedTests[name] = 1
		}
	}
	if len(expectedTests) == 0 || len(expectedPackages) == 0 {
		return 0, errors.New("empty test inventory")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var e struct{ Action, Package, Test string }
		if err := decoder.Decode(&e); err == io.EOF {
			break
		} else if err != nil {
			return 0, fmt.Errorf("invalid Go test events: %w", err)
		}
		if e.Test != "" {
			name := e.Package + "/" + e.Test
			switch e.Action {
			case "run":
				runs[name]++
			case "pass":
				passes[name]++
			case "fail", "skip":
				return 0, fmt.Errorf("test did not pass: %s (%s)", name, e.Action)
			}
		} else if e.Action == "pass" || e.Action == "skip" || e.Action == "fail" {
			if expectedPackages[e.Package] != e.Action {
				return 0, fmt.Errorf("unexpected package result: %s (%s)", e.Package, e.Action)
			}
			packages[e.Package]++
		}
	}
	if !maps.Equal(runs, expectedTests) || !maps.Equal(passes, expectedTests) {
		return 0, errors.New("test runs or passes do not match required inventory")
	}
	if len(packages) != len(expectedPackages) {
		return 0, errors.New("required package did not finish")
	}
	for name, count := range packages {
		if count != 1 {
			return 0, fmt.Errorf("duplicate package result: %s", name)
		}
	}
	return len(passes), nil
}
