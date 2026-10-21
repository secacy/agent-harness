package execution

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const ResourceProtocol = "docker-intent-v1"

var ErrResourcesUnknown = errors.New("execution resources are not confirmed absent")
var resourceID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)
var containerID = regexp.MustCompile(`^[a-f0-9]{64}$`)
var commandID = regexp.MustCompile(`^[a-f0-9]{32}$`)

type ResourcePolicy struct {
	Protocol string `json:"protocol"`
	Context  string `json:"context"`
	EngineID string `json:"engine_id"`
}

func (p ResourcePolicy) Validate() error {
	if p.Protocol != ResourceProtocol || p.Context == "" || strings.HasPrefix(p.Context, "-") || strings.ContainsAny(p.Context, "\x00\n\r") || p.EngineID == "" || strings.ContainsAny(p.EngineID, "\x00\n\r") {
		return errors.New("supported resource protocol, context and engine identity are required")
	}
	return nil
}

type ResourceRegistry struct {
	SchemaVersion int            `json:"schema_version"`
	CampaignID    string         `json:"campaign_id"`
	PlanSHA256    string         `json:"plan_sha256"`
	Policy        ResourcePolicy `json:"policy"`
}

type ResourceOwner struct {
	Root         string           `json:"-"`
	Registry     ResourceRegistry `json:"registry"`
	TargetID     string           `json:"target_id"`
	RunID        string           `json:"run_id"`
	RunDirectory string           `json:"run_directory"`
}

type ResourceMount struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	ReadOnly    bool   `json:"read_only"`
}
type ResourceIntent struct {
	SchemaVersion int             `json:"schema_version"`
	CommandID     string          `json:"command_id"`
	Name          string          `json:"name"`
	Owner         ResourceOwner   `json:"owner"`
	Image         string          `json:"image"`
	ArgsSHA256    string          `json:"args_sha256"`
	Mounts        []ResourceMount `json:"mounts"`
	CreatedAt     time.Time       `json:"created_at"`
}
type ResourceDispatch struct {
	SchemaVersion int    `json:"schema_version"`
	IntentSHA256  string `json:"intent_sha256"`
}
type ResourceCreated struct {
	SchemaVersion int    `json:"schema_version"`
	IntentSHA256  string `json:"intent_sha256"`
	ContainerID   string `json:"container_id"`
}
type ResourceRemoved struct {
	SchemaVersion int    `json:"schema_version"`
	IntentSHA256  string `json:"intent_sha256"`
	ContainerID   string `json:"container_id,omitempty"`
}
type ResourceEntry struct {
	Intent   ResourceIntent
	Dispatch *ResourceDispatch
	Created  *ResourceCreated
	Removed  *ResourceRemoved
}

func ResourceSource(p string) bool { return p == "resources" || strings.HasPrefix(p, "resources/") }
func resourceBytes(value any) ([]byte, error) {
	b, err := json.MarshalIndent(value, "", "  ")
	return append(b, '\n'), err
}
func ResourceDigest(value any) string {
	b, _ := resourceBytes(value)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// DecodeResource 接受宿主持久化的规范 JSON，拒绝重复键、未知字段及隐式缺字段。
func DecodeResource(data []byte, value any) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("resource record must be a JSON object")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if err := d.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("trailing resource JSON")
	}
	b, err := resourceBytes(value)
	if err != nil || !bytes.Equal(data, b) {
		return errors.New("resource JSON is not in its canonical published form")
	}
	return nil
}

