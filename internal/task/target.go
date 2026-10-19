// Package task 描述一次仓库迁移任务的固定输入，不依赖具体迁移场景。
package task

import (
	"encoding/hex"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
)

// Snapshot 使用相对文件路径到文件摘要的有序 JSON 再取 SHA-256 标识目录。
type Snapshot struct {
	Directory string `json:"directory"`
	SHA256    string `json:"sha256"`
}

type Specification struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type Target struct {
	ID            string              `json:"id"`
	ProfileID     string              `json:"profile_id"`
	BuildConfig   string              `json:"build_config"`
	Repository    Snapshot            `json:"repository"`
	Dependencies  map[string]Snapshot `json:"dependencies"`
	Specification Specification       `json:"specification"`
}

var validID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)

// Validate 只检查描述结构，不读取文件，也不认证 Profile 或声明的摘要。
func (t Target) Validate() error {
	if !validID.MatchString(t.ID) || !validID.MatchString(t.ProfileID) || !validID.MatchString(t.BuildConfig) {
		return errors.New("valid target, profile and build configuration IDs are required")
	}
	if !filepath.IsAbs(t.Repository.Directory) || !digest(t.Repository.SHA256) || !filepath.IsAbs(t.Specification.Path) || !digest(t.Specification.SHA256) {
		return errors.New("absolute input paths and SHA-256 digests are required")
	}
	for name, snapshot := range t.Dependencies {
		if !validID.MatchString(name) || name == "workspace" || name == "spec" || !filepath.IsAbs(snapshot.Directory) || !digest(snapshot.SHA256) {
			return errors.New("dependency names, absolute paths and SHA-256 digests must be valid")
		}
	}
	return nil
}
func digest(value string) bool {
	data, err := hex.DecodeString(value)
	return err == nil && len(data) == 32 && strings.ToLower(value) == value
}
