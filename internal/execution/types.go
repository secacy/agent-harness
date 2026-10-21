// Package execution 在可信宿主或受限容器中执行宿主配置的命令。
package execution

import (
	"context"
	"errors"
)

var (
	ErrOutputLimit = errors.New("command output limit exceeded")
	ErrOOM         = errors.New("container memory limit exceeded")
)

const (
	KindLocal  = "trusted_local"
	KindDocker = "docker"
)

type Request struct {
	Args         []string
	Directory    string
	ReadOnlyDirs []string
	CacheDir     string         // 仅本地后端使用；容器缓存随命令销毁。
	MaxBytes     int            // stdout 和 stderr 各自的上限，默认 4 MiB。
	Owner        *ResourceOwner // 由宿主绑定的执行器覆盖，Agent 不能设置归属。
}

type Result struct {
	Stdout      []byte
	Stderr      []byte
	ExitCode    int
	OOMKilled   bool
	OutputLimit bool
	ContainerID string
}

type Info struct {
	Kind        string `json:"kind"`
	Image       string `json:"image,omitempty"`
	CPUs        int    `json:"cpus,omitempty"`
	MemoryBytes int64  `json:"memory_bytes,omitempty"`
	Pids        int    `json:"pids,omitempty"`
}

// Executor 同步等待命令退出并完成回收；普通非零退出保存在 Result 中。
type Executor interface {
	Run(context.Context, Request) (Result, error)
	Info() Info
}
