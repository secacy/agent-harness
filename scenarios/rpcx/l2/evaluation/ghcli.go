package evaluation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2"
	"github.com/secacy/agent-harness/scenarios/rpcx/l2/baseline"
)

const GHCLIVersion = "rpcx-l2-reference-ghcli-v2"
const GHCLIDataVersion = "rpcx-l2-reference-ghcli-data-v2"
const ghcliSpecSHA256 = "6e3a87056ec102536b52e1ffc340dbd8dd541d8d28589c555b9b08bafc1e40e8"
const ghcliFreezeSHA256 = "feb693bc5eb75aa1876bcf7ddc5bde5b8d28bedca92ff9b32fd0f7dea88acf8f"
const ghcliPackage = "github.com/cli/cli/v2/pkg/cmd/release/delete"
const ghcliDir = "pkg/cmd/release/delete"

func ghcliData() (map[string][]byte, error) {
	return loadReferenceData("data/ghcli-v2", GHCLIDataVersion, ghcliFreezeSHA256, ghcliSpecSHA256)
}

// EvaluateGHCLI 使用单独生产副本与固定参考探针，不接受基线豁免或调用方测试选择。
func EvaluateGHCLI(ctx context.Context, req Request) (report Report, err error) {
	if !ghcliID.MatchString(req.TargetID) || !ghcliID.MatchString(req.RunID) || len(req.CandidateSHA256) != 64 || req.Backend == nil {
		return report, errors.New("registered target, run, candidate digest and backend are required")
	}
	if req.Backend.Info().Kind != execution.KindDocker && !(req.TrustedLocal && req.Backend.Info().Kind == execution.KindLocal) {
		return report, errors.New("local reference execution requires explicit trust")
	}
	paths := []*string{&req.FixturesDir, &req.CandidateDir, &req.SpecPath, &req.EvidenceDir}
	for _, p := range paths {
		*p, err = gate.Canonical(*p)
		if err != nil {
			return report, err
		}
	}
	for i, a := range paths {
		for _, b := range paths[i+1:] {
			if gate.Overlap(*a, *b) {
				return report, errors.New("reference input and output paths overlap")
			}
		}
	}
	if err = ctx.Err(); err != nil {
		return report, err
	}
	if err = os.Mkdir(req.EvidenceDir, 0755); err != nil {
		return report, err
	}
	report = Report{SchemaVersion: 1, Version: GHCLIVersion, TargetID: req.TargetID, RunID: req.RunID, CandidateSHA256: req.CandidateSHA256, Backend: req.Backend.Info(), StartedAt: time.Now().UTC(), Automatic: Inconclusive, Checks: map[string]Observation{}, Artifacts: map[string]string{}}
	for _, n := range automaticSections {
		report.Checks[n] = Observation{Status: Inconclusive, Reason: "not executed"}
	}
	defer func() {
		report.FinishedAt = time.Now().UTC()
		if err != nil {
			report.Error = err.Error()
		}
		report.Automatic = Inconclusive
		if report.InputsUnchanged {
			report.Automatic = aggregate(report.Checks)
		}
		p := filepath.Join(req.EvidenceDir, "reference.json")
		if e := gate.WriteJSON(p, report); e != nil {
			err = errors.Join(err, e)
			return
		}
		b, e := os.ReadFile(p)
		if e == nil {
			e = gate.WriteJSON(filepath.Join(req.EvidenceDir, "review-template.json"), reviewTemplate(report, gate.Digest(b)))
		}
		err = errors.Join(err, e)
	}()
	host, err := l2.GHCLIMaterials()
	if err != nil {
		return report, err
	}
	data, err := ghcliData()
	if err != nil {
		return report, err
	}
	tools, err := toolData()
	if err != nil {
		return report, err
	}
	report.ReferenceVersion = GHCLIDataVersion
	report.ReferenceSHA256 = gate.Digest(data["freeze.json"])
	report.ToolSHA256 = gate.TreeHash(tools)
	report.HostMaterialsSHA256, err = l2.HostMaterialsSHA256Version(host, baseline.GHCLIVersion)
	if err != nil {
		return report, err
	}
	var release struct {
		Files map[string]string `json:"files"`
	}
	if err = json.Unmarshal(host["freeze.json"], &release); err != nil {
		return report, err
	}
	fixtures, err := gate.ReadFiles(req.FixturesDir)
	if err != nil {
		return report, err
	}
	expected := map[string]string{}
	for n, h := range release.Files {
		if rel, ok := strings.CutPrefix(n, "fixtures/"); ok {
			expected[rel] = h
		}
	}
	if !maps.Equal(gate.Hashes(fixtures), expected) {
		return report, errors.New("fixture snapshot does not match reference release")
	}
	if err = filepath.WalkDir(req.CandidateDir, func(_ string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if entry.Name() == ".env" || strings.HasPrefix(entry.Name(), ".env.") {
			return errors.New("environment files are not candidate inputs")
		}
		return nil
	}); err != nil {
		return report, err
	}
	candidate, err := gate.ReadFiles(req.CandidateDir)
	if err != nil {
		return report, err
	}
	if gate.TreeHash(candidate) != req.CandidateSHA256 {
		return report, errors.New("candidate digest does not match request")
	}
	spec, err := os.ReadFile(req.SpecPath)
	if err != nil {
		return report, err
	}
	if gate.Digest(spec) != ghcliSpecSHA256 {
		return report, errors.New("unsupported reference specification")
	}
	report.SpecSHA256 = gate.Digest(spec)
	base := gate.Module(fixtures, "ghcli")
	report.BaselineSHA256 = gate.TreeHash(base)
	prod := production(candidate)
	report.ProductionSHA256 = gate.TreeHash(prod)
	report.SDKs = map[string]string{"rpcx-v1": gate.TreeHash(gate.Module(fixtures, "rpcx-v1")), "rpcx-v2": gate.TreeHash(gate.Module(fixtures, "rpcx-v2"))}
	snapshots := map[string]map[string][]byte{req.FixturesDir: fixtures, req.CandidateDir: candidate}
	defer func() {
		var changed error
		for root, before := range snapshots {
			after, e := gate.ReadFiles(root)
			if e != nil || gate.TreeHash(after) != gate.TreeHash(before) {
				changed = errors.Join(changed, errors.New("reference input or execution copy changed"), e)
			}
		}
		after, e := os.ReadFile(req.SpecPath)
		if e != nil || !bytes.Equal(spec, after) {
			changed = errors.Join(changed, errors.New("reference specification changed"), e)
		}
		for name, hash := range report.Artifacts {
			b, e := os.ReadFile(filepath.Join(req.EvidenceDir, name))
			if e != nil || gate.Digest(b) != hash {
				changed = errors.Join(changed, errors.New("reference evidence changed"), e)
			}
		}
		report.InputsUnchanged = changed == nil
		err = errors.Join(err, changed)
	}()
	for _, entry := range []struct {
		name  string
		files map[string][]byte
	}{{"reference", data}, {"tools", tools}} {
		root := filepath.Join(req.EvidenceDir, "materials", entry.name)
		if err = gate.CopyFiles(root, entry.files); err != nil {
			return report, err
		}
		snapshots[root] = entry.files
	}
	// 规范沿用通用报告消费者的固定归档位置，并单独绑定实际字节。
	if err = gate.Write(filepath.Join(req.EvidenceDir, "materials/spec.md"), spec); err != nil {
		return report, err
	}
	report.Artifacts["materials/spec.md"] = gate.Digest(spec)
	layouts := map[string]map[string][]byte{"baseline": base, "candidate": candidate, "production": prod, "original": production(candidate)}
	// 原测试单独置入生产副本，候选新增测试不能替代它们。
	for n, b := range base {
		if strings.HasSuffix(n, "_test.go") {
			layouts["original"][n] = b
		}
	}
	layouts["production"][ghcliDir+"/reference_test.go"] = data["probes_test.go.txt"]
	layouts["production"][ghcliDir+"/reference_command_expiry_test.go"] = data["command_expiry_test.go.txt"]
	for name, files := range layouts {
		root := filepath.Join(req.EvidenceDir, name)
		all := map[string][]byte{}
		for n, b := range files {
			all["ghcli/"+n] = b
		}
		for _, sdk := range []string{"rpcx-v1", "rpcx-v2"} {
			for n, b := range gate.Module(fixtures, sdk) {
				all[sdk+"/"+n] = b
			}
		}
		if err = gate.CopyFiles(root, all); err != nil {
			return report, err
		}
		snapshots[root] = all
	}
	backend, err := l2.PrepareBackendVersion(ctx, filepath.Join(req.EvidenceDir, "modules"), req.Backend, host, baseline.GHCLIVersion)
	if err != nil {
		return report, err
	}
	// 直接登记装配器实际归档的清单、索引与每片；不另造与执行脱节的副本。
	for name, want := range host {
		leaf, ok := strings.CutPrefix(name, "dependencies/")
		if !ok {
			continue
		}
		relative := "modules.bundle/" + leaf
		if leaf == "modules.json" {
			relative = "modules.json"
		}
		actual, e := os.ReadFile(filepath.Join(req.EvidenceDir, filepath.FromSlash(relative)))
		if e != nil {
			return report, e
		}
		if gate.Digest(actual) != gate.Digest(want) {
			return report, errors.New("prepared host dependency archive identity mismatch")
		}
		report.Artifacts[relative] = gate.Digest(actual)
	}
	// 每条命令继续使用现有执行器的禁网、非 root、只读挂载和精确资源回收。
	run := func(id, layout string, args ...string) (execution.Result, error) {
		commandCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		start := time.Now()
		root := filepath.Join(req.EvidenceDir, layout)
		dir := filepath.Join(root, "ghcli")
		result, e := backend.Run(commandCtx, execution.Request{Directory: dir, Args: args, ReadOnlyDirs: []string{dir, filepath.Join(root, "rpcx-v1"), filepath.Join(root, "rpcx-v2")}, CacheDir: filepath.Join(req.EvidenceDir, "cache")})
		if result.OOMKilled || result.OutputLimit {
			e = errors.Join(e, errors.New("reference command exceeded resource limits"))
		}
		record := Command{ID: id, Args: append([]string{"env", "GOMODCACHE=" + backend.Environment()["GOMODCACHE"]}, args...), Directory: dir, ExitCode: result.ExitCode, ContainerID: result.ContainerID, DurationMS: time.Since(start).Milliseconds(), Stdout: "logs/" + id + ".stdout.log", Stderr: "logs/" + id + ".stderr.log"}
		for name, b := range map[string][]byte{record.Stdout: result.Stdout, record.Stderr: result.Stderr} {
			e = errors.Join(e, gate.Write(filepath.Join(req.EvidenceDir, name), b))
			report.Artifacts[name] = gate.Digest(b)
		}
		if e != nil {
			record.Error = e.Error()
		}
		report.Commands = append(report.Commands, record)
		return result, e
	}
	version, err := run("go-version", "baseline", "go", "version")
	if err != nil {
		return report, err
	}
	if version.ExitCode != 0 {
		return report, errors.New("Go environment could not be identified")
	}
	before, err := run("baseline-mod", "baseline", "go", "mod", "edit", "-json")
	if err != nil {
		return report, err
	}
	if before.ExitCode != 0 {
		return report, errors.New("authenticated baseline module unavailable")
	}
	after, err := run("candidate-mod", "candidate", "go", "mod", "edit", "-json")
	if err != nil {
		return report, err
	}
	report.Checks["dependencies"] = combineObservations(ghcliDependencyObservation(before.Stdout, after.Stdout, after.ExitCode), ghcliContract(candidate))
	report.Checks["protected_paths"] = ghcliProtected(base, candidate)
	// 依赖声明未认证时禁止构建，以免候选 replace 穿越固定布局。
	if report.Checks["dependencies"].Status != Pass {
		return report, nil
	}
	var inventory struct {
		Modules map[string]testInventory `json:"modules"`
	}
	if err = json.Unmarshal(host["tests.json"], &inventory); err != nil {
		return report, err
	}
	tests := inventory.Modules["ghcli"]
	for _, layout := range []string{"baseline", "candidate"} {
		result, e := run(layout+"-build", layout, "go", "build", "-o", "/dev/null", "./...")
		if e != nil {
			return report, e
		}
		if result.ExitCode != 0 {
			if layout == "baseline" {
				return report, errors.New("authenticated baseline failed reference build")
			}
			report.Checks["build"] = Observation{Status: Fail, Findings: []Finding{{Rule: "BUILD-001", Detail: "candidate build failed"}}}
			return report, nil
		}
	}
	report.Checks["build"] = Observation{Status: Pass}
	for _, layout := range []string{"baseline", "candidate", "original"} {
		result, e := run(layout+"-tests", layout, "go", "test", "-json", "-count=1", "-timeout=45s", "./...")
		if e != nil {
			return report, e
		}
		observation := observe(result.Stdout, result.ExitCode, tests.Original, tests.Packages, tests.Empty, false)
		if layout == "baseline" {
			if observation.Status != Pass {
				return report, errors.New("authenticated baseline tests could not be confirmed")
			}
		} else if layout == "candidate" {
			report.Checks["candidate_tests"] = observation
		} else {
			report.Checks["candidate_tests"] = combineObservations(report.Checks["candidate_tests"], observation)
		}
	}
	result, err := run("behavior", "production", "go", "test", "-json", "-count=1", "-timeout=40s", "-run=^TestReference(MappingAndRelease|ErrorAndRelease|ParentContexts|FreshPerCallCeilings|LongParentCeiling|ResponseBodyParentCancellation|CommandOrderAndStop)$", "./"+ghcliDir)
	if err != nil {
		return report, err
	}
	report.Checks["behavior"] = ghcliObserve(result.Stdout, result.ExitCode, ghcliBehaviorTests())
	if report.Checks["behavior"].Status != Pass {
		return report, nil
	}
	var expiry []Observation
	for _, name := range []string{"TestReferenceActualDefaultExpiry", "TestReferenceCommandDefaultExpiry"} {
		result, e := run("expiry-"+name, "production", "go", "test", "-json", "-count=1", "-timeout=40s", "-run=^"+name+"$", "./"+ghcliDir)
		if e != nil {
			return report, e
		}
		expiry = append(expiry, ghcliObserve(result.Stdout, result.ExitCode, []string{ghcliPackage + "/" + name}))
	}
	report.Checks["expiry"] = combineObservations(expiry...)
	return report, nil
}

