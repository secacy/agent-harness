package execution

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"
)

// ResourcePolicy 固定当前 context 和 daemon 身份；不会拉取镜像或创建容器。
func (d *Docker) ResourcePolicy(ctx context.Context) (ResourcePolicy, error) {
	b, err := d.control(ctx, "context", "show")
	if err != nil {
		return ResourcePolicy{}, err
	}
	p := ResourcePolicy{Protocol: ResourceProtocol, Context: strings.TrimSpace(string(b))}
	copy := *d
	copy.context = p.Context
	b, err = copy.control(ctx, "info", "--format", "{{.ID}}")
	if err != nil {
		return p, err
	}
	p.EngineID = strings.TrimSpace(string(b))
	return p, p.Validate()
}
func (d *Docker) checkEngine(ctx context.Context, p ResourcePolicy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if d.context != p.Context {
		return errors.New("Docker context differs from resource policy")
	}
	b, err := d.control(ctx, "info", "--format", "{{.ID}}")
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(b)) != p.EngineID {
		return errors.New("Docker engine identity changed")
	}
	return nil
}

func (d *Docker) prepareResource(ctx context.Context, req Request, args []string, id string) (*Docker, ResourceIntent, error) {
	o := *req.Owner
	var registry ResourceRegistry
	if err := resourceRead(o.Root, "resources/registry.json", &registry); err != nil {
		return nil, ResourceIntent{}, err
	}
	if registry != o.Registry {
		return nil, ResourceIntent{}, errors.New("resource owner differs from registry")
	}
	copy := *d
	copy.context = registry.Policy.Context
	if err := copy.checkEngine(ctx, registry.Policy); err != nil {
		return nil, ResourceIntent{}, err
	}
	i := ResourceIntent{SchemaVersion: 1, CommandID: id, Name: "harness-owned-" + id, Owner: o, Image: d.info.Image, ArgsSHA256: ResourceDigest(args), Mounts: []ResourceMount{}}
	i.CreatedAt = time.Now().UTC()
	for n, arg := range args {
		if arg != "--mount" {
			continue
		}
		if n+1 >= len(args) {
			return nil, i, errors.New("missing Docker mount")
		}
		parts := strings.Split(args[n+1], ",")
		m := ResourceMount{}
		for _, part := range parts {
			switch {
			case strings.HasPrefix(part, "source="):
				m.Source = strings.TrimPrefix(part, "source=")
			case strings.HasPrefix(part, "target="):
				m.Destination = strings.TrimPrefix(part, "target=")
			case part == "readonly":
				m.ReadOnly = true
			}
		}
		if m.Source == o.Root || strings.HasPrefix(o.Root, m.Source+string(filepath.Separator)) {
			return nil, i, errors.New("resource control root must not be mounted")
		}
		i.Mounts = append(i.Mounts, m)
	}
	if err := ValidateResourceIntent(i, registry); err != nil {
		return nil, i, err
	}
	dir := filepath.Join(o.Root, "resources", id)
	if err := createResourceDirectory(dir); err != nil {
		return nil, i, err
	}
	if err := publishResource(filepath.Join(dir, "intent.json"), i); err != nil {
		return nil, i, err
	}
	return &copy, i, nil
}

type ownedContainer struct {
	ID     string `json:"Id"`
	Name   string
	Image  string
	Config struct{ Labels map[string]string }
	Mounts []struct {
		Type, Source, Destination string
		RW                        bool
	}
}

func intentLabels(i ResourceIntent) map[string]string {
	return map[string]string{"agent-harness.execution": "true", "agent-harness.intent": ResourceDigest(i), "agent-harness.owner": i.Owner.String(), "agent-harness.command": i.CommandID, "agent-harness.registry": ResourceDigest(i.Owner.Registry)}
}
func (d *Docker) inspectResource(ctx context.Context, i ResourceIntent) (ownedContainer, bool, error) {
	return d.inspectResourceAt(ctx, i, i.Name)
}
func (d *Docker) inspectResourceAt(ctx context.Context, i ResourceIntent, selector string) (ownedContainer, bool, error) {
	var c ownedContainer
	b, err := d.control(ctx, "inspect", selector)
	if err != nil {
		if missingContainer(err) {
			return c, false, nil
		}
		return c, false, err
	}
	var all []ownedContainer
	if err := json.Unmarshal(b, &all); err != nil {
		return c, false, err
	}
	if len(all) != 1 {
		return c, false, errors.New("expected exactly one owned container")
	}
	c = all[0]
	if !containerID.MatchString(c.ID) || c.Name != "/"+i.Name || c.Image != i.Image {
		return c, true, errors.New("container identity or image differs from resource intent")
	}
	for k, v := range intentLabels(i) {
		if c.Config.Labels[k] != v {
			return c, true, errors.New("container ownership labels differ from resource intent")
		}
	}
	actual := []ResourceMount{}
	tmpfs := false
	for _, m := range c.Mounts {
		if m.Type == "tmpfs" && m.Destination == "/tmp" && m.Source == "" && m.RW && !tmpfs {
			tmpfs = true
			continue
		}
		if m.Type != "bind" {
			return c, true, errors.New("container has unexpected mount type")
		}
		actual = append(actual, ResourceMount{m.Source, m.Destination, !m.RW})
	}
	expected := slices.Clone(i.Mounts)
	slices.SortFunc(actual, func(a, b ResourceMount) int { return strings.Compare(a.Destination, b.Destination) })
	slices.SortFunc(expected, func(a, b ResourceMount) int { return strings.Compare(a.Destination, b.Destination) })
	if !reflect.DeepEqual(actual, expected) {
		return c, true, errors.New("container mounts differ from resource intent")
	}
	return c, true, nil
}

