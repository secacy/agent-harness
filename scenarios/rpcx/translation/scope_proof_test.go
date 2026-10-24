package translation

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/secacy/agent-harness/internal/execution"
	"github.com/secacy/agent-harness/scenarios/rpcx/analysis"
	"github.com/secacy/agent-harness/scenarios/rpcx/gate"
)

func scopeReports(t *testing.T, base, candidate, fixtures map[string][]byte, backend execution.Executor, cache string) map[string]ScopeAnalysis {
	t.Helper()
	root := t.TempDir()
	out := map[string]ScopeAnalysis{}
	for _, side := range []struct {
		name  string
		files map[string][]byte
	}{{"baseline", base}, {"candidate", candidate}} {
		for _, entry := range []struct {
			name  string
			files map[string][]byte
		}{{"translation", side.files}, {"rpcx-v1", gate.Module(fixtures, "rpcx-v1")}, {"rpcx-v2", gate.Module(fixtures, "rpcx-v2")}} {
			if err := gate.CopyFiles(filepath.Join(root, side.name, entry.name), entry.files); err != nil {
				t.Fatal(err)
			}
		}
		for _, c := range Configurations() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			result, err := analysis.Inspect(ctx, analysis.Request{ModuleDir: filepath.Join(root, side.name, "translation"), SDKDirs: []string{filepath.Join(root, side.name, "rpcx-v1"), filepath.Join(root, side.name, "rpcx-v2")}, EvidenceDir: filepath.Join(root, "evidence", side.name, c.ID), CacheDir: cache, Tags: c.Tags, Backend: backend})
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			p := out[c.ID]
			if side.name == "baseline" {
				p.Baseline = result.Report
			} else {
				p.Candidate = result.Report
			}
			out[c.ID] = p
		}
	}
	return out
}
func TestScopeProofSupportedAndUnexplainedChanges(t *testing.T) {
	fixtures, err := LoadFixtures("../l2/data/rpcx-l2-translation-v1/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	base := gate.Module(fixtures, "translation")
	correct := controlCandidate(t, "correct")
	backend, err := PrepareBackend(context.Background(), filepath.Join(t.TempDir(), "modules"), execution.Local{})
	if err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	for _, name := range []string{"literal", "comments", "names", "constant", "combined", "mapping-helper", "business-change", "extra-constant-use", "shared-constant", "directive", "wrong-duration"} {
		t.Run(name, func(t *testing.T) {
			candidate := map[string][]byte{}
			for path, data := range correct {
				candidate[path] = append([]byte{}, data...)
			}
			if name == "mapping-helper" {
				candidate = controlCandidate(t, "mapping-helper")
			}
			for _, path := range adapters {
				s := string(candidate[path])
				if name == "comments" || name == "combined" {
					s = strings.Replace(s, "本地 rpcx-v1", "本地 rpcx-v2", 1)
				}
				if name == "names" || name == "combined" {
					s = strings.Replace(s, "ctx, cancel :=", "callCtx, release :=", 1)
					s = strings.Replace(s, "defer cancel()", "defer release()", 1)
					s = strings.Replace(s, ".Call(ctx,", ".Call(callCtx,", 1)
				}
				if name == "constant" || name == "combined" || name == "extra-constant-use" || name == "shared-constant" {
					s = strings.Replace(s, "30*time.Second)", "rpcTimeout)", 1)
					if name != "shared-constant" {
						s = strings.Replace(s, "type TranslationWebAPI", "const rpcTimeout time.Duration = 30*time.Second\n\ntype TranslationWebAPI", 1)
					}
					if name == "extra-constant-use" {
						s += "\nvar extra = rpcTimeout\n"
					}
				}
				if name == "business-change" {
					s = strings.Replace(s, "translation.Translation =", "translation.Original =", 1)
				}
				if name == "directive" {
					s = strings.Replace(s, "func (t *TranslationWebAPI) Translate", "//go:noinline\nfunc (t *TranslationWebAPI) Translate", 1)
				}
				if name == "wrong-duration" {
					s = strings.Replace(s, "30*time.Second", "5*time.Second", 1)
				}
				if name == "shared-constant" {
					s = strings.Replace(s, "\n\t\"time\"", "", 1)
				}
				candidate[path] = []byte(s)
			}
			if name == "shared-constant" {
				candidate["internal/repo/webapi/shared_timeout.go"] = []byte("package webapi\nimport \"time\"\nconst rpcTimeout = 30*time.Second\n")
			}
			reports := scopeReports(t, base, candidate, fixtures, backend, cache)
			proof, err := ProveScope(base, candidate, fixtures, reports)
			if err != nil {
				t.Fatal(err)
			}
			want := name == "literal" || name == "comments" || name == "names" || name == "constant" || name == "combined"
			if proof.Matched != want {
				t.Fatalf("%s matched=%t gaps=%v", name, proof.Matched, proof.Gaps)
			}
			if name == "combined" {
				raw, _ := json.Marshal(reports)
				for _, mode := range []string{"missing-config", "missing-proof", "range", "source-hash", "tool-hash", "tags", "input"} {
					t.Run(mode, func(t *testing.T) {
						var changed map[string]ScopeAnalysis
						json.Unmarshal(raw, &changed)
						pair := changed["default"]
						switch mode {
						case "missing-config":
							delete(changed, "rpcx_alternate")
						case "missing-proof":
							pair.Candidate.Calls[0].Boundary = nil
						case "range":
							pair.Candidate.Calls[0].Boundary.Nodes[0].End++
						case "source-hash":
							for _, p := range pair.Candidate.Packages {
								for k := range p.Files {
									p.Files[k] = "forged"
								}
							}
						case "tool-hash":
							pair.Candidate.ToolSHA256 = "forged"
						case "tags":
							pair.Candidate.Config.Tags = []string{"rpcx_alternate"}
						case "input":
							pair.Candidate.InputSHA256 = "forged"
						}
						if mode != "missing-config" {
							changed["default"] = pair
						}
						p, err := ProveScope(base, candidate, fixtures, changed)
						if err == nil && p.Matched {
							t.Fatal("forged scope evidence accepted")
						}
					})
				}
			}
		})
	}
}

func TestScopeCommentsPreserveDirectivesAndStrings(t *testing.T) {
	base := []byte("//go:build !rpcx_alternate\n\npackage p\n// note\nvar value = \"//go:linkname literal\"\n")
	for _, tc := range []struct {
		name, text string
		want       bool
	}{
		{"ordinary", strings.Replace(string(base), "// note", "// revised explanation", 1), true},
		{"package-doc", strings.Replace(string(base), "package p", "// Package p describes the adapter.\npackage p", 1), true},
		{"moved-tag", "// explanation\n" + string(base), false},
		{"tag-spacing", strings.Replace(string(base), "!rpcx_alternate", "!rpcx_alternate  ", 1), false},
		{"tag", strings.Replace(string(base), "!rpcx_alternate", "rpcx_alternate", 1), false},
		{"directive", strings.Replace(string(base), "// note", "//go:generate echo ignored", 1), false},
		{"line", strings.Replace(string(base), "// note", "//line p.go:500", 1), false},
		{"generated", strings.Replace(string(base), "// note", "// Code generated by other. DO NOT EDIT.", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 只解析构造源码，不执行生成指令。
			f, err := parseScopeTest([]byte(tc.text))
			if err != nil {
				t.Fatal(err)
			}
			if scopeComments(base, f) != tc.want {
				t.Fatal("directive classification changed")
			}
		})
	}
}

func parseScopeTest(data []byte) (*ast.File, error) {
	return parser.ParseFile(token.NewFileSet(), "test.go", data, parser.ParseComments|parser.SkipObjectResolution)
}