func ghcliDependencyObservation(before, after []byte, exit int) Observation {
	fail := func() Observation {
		return Observation{Status: Fail, Findings: []Finding{{Rule: "DEP-001", Detail: "module declarations differ from prescribed v2 migration"}}}
	}
	if exit != 0 {
		return fail()
	}
	var b, a map[string]any
	if json.Unmarshal(before, &b) != nil || json.Unmarshal(after, &a) != nil {
		return Observation{Status: Inconclusive, Reason: "invalid go mod edit JSON"}
	}
	requirements, ok := b["Require"].([]any)
	if !ok {
		return Observation{Status: Inconclusive, Reason: "baseline requirements missing"}
	}
	found := false
	for _, v := range requirements {
		m, ok := v.(map[string]any)
		if !ok {
			return fail()
		}
		if m["Path"] == "example.com/rpcx" {
			m["Path"] = "example.com/rpcx/v2"
			m["Version"] = "v2.0.0"
			found = true
		}
	}
	replacements, ok := b["Replace"].([]any)
	if !ok || !found {
		return Observation{Status: Inconclusive, Reason: "baseline module contract missing"}
	}
	found = false
	for _, v := range replacements {
		m, ok := v.(map[string]any)
		if !ok {
			return fail()
		}
		old, ok := m["Old"].(map[string]any)
		if !ok {
			return fail()
		}
		if old["Path"] == "example.com/rpcx" {
			old["Path"] = "example.com/rpcx/v2"
			m["New"] = map[string]any{"Path": "../rpcx-v2"}
			found = true
		}
	}
	if !found {
		return Observation{Status: Inconclusive, Reason: "baseline replacement missing"}
	}
	for _, doc := range []map[string]any{b, a} {
		for _, key := range []string{"Require", "Replace"} {
			if entries, ok := doc[key].([]any); ok {
				slices.SortFunc(entries, func(x, y any) int { a, _ := json.Marshal(x); b, _ := json.Marshal(y); return bytes.Compare(a, b) })
			}
		}
	}
	x, _ := json.Marshal(b)
	y, _ := json.Marshal(a)
	if !bytes.Equal(x, y) {
		return fail()
	}
	return Observation{Status: Pass}
}

