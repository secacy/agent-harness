# rpcx 单候选质量门禁

`Evaluate(ctx, EvaluationRequest) (Evaluation, error)` 根据宿主提供的规范字节摘要选择检查集合，不读取 cases 预期或 oracle。v0.1.0 沿用原六项检查和 schema 2；T3 v0.2.0 使用八项必需检查和 schema 3，要求显式 execution 后端。未知规范、混用场景、输入或执行错误返回 error，Verdict 为空。

T3 输入仍为两套同级布局，各自包含 demo-orders、rpcx-v1、rpcx-v2；SpecPath 在模块外，EvidenceDir 为空且不与任何输入重叠。基线业务及两个可信 SDK 的完整文件集合由 t3-policy.json 认证。候选 SDK 被改动会使依赖检查失败；实际执行副本始终安装认证的基线侧 SDK。每树最多 10000 个普通文件、16 MiB，单文件最多 1 MiB。

| 必需检查 | 依据与边界 |
| --- | --- |
| dependencies | go.mod 的模块、版本、replace、旧导入及 SDK 不可改；DEP-001 |
| protected_paths | internal/pb 的文件集合与字节；PATH-001 |
| build | 候选生产代码实际构建；API-001、CTX-001 |
| candidate_tests | 候选测试流中十个原测试的唯一执行和终结事件；显式跳过或失败拒绝，无事件/异常退出未知 |
| test_structure | testintegrity 的原测试和 helper 结构比较；明确删除拒绝，未证明重写未知 |
| request_scope | 冻结生产基线的最小适配；未解释的调用链、声明、配置或文件变化未知 |
| migration_analysis | 类型身份、基线/候选 RPC 位置对应、超时事实及配置缺口；不独自证明调用链 |
| trusted_behavior | 同一候选生产字节上的业务、请求传播及真实到期检查；不消除结构或覆盖未知 |

先认证及执行可信基线，基线失败不属于候选质量判定。随后依赖/构建失败阻断下游执行。其他充分违规已出现时也不再运行可信行为，记录 blocked 与原因；保留已有未知。对尚无违规但存在未知的候选仍运行行为检查，保留“行为已验证，覆盖仍不足”的证据。

`request_scope` 针对冻结基线生成导入/API/context 参数及调用点的最小适配，比较完整 AST；忽略位置、格式和普通注释，语义指令另留未知。两处 inventory RPC 函数体交给类型分析加行为检查，签名、构造、其他声明和文件变化仍须解释。它不按候选的函数名消除任意变化；名称驱动的宿主适配只作用于完整摘要已认证的基线。此边界允许明确的最小迁移，也会使合理重构成为 NeedsAttention。

这项检查补足当前分析器只覆盖 RPC 封装的限制。例如增加未被现有测试触发的 handler 分支，原测试及可信行为仍可能全部通过；request_scope 仍将其标为 unknown。它不声称完成了通用调用图或任意 Go 语义等价证明。

结构、分析和行为各自绑定到同一基线/候选摘要，额外核对分析中的 SDK 输入及行为副本中的适配测试。报告为每项检查保存规范条款、原因、位置及证据引用。删除断言若定位到 adapted，只通过实际保存的 adapted-tests 材料定位，不冒充原文件行号。

最终使用 internal/quality.Decide：八项充分通过才 ReadyForReview，充分违规优先 Rejected，其余为 NeedsAttention。证据跨候选、摘要不符或执行错误不能变成质量结果。输入及验证副本在返回前再次核验，发生变化撤销 Verdict。

证据包括规范、宿主策略、输入集合摘要、验证器源码材料、两套实际执行副本、适配测试、命令日志和三个生产者的原始材料。生产者内部命令见各自 execution.json，顶层 Commands 保存门禁直接执行的命令。MaterialsSHA256 绑定本门禁及复用实现；分析、结构和行为材料另有自己的摘要。摘要用于一致性审查，可信宿主仍是前提。

本接口不创建 Run，不持久化最终 evaluation.json，也不调用模型；调用方保存 Evaluation。证据目录附带独立 go.mod，以免生成的工具源码参与项目根模块扫描。[冻结 L1 的完整独立交叉验收](../l1/README.md)已通过；旧 CLI/Runner 仍选择旧场景，新规范 Runner 与审核交付尚待实现。

测试命令见[命令说明](../../../docs/command.md#m23-t3-组合质量门禁)，方案与实际验证见 [M2 工作记录](../../../docs/worklog/2026-10-18-m2-evidence-gate.md)。
