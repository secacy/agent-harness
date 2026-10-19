# 模型调用预算

本包为单进程中的多个 Target 提供共享额度。`Create` 独占创建一个新的账本目录，`NewModel` 将一个已注册 Target、唯一 Run 和模型配置绑定。模型包装器在真实 Complete 前保存预留，调用返回后保存结算，再将回复或错误交回 Agent。

预算按请求次数和输入加输出 Token 计量。每次保守预留 `ContextTokens + MaxOutputTokens`；缓存命中输入仍属于输入，不重复叠加缓存统计。批次与 Target 都需满足：

```text
已知消耗 + 在途预留 + 未知占用 <= Token 上限
准入调用数 <= 调用次数上限
```

合法 usage 释放预留与实际值的差额。缺失 usage、HTTP 错误、断连或取消保留整额未知占用；回复协议错误但携带合法 usage 仍按实际结算。非法/超界 usage 保存原值并停止新增准入，取消其他在途模型调用；不能通过截断原值假装仍符合上界。重复结算不重复释放，冲突结算封闭准入。

暂时缺额度时按可满足请求的 FIFO 顺序等待结算，等待可取消；已知永久不足返回 `Denied`，其中 Scope/Resource 分别标记 campaign/target 与 calls/tokens。Agent 将该错误归为 limit_reached。Loop 的 ModelCalls 仍是 Complete 尝试数，可能包含被拒绝的尝试；真实准入数以账本 Totals.Calls 为准，也包含准入后无法确认是否发送的保守占用。

## 宿主接入

模型必须实现 `ProfiledModel`，返回由宿主核实、与实际请求一致的 Profile。Profile 的格式检查及摘要匹配不能证明供应商声明真实；SourceSHA256 指向宿主保留的能力材料，VerifiedAt 记录核实时间。本包不自动请求供应商，不根据模型别名猜测上界，也不接受候选提供的预算依据。

DeepSeek 的 `Config.BudgetProfile` 是显式宿主入口：校验 provider/endpoint/model/max_tokens 一致，固定 thinking disabled 和非流式，不自动重试，不跟随重定向；预算模式只接受标准 HTTP Transport。配置为空时不能被预算包装器接纳。[harness batch](../../docs/command.md#受预算约束的批次入口)在每批生成前通过同源 Models API 获取能力材料，保存原始响应并按其摘要构造 Profile；用户配置不能覆盖上界。本机模拟服务已验证机制；[单次真实能力查询与预算准入复核](../../docs/worklog/2026-10-20-l2-experiment-preparation.md#授权后实查模型能力与预算准入)已取得服务声明及原文摘要，未完成真实付费批次验收。

宿主在独立输出目录中创建账本，父目录须已存在：

```go
ledger, err := budget.Create(budget.Config{
    Directory:  filepath.Join(campaignDir, "budget"),
    CampaignID: campaignID,
    Profile:    verifiedProfile,
    Limits:     budget.Limits{MaxCalls: 80, MaxTokens: campaignTokenLimit},
    Targets: map[string]budget.Limits{
        "orders": {MaxCalls: 40, MaxTokens: targetTokenLimit},
    },
})
```

检查错误后，已有 rpcx Runner 可设置 `Config.Budget = &budget.Target{Ledger: ledger, ID: "orders"}`。Runner 在创建实际 Run 后包装模型，inputs/task.json、artifacts/report.json 和 review.md 保存账本引用及模型配置摘要。共享账本位于 Agent 工具可见目录外；不将其余额副本当作不会变化的 Run 事实。

也可在已经具有 Run ID 的宿主中直接调用 `NewModel(ledger, targetID, runID, model)`，将返回值作为 agent.Model。两种接法选择一种，不能重复包装。一个 Target 在此版本只绑定一个 Run，已有账本目录不能重新打开、重置或恢复。

## 持久化与故障

budget.json 使用 schema_version=1，包含 Profile、两级限制和汇总、逐请求身份/输入摘要/预留/原始用量、拒绝记录和单调 revision。输入摘要是消息与工具的 JSON 身份，不是 HTTP 请求字节摘要。账本不保存 API Key、完整提示或供应商错误正文。

每次写入先创建同目录临时文件，再同步文件、rename、同步目录，成功后才发布内存状态。创建账本目录后也同步父目录。单次持久化结果不确定即关闭新增准入；在途调用仍尝试结算，失败时原持久化预留继续保守占用。结算不依赖已取消的 Run context；同步文件 I/O 本身不能由 context 强制中止，不用后台遗留写操作伪装超时保证。

Snapshot 返回独立副本；Done/Err 供未来 Campaign 观察共同故障。当前没有打开旧账本、恢复、重试、跨进程共享或离线审计命令；这些属于 M4。Token 保证以供应商遵守上界、宿主客户端不绕过包装器为前提，不等于金额账单封顶。

验证命令见[预算测试](../../docs/command.md#m31-模型预算与单-run-接入)。设计和取舍见 [M3 工作记录](../../docs/worklog/2026-10-19-m3-batch-runtime.md)。