func ghcliProtected(base, candidate map[string][]byte) Observation {
	out := Observation{Status: Pass}
	allowed := map[string]bool{"go.mod": true, ghcliDir + "/delete.go": true, ghcliDir + "/outbound.go": true}
	union := map[string]bool{}
	for n := range base {
		union[n] = true
	}
	for n := range candidate {
		union[n] = true
	}
	for _, n := range slices.Sorted(maps.Keys(union)) {
		if allowed[n] {
			continue
		}
		if _, ok := base[n]; !ok && strings.HasSuffix(n, "_test.go") {
			continue
		}
		_, baseExists := base[n]
		_, candidateExists := candidate[n]
		if baseExists != candidateExists || !bytes.Equal(base[n], candidate[n]) {
			out.Status = Fail
			out.Findings = append(out.Findings, Finding{Rule: "PATH-001", Detail: "protected path changed: " + n})
		}
	}
	return out
}

func ghcliBehaviorTests() []string {
	names := []string{"MappingAndRelease", "ErrorAndRelease", "ParentContexts", "ParentContexts/pre", "ParentContexts/during", "ParentContexts/short", "FreshPerCallCeilings", "LongParentCeiling", "ResponseBodyParentCancellation", "ResponseBodyParentCancellation/cancel", "ResponseBodyParentCancellation/short", "CommandOrderAndStop", "CommandOrderAndStop/complete", "CommandOrderAndStop/stop"}
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = ghcliPackage + "/TestReference" + n
	}
	return out
}

