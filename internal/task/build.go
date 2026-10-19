package task

import (
	"errors"
	"strings"
)

// BuildConfiguration 是宿主固定的必需构建配置，不接受模型自由选择标签。
type BuildConfiguration struct {
	ID   string   `json:"id"`
	Tags []string `json:"tags"`
}

// Arguments 为已有 Go 命令插入标签；args 须包含可执行文件和子命令。
func (c BuildConfiguration) Arguments(args []string) []string {
	out := append([]string{}, args...)
	if len(c.Tags) == 0 {
		return out
	}
	return append([]string{out[0], out[1], "-tags=" + strings.Join(c.Tags, ",")}, out[2:]...)
}
func ValidateBuildConfigurations(configs []BuildConfiguration) error {
	seen := map[string]bool{}
	for _, c := range configs {
		if !validID.MatchString(c.ID) || seen[c.ID] {
			return errors.New("build configuration IDs must be valid and unique")
		}
		seen[c.ID] = true
		tags := map[string]bool{}
		for _, tag := range c.Tags {
			if !validID.MatchString(tag) || tags[tag] {
				return errors.New("build tags must be valid and unique")
			}
			tags[tag] = true
		}
	}
	return nil
}
