# 文档约定

项目文档用于说明系统如何运行、为什么这样设计，以及哪些能力已经得到验证。文档随实际开发进度更新，不将计划或设计视为已完成的功能。

## 文档组织

| 位置 | 内容 | Git 管理 |
| --- | --- | --- |
| `README.md` | 项目定位、核心能力、架构概览与文档入口 | 是 |
| `MILESTONES.md` | 阶段目标、当前进度、验收结果与证据缺口 | 是 |
| `DELIVERY.md` / `DEMO.md` | 当前交付版本与限制、离线演示步骤及预期结果 | 是 |
| `command.md` | 已实现且验证可用的开发、测试和运行命令 | 是 |
| `adr/` | 一个重要技术选择、选择依据及其长期影响 | 是 |
| `worklog/` | 一项工作的推进、实现与验证过程，必要时包含方案权衡 | 是 |
| `experiments/` | 实验条件、指标、原始结果与分析结论 | 是 |
| `development.md` | 与 AI 助手的开发协作约定 | 否，本地保留 |
| `personal/` | 个人技术复盘、简历及面试准备 | 否，本地保留 |

本地文档由根目录 `.gitignore` 排除。

首次阅读从[项目首页](../README.md)开始，按[离线演示](DEMO.md)复验固定四任务原批次，再由[交付说明](DELIVERY.md)区分当前源码、原评测归档和待优化项。入口与命令核对见[收敛记录](worklog/2026-10-22-delivery-entry.md)。下文各工作记录保留实施当时的结论，当前状态以交付说明和里程碑为准。

整体开发路线、阶段交付与验收边界以[里程碑](MILESTONES.md)为准；阶段顺序及预算、判定、评测边界的取舍见[路线规划记录](worklog/2026-10-18-milestone-roadmap.md)。[M3 实施与交付](worklog/2026-10-19-m3-batch-runtime.md)已有声明范围证据，总体验收仍待；与其并行推进的 M4 已交付范围内的只读审计、单代受控重跑和 Docker 精确清理。[M4 收尾核对](worklog/2026-10-21-m4-closeout-review.md)逐项保留剩余故障窗口和评测冻结缺口，不据模块通过宣布整体完成。预算的长期约束见 [ADR-006](adr/006-campaign-budget-reservations.md)，任务输入与宿主验证配置的边界见 [ADR-007](adr/007-host-owned-task-profiles.md)。

M3.4 已完成[样本设计与选择](worklog/2026-10-19-m3-4-sample-selection.md)及 [Goddd 离线基线与共用 L2 SDK](worklog/2026-10-19-goddd-baseline.md)。三个开发来源、当前准备状态与待独立准备的留出名额见 [L2 说明](../scenarios/rpcx/l2/README.md)；[Goddd 新规范、Profile、三类判定和 Runner 接入](worklog/2026-10-19-goddd-evidence-gate.md)已完成开发校准；[Translation 双配置离线基线](worklog/2026-10-19-translation-baseline.md)及[双配置迁移门禁/Runner](worklog/2026-10-19-translation-evidence-gate.md)已接入，[Workouts 多模块离线基线](worklog/2026-10-19-workouts-baseline.md)也已准备，[Workouts 迁移规范、门禁和确定性 Docker Runner](worklog/2026-10-19-workouts-evidence-gate.md)已接入。[三个异构 Target 的确定性 Campaign](worklog/2026-10-19-m3-4-heterogeneous-campaign.md)已通过；后续真实批次及独立自动参考已完成，结果与剩余人类复核缺口见下文。