func ghcliObserve(data []byte, exit int, required []string) Observation {
	// 看门狗终止不能伪装成已归因的业务失败。
	if bytes.Contains(data, []byte("panic: test timed out")) {
		return Observation{Status: Inconclusive, Reason: "reference watchdog expired"}
	}
	out := observe(data, exit, required, []string{ghcliPackage}, nil, false)
	if out.Status == Fail {
		out.Findings = []Finding{{Rule: "BEHAVIOR-001", Detail: fmt.Sprintf("independent production assertion failed (%d required events)", len(required))}}
	}
	return out
}

var ghcliID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// 只对新版本加强发布绑定，旧历史报告保持原格式与语义。
func validateGHCLIReport(report Report) error {
	if report.Version != GHCLIVersion || !report.InputsUnchanged {
		return nil
	}
	identity, err := PublishedIdentity(GHCLIDataVersion)
	if err != nil {
		return err
	}
	if report.ReferenceVersion != GHCLIDataVersion || report.ReferenceSHA256 != identity.ReferenceSHA256 || report.SpecSHA256 != identity.SpecSHA256 || report.ToolSHA256 != identity.ToolSHA256 {
		return errors.New("reference report does not match published identity")
	}
	host, err := l2.GHCLIMaterials()
	if err != nil {
		return err
	}
	hostHash, err := l2.HostMaterialsSHA256Version(host, baseline.GHCLIVersion)
	if err != nil {
		return err
	}
	var published struct {
		Modules map[string]struct {
			TreeSHA256 string `json:"tree_sha256"`
		} `json:"modules"`
	}
	if err = json.Unmarshal(host["identity.json"], &published); err != nil {
		return err
	}
	if report.HostMaterialsSHA256 != hostHash || report.BaselineSHA256 != published.Modules["ghcli"].TreeSHA256 || len(report.SDKs) != 2 {
		return errors.New("reference report does not match published inputs")
	}
	for _, name := range []string{"rpcx-v1", "rpcx-v2"} {
		if report.SDKs[name] != published.Modules[name].TreeSHA256 {
			return errors.New("reference report SDK identity mismatch")
		}
	}
	digest := regexp.MustCompile(`^[a-f0-9]{64}$`)
	if !ghcliID.MatchString(report.TargetID) || !ghcliID.MatchString(report.RunID) || !digest.MatchString(report.CandidateSHA256) || !digest.MatchString(report.ProductionSHA256) {
		return errors.New("invalid reference report binding")
	}
	return nil
}

