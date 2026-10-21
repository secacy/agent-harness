# 当前交付说明

- 更新：2026-10-21。
- 定位：现有开发成果、证据和剩余工作的统一入口，尚非最终验收报告。
- 当前优先级：完成可审查、可复验的 Harness 工程交付；暂缓 Translation 支持范围扩展。M3 阶段交付已形成，M4 并行完成了声明范围的审计、单代恢复、精确清理及客户端连接故障验证；当前通过[工程交付包](worklog/2026-10-21-engineering-delivery.md)固定实现和选定材料，保留正式冻结缺口。M3/M4 总体验收均未通过，M5 尚未启动。优先级背景见[交付收敛记录](worklog/2026-10-20-delivery-focus.md)。

## 阶段交付判定

M3 的三仓受托复核与[阶段交付结论](worklog/2026-10-20-m3-closeout-review.md#7-m3-阶段交付结论2026-10-20)已整理完成，已有可演示的开发版；M3 总体验收及 M4 移交仍未完成。A1—A5 工程证据在声明范围充分，A6/A7 保留原人员口径与实际留出缺口，不能累计成阶段通过。

本地[阶段交付索引](../runs/m3-stage-delivery-01/README.md)统一定位三仓的原补丁/报告、参考、复核及其摘要，并列原正式与助手补充汇总。它依赖原 runs 归档，单独复制索引或仅克隆源码不能复验原实验；不是可移植的完整运行包。

新增[工程开发版交付入口](../runs/deliveries/harness-engineering-v0.1.1/README.md)及其 tar.gz：导出的工作区源码、实际构建二进制、显式选定的原始运行/复核材料、逐文件摘要和离线复核工具一并交付。已在新目录且禁止读取原工作区/联网的进程中验证运行审计与三份汇总重算；不是新增效果实验或正式评测冻结。原 M3 索引仍保留为当时记录，实际随包范围以 manifest 和[材料清单](delivery/engineering-v0.1.1.json)为准。

## 项目解决什么

公共 SDK 的不兼容升级需要同时修改多个仓库。Agent 根据版本化规范探索、修改和修复代码；Harness 管理任务隔离、并发、调用前预算、执行状态、独立门禁和补丁证据交付。

```mermaid
flowchart LR
    Input[固定 Target 与迁移规范] --> Runtime[隔离执行与预算调度]
    Runtime --> Agent[Agent 修改与迭代修复]
    Agent --> Candidate[固定候选]
    Candidate --> Gate[独立质量门禁]
    Gate --> Delivery[补丁、条款、证据与未知项]
    Delivery --> Evaluation[离线参考与实际人工复核]
```

通用运行机制位于 internal，rpcx 迁移规则及三个仓库的业务验证位于 scenarios/rpcx。Completed 表示运行交付完成，ReadyForReview 表示必需质量证据充分；NeedsAttention 是需要进一步确认的结果。

## 已有能力与证据

| 能力 | 已有交付 | 证据与范围 |
| --- | --- | --- |
| 单仓库自主迁移 | 工具调用、构建/测试反馈、预算内提交补丁 | [M1 真实实验 03](experiments/2026-10-18-rpcx-real-model-03.md)：原 Run 放行；不代表任意仓库稳定成功 |
| 独立质量判定 | 类型化调用/期限、测试保留、保护路径、业务探针、条款与证据绑定 | [M2 与 L1](experiments/2026-10-18-rpcx-l1-01.md)：19 项构造任务匹配预期；覆盖编译/测试通过但迁移错误的情况 |
| 多仓库运行控制 | 独立 Target/Run、有界任务/命令并发、两级预算预留、状态与混合汇总 | [M3.1—M3.3](worklog/2026-10-19-m3-batch-runtime.md)：竞争、局部故障、取消、持久化故障及执行容器被杀的对照；尚非 Harness 崩溃恢复 |
| 异构接入 | Goddd、Translation 双配置、Workouts 多模块与回调 | [确定性三仓批次](worklog/2026-10-19-m3-4-heterogeneous-campaign.md)：单独/并发/故障结果对照；脚本候选不计真实效果 |
| 真实批量观察 | 三个真实生成候选、原质量报告、独立自动参考和离线汇总 | [首轮 L2 实验](experiments/2026-10-20-rpcx-l2-real-model-01.md)：分列生成结果、未知、成本与未完成人工复核 |
| 审核交付 | candidate.patch、report.json、review.md、日志和输入/工具摘要 | [源码定位修复](worklog/2026-10-20-review-source-links.md)适用于新呈现；原实验材料保持原样 |
| 中断审计与受控恢复 | 原锁、不可变检查点、累计预算、新 Run 与独立历史；再次中断阻塞 | [M4.1](worklog/2026-10-20-m4-readonly-audit.md)、[M4.2c](worklog/2026-10-21-m4-controlled-rerun.md)：真实 SIGKILL 与受控模型完整重跑；旧 v1 只读，不是付费恢复或连续多代恢复 |
| Docker 资源归属与清理 | 创建前意图、固定引擎、按完整 ID 清理、实时静止检查 | [M4.3](worklog/2026-10-21-m4-resource-ownership.md)：真实宿主强杀后精确清理、新 Run 交付和邻居保留；未知创建阻塞，不能据此断言引擎故障链已实测 |

已有 Goddd、Workouts 的后续有限规则开发校准，详见[里程碑](MILESTONES.md)。它们不改变原真实实验的判定或指标；Translation 继续保留已记录的未知。

## 原真实实验应如何讲清楚

| 观察 | 原实验结果 |
| --- | --- |
| 执行交付 | 三个 Target 均 Completed，无执行失败/未启动 |
| 原质量判定 | 三项 NeedsAttention，自动放行数 A=0 |
| 独立参考 | 自动检查三项 pass；因缺实际人工复核，完整参考三项 inconclusive |
| 有效自动成功 | 0/3；后续复核也不会把原 NeedsAttention 改计自动成功 |
| 错误放行率 | 无定义；A=0 不能填 0% |
| 需人工确认 | 3/3，不等于已测得审核耗时 |
| 执行成本 | 81 次模型准入、1,060,890 已知 Token；未知/在途占用为 0；批次墙钟 414.611 秒 |

这次实验说明系统完成了三种异构任务的受预算生成、判定和交付；未证明批量自动成功、普遍错误放行概率或审核成本降低。缺口及局限是交付结论的一部分。

后续实际复核：secacy 已完成 Goddd、Translation，二者均为测试保留 pass、变更覆盖 pass、交付依据 fail。[最新离线汇总](worklog/2026-10-20-translation-human-review.md#实际人工复核与接入2026-10-20)保留两项交付类 fail；Workouts 仍 inconclusive。上表保留原实验时点的观测，原 Run/判定、自动成功率及成本不改；交付 fail 不等于业务迁移错误。

随后按用户委托完成了 [Workouts 助手评审](worklog/2026-10-20-workouts-assistant-review.md)，同样为测试保留/变更覆盖 pass、交付依据 fail。单独的 development_calibration 补充汇总纳入两份用户记录与一份助手记录，三项均为交付类 fail；原正式汇总不变，不能将补充评审称为 3/3 实际人员或独立盲审。

## 当前未完成项及处置

| 项目 | 处置与完成条件 |
| --- | --- |
| 原协议人员复核口径 | Goddd、Translation 为用户确认的实际记录；Workouts 已按委托由助手完成并交付单独开发校准，不要求用户重复填写本次评审，但不能计作原协议的第三份实际人员签署，严格口径缺口保留 |
| 独立留出 | 名额和准入标准保留，仍待独立准备；开发者已见样本不能充作未见留出，不因此宣布 M3/M5 已验收 |
| Translation 有限分析扩展 | 暂缓；保留 NeedsAttention 和现有设计，工程交付不要求所有合理写法都自动放行 |
| M4 工程与正式验收 | [持久化窗口](worklog/2026-10-21-m4-persistence-windows.md)和[真实客户端连接故障](worklog/2026-10-21-m4-docker-connection.md)已有范围证据：断连拒绝、恢复后精确清理及新 Run 交付；预算组件不冒充完整 Campaign，客户端连接故障不冒充 daemon 重启。总体验收仍缺正式冻结材料 |
| 正式评测 | 已整理[协议草案](experiments/m5-evaluation-draft.md)，未冻结；留出、对应验证支持、人员和最终实现/环境/模型/预算等齐备后发布，M5 再执行。现有 development 汇总入口不能直接充作正式入口 |

M3 收尾评审已完成，总体验收未通过：五项条件已有声明范围证据，两项仍部分满足。当前可以演示开发版及 M4 有限恢复链；M4 与 M3 剩余验收并行，不代表已完成 M3→M4 移交。正式效果评测尚需 M5，等待外部材料不会自动使阶段通过。

已知交付缺陷并非仍待修复的六个新问题：它们来自同一渲染错误，新报告的修复已有回归；旧报告和交付 fail 继续作为实验事实保留。Translation 扩展暂缓和真实批次 A=0 也不构成追加生成的理由。三仓测试保留/变更覆盖的受托复核均已完成，不再反复安排相同材料整理。

## 复核入口与复验方式

已完成运行的本地证据在 `runs/l2-dev-eval-v0.1.0/`，Git 不携带这些产物。对外复核原实验时需另外提供相应归档及摘要；只有源码克隆不能查看原日志，也不等于重新生成相同模型结果。

- `campaign/summary.{json,md}`、`campaign/budget/budget.json`：执行、质量与调用成本。
- `campaign/targets/<id>/runs/<run>/artifacts/`：补丁、报告及审核入口；三个 Run ID 见原实验表格。
- `references/<id>/reference.json`、`review-template.json`：独立自动参考及未填的三项实际人工复核。
- `round-with-references/round.json`、`summary.md`：原轮次计数、分母及未知状态。

复核应从原补丁、原规范和原材料出发；后续 Goddd/Workouts 校准只能作辅助材料。即使确认迁移正确，也须如实记录原交付断链，不能把当前修复后的呈现当作当时已正确交付。

对尚未填写的 review-template.json 另存复核记录，保留 Target/Run/候选及 reference.json 摘要绑定，填写真实身份、时间、三项结论和文件/证据定位。按照[离线参考接口](../scenarios/rpcx/l2/evaluation/README.md)与[汇总接口](../scenarios/rpcx/l2/round/README.md)消费；模板本身不代表复核通过。

Goddd 的[材料包与实际人工复核记录](worklog/2026-10-20-goddd-human-review.md)已完成接入；单仓复核闭环不等于三仓 R1 全部完成。实际记录与新汇总另存，原模板和材料包仍保留原样。

Translation 的[双配置材料与实际人工复核](worklog/2026-10-20-translation-human-review.md)已完成接入，保留原三处断链及全部原判定；该正式汇总同时消费 Goddd 和 Translation 的实际记录，Workouts 为 inconclusive。其助手评审已另存补充汇总，原材料包与模板保持原样。

没有原运行归档时，可按[命令说明](command.md)复跑冻结校准和确定性 Runner/Campaign，验证三类判定与故障边界。真实模型生成是另一项实验，需要新的协议和授权，不作为日常演示的前提。

## 接下来做什么

1. [工程包](worklog/2026-10-21-engineering-delivery.md)已固定完整实现/二进制身份及选定归档，可先离线复核当前开发版；暂不扩展 daemon 重启、多代恢复或业务场景。
2. 保留原协议人员口径与实际留出缺口，不要求用户重复填写已委托评审。实际留出、复核安排及最终发布身份齐备后，整体冻结正式协议；缺项时保持草案。
3. M4 缺口关闭并满足冻结条件后记录总体验收，再按新协议和授权执行 M5。当前工程证据可独立交付，旧实验与后续开发校准分别保留。
