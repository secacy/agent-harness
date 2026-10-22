# GHCLI 固定切片的运行时门禁

本包将 `rpcx-l2-ghcli-v1` 发布输入和 v0.6.0 规范接入现有 Harness。实现与校准状态见[工作记录](../../../docs/worklog/2026-10-21-isolated-application-integration.md#9-第二实现单元单仓库-harness-与独立参考闭环)，不能以接口已注册推定完整验收。

宿主入口为 `runner.ProfileGHCLI`、`runner.TargetFromGHCLI(id, fixtures, spec)`；执行仍走 Campaign、原 Runner、Agent Loop、预算账本与 Executor。新 Profile 只允许 Docker，生成前实际检查基线；业务输入与 SDK 只读副本、依赖缓存和验证材料各有独立身份与可见范围。

门禁提供 `Evaluate(ctx, checks.EvaluationRequest)`、`PrepareBackend`、`HostMaterialsSHA256`。它独立检查依赖、保护范围、构建及原测试、测试保留、类型身份与有限迁移结构，以及行为和到期证据，再由现有 `quality.Decide` 聚合；不调用离线参考或读取控制答案。超出证明范围的表达保留 unknown/NeedsAttention，不能按模型自述放行。

父上下文按对象身份和语句顺序追踪：直接请求 Context()、直线局部别名以及已证明非 nil 的有限 Background 兜底可证明保留；直接或经直线别名的 Background/TODO/WithoutCancel 明确脱离取消链。重赋值、取址、未知 helper、闭包等保留 unknown，不能沿用失效来源；同名业务方法不冒充标准库。未知不掩盖可独立证明的错误常量超时。分类修正见[前序记录](../../../docs/worklog/2026-10-22-ghcli-proof-classification.md)，当前支持与验收见[有限证明工作记录](../../../docs/worklog/2026-10-22-ghcli-bounded-proof.md#8-第一单元函数内来源与证据链)。

隔离 worker 输出 GHCLI schema 2 信封，携带原 schema 2 类型报告、来源/资源证据与完整边界比较。Body 模式只接受错误和空 Body 退出释放、成功转交原 Body 与同一 cancel，以及唯一包装器按原 Close 错误返回后释放；额外资源用途或未知 helper 保留 unknown。旧局部 defer 模式保留。

完整边界比较在 worker 内使用当前活动包的类型信息，仅消去有证明的来源、期限、包装器与 SDK 适配节点，保留请求参数、调用前业务、dispatchHTTP、原 cancelBody 和其他声明。标准库 Context 返回值的不可达 nil 分支单独证明；新增语义指令保持 unknown。宿主认证基线/候选/工具/配置、活动文件、节点范围/摘要、资源路径、编辑种类和归约摘要，再保存 typed-analysis.json 与 boundary-proof.json；其他文件修改和新增文件仍独立检查。详情见[生产闭环记录](../../../docs/worklog/2026-10-22-ghcli-bounded-proof.md#10-资源证据与完整边界的生产接入2026-10-23)。

公开规范的父 context 义务以既有 HTTP 请求为边界。门禁规则标识由宿主分配，归档的规则映射说明每项来自规范哪个段落/行号，或来自哪项宿主策略；这些标识不是规范原文自带编号。审核材料引用映射与实际验证证据，区分迁移要求和宿主支持范围。

依赖使用固定发布的清单、索引与五个分片，装配和审计逐件认证；不提高普通业务快照、工具树或审计的全局读取上界。生成工具只看自己的业务、SDK 与纯规范，独立门禁和参考在生成结束后使用各自副本。

离线参考位于 [l2/evaluation](../l2/evaluation/README.md)，当前活跃入口为 `EvaluateGHCLI`，数据版本为 `rpcx-l2-reference-ghcli-data-v2`。原 v1 参考额外要求命令入口上下文传播，作为诊断保留；v2 对齐已发布规范。参考报告不修改原 Run 判定，未填写的复核仍为 inconclusive。

该固定切片已接入四任务 schema 2 有限开发评测，见[真实实验](../../../docs/experiments/2026-10-22-rpcx-fixed-real-model-01.md)。本次来源追踪只重验已保存候选和构造变体，不新增模型效果或严格留出证据。


[ownership 研究记录](ownership/README.md)保留有限模式与动态见证的说明；实际规则和测试已迁入 worker，不维护两份实现。保存候选的新门禁结果以生产闭环记录为准，原固定实验及指标不回写。
