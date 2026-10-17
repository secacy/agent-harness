package execution

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLocalPreservesStreamsExitAndControlledEnvironment(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("local process groups require macOS or Linux")
	}
	t.Setenv("DEEPSEEK_API_KEY", "must-not-reach-tests")
	t.Setenv("GOFLAGS", "host-flags")
	result, err := (Local{}).Run(context.Background(), Request{
		Args:      []string{"/bin/sh", "-c", `printf '%s|%s|%s' "$GOFLAGS" "$GOENV" "$DEEPSEEK_API_KEY"; printf diagnostic >&2; exit 7`},
		Directory: t.TempDir(), CacheDir: t.TempDir(),
	})
	if err != nil || result.ExitCode != 7 || string(result.Stdout) != "-mod=readonly|off|" || string(result.Stderr) != "diagnostic" {
		t.Fatalf("local result: %+v %v", result, err)
	}
}

func TestDockerConfigurationHasRestrictedMountsAndResources(t *testing.T) {
	d := &Docker{info: Info{Kind: "docker", Image: "sha256:test", CPUs: 2, MemoryBytes: 2048, Pids: 128}}
	dir := t.TempDir()
	args, err := d.createArgs(Request{Directory: dir, ReadOnlyDirs: []string{dir}, Args: []string{"go", "test", "./..."}})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, flag := range []string{"--pull=never", "--read-only", "--network=none", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user=65534:65534", "--pids-limit=128", "--cpus=2", "--memory=2048", "readonly", "sha256:test -i"} {
		if !strings.Contains(joined, flag) {
			t.Errorf("missing %s in %v", flag, args)
		}
	}
	if strings.Contains(joined, "docker.sock") || strings.Contains(joined, "--privileged") {
		t.Fatal("unexpected host privilege")
	}
	if _, err := d.createArgs(Request{Directory: dir, Args: []string{"go"}}); err == nil {
		t.Fatal("accepted implicit mounts")
	}
	if err := os.Symlink(os.DevNull, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := d.createArgs(Request{Directory: dir, ReadOnlyDirs: []string{dir}, Args: []string{"go"}}); err == nil {
		t.Fatal("accepted symlink in mounted source")
	}
}

func dockerForTest(t *testing.T, memory int64) *Docker {
	t.Helper()
	if os.Getenv("HARNESS_DOCKER_TEST") != "1" {
		t.Skip("set HARNESS_DOCKER_TEST=1 to run real Docker integration tests")
	}
	d, err := NewDocker(context.Background(), DockerConfig{MemoryBytes: memory})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func assertRemoved(t *testing.T, d *Docker, id string) {
	t.Helper()
	if id == "" {
		t.Fatal("container was never created")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	data, err := d.control(ctx, "inspect", id)
	if err == nil || !missingContainer(err) {
		t.Fatalf("container was not removed: %s %v", data, err)
	}
}

func TestDockerIsolationAndOrdinaryFailure(t *testing.T) {
	d := dockerForTest(t, 0)
	t.Setenv("DEEPSEEK_API_KEY", "must-not-reach-container")
	root := t.TempDir()
	source := filepath.Join(root, "demo-orders")
	if err := os.Mkdir(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "source.txt"), []byte("immutable"), 0644); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(root, "unmounted-secret")
	if err := os.WriteFile(secret, []byte("host-only"), 0644); err != nil {
		t.Fatal(err)
	}
	script := `set -eu
test "$(id -u)" = 65534
test -z "${DEEPSEEK_API_KEY:-}"
test ! -e "$1"
test ! -S /var/run/docker.sock
if printf corrupted > source.txt 2>/dev/null; then exit 40; fi
if printf corrupted > /etc/forbidden 2>/dev/null; then exit 41; fi
if timeout 1 bash -c 'echo network >/dev/tcp/1.1.1.1/80' 2>/dev/null; then exit 42; fi
test "$(cat /sys/fs/cgroup/pids.max)" = 128
test "$(cat /sys/fs/cgroup/memory.max)" = 2147483648
read quota period < /sys/fs/cgroup/cpu.max
test "$quota" -eq "$((2 * period))"
grep -q '^CapEff:[[:space:]]*0000000000000000$' /proc/self/status
grep -q '^NoNewPrivs:[[:space:]]*1$' /proc/self/status
printf temporary >/tmp/allowed
printf isolated
printf diagnostic >&2
exit 17`
	result, err := d.Run(context.Background(), Request{Directory: source, ReadOnlyDirs: []string{source}, Args: []string{"bash", "-c", script, "probe", secret}})
	if err != nil || result.ExitCode != 17 || string(result.Stdout) != "isolated" || !strings.HasSuffix(string(result.Stderr), "diagnostic") {
		t.Fatalf("Docker isolation: %+v %v", result, err)
	}
	assertRemoved(t, d, result.ContainerID)
	if data, err := os.ReadFile(filepath.Join(source, "source.txt")); err != nil || string(data) != "immutable" {
		t.Fatalf("source changed: %s %v", data, err)
	}
	second, err := d.Run(context.Background(), Request{Directory: source, ReadOnlyDirs: []string{source}, Args: []string{"bash", "-c", "test ! -e /tmp/allowed"}})
	if err != nil || second.ExitCode != 0 {
		t.Fatalf("temporary state leaked into another command: %+v %v", second, err)
	}
	assertRemoved(t, d, second.ContainerID)
}

func TestDockerCancellationOutputLimitOOMAndStartupFailure(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline", "output", "oom", "startup"} {
		t.Run(mode, func(t *testing.T) {
			memory := int64(0)
			if mode == "oom" {
				memory = 32 * 1024 * 1024
			}
			d := dockerForTest(t, memory)
			dir := t.TempDir()
			if err := os.Chmod(dir, 0755); err != nil {
				t.Fatal(err)
			}
			req := Request{Directory: dir, ReadOnlyDirs: []string{dir}, Args: []string{"bash", "-c", "sleep 60 & wait"}}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			want := error(context.DeadlineExceeded)
			switch mode {
			case "cancel":
				ctx2, cancel2 := context.WithCancel(ctx)
				defer cancel2()
				ctx = ctx2
				timer := time.AfterFunc(time.Second, cancel2)
				defer timer.Stop()
				want = context.Canceled
			case "output":
				req.Args, req.MaxBytes = []string{"bash", "-c", "yes output"}, 1024
				want = ErrOutputLimit
			case "oom":
				req.Args = []string{"bash", "-c", `x=$(head -c 8388608 /dev/zero | tr '\0' x); while :; do x="$x$x"; done`}
				want = ErrOOM
			case "startup":
				d.info.Image = "sha256:" + strings.Repeat("0", 64)
			}
			result, err := d.Run(ctx, req)
			if mode == "startup" {
				if err == nil || result.ExitCode == 0 {
					t.Fatalf("startup failure accepted: %+v %v", result, err)
				}
				return
			}
			if !errors.Is(err, want) || result.ExitCode == 0 {
				t.Fatalf("%s: %+v %v", mode, result, err)
			}
			if mode == "output" && (len(result.Stdout) > 1024 || !result.OutputLimit) {
				t.Fatalf("unbounded output: %+v", result)
			}
			assertRemoved(t, d, result.ContainerID)
		})
	}
}

func TestLocalOutputLimitIsAnExecutionError(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("requires process groups")
	}
	dir := t.TempDir()
	result, err := (Local{}).Run(context.Background(), Request{Args: []string{"/bin/sh", "-c", "yes output"}, Directory: dir, CacheDir: t.TempDir(), MaxBytes: 100})
	if !errors.Is(err, ErrOutputLimit) || len(result.Stdout) > 100 || !result.OutputLimit {
		t.Fatalf("output limit: %+v %v", result, err)
	}
}

