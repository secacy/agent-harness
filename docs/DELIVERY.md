# 当前交付说明

- 更新：2026-10-25。
- 定位：可审查、可复验的工程开发版，包含有限真实评测及其后续修正。
- 当前实现：包含 GHCLI 响应契约、公共命令取证、只读基线、可选测试结构诊断与末轮交接，以及显式 Translation v2 有限范围证明；已通过各自声明范围的机制/交付校准。真实效果边界见下文及收益盘点，原评测归档保留冻结实现和原结果。
- 首次阅读：[项目首页](../README.md) → [离线演示](DEMO.md) → [四任务实验报告](experiments/2026-10-22-rpcx-fixed-real-model-01.md)。

## 当前能够交付什么

Harness 已将固定输入、基线检查、Agent 修改/修复、独立门禁、补丁与审核材料串成单机批量流程。每个 Target 的工作区和记录独立，任务/命令并发有界，模型调用前同时取得批次与 Target 预算。候选质量、执行状态和离线效果分别记录。

通用机制位于 `internal/`，rpcx 的可信 Profile、业务验证与独立评测位于 `scenarios/rpcx/`。现有开发输入包含 Goddd、Translation、Workouts 和 GHCLI；新增仓库需要证明可信配置及检查的适用性，不能仅改目录。

| 能力 | 交付与验收边界 | 依据 |
| --- | --- | --- |
| 单仓库 Agent 闭环 | 通过受限工具探索、修改、构建/测试反馈及预算内提交；真实模型完成过受控迁移 | [M1 实验 03](experiments/2026-10-18-rpcx-real-model-03.md) |
| 独立质量门禁 | 类型身份、超时语义、测试保留、保护路径、行为探针及条款/证据绑定；超出支持范围记未知 | [M2/L1](experiments/2026-10-18-rpcx-l1-01.md)、[GHCLI 分类修正](worklog/2026-10-22-ghcli-proof-classification.md) |
| 批次与预算 | 隔离 Target/Run、双层并发限制、调用前预留、未知费用保留、故障局部化及汇总 | [M3.1—M3.3](worklog/2026-10-19-m3-batch-runtime.md) |
| 中断与恢复 | 只读审计、写者排他、不可变检查点、继承累计预算的新 Run；恢复为受控单代完整重跑 | [M4 收尾](worklog/2026-10-21-m4-closeout-review.md)、[持久化窗口](worklog/2026-10-21-m4-persistence-windows.md) |
| Docker 资源回收 | 创建前意图、固定引擎、精确 ID 清理；未知创建阻塞；客户端连接故障有范围实测 | [资源归属](worklog/2026-10-21-m4-resource-ownership.md)、[连接故障](worklog/2026-10-21-m4-docker-connection.md) |
| 审核与效果复核 | 补丁、报告、审核导航、独立参考、实际候选复核及原始计数；归档可离线审计和精确重算 | [四任务报告](experiments/2026-10-22-rpcx-fixed-real-model-01.md)、[交付收据](delivery/fixed-evaluation-v0.1.0.json) |

`Completed` 表示执行交付完成；`ReadyForReview` 表示必需质量证据充分；`NeedsAttention` 表示仍需确认；`Rejected` 表示已有违规证据。退出 0、测试通过或记录审计 pass 都不能单独代表迁移正确。当前 CLI 提供单任务、batch 和 audit，恢复由 Go API 提供。

## 真实实验与事后修正

此前两次 Translation 完整迁移观察分别为[诊断后未提交](experiments/2026-10-24-translation-diagnostics-real-model-01.md)和[第 31 次提前交付](experiments/2026-10-24-translation-handoff-real-model-01.md)，原有效自动成功均为 0/1。两次观察中诊断没有遇到原测试违规，末轮策略没有触发，未证明改动的真实修复/提交收益。随后[Translation v2](worklog/2026-10-24-translation-scope-proof.md)使最近同字节候选在新开发校准中 ReadyForReview；原自述计数错误及原实验指标保持。这些工作改善了局部控制与证明能力，稳定成功率、审核耗时和接入成本仍未量化。

