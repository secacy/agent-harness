//go:build rpcx_alternate

package webapi

import (
	"context"
	"example.com/rpcx"
	"fmt"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/repo"
)

// TranslationWebAPI 保留翻译出站边界，传输替换为本地 rpcx-v1。
type TranslationWebAPI struct{ client *rpcx.Client }

// adapterVariant 仅标识本配置的实际出站实现。
const adapterVariant = "rpcx_alternate"

// New 连接宿主准备的本地后端，并保留原 tracing 包装。
func New(addr string) (repo.TranslationWebAPI, error) {
	client, err := rpcx.NewClient(addr)
	if err != nil {
		return nil, fmt.Errorf("TranslationWebAPI - New: %w", err)
	}
	return newTraced(&TranslationWebAPI{client: client}), nil
}

// Translate 保留原取消预检查、结果映射和可解包的错误链。
func (t *TranslationWebAPI) Translate(ctx context.Context, translation entity.Translation) (entity.Translation, error) {
	if err := ctx.Err(); err != nil {
		return entity.Translation{}, fmt.Errorf("TranslationWebAPI - Translate - ctx: %w", err)
	}
	request := translateRequest{Text: translation.Original, Source: translation.Source, Destination: translation.Destination}
	response := new(translateResponse)
	if err := t.client.Call("translate", request, response); err != nil {
		return entity.Translation{}, fmt.Errorf("TranslationWebAPI - Translate - trans.Translate: %w", err)
	}
	translation.Translation = response.Text
	return translation, nil
}
