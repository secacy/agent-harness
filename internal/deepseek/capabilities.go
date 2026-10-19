package deepseek

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/secacy/agent-harness/internal/budget"
)

// Capabilities 保存服务声明及其原始响应身份；不证明服务内部实现或实际计费。
type Capabilities struct {
	URL              string         `json:"url"`
	Profile          budget.Profile `json:"profile"`
	ModelOutputLimit int64          `json:"model_output_limit"`
	Response         []byte         `json:"-"`
}

type modelMetadata struct {
	ID               string   `json:"id"`
	ContextWindow    int64    `json:"context_window"`
	MaxOutputTokens  int64    `json:"max_output_tokens"`
	InputModalities  []string `json:"input_modalities"`
	OutputModalities []string `json:"output_modalities"`
}

// Discover 从同源模型端点读取当前上界；不发送生成请求，不猜测缺失字段。
func (c *Client) Discover(ctx context.Context) (Capabilities, error) {
	endpoint, err := ModelsEndpoint(c.endpoint)
	if err != nil {
		return Capabilities{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Capabilities{}, err
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Accept", "application/json")
	response, err := c.httpClient.Do(request)
	if err != nil {
		return Capabilities{}, fmt.Errorf("query model capabilities: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		// 不保存未知错误正文，避免鉴权信息被服务回显到报告。
		return Capabilities{}, &HTTPError{StatusCode: response.StatusCode, Message: "model capabilities unavailable"}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return Capabilities{}, fmt.Errorf("read model capabilities: %w", err)
	}
	if len(data) > maxResponseBytes || bytes.Contains(data, []byte(c.apiKey)) {
		return Capabilities{}, errors.New("model capabilities response exceeds limit or contains credentials")
	}
	var models struct {
		Object string          `json:"object"`
		Data   []modelMetadata `json:"data"`
	}
	if err := json.Unmarshal(data, &models); err != nil {
		return Capabilities{}, errors.New("invalid model capabilities JSON")
	}
	var selected modelMetadata
	matches := 0
	for _, model := range models.Data {
		if model.ID == c.model {
			selected = model
			matches++
		}
	}
	if models.Object != "list" || matches != 1 || selected.ContextWindow <= 0 || selected.MaxOutputTokens <= 0 ||
		selected.MaxOutputTokens > selected.ContextWindow || !slices.Contains(selected.InputModalities, "text") || !slices.Contains(selected.OutputModalities, "text") {
		return Capabilities{}, errors.New("one matching text model with explicit positive token bounds is required")
	}
	if int64(c.maxTokens) > selected.MaxOutputTokens {
		return Capabilities{}, errors.New("max tokens exceeds the declared model output limit")
	}
	hash := sha256.Sum256(data)
	profile := budget.Profile{Provider: "deepseek", Endpoint: c.endpoint, Model: c.model,
		ContextTokens: selected.ContextWindow, MaxOutputTokens: int64(c.maxTokens),
		SourceSHA256: hex.EncodeToString(hash[:]), VerifiedAt: time.Now().UTC()}
	if err := profile.Validate(); err != nil {
		return Capabilities{}, err
	}
	return Capabilities{URL: endpoint, Profile: profile, ModelOutputLimit: selected.MaxOutputTokens, Response: data}, nil
}

// ModelsEndpoint 保留部署前缀并固定同源路径；不允许将凭证重定向到其他端点。
func ModelsEndpoint(endpoint string) (string, error) {
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || !strings.HasSuffix(u.Path, "/chat/completions") {
		return "", errors.New("capability discovery requires a chat/completions endpoint without URL decorations")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && ip != nil && ip.IsLoopback()) {
		return "", errors.New("capability discovery requires HTTPS or an HTTP loopback address")
	}
	u.Path = strings.TrimSuffix(u.Path, "/chat/completions") + "/models"
	return u.String(), nil
}
