package evaluation

import (
	"errors"
	"reflect"

	"github.com/secacy/agent-harness/scenarios/rpcx/l2"
)

const WorkoutsVersion = "rpcx-l2-reference-workouts-v1"
const workoutsPackage = "github.com/ThreeDotsLabs/wild-workouts-go-ddd-example/internal/trainings"
const workoutsSpecSHA256 = "789d30540799249d0f5d96b3ba921b7e00d4cbc1e457491a2a79eba89829a526"

func workoutsScenario() referenceScenario {
	return referenceScenario{version: WorkoutsVersion, module: "trainings", specSHA256: workoutsSpecSHA256, dataVersion: workoutsDataVersion,
		data: workoutsData, materials: l2.WorkoutsMaterials, protected: []string{"LICENSE", "go.sum"},
		probeFile: "adapters/l2_reference_test.go", probeData: "workouts_test.go.txt", probePackage: "./adapters", behaviorPattern: "^TestL2ReferenceCancellation$",
		behaviorTests: workoutsReferenceTests(), configurations: []buildConfiguration{{id: "default"}}}
}

var workoutsModes = []string{"attendee-free", "trainer-free", "trainer-penalty", "zero-balance", "users-error", "trainer-error", "repository-error", "already-cancelled", "callback-cancelled", "callback-expired", "short-callback", "long-callback", "users-parent-cancel", "trainer-parent-cancel", "per-call-origin", "repeat"}

func workoutsReferenceTests() []string {
	out := []string{workoutsPackage + "/adapters/TestL2ReferenceCancellation"}
	for _, mode := range workoutsModes {
		out = append(out, workoutsPackage+"/adapters/TestL2ReferenceCancellation/"+mode)
	}
	return out
}

func workoutsSteps() []referenceStep {
	out := []referenceStep{{"behavior", "^TestL2ReferenceCancellation$", workoutsReferenceTests()}}
	for _, client := range []string{"users", "trainer"} {
		out = append(out, referenceStep{"expiry-" + client, "^TestL2ReferenceExpiry$/^" + client + "$", []string{workoutsPackage + "/adapters/TestL2ReferenceExpiry", workoutsPackage + "/adapters/TestL2ReferenceExpiry/" + client}})
	}
	return out
}

func checkCommon(run commandRunner, dir, hash string, tests testInventory) (ModuleReport, error) {
	out := ModuleReport{SHA256: hash, Checks: map[string]Observation{"build": {Status: Inconclusive, Reason: "not executed"}, "tests": {Status: Inconclusive, Reason: "not executed"}}}
	result, err := run("common-build", dir, "go", "build", "-o", "/dev/null", "./...")
	if err != nil {
		return out, err
	}
	if result.ExitCode != 0 {
		return out, errors.New("authenticated common module failed reference build")
	}
	out.Checks["build"] = Observation{Status: Pass}
	result, err = run("common-tests", dir, "go", "test", "-json", "-count=1", "-timeout=45s", "./...")
	if err != nil {
		return out, err
	}
	out.Checks["tests"] = observe(result.Stdout, result.ExitCode, append(append([]string{}, tests.Original...), tests.Added...), tests.Packages, tests.Empty, false)
	if out.Checks["tests"].Status != Pass {
		out.Checks["tests"] = Observation{Status: Inconclusive, Reason: "authenticated common tests could not be confirmed"}
		return out, errors.New("authenticated common module tests could not be confirmed")
	}
	return out, nil
}

func automaticSectionsFor(version string) []string {
	sections := append([]string{}, automaticSections...)
	if version == WorkoutsVersion {
		sections = append(sections, "local_modules")
	}
	return sections
}

func localModuleObservation(modules map[string]ModuleReport) Observation {
	common := modules["common"]
	return combineObservations(common.Checks["build"], common.Checks["tests"])
}

func mergeLocalModules(report *Report) {
	report.Checks["local_modules"] = localModuleObservation(report.LocalModules)
}

func validateLocalModules(report Report) error {
	if report.Version != WorkoutsVersion {
		if len(report.LocalModules) != 0 {
			return errors.New("unexpected local module records")
		}
		return nil
	}
	record, ok := report.LocalModules["common"]
	if !ok || len(report.LocalModules) != 1 || len(record.Checks) != 2 {
		return errors.New("common module build and tests are required")
	}
	for _, name := range []string{"build", "tests"} {
		if !validStatus(record.Checks[name].Status) {
			return errors.New("invalid common module check")
		}
		if record.Checks[name].Status == Pass && len(record.SHA256) != 64 {
			return errors.New("common module digest is required")
		}
	}
	if report.Checks["expiry"].Status == Pass {
		for _, step := range workoutsSteps()[1:] {
			for _, test := range step.required {
				if report.Checks["expiry"].Tests[test] != "pass" {
					return errors.New("both Workouts client expiry events are required")
				}
			}
		}
	}
	if !reflect.DeepEqual(report.Checks["local_modules"], localModuleObservation(report.LocalModules)) {
		return errors.New("local module aggregate is inconsistent")
	}
	return nil
}
