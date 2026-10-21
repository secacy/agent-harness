package execution

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// 替身实际经过 CLI 进程边界，故障状态保存在本测试的独占目录。
const resourceDockerDouble = `#!/usr/bin/env python3
import sys,json,os,pathlib
p=pathlib.Path(os.environ['HARNESS_RESOURCE_DOUBLE'])
s=json.loads(p.read_text());a=sys.argv[1:]
if a[:1]==['--context']:a=a[2:]
s.setdefault('commands',[]).append(a)
def save():p.write_text(json.dumps(s))
def fail(text):save();print(text,file=sys.stderr);sys.exit(1)
cmd=a[0]
if s.get('down'):fail('daemon unavailable')
if cmd=='context':print('test-context')
elif cmd=='info':print(s.get('engine','test-engine'))
elif cmd=='create':
 name=a[a.index('--name')+1];labels={};mounts=[]
 for n,x in enumerate(a):
  if x=='--label':k,v=a[n+1].split('=',1);labels[k]=v
  if x=='--mount':
   b=dict(x.split('=',1) for x in a[n+1].split(',') if '='in x)
   mounts.append({'Type':'bind','Source':b['source'],'Destination':b['target'],'RW':False})
 image=next(x for x in a if x.startswith('sha256:'));cid=labels['agent-harness.command']*2
 c={'Id':cid,'Name':'/'+name,'Image':image,'Config':{'Labels':labels},'Mounts':mounts}
 if s.get('mode')=='late':s['late']=c;fail('response unknown before creation')
 s.setdefault('containers',{})[name]=c
 if s.get('mode')=='lost':fail('response lost after creation')
 print(cid)
elif cmd=='start':print('ok',end='')
elif cmd=='inspect':
 if '--format'in a:print(json.dumps({'Status':'exited','ExitCode':0,'Running':False,'OOMKilled':False,'Error':''}))
 else:
  key=a[-1];c=next((v for k,v in s.get('containers',{}).items() if k==key or v['Id']==key),None)
  if c is None:fail('No such container: '+key)
  print(json.dumps([c]))
elif cmd=='rm':
 if s.get('remove_fail'):fail('injected removal failure')
 key=a[-1];found=[k for k,v in s.get('containers',{}).items() if v['Id']==key]
 if not found:fail('refused removal without an exact full ID')
 del s['containers'][found[0]]
elif cmd=='ps':
 label=a[a.index('--filter')+1][6:];k,v=label.split('=',1)
 print('\n'.join(c['Id'] for c in s.get('containers',{}).values() if c['Config']['Labels'].get(k)==v))
else:fail('unsupported fake command '+cmd)
save()
`

