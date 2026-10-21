package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// 发布完成标记最后写入；它只认证这份审计产物已经完整落盘。
func publish(directory string, report Report) error {
	files := map[string]string{}
	write := func(name string, value any) error {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		data = append(data, '\n')
		if err = writeOutput(filepath.Join(directory, name), data); err != nil {
			return err
		}
		files[name] = checksum(data)
		return nil
	}
	if err := write("audit.json", report); err != nil {
		return err
	}
	if err := write("sources.json", report.Sources); err != nil {
		return err
	}
	text := fmt.Sprintf("# 只读审计\n\n一致性：%s；执行发布状态：%s。\n\n违规 %d；证据缺口 %d；观察 %d。写者活性未检查。\n\n详细依据见 [audit.json](audit.json) 与 [sources.json](sources.json)。审计结果不授予恢复或释放预算的许可。\n", report.Consistency, report.ExecutionState, len(report.Violations), len(report.Gaps), len(report.Observations))
	if report.RecoveryExecutionState != "" {
		text += fmt.Sprintf("\n恢复代次执行状态：%s；原根执行状态保留为历史事实。\n", report.RecoveryExecutionState)
	}
	if report.ResourceState != "" {
		text += fmt.Sprintf("\n资源记录状态：%s；本审计未连接 Docker，不证明当前资源已停止。\n", report.ResourceState)
	}
	if err := writeOutput(filepath.Join(directory, "audit.md"), []byte(text)); err != nil {
		return err
	}
	files["audit.md"] = checksum([]byte(text))
	return write("published.json", struct {
		SchemaVersion int               `json:"schema_version"`
		Files         map[string]string `json:"files"`
	}{1, files})
}

func writeOutput(p string, data []byte) error {
	file, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	if err = errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(p))
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}