[2026-10-25 定点修复真实实验](experiments/2026-10-25-diagnostic-repair-real-model-01.md)已按一次授权结束：A 无诊断恢复目标断言并提交，11 次生成 / 24 次工具 / 76,183 Token；独立验证超时、整轮 Aborted，B 未生成，诊断差异不可计算。收尾策略因工具额度耗尽实际触发，但没有提交率对照；A 的说明把原有测试列为新增，交付依据复核 fail。36 个容器全部回收。该局部试验不并入完整迁移指标，也不自动追加重跑。

最新[2026-10-24 四任务开发观察](experiments/2026-10-24-rpcx-current-dev-real-model-01.md)已完成：4 Completed、3 NeedsAttention、1 Rejected；自动成功 0/4，A=0 时错误放行率无定义。实际复核支持 Goddd/GHCLI；Translation 原断言削弱、Workouts 直接 RPC 无期限，完整结果分别为 test/migration fail。107 次生成、201 次工具、1,642,276 Token；原记录与冻结实现保留，131 个容器确认回收。本轮全部已见开发输入，不能据此推断泛化或单一修复收益。以下保留旧轮次。

2026-10-22 四任务固定样本采用冻结协议，每任务一次尝试，四个 Run 均 Completed：

| Target | 原门禁 | 独立自动参考 | 实际助手复核后的完整结果 |
| --- | --- | --- | --- |
| Goddd | NeedsAttention | pass | inconclusive：额外 mock 分支改变行为，依据不足 |
| Translation | NeedsAttention | pass，两配置 | pass：不回写原门禁 |
| Workouts | ReadyForReview | pass，含 common 和实际到期 | pass：本轮唯一有效自动成功 |
| GHCLI | Rejected | pass，含实际到期 | fail，类别 delivery：原拒绝依据误判，不是确认迁移行为错误 |

有效自动成功 **1/4**，错误放行 **0/1**，需确认 **2/4**；120 次生成准入、196 次工具、1,885,967 已知 Token，未知/在途占用均为 0，批次墙钟 679.186 秒。三项已见开发样本与一项隔离准备后已校准样本分层报告，四个固定样本不能推断生产概率；账单金额和实际人类活动耗时未知。

随后完成 [GHCLI 证据分类修正](worklog/2026-10-22-ghcli-proof-classification.md)：保存候选在新门禁下为 NeedsAttention，直接 Background 父参数变体仍 Rejected；原可证明写法仍通过。该工作没有新增模型调用，没有修改原候选、原判定、指标或归档。随后实现有限函数内来源与证据链，保存候选的请求别名及限定 nil 分支已可证明；后续有限 Body 资源与完整边界证明已接入，保存候选在新门禁下为 ReadyForReview；未知 helper 与额外业务修改仍需证据，不能直接放行。

随后完成[新证明的 Harness 交付验收](worklog/2026-10-23-ghcli-harness-delivery.md)：脚本模型经真实工具、预算、Docker 和 Runner 交付两个 Run，分别保留 ReadyForReview / NeedsAttention；补丁重放、审核导航和完整 Campaign 审计通过，证明文件副本的单独篡改被 API/CLI 审计定位。该结果补足工程集成证据，不增加自主迁移样本或改写原指标。

采用新能力契约的[GHCLI 一次开发评测 v0.2.0](experiments/2026-10-23-ghcli-dev-real-model-02.md)随后完成：Completed / NeedsAttention，自动参考 pass，实际助手复核发现额外清空响应的返回值差异并记 fail。该轮 N=1、自动成功 0/1、A=0 时错误放行率无定义；17 次生成、25 次工具、140,909 Token。材料审计、补丁重放与 27 个容器回收核对通过，原四任务 1/4 不改，两个轮次不合并。

## 版本与复验材料

