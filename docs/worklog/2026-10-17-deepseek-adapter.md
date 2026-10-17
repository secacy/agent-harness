# DeepSeek 模型适配器

- 日期：2026-10-17
- 状态：响应转换、HTTP 客户端与本地循环联调已通过
- 对应里程碑：[M1](../MILESTONES.md)

## 职责与调用流程

`internal/deepseek.Client` 实现 `agent.Model`。`protocol.go` 定义 API 数据结构和双向转换，`client.go` 负责配置与单次 HTTP 请求，循环继续管理工具执行、预算和退出。

```text
Loop → Client.Complete
     → encodeMessages / encodeTools
     → POST /chat/completions
     → decodeReply
     → agent.Reply
     → Loop 执行工具并追加结果，发起下一轮
```

客户端由宿主显式配置，不自行读取环境变量：

```go
model, err := deepseek.New(deepseek.Config{
    APIKey: apiKey,
    Model: modelName,
    MaxTokens: 4096,
})
if err != nil {
    return err
}
loop := agent.Loop{
    Model: model,
    Executor: executor,
    Tools: executor.Specs(),
    Limits: agent.Limits{MaxModelCalls: 20, MaxToolCalls: 40},
}
result, err := loop.Run(ctx, initialMessages)
```

模型名必须提供，不在代码中维护模型版本白名单。默认 Endpoint 为 `https://api.deepseek.com/chat/completions`；可传入其他完整 HTTP/HTTPS 地址以便测试。默认单次输出上限 4096 Token，默认 HTTP 超时两分钟；传入的 `HTTPClient` 若已有正超时则沿用，否则补上默认值。客户端复制配置，不改动调用方的 HTTP 客户端。

## 协议与错误约定

当前采用非流式 Chat Completions，显式发送 `thinking.type=disabled`。思考模式工具对话需要保存并回传 `reasoning_content`，现有消息结构未支持该字段；后续如需启用，必须先扩展历史记录与回传逻辑。协议依据：[Chat Completions](https://api-docs.deepseek.com/api/create-chat-completion/)、[工具调用](https://api-docs.deepseek.com/guides/tool_calls/)、[思考模式](https://api-docs.deepseek.com/guides/thinking_mode/)。

响应必须包含一个 choice，消息角色必须为 assistant；`stop`、`tool_calls`、`length` 映射为现有结束原因。未知结束原因、异常 choice 数量或不支持的工具类型返回英文错误。截断回复可能包含不完整工具调用，保留已收到的文本和调用，交给循环按截断失败退出，不执行工具。

工具参数保留字符串原文，合法 JSON 与参数规则仍由执行器检查。工具描述中的 Schema 使用 JSON 对象编码。输入消息及工具定义只读，工具错误通过结果文本前缀 `Tool error: `反馈模型。

父 context 传入 HTTP 请求，发送和读取错误使用 `%w` 保留取消或超时原因。响应体最多读取 4 MiB 加一个字节，超过 4 MiB 明确失败，始终关闭响应体。非 200 状态返回 `HTTPError`，保留状态码及最多约 1 KiB 的 API 错误摘要；不回显整个响应，摘要中的配置密钥会被替换。没有应用层自动重试，HTTP 重定向也作为错误返回。

## 用量记录

`agent.Reply.Usage` 返回单次输入、输出、总 Token 及可选缓存命中/未命中计数。`agent.Result.Usage` 按模型调用顺序保存快照，长度与 `ModelCalls` 一致；空项表示未知，不能视为零成本。

若 JSON 响应已解析出 usage，但后续协议检查失败，该用量仍随错误返回，并由循环记录。网络失败、JSON 解码失败等情况可能无法取得 usage，也可能已经产生服务侧费用；不推算缺失用量。当前不计算货币成本，也不将本地模拟计数当作实际模型开销。

## 验收与限制

`go test ./...` 已通过，包括：

- 请求消息、工具定义与响应映射；空文本、多调用、参数原文及协议异常。
- 鉴权头、固定请求设置、HTTP 错误、错误摘要脱敏、超大或非法响应和重定向拒绝。
- 请求前取消、等待响应时取消、读取响应体时取消及 HTTP 超时。
- 本地模拟 API → Loop → 真实文件工具：错误参数反馈 → 修正参数 → 读取文件 → 正常结束。模型调用三次，工具尝试两次。
- 截断回复保留但不执行工具；失败退出保留已知用量，缺失用量保持未知，快照不受后续修改影响。

HTTP 测试使用 `httptest.Server`，需要本机临时监听权限，不调用真实 DeepSeek 服务；其响应和 Token 数都是测试数据。没有验证真实模型迁移、真实服务可用性或迁移成功率。

上述组件现已由 [单仓库 Runner](2026-10-17-single-repo-runner.md) 串联，并通过可控模型和本地模拟 API 验收。命令入口现默认使用 [Docker 后端](2026-10-17-docker-execution.md) 执行构建和验收；真实模型迁移实验仍未开展。
