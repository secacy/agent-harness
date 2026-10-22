# GHCLI 固定切片的运行时门禁

本包将 `rpcx-l2-ghcli-v1` 发布输入和 v0.6.0 规范接入现有 Harness。实现与校准状态见[工作记录](../../../docs/worklog/2026-10-21-isolated-application-integration.md#9-第二实现单元单仓库-harness-与独立参考闭环)，不能以接口已注册推定完整验收。

宿主入口为 `runner.ProfileGHCLI`、`runner.TargetFromGHCLI(id, fixtures, spec)`；执行仍走 Campaign、原 Runner、Agent Loop、预算账本与 Executor。新 Profile 只允许 Docker，生成前实际检查基线；业务输入与 SDK 只读副本、依赖缓存和验证材料各有独立身份与可见范围。

门禁提供 `Evaluate(ctx, checks.EvaluationRequest)`、`PrepareBackend`、`HostMaterialsSHA256`。它独立检查依赖、保护范围、构建及原测试、测试保留、类型身份与有限迁移结构，以及行为和到期证据，再由现有 `quality.Decide` 聚合；不调用离线参考或读取控制答案。超出证明范围的表达保留 unknown/NeedsAttention，不能按模型自述放行。

父上下文按对象身份和语句顺序追踪：直接请求 Context()、直线局部别名以及已证明非 nil 的有限 Background 兜底可证明保留；直接或经直线别名的 Background/TODO/WithoutCancel 明确脱离取消链。重赋值、取址、未知 helper、闭包等保留 unknown，不能沿用失效来源；同名业务方法不冒充标准库。未知不掩盖可独立证明的错误常量超时。分类修正见[前序记录](../../../docs/worklog/2026-10-22-ghcli-proof-classification.md)，当前支持与验收见[有限证明工作记录](../../../docs/worklog/2026-10-22-ghcli-bounded-proof.md#8-第一单元函数内来源与证据链)。

隔离 worker 输出 GHCLI schema 1 信封，携带原 schema 2 类型报告和来源证明。宿主严格核对配置、输入/工具摘要、活动文件、原节点范围/摘要、定义位置、状态与原因，再保存 typed-analysis.json 与 boundary-proof.json。源码使用原字节，不按名字抹去修改；scope 尚未消费新证明做扩展归约。旧局部 defer 模式继续检查，响应 Body 转移仍未支持，所以保存候选的父来源虽已证明，完整门禁仍为 NeedsAttention。

公开规范的父 context 义务以既有 HTTP 请求为边界。门禁规则标识由宿主分配，归档的规则映射说明每项来自规范哪个段落/行号，或来自哪项宿主策略；这些标识不是规范原文自带编号。审核材料引用映射与实际验证证据，区分迁移要求和宿主支持范围。

依赖使用固定发布的清单、索引与五个分片，装配和审计逐件认证；不提高普通业务快照、工具树或审计的全局读取上界。生成工具只看自己的业务、SDK 与纯规范，独立门禁和参考在生成结束后使用各自副本。

离线参考位于 [l2/evaluation](../l2/evaluation/README.md)，当前活跃入口为 `EvaluateGHCLI`，数据版本为 `rpcx-l2-reference-ghcli-data-v2`。原 v1 参考额外要求命令入口上下文传播，作为诊断保留；v2 对齐已发布规范。参考报告不修改原 Run 判定，未填写的复核仍为 inconclusive。

该固定切片已接入四任务 schema 2 有限开发评测，见[真实实验](../../../docs/experiments/2026-10-22-rpcx-fixed-real-model-01.md)。本次来源追踪只重验已保存候选和构造变体，不新增模型效果或严格留出证据。