| 材料 | 用途与版本边界 |
| --- | --- |
| 当前 Git 源码 | 已包含近期生成机制及 Translation v2；源码快速检查见[首页](../README.md#从哪里开始)；不是旧评测使用的实现身份 |
| 四任务固定评测包 v0.1.0 | 完整协议、实际源码/二进制、四候选、预算、日志、参考、助手复核和汇总；保留修正前 GHCLI Rejected；[收据](delivery/fixed-evaluation-v0.1.0.json)记录摘要 |
| 工程包 v0.1.1 | 此前工程实现与选定三仓运行的离线审计/重算材料；[收据](delivery/engineering-v0.1.1.json)及[说明](worklog/2026-10-21-engineering-delivery.md)保留原版本用途 |
| M3 阶段索引 | 当时三仓补丁/报告/复核的导航，依赖原 runs，不是完整可移植包；见[M3 收尾](worklog/2026-10-20-m3-closeout-review.md) |

归档和运行产物不随 Git 提供，需要另行交付。四任务包约 721 MB，所附执行复验二进制为 darwin/arm64；Python 3.11+ 可在包外新目录执行复验，无需 Go、Docker 或模型密钥。操作顺序、预期计数及 Workouts/GHCLI 的证据阅读路径见[演示指南](DEMO.md)。完整性检查、运行记录审计、指标重算与重新执行候选是不同范围，不互相替代。

## 历史结果与未完成项

旧[三仓真实实验](experiments/2026-10-20-rpcx-l2-real-model-01.md)仍是 3 Completed / NeedsAttention，自动成功 0/3，A=0 时错误放行率无定义。Goddd、Translation 的实际人员复核和 Workouts 的受托助手复核另有记录，均保留原交付依据问题。六处审核链接缺陷已经修复，后续规则开发校准也另存，不能改写原实验结论。

M0—M2 已在声明边界验收；M3/M4 已有范围内工程交付，原总体验收仍有缺口；M5 完成一次有限固定样本评测。具体逐项结论以[里程碑](MILESTONES.md)为准。

| 剩余事项 | 当前处置 |
| --- | --- |
| 原人员口径及严格未见留出 | 原三仓协议的实际人员为 2/3，受托助手复核不补作第三份人员签署；四任务也采用助手复核。隔离准备后参与校准的 GHCLI 不充当未见留出，原总体验收条件保留 |
| 严格留出正式协议 | [原协议草案](experiments/m5-evaluation-draft.md)未冻结；已完成的四任务有限协议不替代该验收 |
| 验证器支持范围 | Translation v2 已支持精确类型绑定下的直接期限、文件内私有常量、合法派生名及普通注释；共享常量/helper/派生前准备暂缓。GHCLI 支持已验收的来源/Body 与响应契约，其他形态仍保留缺口 |
| 运行范围 | 单机；付费恢复、连续多代恢复、Docker daemon 重启及更广外部环境未验收 |
| 效果与成本 | 当前小样本不证明稳定成功率、普遍错误放行概率或人工审核成本降低 |

## 后续工作

[收益盘点](worklog/2026-10-25-improvement-value-review.md)已核对近期改动、三轮实际结果和资源消耗。当前暂停默认追加完整模型观察，也停止继续扩展已见候选的证明形式。工程验收保证机制可靠，收益观察必须另有能被支持或否定的问题；两者不相互代替。

上述定点对照已经设计、实现并完成离线校准，唯一一次真实启动因验证超时中止，现按实际结果归档。已知本例原文对照足以恢复断言，诊断增量仍无比较证据。后续不直接增加时限或再跑一轮。2026-10-26 用户确认[离线阶段调整](worklog/2026-10-25-diagnostic-repair-comparison.md#10-生成与验证分阶段的离线调整)：先发布两组生成结果，再分别使用原完整验证；离线机制及竞态检查已完成，真实模型入口关闭，新版 Docker 完整校准未执行。该变化不改旧实验或现有交付包，也不证明诊断收益；完成后仍可结束优化，交付现有工程版与明确局限。

严格未见样本、原人员口径和可量化审核收益继续独立登记。原指标、报告及交付归档保持，已完成的公共取证和历史回顾见原工作记录；不重复打包或增加验收轮数表示进展。