// 独立语法检查只限定公开 SDK 契约；上下文语义仍由实际生产探针观察。
func ghcliContract(files map[string][]byte) Observation {
	imported, dialed, called := false, false, false
	for name, data := range production(files) {
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, data, 0)
		if err != nil {
			return Observation{Status: Fail, Findings: []Finding{{Rule: "DEP-001", Detail: "invalid production Go source"}}}
		}
		aliases := map[string]bool{}
		for _, spec := range file.Imports {
			path, e := strconv.Unquote(spec.Path.Value)
			if e != nil {
				continue
			}
			if path == "example.com/rpcx" {
				return Observation{Status: Fail, Findings: []Finding{{Rule: "DEP-001", Detail: "v1 production import retained"}}}
			}
			if path == "example.com/rpcx/v2" {
				imported = true
				alias := "rpcx"
				if spec.Name != nil {
					alias = spec.Name.Name
				}
				aliases[alias] = true
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if receiver, ok := selector.X.(*ast.Ident); ok && aliases[receiver.Name] && selector.Sel.Name == "Dial" {
				dialed = true
			}
			if selector.Sel.Name == "Call" && len(call.Args) == 4 {
				called = true
			}
			return true
		})
	}
	if !imported || !dialed || !called {
		return Observation{Status: Fail, Findings: []Finding{{Rule: "DEP-001", Detail: "v2 Dial and context-bearing Call contract missing"}}}
	}
	return Observation{Status: Pass}
}
