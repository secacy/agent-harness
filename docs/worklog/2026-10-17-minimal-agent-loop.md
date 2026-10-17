# 最小 Agent 循环：从模型回复到工具反馈

- 日期：2026-10-17
- 状态：已验收（循环机制）；后续单仓库真实闭环亦已通过一次验收
- 对应里程碑：[M1](../MILESTONES.md)
- 当前结论：已实现 `Loop.Run`，保留并沿用开发者准备的消息、工具和预算类型。本记录覆盖循环机制；后续 [仓库工具](2026-10-17-repository-tools.md) 与 [DeepSeek 适配器](2026-10-17-deepseek-adapter.md) 已通过本地联调，[实验 03](../experiments/2026-10-18-rpcx-real-model-03.md) 验证了受控场景下的一次真实模型闭环。

## 目标与推进顺序

阶段目标：从 demo-orders 的迁移前副本开始，只提供任务、迁移规范和允许使用的工具，由模型自主探索、修改和测试，最后由 Harness 调用 `Evaluate`，导出实际补丁及报告。参考补丁、cases 和 oracle 不进入模型输入或工具可访问范围。

先实现循环并用可控模型替身验证消息和退出行为，再添加真实仓库工具和 DeepSeek 适配器，随后完成第一次真实模型迁移。模型替身测试只证明循环机制，不计为自主迁移能力。完整 Runner 状态持久化、崩溃恢复和批量调度在实际闭环后继续补齐；此前的 Runner 设计保留为后续约束。

## 当前第一小步

实现文件：`internal/agent/types.go`、`loop.go` 和 `loop_test.go`。本次由开发者明确委托助手实现。

循环只依赖两个边界：

```go
type Model interface {
    Complete(ctx context.Context, messages []Message, tools []ToolSpec) (Reply, error)
}

type ToolExecutor interface {
    Execute(ctx context.Context, call ToolCall) (ToolResult, error)
}
```

已实现的类型保留以下信息：

| 类型 | 必需信息 |
| --- | --- |
| `Message` | role、文本、assistant 的完整工具调用列表、tool 消息对应的调用 ID |
| `ToolSpec` | 名称、说明、参数 JSON Schema |
| `ToolCall` | 非空 ID、工具名、参数字符串（保留模型原始输出，执行器负责解析 JSON） |
| `ToolResult` | 内容、是否为可反馈给模型的工具错误 |
| `Reply` | assistant 文本、工具调用、结束原因及可选用量；区分正常结束、工具调用与输出截断 |
| `Limits` | 正整数 `MaxModelCalls`、`MaxToolCalls` |
| `Result` | 结束原因、对话记录、已用模型调用次数和工具执行次数，以及按调用顺序保存的用量快照 |

结束原因至少区分 `completed`、`limit_reached`、`cancelled`、`deadline_exceeded` 和 `failed`。模型明确正常结束才记录 completed；这仅表示提交候选，不代表迁移通过门禁。输出截断或不一致的工具调用回复不能视为完成。

## 循环规则

1. 验证限制和初始消息，循环持有消息历史；Model 和 ToolExecutor 不得修改它们收到的输入。
2. 每次调用模型前检查父 context 和模型调用预算，追加一条宿主生成的 user 预算通知，再计入本次调用。通知说明本次运行的剩余模型与工具调用次数，模型次数包含本次请求；临近上限提示收尾。模型/API 错误直接退出并保留历史。
3. 完整追加 assistant 回复，保留文本、全部工具调用及其 ID。
4. 正常结束且无工具调用时返回 completed。无工具调用但输出截断时返回失败；其他不一致回复按协议错误退出。
5. 执行工具前检查名称是否在公布的工具集合中、调用 ID 是否非空且不重复，以及整批工具调用是否超过剩余预算。整批超出预算时退出，避免本轮执行部分写操作后才发现预算不足。
6. 首版按顺序执行工具。每次执行前检查父 context；每个工具结果追加为一条带对应调用 ID 的 tool 消息。所有结果追加后才进行下一次模型调用。
7. 路径拒绝、参数不合法、未知工具、测试未通过等可修复结果，作为 `ToolResult` 的错误内容反馈模型。进程无法启动等执行基础设施错误，以及 context 取消，作为 Go error 终止循环。非法/重复调用 ID 因无法可靠配对消息而终止循环。

工具拒绝也计入工具尝试预算，避免反复错误请求绕过限制。模型调用预算在发起下一次请求前检查，因此最后一次允许的模型回复仍然可以正常完成。没有剩余预算却尚未结束时明确返回 limit_reached。

对话记录是循环的返回证据；当前不实现独立事件存储。模型适配器后续负责保留协议需要的额外字段、映射结束原因与统计实际 usage，不能在循环中写死某个模型的 HTTP 格式。

预算通知及软提醒于 2026-10-18 加入；具体取舍、阈值和验收见[预算感知记录](2026-10-18-budget-awareness.md)。通知在完整工具批次之后追加，也随模型请求失败的历史保留。原有硬限制与提交判定继续生效。

## 第一小步的验收

使用按顺序返回预设回复的模型替身，以及记录调用顺序的工具替身，检验循环是否正确连接了模型与工具：

