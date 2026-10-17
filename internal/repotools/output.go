package repotools

import (
	"strings"
	"sync"
	"unicode/utf8"
)

const truncatedMarker = "\n[output truncated]\n"

// boundedOutput 持续接收命令输出但只保留有限前缀，避免管道阻塞或内存无界增长。
type boundedOutput struct {
	mu        sync.Mutex
	data      []byte
	limit     int
	truncated bool
}

func newOutput(limit int) *boundedOutput { return &boundedOutput{limit: limit} }

func (o *boundedOutput) Write(data []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	remaining := o.limit - len(o.data)
	if len(data) > remaining {
		o.truncated = true
	}
	o.data = append(o.data, data[:min(len(data), remaining)]...)
	return len(data), nil
}

func (o *boundedOutput) full() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.truncated
}

func (o *boundedOutput) String() string {
	return o.stringWithPrefix("")
}

// stringWithPrefix 将状态前缀和原始输出一起截断，只生成一次截断标记。
func (o *boundedOutput) stringWithPrefix(prefix string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	text := prefix + strings.ToValidUTF8(string(o.data), "�")
	if !o.truncated && len(text) <= o.limit {
		return text
	}
	text = text[:min(len(text), o.limit-len(truncatedMarker))]
	for !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text + truncatedMarker
}
