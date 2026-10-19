package evaluation

import (
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"strings"

	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2"
)

const Version = "rpcx-l2-reference-goddd-v1"
const godddSpecSHA256 = "917575e0f4fc571f6f72c743ffded00771da838144371435ab0edfb61b578610"
const referenceDataVersion = "rpcx-l2-reference-goddd-data-v2"
const referenceFreezeSHA256 = "5cd44c1d8c00f147809d41545086ce1c32ae7cfcc0f6bb47b867c8a7601715c7"

//go:embed data/goddd-v1/* data/goddd-v2/* data/translation-v1/* data/translation-v2/* data/workouts-v1/* *.go
var material embed.FS

func referenceData() (map[string][]byte, error) {
	return loadReferenceData("data/goddd-v2", referenceDataVersion, referenceFreezeSHA256, godddSpecSHA256)
}

const translationDataVersion = "rpcx-l2-reference-translation-data-v2"
const translationFreezeSHA256 = "1687c993a76ff6682111f0f6eb6b9662e4bc193b7e92d3d4baa2c2fdcf54acbd"

func translationData() (map[string][]byte, error) {
	return loadReferenceData("data/translation-v2", translationDataVersion, translationFreezeSHA256, translationSpecSHA256)
}

func loadReferenceData(directory, version, hash, spec string) (map[string][]byte, error) {
	files, err := fs.ReadDir(material, directory)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, file := range files {
		if file.IsDir() {
			return nil, errors.New("unexpected reference directory")
		}
		data, err := material.ReadFile(directory + "/" + file.Name())
		if err != nil {
			return nil, err
		}
		out[file.Name()] = data
	}
	if gate.Digest(out["freeze.json"]) != hash {
		return nil, errors.New("reference release identity changed")
	}
	var freeze struct {
		SchemaVersion int               `json:"schema_version"`
		Version       string            `json:"version"`
		SpecSHA256    string            `json:"spec_sha256"`
		Files         map[string]string `json:"files"`
	}
	if err := json.Unmarshal(out["freeze.json"], &freeze); err != nil {
		return nil, err
	}
	actual := gate.Hashes(out)
	delete(actual, "freeze.json")
	if freeze.SchemaVersion != 1 || freeze.Version != version || freeze.SpecSHA256 != spec || !maps.Equal(actual, freeze.Files) {
		return nil, errors.New("reference data does not match release")
	}
	return out, nil
}

func toolData() (map[string][]byte, error) {
	out := map[string][]byte{}
	names, err := fs.Glob(material, "*.go")
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := material.ReadFile(name)
		if err != nil {
			return nil, err
		}
		out["evaluation/"+name] = data
	}
	shared, err := gate.Materials()
	if err != nil {
		return nil, err
	}
	for name, data := range shared {
		out["gate/"+name] = data
	}
	modules, err := l2.ExecutionMaterials()
	if err != nil {
		return nil, err
	}
	for name, data := range modules {
		out["l2/"+name] = data
	}
	return out, nil
}

func production(files map[string][]byte) map[string][]byte {
	out := map[string][]byte{}
	for name, data := range files {
		if !strings.HasSuffix(name, "_test.go") {
			out[name] = data
		}
	}
	return out
}

const workoutsDataVersion = "rpcx-l2-reference-workouts-data-v1"
const workoutsFreezeSHA256 = "400e7c32a2e6b9cac3936aa6bd5e2f1c4911d96aeca44fc25a20ff58dab6f669"

func workoutsData() (map[string][]byte, error) {
	return loadReferenceData("data/workouts-v1", workoutsDataVersion, workoutsFreezeSHA256, workoutsSpecSHA256)
}