| 情况 | 预期 |
| --- | --- |
| 第一轮请求 read_file，第二轮正常结束 | 工具执行一次；第二次模型输入包含完整 assistant 调用和匹配 ID 的结果 |
| 同一回复有两个工具调用 | 顺序执行；两个结果均追加后才再次调用模型 |
| 工具返回可修复错误 | 错误反馈给模型，模型有机会改正请求 |
| 模型持续请求工具 | 到达调用预算后停止，模型与工具次数均不超限 |
| 最后一次允许的模型调用正常结束 | completed，不能误判为预算耗尽 |
| 已取消或执行中取消 | 父 context 传递到 Model 和 ToolExecutor，停止后续调用并保留已有记录 |
| 输出截断、重复调用 ID 或模型/API 错误 | 明确失败，不宣称完成 |

以上测试已实现并通过。测试重点是消息关联、反馈与副作用边界，不验证 rpcx 迁移正确性。

## 实际接口与退出契约

```go
loop := agent.Loop{
    Model:    model,
    Executor: executor,
    Tools:    toolSpecs,
    Limits:   agent.Limits{MaxModelCalls: 20, MaxToolCalls: 40},
}
result, err := loop.Run(ctx, initialMessages)
```

`Run` 在入口复制初始消息、工具列表及其嵌套切片，返回自己持有的对话和本次调用计数。每轮直接向 Model 传递循环持有的数据，不重复复制；Model 必须遵守输入只读约定，不得在调用结束后继续使用输入。构造模型请求所需的数据转换由适配器负责。模型返回的工具列表仍在执行前复制。初始历史支持已完成的工具轮次，要求调用与结果配对完整。计数不包含初始历史，不提供中断批次续跑。

| 返回原因 | Go error | 含义 |
| --- | --- | --- |
| `completed` | nil | 模型明确正常结束，候选可交给独立验证器 |
| `limit_reached` | nil | 达到模型调用或工具尝试预算；必须读取 Reason，不能因 err 为 nil 就视作完成 |
| `cancelled` | 非 nil，可用 errors.Is 判断 context.Canceled | 用户取消或依赖返回取消错误 |
| `deadline_exceeded` | 非 nil，可用 errors.Is 判断 context.DeadlineExceeded | 父 deadline 或依赖 deadline 耗尽 |
| `failed` | 非 nil | 输入无效、回复协议错误、输出截断、模型/API 或工具执行基础设施错误 |

父 context 的结束原因优先决定取消分类，原始依赖错误仍保留。`ErrInvalidInput`、`ErrProtocol` 和 `ErrOutputTruncated` 可用 `errors.Is` 区分。超限或错误退出的历史可能含未执行/未完成的工具批次，仅作为部分证据保留，不能直接当作完整对话续跑。

未知工具由循环拒绝且计入工具尝试预算；已公布工具的参数 JSON 和具体参数要求由 ToolExecutor 校验。输入 schema 当前只检查是合法 JSON 对象，没有实现 JSON Schema 校验器。`ToolResult.IsError` 表示可反馈给模型修正的错误；工具返回 Go error 时保留该调用的部分诊断并退出，不执行批次内后续工具。

## 代码阅读与运行示例

先看 `types.go` 的 `Message`、`ToolCall` 和 `Reply`，再看 `loop.go` 的 `Loop.Run`。后者按以下顺序推进：

1. 校验输入和预算，复制初始历史。
2. 追加剩余预算通知，在预算内调用 Model，保存完整 assistant 回复。
3. 根据 Finish 决定正常结束、协议失败或执行工具。
4. 在任何工具副作用之前验证整批调用 ID 和剩余工具预算。
5. 顺序执行，将每条结果按 ToolCallID 追加，回到下一次模型调用。

`ExampleLoop_Run` 提供可执行示例，其模型与文件读取均为替身：

```text
user:      Read client.go
user:      Harness budget: model=2（含本次），tools=1，提示收尾
assistant: read_file({"path":"client.go"}), ID=read-1
tool:      package inventory, ToolCallID=read-1
user:      Harness budget: model=1（含本次），tools=0，提示最后一次回复
assistant: Done, Finish=stop

Result: completed，模型调用 2 次，工具尝试 1 次
```

预算通知在示例中为缩写，实际内容为英文，完整保存在对话记录中。

## 验收记录

- `go test ./internal/agent`：通过，覆盖模型/工具消息关联、多调用顺序、未知工具和参数错误反馈、预算边界、截断及非法回复、重复 ID、输入校验、执行错误、取消/deadline 和数据所有权；可执行示例通过。
- 根模块 `go test ./...`：通过，包含已有单候选验证器的回归测试。
- 本阶段未执行真实模型迁移；没有新增真实 API 调用、仓库工具或模型成本成绩。

当前调用同步执行，依赖 Model/ToolExecutor 响应 context，不通过遗留后台 goroutine 伪装成取消成功。后续仓库工具已限制输出，DeepSeek 适配器已设置单次输出 Token 上限并返回服务用量，循环按调用保存快照；总 Token/成本预算、完整进程树管理与恢复仍待运行层落实。

## 随后的两个小步

**仓库工具与模型适配：** 仓库文件工具与可信夹具的本地 Go 检查已实现，见 [实现记录](2026-10-17-repository-tools.md)。路径限制不等于进程沙箱，真实模型运行前仍需落实执行边界。随后按项目选型接入 DeepSeek；具体 API 与模型参数在实现适配器时核对官方文档。

**与验证器衔接：** 模型正常结束后停止编辑，固定候选摘要并调用 `Evaluate`，保存候选补丁与验证结果。首版 Agent 可根据工具的构建/测试反馈自修复，独立门禁先在候选提交后运行；门禁拒绝后的自动再修复作为后续显式扩展。真实模型运行保留失败与成本记录，最终判定由验证器提供。