type ResourceObservation struct {
	CommandID   string `json:"command_id"`
	Name        string `json:"name"`
	ContainerID string `json:"container_id,omitempty"`
	State       string `json:"state"`
	Error       string `json:"error,omitempty"`
}
type ResourceReport struct {
	Registry ResourceRegistry      `json:"registry"`
	Stopped  bool                  `json:"stopped"`
	Commands []ResourceObservation `json:"commands"`
}

// ReconcileResources 只处理注册表列明的资源；cleanup=false 始终不写入或删除。
// 清理调用方必须持原 Campaign 写锁，且已核对计划、Run 与资源静态关系。
func (d *Docker) ReconcileResources(ctx context.Context, root string, registry ResourceRegistry, cleanup bool) (ResourceReport, error) {
	r := ResourceReport{Registry: registry, Commands: []ResourceObservation{}}
	entries, err := ReadResources(root, registry)
	if err != nil {
		return r, errors.Join(ErrResourcesUnknown, err)
	}
	copy := *d
	copy.context = registry.Policy.Context
	if err := copy.checkEngine(ctx, registry.Policy); err != nil {
		return r, errors.Join(ErrResourcesUnknown, err)
	}
	var failures error
	for _, e := range entries {
		o, err := copy.reconcileResource(ctx, root, e, cleanup)
		r.Commands = append(r.Commands, o)
		failures = errors.Join(failures, err)
	}
	// 注册表标签用于发现丢失记录的孤儿；未知 ID 只阻塞，绝不猜测归属后删除。
	data, listErr := copy.control(ctx, "ps", "--all", "--no-trunc", "--filter", "label=agent-harness.registry="+ResourceDigest(registry), "--format", "{{.ID}}")
	if listErr != nil {
		failures = errors.Join(failures, listErr)
	} else {
		known := map[string]bool{}
		for _, o := range r.Commands {
			if o.ContainerID != "" {
				known[o.ContainerID] = true
			}
		}
		for _, id := range strings.Fields(string(data)) {
			if !containerID.MatchString(id) || !known[id] {
				failures = errors.Join(failures, errors.New("registry inventory contains an unrecorded container"))
			} else {
				failures = errors.Join(failures, errors.New("registry inventory still contains a container"))
			}
		}
	}
	r.Stopped = failures == nil
	if failures != nil {
		return r, errors.Join(ErrResourcesUnknown, failures)
	}
	return r, nil
}

func (d *Docker) reconcileResource(ctx context.Context, root string, e ResourceEntry, cleanup bool) (ResourceObservation, error) {
	i := e.Intent
	dir := filepath.Join(root, "resources", i.CommandID)
	h := ResourceDigest(i)
	o := ResourceObservation{CommandID: i.CommandID, Name: i.Name, State: "pending"}
	fail := func(err error) (ResourceObservation, error) {
		o.State = "unknown"
		o.Error = err.Error()
		return o, err
	}
	c, found, err := d.inspectResource(ctx, i)
	if err != nil {
		return fail(err)
	}
	if !found && e.Created != nil {
		c, found, err = d.inspectResourceAt(ctx, i, e.Created.ContainerID)
		if err != nil {
			return fail(err)
		}
	}
	if !found {
		if e.Dispatch != nil && e.Created == nil {
			return fail(errors.New("creation may still complete; absence is not a completion witness"))
		}
		o.State = "absent"
		if e.Created != nil {
			o.ContainerID = e.Created.ContainerID
		}
		if cleanup && e.Removed == nil {
			if err := publishResource(filepath.Join(dir, "removed.json"), ResourceRemoved{1, h, o.ContainerID}); err != nil {
				return fail(err)
			}
		}
		return o, nil
	}
	o.ContainerID = c.ID
	if e.Dispatch == nil || e.Created != nil && e.Created.ContainerID != c.ID || e.Removed != nil {
		return fail(errors.New("container conflicts with its creation or removal witness"))
	}
	if !cleanup {
		o.State = "present"
		return o, errors.New("owned execution container still exists")
	}
	// 先保存创建完成见证，再删除精确 ID；名称随后被重用也不能导致误删。
	if e.Created == nil {
		if err := publishResource(filepath.Join(dir, "created.json"), ResourceCreated{1, h, c.ID}); err != nil {
			return fail(err)
		}
	}
	if _, err := d.control(ctx, "rm", "--force", "--volumes", c.ID); err != nil {
		return fail(err)
	}
	if _, exists, err := d.inspectResource(ctx, i); err != nil || exists {
		if err == nil {
			err = errors.New("container still exists after removal")
		}
		return fail(err)
	}
	if err := publishResource(filepath.Join(dir, "removed.json"), ResourceRemoved{1, h, c.ID}); err != nil {
		return fail(err)
	}
	o.State = "removed"
	return o, nil
}

func (d *Docker) finishResource(ctx context.Context, i ResourceIntent) error {
	if err := d.checkEngine(ctx, i.Owner.Registry.Policy); err != nil {
		return errors.Join(ErrResourcesUnknown, err)
	}
	e, err := readResourceEntry(i.Owner.Root, i.CommandID, i.Owner.Registry)
	if err != nil {
		return err
	}
	_, err = d.reconcileResource(ctx, i.Owner.Root, e, true)
	return err
}