当前的[开发评测工作](worklog/2026-10-19-l2-development-evaluation.md)记录参考独立性的取舍、三仓判据、一次尝试口径及未知结果处理；Goddd、Translation 双配置与 Workouts 回调的单候选自动参考及待复核记录已实现；[轮次离线汇总](../scenarios/rpcx/l2/round/README.md)已完成机制与既有受控三仓 Campaign 的关联验收；[预算批次入口](worklog/2026-10-20-budgeted-batch-cli.md)已通过本机模拟 API 验收；[真实能力查询及预算准入复核](worklog/2026-10-20-l2-experiment-preparation.md#授权后实查模型能力与预算准入)已完成；[首轮真实开发实验](experiments/2026-10-20-rpcx-l2-real-model-01.md)已按冻结协议完成生成、自动参考与汇总；后续两份实际人员记录和一份受托助手评审已分列，原人员口径缺口见 M3 收尾评审。

[生成前的计划与纳入绑定](worklog/2026-10-20-pre-generation-enrollment.md)记录静态 Enrollment 与动态 Manifest 的边界、Campaign 准备屏障和零任务接纳的失败语义。纳入清单固定参考身份，完整实验协议、预算与真实生成仍按 M3.4 推进。

[v2 评测协议契约](worklog/2026-10-21-evaluation-contract.md)在原链路上绑定计划设置、实际后端、四份材料快照及预定复核人，汇总重查生成前副本；旧开发格式保持兼容。当前仅用已见开发夹具和本机模拟服务验证，一次尝试、development 分区；不认证留出独立性或实际签署，不表示正式 M5 已启动。

[首轮开发实验协议 v0.1.0](experiments/l2-dev-eval-v0.1.0/README.md)已固定实际三仓配置、750 万 Token 总额度、源码/二进制/能力依据及参考与复核规则，随后获授权执行唯一真实批次；[发布工作记录](worklog/2026-10-20-l2-protocol-release.md)说明预算取舍、容量漂移拒绝和离线验收。原三项 NeedsAttention 保留，独立自动参考均 pass；本轮没有自动放行，错误放行率无定义，另暴露 6 个审核定位链接缺陷。后续[按实际模块的源码定位修复](worklog/2026-10-20-review-source-links.md)已通过呈现回归及原三仓报告只读消费，旧报告保持原样。[六项未知检查的根因与支持范围评审](worklog/2026-10-20-needs-attention-review.md)已完成；随后[Workouts 导入顺序归一](worklog/2026-10-20-workouts-import-order.md)通过正反例、冻结矩阵及三组 Docker 开发校准，原判定和正式指标保持不变。[Goddd mock/context 有限适配](worklog/2026-10-20-goddd-context-adaptation.md)已接入固定变体与可追溯的适配选择，旧校准及新正反例通过；该单元真实候选副本的测试保留通过，但 proxying.go 的范围缺口当时仍为 NeedsAttention。原实验记录与指标保持不变。后续[命名期限常量及构造器读取证明](worklog/2026-10-20-goddd-routing-scope.md)已接入局部节点证据与完整剩余结构比较；原样候选副本在新 Docker 校准中八项检查通过，得到 ReadyForReview。旧 NeedsAttention、原实验指标及人类复核状态保持，新增结果仅属于已见开发校准。

[Translation 剩余支持范围设计](worklog/2026-10-20-translation-boundary-support.md)已完成评审和最小原型，按最新交付优先级暂缓实施。当前以[交付说明](DELIVERY.md)关联已有能力、原实验与剩余项；[交付收敛记录](worklog/2026-10-20-delivery-focus.md)保留固定支持范围的取舍。最新 [M3 收尾验收评审](worklog/2026-10-20-m3-closeout-review.md)确认五项条件已有证据、两项部分满足；三仓受托复核已完成，原人员口径与独立留出缺口继续披露。M4.1 按已明确的并行边界实现，不冒充 M3 验收或移交；[核心模块](../internal/audit/README.md)说明当前 API、报告与覆盖边界。

[M4.2a 写者排他与只读检查](worklog/2026-10-20-m4-recovery.md#10-m42a写者排他与只读恢复检查)及 [M4.2b 恢复检查点](worklog/2026-10-20-m4-recovery.md#11-m42b恢复控制检查点)已在声明范围验收；[recovery.Prepare](../internal/recovery/README.md#恢复准备检查点)保存原文、累计预算与尝试历史，单一 head 提交后仍不激活新 Run。写者规则见 [ADR-008](adr/008-campaign-writer-ownership.md)，不可变检查点取舍见 [ADR-009](adr/009-immutable-recovery-checkpoints.md)。[M4.2c 受控完整重跑](worklog/2026-10-21-m4-controlled-rerun.md)已打通单代激活、累计预算与新 Run 交付；新尝试须有首次计划中的次数/期限授权，再次中断只审计并阻塞。旧格式写恢复、付费恢复和连续多代仍未开放。

[M4.3 资源归属与清理](worklog/2026-10-21-m4-resource-ownership.md)记录创建前意图、engine/Run 绑定、精确 ID 清理、未知创建阻塞及真实 Docker 强杀后单代重跑；长期取舍见 [ADR-010](adr/010-docker-resource-ownership.md)。审计只核对记录，实时检查与清理在原 Campaign 锁内完成。[正式评测草案](experiments/m5-evaluation-draft.md)记录一次尝试、独立参考/复核、分母和未知处理；实际留出及其他冻结材料未齐备，当前不是冻结协议或执行命令。

[M4 持久化窗口补充](worklog/2026-10-21-m4-persistence-windows.md)区分 Runner 完整恢复和预算组件强杀：基线后/候选验证前的新 Run 交付保留旧成果，预留后未调用/响应后未结算的账目保留整笔未知占用。[真实 Docker 连接中断](worklog/2026-10-21-m4-docker-connection.md)又覆盖清理前、清理后、检查点后三次断连，验证阻塞及恢复同一引擎后新 Run 交付；不停止共享 daemon，不等同 daemon 重启。正式冻结仍待，M4 总体验收保持未通过。

[M5 首轮范围与冻结条件](worklog/2026-10-21-m5-freeze-readiness.md)复用已验收接入，选定四个固定样本及助手候选复核，分开报告三个已见开发样本与一个隔离准备后校准样本。新应用 schema 2 协议准入已实现，下一步发布实际材料与预算；原严格留出条件保留，当前未冻结、未启动生成。

[四任务实际配置与材料固定](worklog/2026-10-22-fixed-evaluation-preparation.md)已形成[协议 v0.1.0](experiments/l2-fixed-eval-v0.1.0/README.md)：本地输入/参考身份、源码归档/二进制、环境、预算和实际助手复核者均登记，离线检查通过。能力采用明确日期的历史材料作期望，实际启动查询后不匹配就零生成停止；本次外发/消费待授权，原严格留出协议仍未冻结。

随后获授权的[四任务唯一真实评测](experiments/2026-10-22-rpcx-fixed-real-model-01.md)已完成：自动成功 1/4，错误放行 0/1，保留两项 NeedsAttention 与一次 GHCLI 判定依据误拒。四项自动参考和实际助手复核分列，原门禁不回写；完整材料、成本、分层与局限均另存，不把有限已见评测当严格留出验收。

[GHCLI 证据分类修正](worklog/2026-10-22-ghcli-proof-classification.md)随后区分明确丢失请求父上下文与局部证明不足；原样候选的新门禁回归为 NeedsAttention，直接 Background 变体仍 Rejected。保留旧实验结果，未扩大自动放行形式或调用模型。

[GHCLI 有限来源与资源证明](worklog/2026-10-22-ghcli-bounded-proof.md)已按三个单元推进：函数内来源/证据链、资源原型、[生产资源与完整边界闭环](worklog/2026-10-22-ghcli-bounded-proof.md#10-资源证据与完整边界的生产接入2026-10-23)。有限 Body 模式和完整剩余结构证据已接入 worker/宿主；保存候选的新门禁为 ReadyForReview。此为已见样本的开发支持扩展，历史实验、原归档及 1/4 指标保持不变。 随后[完整 Harness 交付验收](worklog/2026-10-23-ghcli-harness-delivery.md)用两个脚本化任务检验预算、补丁/审核材料和新证明的审计正反例，未新增模型实验。


[工程交付包](worklog/2026-10-21-engineering-delivery.md)说明显式材料选择、工作区源码/二进制身份、原文保留、请求路径映射和离线迁移验收；[材料清单](delivery/engineering-v0.1.1.json)不包含构建缓存或凭证。归档中的工具可重做运行审计和三个原轮次的指标汇总，不重新生成候选或执行 Docker。

[GHCLI 一次新生成协议](experiments/ghcli-dev-eval-v0.1.0/README.md)和[准备记录](worklog/2026-10-23-ghcli-generation-preparation.md)已固定当前实现与一个已见 Target。[获授权的一次启动](experiments/2026-10-23-ghcli-dev-real-model-01.md)因 Models 附加元数据改变原文摘要而在生成前停止，0 Run / 0 生成，未重试。[能力契约分离](worklog/2026-10-23-capability-contract.md)随后已实现并在模拟服务及旧轮次离线复验中通过；新策略必须显式用于另版协议，原结果保持。长期选择见 [ADR-011](adr/011-capability-contract-binding.md)。

[新版 GHCLI 协议 v0.2.0](experiments/ghcli-dev-eval-v0.2.0/README.md)和[准备记录](worklog/2026-10-23-ghcli-v02-preparation.md)固定新策略及实际材料；随后获授权的[唯一真实执行](experiments/2026-10-23-ghcli-dev-real-model-02.md)已完成：NeedsAttention、自动参考 pass、实际助手复核 fail，额外清空错误响应的分支改变返回值。没有自动放行或重试，旧指标保持。随后[响应返回值的离线回归](worklog/2026-10-23-ghcli-response-regression.md)已在 Git 中保存原样反例、基线及正负对照，默认离线运行；生产门禁与固定参考不变。

## 记录原则

**里程碑与工程记录**：`MILESTONES.md` 只跟踪已确定的开发目标及其验收状态。`worklog/` 按需记录重要的设计与开发过程，也可暂存尚未确定的技术问题；不要求每天或每次修改都写日志。

**设计权衡**：重要设计先权衡再实现。当一项工作有多个可行方案，且它们在关心的目标上各有得失时，在对应 worklog 中说明目标、备选方案、取舍和验证方式。重点关注：

1. 两个目标不能同时做到最好。
2. 同一个问题有多种解决方式，而且行为不同。
3. 选择会形成长期约束，之后修改成本较高。
4. 为解决当前问题，需要引入明显复杂度。
5. 已有证据表明原方案开始不适用了。

不必每次都写完整的方案比较。当约束已经明确、选择影响很小，或者属于既定方案中的直接实现时，简短说明即可。

**技术决策与实验**：ADR 聚焦一个重要技术选择、选择依据及其长期影响，说明接受的代价、约束和重新评估条件。长期保留的是决策依据，不代表方案永远不变；并非每次方案权衡都需要 ADR。worklog 记录一项工作如何推进、实现和验证，必要时包含方案权衡，ADR 可引用相关记录。实验记录保存具体条件、数据与结论，其他文档引用证据，不重复实验报告。

**结果与证据**：区分设计完成、模块测试通过和端到端能力验收。记录已验证的事实、尚未验证的假设及已知限制，不将模拟结果或局部测试结论扩大为整体能力证明。

**维护要求**：优先更新现有文档，避免同一事项在多处重复维护。保留重要失败记录和决策变更历史；普通代码修改由 Git 历史记录即可。

**提交节奏**：已有授权范围内，按可验收工作单元提交相关代码、测试和文档，不按文件或微小函数拆分，也不等待整个里程碑验收。历史 worklog 中“未暂存、未提交”等描述只记录当时状态，不是后续禁止提交的要求；代码提交与里程碑验收分别判断。