func ValidateResourceIntent(i ResourceIntent, r ResourceRegistry) error {
	o := i.Owner
	if i.SchemaVersion != 1 || o.Registry != r || r.SchemaVersion != 1 || r.Policy.Validate() != nil || !resourceID.MatchString(r.CampaignID) || !containerID.MatchString(r.PlanSHA256) || !resourceID.MatchString(o.TargetID) || !resourceID.MatchString(o.RunID) || !commandID.MatchString(i.CommandID) || i.Name != "harness-owned-"+i.CommandID || !strings.HasPrefix(i.Image, "sha256:") || !containerID.MatchString(strings.TrimPrefix(i.Image, "sha256:")) || !containerID.MatchString(i.ArgsSHA256) || i.CreatedAt.IsZero() || len(i.Mounts) == 0 {
		return errors.New("invalid resource intent or owner")
	}
	first := "targets/" + o.TargetID + "/runs/" + o.RunID
	if o.RunDirectory != first && o.RunDirectory != "recoveries/execution/"+first {
		return errors.New("resource run directory differs from its owner")
	}
	seen := map[string]bool{}
	for _, m := range i.Mounts {
		if !filepath.IsAbs(m.Source) || filepath.Clean(m.Source) != m.Source || !strings.HasPrefix(m.Destination, "/workspace/") || m.Destination != "/workspace/"+filepath.Base(m.Source) || !m.ReadOnly || seen[m.Destination] {
			return errors.New("invalid resource mount")
		}
		seen[m.Destination] = true
	}
	return nil
}

func ValidateResourceEntry(e ResourceEntry, r ResourceRegistry) error {
	if err := ValidateResourceIntent(e.Intent, r); err != nil {
		return err
	}
	h := ResourceDigest(e.Intent)
	if e.Dispatch != nil && (e.Dispatch.SchemaVersion != 1 || e.Dispatch.IntentSHA256 != h) {
		return errors.New("resource dispatch differs from intent")
	}
	if e.Created != nil && (e.Dispatch == nil || e.Created.SchemaVersion != 1 || e.Created.IntentSHA256 != h || !containerID.MatchString(e.Created.ContainerID)) {
		return errors.New("resource creation witness differs from intent")
	}
	if e.Removed != nil && (e.Removed.SchemaVersion != 1 || e.Removed.IntentSHA256 != h || e.Dispatch != nil && (e.Created == nil || e.Removed.ContainerID != e.Created.ContainerID) || e.Dispatch == nil && e.Removed.ContainerID != "") {
		return errors.New("resource removal witness lacks creation provenance")
	}
	return nil
}

func resourceRead(root, relative string, value any) error {
	if !safeResourcePath(relative) {
		return errors.New("invalid resource path")
	}
	p := root
	for _, part := range strings.Split(relative, "/") {
		p = filepath.Join(p, part)
		st, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return errors.New("resource path must not contain links")
		}
	}
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > 64<<10 {
		_ = f.Close()
		return errors.New("resource record must be a bounded regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if len(b) > 64<<10 {
		return errors.New("resource record too large")
	}
	return DecodeResource(b, value)
}
func safeResourcePath(p string) bool {
	return p != "" && p != "." && !filepath.IsAbs(p) && filepath.ToSlash(filepath.Clean(p)) == p && !strings.Contains(p, "\\") && p != ".." && !strings.HasPrefix(p, "../")
}
func syncResourceDirectory(p string) error {
	d, err := os.Open(p)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}
func createResourceDirectory(p string) error {
	if err := os.Mkdir(p, 0700); err != nil {
		return err
	}
	return syncResourceDirectory(filepath.Dir(p))
}
func publishResource(p string, value any) error {
	b, err := resourceBytes(value)
	if err != nil {
		return err
	}
	if st, err := os.Lstat(p); err == nil {
		if !st.Mode().IsRegular() {
			return errors.New("resource publication is not a regular file")
		}
		old, err := os.ReadFile(p)
		if err != nil || !bytes.Equal(old, b) {
			return errors.New("immutable resource record differs")
		}
		return syncResourceDirectory(filepath.Dir(p))
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".resource-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err = errors.Join(f.Sync(), f.Close()); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), p); err != nil {
		return err
	}
	return syncResourceDirectory(filepath.Dir(p))
}

// CreateResourceRegistry 必须在持有 Campaign 写锁且发布计划后、接纳任务前调用。
func CreateResourceRegistry(root string, r ResourceRegistry) error {
	if r.SchemaVersion != 1 || r.Policy.Validate() != nil || !resourceID.MatchString(r.CampaignID) || !containerID.MatchString(r.PlanSHA256) {
		return errors.New("invalid resource registry")
	}
	dir := filepath.Join(root, "resources")
	if err := createResourceDirectory(dir); err != nil {
		return err
	}
	return publishResource(filepath.Join(dir, "registry.json"), r)
}

