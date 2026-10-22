# 当前交付说明

- 更新：2026-10-22。
- 定位：可审查、可复验的工程开发版，包含有限真实评测及其后续修正。
- 当前实现：GHCLI 分类修正已提交于 `b61eafb`；原评测归档保留冻结实现和原结果。
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

## 最新真实实验与事后修正

2026-10-22 四任务固定样本采用冻结协议，每任务一次尝试，四个 Run 均 Completed：

| Target | 原门禁 | 独立自动参考 | 实际助手复核后的完整结果 |
| --- | --- | --- | --- |
| Goddd | NeedsAttention | pass | inconclusive：额外 mock 分支改变行为，依据不足 |
| Translation | NeedsAttention | pass，两配置 | pass：不回写原门禁 |
| Workouts | ReadyForReview | pass，含 common 和实际到期 | pass：本轮唯一有效自动成功 |
| GHCLI | Rejected | pass，含实际到期 | fail，类别 delivery：原拒绝依据误判，不是确认迁移行为错误 |

有效自动成功 **1/4**，错误放行 **0/1**，需确认 **2/4**；120 次生成准入、196 次工具、1,885,967 已知 Token，未知/在途占用均为 0，批次墙钟 679.186 秒。三项已见开发样本与一项隔离准备后已校准样本分层报告，四个固定样本不能推断生产概率；账单金额和实际人类活动耗时未知。

随后完成 [GHCLI 证据分类修正](worklog/2026-10-22-ghcli-proof-classification.md)：保存候选在新门禁下为 NeedsAttention，直接 Background 父参数变体仍 Rejected；原可证明写法仍通过。该工作没有新增模型调用，没有修改原候选、原判定、指标或归档。当前仍未证明别名及返回资源/辅助函数结构，不能直接自动放行。

## 版本与复验材料

| 材料 | 用途与版本边界 |
| --- | --- |
| 当前 Git 源码 | 已包含 GHCLI 修正；源码快速检查见[首页](../README.md#从哪里开始)；不是旧评测使用的实现身份 |
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
| 验证器支持范围 | Translation 扩展暂缓；GHCLI 的别名、资源生命周期及辅助函数证明尚有缺口；额外业务改动仍需依据，不能仅凭探针通过放行 |
| 运行范围 | 单机；付费恢复、连续多代恢复、Docker daemon 重启及更广外部环境未验收 |
| 效果与成本 | 当前小样本不证明稳定成功率、普遍错误放行概率或人工审核成本降低 |

## 后续工作

项目入口与演示已收敛到当前源码、四任务原评测和事后修正三份清晰证据。无需重复打包或再复核同一批材料来推进状态。

下一项能力优化可选择 GHCLI 的有边界别名/资源释放证明：先确定支持范围和正反例，再判断是否值得扩展；Translation 暂缓。任何新效果评测均使用新实现身份和新协议，历史结果保留。严格留出及人员安排齐备后，再对照原总体验收条件推进。