// 防止测试环境缺少 CLI 时误将真实容器测试标记为成功。
func TestDockerMissingImageDoesNotPull(t *testing.T) {
	if os.Getenv("HARNESS_DOCKER_TEST") != "1" {
		t.Skip("requires Docker")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal(err)
	}
	_, err := NewDocker(context.Background(), DockerConfig{Image: "harness-missing-image:no-pull"})
	if err == nil || !strings.Contains(err.Error(), "pull it before running") {
		t.Fatalf("missing image: %v", err)
	}
}

func TestDockerCleanupFailureCannotReturnSuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires shell test double")
	}
	binary := filepath.Join(t.TempDir(), "docker-double")
	script := `#!/bin/sh
case "$1" in
create) printf fake-container ;;
start) printf done ;;
inspect) printf '{"Status":"exited","ExitCode":0,"Running":false,"OOMKilled":false}' ;;
rm) printf 'injected cleanup failure' >&2; exit 1 ;;
*) exit 2 ;;
esac
`
	if err := os.WriteFile(binary, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	d := &Docker{binary: binary, info: Info{Kind: "docker", Image: "sha256:test", CPUs: 1, MemoryBytes: 64 * 1024 * 1024, Pids: 32}}
	dir := t.TempDir()
	result, err := d.Run(context.Background(), Request{Directory: dir, ReadOnlyDirs: []string{dir}, Args: []string{"go", "version"}})
	if err == nil || !strings.Contains(err.Error(), "remove container") || result.ExitCode != 0 || string(result.Stdout) != "done" {
		t.Fatalf("cleanup failure lost: %+v %v", result, err)
	}
}
