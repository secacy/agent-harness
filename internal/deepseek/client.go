package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/secacy/agent-harness/internal/agent"
	"github.com/secacy/agent-harness/internal/budget"
)

const (
	defaultEndpoint  = "https://api.deepseek.com/chat/completions"
	defaultMaxTokens = 4096
	maxResponseBytes = 4 * 1024 * 1024
	maxErrorBytes    = 1024
)

// Config 由调用方提供，密钥不从环境隐式读取，模型名不绑定特定版本。
type Config struct {
	APIKey     string
	Model      string
	Endpoint   string
	MaxTokens  int
	HTTPClient *http.Client
	// BudgetProfile 由可信宿主核实；为空时只能运行原有无批次预算的单仓库流程。
	BudgetProfile *budget.Profile
}

type Client struct {
	apiKey        string
	model         string
	endpoint      string
	maxTokens     int
	httpClient    *http.Client
	budgetProfile *budget.Profile
}

var _ agent.Model = (*Client)(nil)

// HTTPError 保留服务状态码，便于调用方区分鉴权、限流与服务故障。
type HTTPError struct {
	StatusCode int
	Message    string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("DeepSeek API returned HTTP %d: %s", e.StatusCode, e.Message)
}

func New(config Config) (*Client, error) {
	key, model := strings.TrimSpace(config.APIKey), strings.TrimSpace(config.Model)
	if key == "" || model == "" {
		return nil, errors.New("API key and model are required")
	}
	if strings.ContainsAny(key, "\r\n") {
		return nil, errors.New("API key contains invalid header characters")
	}
	endpoint := config.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, errors.New("invalid endpoint URL")
	}
	if parsed.Host == "" ||
		(parsed.Scheme != "https" && parsed.Scheme != "http") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("endpoint must be an absolute HTTP or HTTPS URL without credentials, query or fragment")
	}
	maxTokens := config.MaxTokens
	if maxTokens == 0 {
		maxTokens = defaultMaxTokens
	}
	if maxTokens < 1 {
		return nil, errors.New("max tokens must be positive")
	}
	// 复制客户端配置，避免改动调用方的超时和重定向策略。
	client := http.Client{}
	if config.HTTPClient != nil {
		client = *config.HTTPClient
	}
	if client.Timeout < 0 {
		return nil, errors.New("HTTP timeout must not be negative")
	}
	if client.Timeout == 0 {
		client.Timeout = 2 * time.Minute
	}
	var profile *budget.Profile
	if config.BudgetProfile != nil {
		value := *config.BudgetProfile
		if err := value.Validate(); err != nil {
			return nil, err
		}
		if value.Provider != "deepseek" || value.Endpoint != endpoint || value.Model != model || value.MaxOutputTokens != int64(maxTokens) {
			return nil, errors.New("budget profile does not match the DeepSeek request configuration")
		}
		// 预算模式只允许标准传输，防止自定义 RoundTripper 隐式重试请求。
		if client.Transport == nil {
			client.Transport = http.DefaultTransport
		}
		if client.Transport != nil {
			if _, ok := client.Transport.(*http.Transport); !ok {
				return nil, errors.New("budgeted DeepSeek calls require a standard HTTP transport")
			}
		}
		profile = &value
	}
	// 重定向作为 HTTP 错误返回，不向其他地址自动重放带密钥的请求。
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{
		apiKey:        key,
		model:         model,
		endpoint:      endpoint,
		maxTokens:     maxTokens,
		httpClient:    &client,
		budgetProfile: profile,
	}, nil
}

func (c *Client) Complete(ctx context.Context, messages []agent.Message, tools []agent.ToolSpec) (agent.Reply, error) {
	if err := ctx.Err(); err != nil {
		return agent.Reply{}, err
	}
	if len(messages) == 0 {
		return agent.Reply{}, errors.New("messages are required")
	}
	payload, err := json.Marshal(chatRequest{
		Model:     c.model,
		Messages:  encodeMessages(messages),
		Tools:     encodeTools(tools),
		MaxTokens: c.maxTokens,
		Stream:    false,
		Thinking:  thinkingConfig{Type: "disabled"},
	})
	if err != nil {
		return agent.Reply{}, fmt.Errorf("encode completion request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return agent.Reply{}, fmt.Errorf("create completion request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	response, err := c.httpClient.Do(request)
	if err != nil {
		return agent.Reply{}, fmt.Errorf("send completion request: %w", err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return agent.Reply{}, fmt.Errorf("read completion response: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		message := "unexpected response status"
		var envelope struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if len(data) <= maxResponseBytes && json.Unmarshal(data, &envelope) == nil && envelope.Error.Message != "" {
			message = strings.ReplaceAll(envelope.Error.Message, c.apiKey, "[redacted]")
			if len(message) > maxErrorBytes {
				message = strings.ToValidUTF8(message[:maxErrorBytes], "") + " [truncated]"
			}
		}
		return agent.Reply{}, &HTTPError{StatusCode: response.StatusCode, Message: message}
	}
	if len(data) > maxResponseBytes {
		return agent.Reply{}, errors.New("completion response exceeds 4 MiB limit")
	}
	var completion chatResponse
	if err := json.Unmarshal(data, &completion); err != nil {
		return agent.Reply{}, fmt.Errorf("decode completion response: %w", err)
	}
	return decodeReply(completion)
}

// BudgetProfile 返回宿主提供的配置副本，不根据模型别名猜测上界。
func (c *Client) BudgetProfile() (budget.Profile, error) {
	if c.budgetProfile == nil {
		return budget.Profile{}, errors.New("DeepSeek budget profile has not been verified by the host")
	}
	return *c.budgetProfile, nil
}