type ownedExecutor struct {
	Executor
	owner ResourceOwner
}

// WithResourceOwner 覆盖宿主请求中的归属，适用于共享槽位及场景适配器下层。
func WithResourceOwner(backend Executor, owner ResourceOwner) (Executor, error) {
	if backend == nil || backend.Info().Kind != KindDocker || !filepath.IsAbs(owner.Root) || !resourceID.MatchString(owner.TargetID) || !resourceID.MatchString(owner.RunID) {
		return nil, errors.New("Docker backend and fixed run ownership are required")
	}
	var actual ResourceRegistry
	if err := resourceRead(owner.Root, "resources/registry.json", &actual); err != nil {
		return nil, err
	}
	if actual != owner.Registry {
		return nil, errors.New("resource registry differs from the run scope")
	}
	return &ownedExecutor{backend, owner}, nil
}
func (e *ownedExecutor) Run(ctx context.Context, r Request) (Result, error) {
	owner := e.owner
	r.Owner = &owner
	return e.Executor.Run(ctx, r)
}

// ReadResources 仅加载控制记录；不调用 Docker 或推断运行资源已停止。
func ReadResources(root string, expected ResourceRegistry) ([]ResourceEntry, error) {
	var registry ResourceRegistry
	if err := resourceRead(root, "resources/registry.json", &registry); err != nil {
		return nil, err
	}
	if registry != expected {
		return nil, errors.New("resource registry identity mismatch")
	}
	entries, err := os.ReadDir(filepath.Join(root, "resources"))
	if err != nil {
		return nil, err
	}
	if len(entries) > 10001 {
		return nil, errors.New("too many resource records")
	}
	out := []ResourceEntry{}
	for _, dir := range entries {
		if dir.Name() == "registry.json" || strings.HasPrefix(dir.Name(), ".resource-") && dir.Type().IsRegular() {
			continue
		}
		if !dir.IsDir() || !commandID.MatchString(dir.Name()) {
			return nil, errors.New("unexpected resource entry")
		}
		p := "resources/" + dir.Name()
		var e ResourceEntry
		if err := resourceRead(root, p+"/intent.json", &e.Intent); err != nil {
			members, listErr := os.ReadDir(filepath.Join(root, p))
			unpublished := listErr == nil
			for _, m := range members {
				if !m.Type().IsRegular() || !strings.HasPrefix(m.Name(), ".resource-") {
					unpublished = false
				}
			}
			if errors.Is(err, os.ErrNotExist) && unpublished {
				continue
			}
			return nil, err
		}
		e, err = readResourceEntry(root, dir.Name(), registry)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func (o ResourceOwner) String() string {
	return fmt.Sprintf("%s/%s/%s", o.Registry.CampaignID, o.TargetID, o.RunID)
}

// 从末端向前读取只追加的见证，避免读取到先缺前驱、后见后继的不可能前缀。
func readResourceEntry(root, id string, registry ResourceRegistry) (ResourceEntry, error) {
	var e ResourceEntry
	p := "resources/" + id
	if err := resourceRead(root, p+"/intent.json", &e.Intent); err != nil {
		return e, err
	}
	if e.Intent.CommandID != id {
		return e, errors.New("resource command directory mismatch")
	}
	for _, f := range []struct {
		name  string
		value any
	}{{"removed.json", &e.Removed}, {"created.json", &e.Created}, {"dispatch.json", &e.Dispatch}} {
		if err := resourceRead(root, p+"/"+f.name, f.value); err != nil && !errors.Is(err, os.ErrNotExist) {
			return e, err
		}
	}
	members, err := os.ReadDir(filepath.Join(root, p))
	if err != nil {
		return e, err
	}
	for _, m := range members {
		if m.Name() != "intent.json" && m.Name() != "dispatch.json" && m.Name() != "created.json" && m.Name() != "removed.json" && !(m.Type().IsRegular() && strings.HasPrefix(m.Name(), ".resource-")) {
			return e, errors.New("unexpected resource command member")
		}
	}
	return e, ValidateResourceEntry(e, registry)
}