func resourceFixture(t *testing.T) (*Docker, ResourceOwner, Request, string) {
	t.Helper()
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	policy := ResourcePolicy{ResourceProtocol, "test-context", "test-engine"}
	registry := ResourceRegistry{1, "campaign", strings.Repeat("a", 64), policy}
	if err := CreateResourceRegistry(root, registry); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0755); err != nil {
		t.Fatal(err)
	}
	o := ResourceOwner{Root: root, Registry: registry, TargetID: "orders", RunID: "run-one", RunDirectory: "targets/orders/runs/run-one"}
	state := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(state, []byte(`{"containers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HARNESS_RESOURCE_DOUBLE", state)
	binary := filepath.Join(t.TempDir(), "docker-double")
	if err := os.WriteFile(binary, []byte(resourceDockerDouble), 0755); err != nil {
		t.Fatal(err)
	}
	d := &Docker{binary: binary, info: Info{Kind: KindDocker, Image: "sha256:" + strings.Repeat("b", 64), CPUs: 1, MemoryBytes: 64 << 20, Pids: 32}}
	return d, o, Request{Directory: source, ReadOnlyDirs: []string{source}, Args: []string{"go", "version"}, Owner: &o}, state
}
func doubleState(t *testing.T, p string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var s map[string]any
	if err = json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}
func saveDouble(t *testing.T, p string, s map[string]any) {
	t.Helper()
	b, _ := json.Marshal(s)
	if err := os.WriteFile(p, b, 0600); err != nil {
		t.Fatal(err)
	}
}
func onlyResource(t *testing.T, o ResourceOwner) ResourceEntry {
	t.Helper()
	all, err := ReadResources(o.Root, o.Registry)
	if err != nil || len(all) != 1 {
		t.Fatal(all, err)
	}
	return all[0]
}

func TestOwnedDockerNormalCompletionAndRepeatedCleanup(t *testing.T) {
	d, o, req, state := resourceFixture(t)
	r, err := d.Run(context.Background(), req)
	if err != nil || r.ExitCode != 0 || string(r.Stdout) != "ok" {
		t.Fatal(r, err)
	}
	e := onlyResource(t, o)
	if e.Dispatch == nil || e.Created == nil || e.Removed == nil || e.Created.ContainerID != r.ContainerID {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(filepath.Join(o.Root, "resources", e.Intent.CommandID, "removed.json"))
	for range 2 {
		report, err := d.ReconcileResources(context.Background(), o.Root, o.Registry, true)
		if err != nil || !report.Stopped {
			t.Fatal(report, err)
		}
	}
	after, _ := os.ReadFile(filepath.Join(o.Root, "resources", e.Intent.CommandID, "removed.json"))
	if string(before) != string(after) {
		t.Fatal("repeated cleanup changed its witness")
	}
	for _, c := range doubleState(t, state)["commands"].([]any) {
		a := c.([]any)
		if a[0] == "rm" && a[len(a)-1] != r.ContainerID {
			t.Fatal("cleanup used a name instead of exact ID", a)
		}
	}
}

func TestOwnedDockerLostResponseIsReconciled(t *testing.T) {
	d, o, req, state := resourceFixture(t)
	s := doubleState(t, state)
	s["mode"] = "lost"
	saveDouble(t, state, s)
	if _, err := d.Run(context.Background(), req); err == nil {
		t.Fatal("lost create response accepted as command success")
	}
	e := onlyResource(t, o)
	if e.Created == nil || e.Removed == nil {
		t.Fatal("verified container did not produce creation/removal witnesses", e)
	}
	if r, err := d.ReconcileResources(context.Background(), o.Root, o.Registry, false); err != nil || !r.Stopped {
		t.Fatal(r, err)
	}
}

func TestOwnedDockerDelayedCreateCannotBeClearedByAbsence(t *testing.T) {
	d, o, req, state := resourceFixture(t)
	s := doubleState(t, state)
	s["mode"] = "late"
	saveDouble(t, state, s)
	if _, err := d.Run(context.Background(), req); err == nil {
		t.Fatal("unknown create accepted")
	}
	for range 2 {
		r, err := d.ReconcileResources(context.Background(), o.Root, o.Registry, true)
		if !errors.Is(err, ErrResourcesUnknown) || r.Stopped {
			t.Fatal("absence released an uncertain create", r, err)
		}
	}
	e := onlyResource(t, o)
	if e.Created != nil || e.Removed != nil {
		t.Fatal("missing container forged completion evidence")
	}
	s = doubleState(t, state)
	c := s["late"].(map[string]any)
	s["containers"].(map[string]any)[strings.TrimPrefix(c["Name"].(string), "/")] = c
	delete(s, "late")
	saveDouble(t, state, s)
	r, err := d.ReconcileResources(context.Background(), o.Root, o.Registry, true)
	if err != nil || !r.Stopped || onlyResource(t, o).Removed == nil {
		t.Fatal("late creation could not be precisely reclaimed", r, err)
	}
}

func TestOwnedDockerRefusesForeignResourcesAndUnavailableEngine(t *testing.T) {
	for _, mode := range []string{"labels", "image", "mount", "renamed", "id", "engine", "down", "remove", "orphan"} {
		t.Run(mode, func(t *testing.T) {
			d, o, req, state := resourceFixture(t)
			s := doubleState(t, state)
			s["mode"] = "late"
			saveDouble(t, state, s)
			_, _ = d.Run(context.Background(), req)
			s = doubleState(t, state)
			c := s["late"].(map[string]any)
			name := strings.TrimPrefix(c["Name"].(string), "/")
			s["containers"].(map[string]any)[name] = c
			delete(s, "late")
			e := onlyResource(t, o)
			switch mode {
			case "labels":
				c["Config"].(map[string]any)["Labels"].(map[string]any)["agent-harness.owner"] = "another-run"
			case "image":
				c["Image"] = "sha256:" + strings.Repeat("c", 64)
			case "mount":
				c["Mounts"].([]any)[0].(map[string]any)["RW"] = true
			case "renamed":
				if err := publishResource(filepath.Join(o.Root, "resources", e.Intent.CommandID, "created.json"), ResourceCreated{1, ResourceDigest(e.Intent), c["Id"].(string)}); err != nil {
					t.Fatal(err)
				}
				delete(s["containers"].(map[string]any), name)
				c["Name"] = "/renamed"
				s["containers"].(map[string]any)["renamed"] = c
			case "id":
				if err := publishResource(filepath.Join(o.Root, "resources", e.Intent.CommandID, "created.json"), ResourceCreated{1, ResourceDigest(e.Intent), strings.Repeat("f", 64)}); err != nil {
					t.Fatal(err)
				}
			case "engine":
				s["engine"] = "replaced-engine"
			case "down":
				s["down"] = true
			case "remove":
				s["remove_fail"] = true
			case "orphan":
				foreign := map[string]any{}
				b, _ := json.Marshal(c)
				_ = json.Unmarshal(b, &foreign)
				foreign["Id"] = strings.Repeat("e", 64)
				foreign["Name"] = "/orphan"
				s["containers"].(map[string]any)["orphan"] = foreign
			}
			saveDouble(t, state, s)
			before := s["containers"]
			r, err := d.ReconcileResources(context.Background(), o.Root, o.Registry, true)
			if !errors.Is(err, ErrResourcesUnknown) || r.Stopped {
				t.Fatal("unsafe cleanup accepted", r, err)
			}
			after := doubleState(t, state)["containers"]
			if mode != "orphan" && !reflect.DeepEqual(before, after) {
				t.Fatal("foreign resource was changed")
			}
			if mode == "orphan" {
				if _, ok := after.(map[string]any)["orphan"]; !ok {
					t.Fatal("unrecorded resource was deleted")
				}
			}
		})
	}
}

func TestResourceOwnerCannotBeReplacedByCommandArguments(t *testing.T) {
	d, o, req, _ := resourceFixture(t)
	wrapped, err := WithResourceOwner(d, o)
	if err != nil {
		t.Fatal(err)
	}
	other := o
	other.TargetID = "another"
	req.Owner = &other
	if _, err = wrapped.Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if e := onlyResource(t, o); e.Intent.Owner.TargetID != "orders" {
		t.Fatal("request overrode its bound owner")
	}
}

func TestOwnedDockerCannotCreateBeforeIntentIsDurable(t *testing.T) {
	d, o, req, state := resourceFixture(t)
	dir := filepath.Join(o.Root, "resources")
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0700)
	if _, err := d.Run(context.Background(), req); err == nil {
		t.Fatal("unwritable intent directory accepted")
	}
	for _, raw := range doubleState(t, state)["commands"].([]any) {
		if raw.([]any)[0] == "create" {
			t.Fatal("create was dispatched before durable intent")
		}
	}
}

func TestResourceGuardStopsNewCommandsAfterUnknownCleanup(t *testing.T) {
	d, o, req, state := resourceFixture(t)
	s := doubleState(t, state)
	s["mode"] = "late"
	saveDouble(t, state, s)
	guard := NewResourceGuard(d)
	if _, err := guard.Run(context.Background(), req); !errors.Is(err, ErrResourcesUnknown) {
		t.Fatal("unknown cleanup was not classified", err)
	}
	before := doubleState(t, state)["commands"]
	other := o
	other.RunID = "another-run"
	other.RunDirectory = "targets/orders/runs/another-run"
	req.Owner = &other
	if _, err := guard.Run(context.Background(), req); !errors.Is(err, ErrResourcesUnknown) {
		t.Fatal("poisoned generation admitted another command", err)
	}
	if !reflect.DeepEqual(before, doubleState(t, state)["commands"]) {
		t.Fatal("another Docker command was dispatched after unknown cleanup")
	}
	if _, err := NewResourceGuard(d).Run(context.Background(), Request{}); err == nil {
		t.Fatal("tracked generation accepted a missing owner")
	}
}

func TestNullResourceWitnessCannotPretendToBeMissing(t *testing.T) {
	for _, name := range []string{"dispatch.json", "created.json", "removed.json"} {
		t.Run(name, func(t *testing.T) {
			d, o, req, _ := resourceFixture(t)
			if _, err := d.Run(context.Background(), req); err != nil {
				t.Fatal(err)
			}
			e := onlyResource(t, o)
			if err := os.WriteFile(filepath.Join(o.Root, "resources", e.Intent.CommandID, name), []byte("null\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadResources(o.Root, o.Registry); err == nil {
				t.Fatal("null witness was treated as a missing publication")
			}
			if r, err := d.ReconcileResources(context.Background(), o.Root, o.Registry, true); !errors.Is(err, ErrResourcesUnknown) || r.Stopped {
				t.Fatal("null witness released resources", r, err)
			}
		})
	}
}
