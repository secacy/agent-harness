# GHCLI 固定切片的运行时门禁

本包将 `rpcx-l2-ghcli-v1` 发布输入和 v0.6.0 规范接入现有 Harness。实现与校准状态见[工作记录](../../../docs/worklog/2026-10-21-isolated-application-integration.md#9-第二实现单元单仓库-harness-与独立参考闭环)，不能以接口已注册推定完整验收。

宿主入口为 `runner.ProfileGHCLI`、`runner.TargetFromGHCLI(id, fixtures, spec)`；执行仍走 Campaign、原 Runner、Agent Loop、预算账本与 Executor。新 Profile 只允许 Docker，生成前实际检查基线；业务输入与 SDK 只读副本、依赖缓存和验证材料各有独立身份与可见范围。

门禁提供 `Evaluate(ctx, checks.EvaluationRequest)`、`PrepareBackend`、`HostMaterialsSHA256`。它独立检查依赖、保护范围、构建及原测试、测试保留、类型身份与有限迁移结构，以及行为和到期证据，再由现有 `quality.Decide` 聚合；不调用离线参考或读取控制答案。超出证明范围的表达保留 unknown/NeedsAttention，不能按模型自述放行。

父上下文的局部证明区分三类：直接使用 HTTP 请求参数的 `Context()` 可证明保留；类型身份认证的 `context.Background()`、`TODO()` 或 `WithoutCancel(...)` 直接作为派生父参数属于明确违规；局部别名、辅助函数等超出当前证明范围，记 `unknown / request_parent_unproven`。未知不会自动放行，也不掩盖可独立证明的错误常量超时。分类修正与保存候选回归见[工作记录](../../../docs/worklog/2026-10-22-ghcli-proof-classification.md)。

公开规范的父 context 义务以既有 HTTP 请求为边界。门禁规则标识由宿主分配，归档的规则映射说明每项来自规范哪个段落/行号，或来自哪项宿主策略；这些标识不是规范原文自带编号。审核材料引用映射与实际验证证据，区分迁移要求和宿主支持范围。

依赖使用固定发布的清单、索引与五个分片，装配和审计逐件认证；不提高普通业务快照、工具树或审计的全局读取上界。生成工具只看自己的业务、SDK 与纯规范，独立门禁和参考在生成结束后使用各自副本。

离线参考位于 [l2/evaluation](../l2/evaluation/README.md)，当前活跃入口为 `EvaluateGHCLI`，数据版本为 `rpcx-l2-reference-ghcli-data-v2`。原 v1 参考额外要求命令入口上下文传播，作为诊断保留；v2 对齐已发布规范。参考报告不修改原 Run 判定，未填写的复核仍为 inconclusive。

该固定切片只准入 schema 1 `development_calibration`。正式协议、heldout 和真实供应商实验没有因新增 Profile 自动获得准入；受控模型重放候选验证的是机制，不是自主迁移效果。
