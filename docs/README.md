# 文档约定

项目文档用于说明系统如何运行、为什么这样设计，以及哪些能力已经得到验证。文档随实际开发进度更新，不将计划或设计视为已完成的功能。

## 文档组织

| 位置 | 内容 | Git 管理 |
| --- | --- | --- |
| `README.md` | 项目定位、核心能力、架构概览与文档入口 | 是 |
| `MILESTONES.md` | 阶段目标、当前进度、验收结果与证据缺口 | 是 |
| `command.md` | 已实现且验证可用的开发、测试和运行命令 | 是 |
| `adr/` | 一个重要技术选择、选择依据及其长期影响 | 是 |
| `worklog/` | 一项工作的推进、实现与验证过程，必要时包含方案权衡 | 是 |
| `experiments/` | 实验条件、指标、原始结果与分析结论 | 是 |
| `development.md` | 与 AI 助手的开发协作约定 | 否，本地保留 |
| `personal/` | 个人技术复盘、简历及面试准备 | 否，本地保留 |

本地文档由根目录 `.gitignore` 排除。

整体开发路线、阶段交付与验收边界以[里程碑](MILESTONES.md)为准；本轮阶段顺序及预算、判定、评测边界的取舍见[路线规划记录](worklog/2026-10-18-milestone-roadmap.md)。当前推进 [M3 具体设计](worklog/2026-10-19-m3-batch-runtime.md)，预算的长期约束见 [ADR-006](adr/006-campaign-budget-reservations.md)。任务输入与宿主验证配置的边界见 [ADR-007](adr/007-host-owned-task-profiles.md)。

M3.4 已完成[样本设计与选择](worklog/2026-10-19-m3-4-sample-selection.md)及 [Goddd 离线基线与共用 L2 SDK](worklog/2026-10-19-goddd-baseline.md)。三个开发来源、当前准备状态与待独立准备的留出名额见 [L2 说明](../scenarios/rpcx/l2/README.md)；[Goddd 新规范、Profile、三类判定和 Runner 接入](worklog/2026-10-19-goddd-evidence-gate.md)已完成开发校准；[Translation 双配置离线基线](worklog/2026-10-19-translation-baseline.md)及[双配置迁移门禁/Runner](worklog/2026-10-19-translation-evidence-gate.md)已接入，[Workouts 多模块离线基线](worklog/2026-10-19-workouts-baseline.md)也已准备，[Workouts 迁移规范、门禁和确定性 Docker Runner](worklog/2026-10-19-workouts-evidence-gate.md)已接入。[三个异构 Target 的确定性 Campaign](worklog/2026-10-19-m3-4-heterogeneous-campaign.md)已通过；后续真实批次及独立自动参考已完成，结果与剩余人类复核缺口见下文。

当前的[开发评测工作](worklog/2026-10-19-l2-development-evaluation.md)记录参考独立性的取舍、三仓判据、一次尝试口径及未知结果处理；Goddd、Translation 双配置与 Workouts 回调的单候选自动参考及待复核记录已实现；[轮次离线汇总](../scenarios/rpcx/l2/round/README.md)已完成机制与既有受控三仓 Campaign 的关联验收；[预算批次入口](worklog/2026-10-20-budgeted-batch-cli.md)已通过本机模拟 API 验收；[真实能力查询及预算准入复核](worklog/2026-10-20-l2-experiment-preparation.md#授权后实查模型能力与预算准入)已完成；[首轮真实开发实验](experiments/2026-10-20-rpcx-l2-real-model-01.md)已按冻结协议完成生成、自动参考与汇总，人类复核仍待完成。

[生成前的计划与纳入绑定](worklog/2026-10-20-pre-generation-enrollment.md)记录静态 Enrollment 与动态 Manifest 的边界、Campaign 准备屏障和零任务接纳的失败语义。纳入清单固定参考身份，完整实验协议、预算与真实生成仍按 M3.4 推进。

[首轮开发实验协议 v0.1.0](experiments/l2-dev-eval-v0.1.0/README.md)已固定实际三仓配置、750 万 Token 总额度、源码/二进制/能力依据及参考与复核规则，随后获授权执行唯一真实批次；[发布工作记录](worklog/2026-10-20-l2-protocol-release.md)说明预算取舍、容量漂移拒绝和离线验收。原三项 NeedsAttention 保留，独立自动参考均 pass；本轮没有自动放行，错误放行率无定义，另暴露 6 个审核定位链接缺陷。后续[按实际模块的源码定位修复](worklog/2026-10-20-review-source-links.md)已通过呈现回归及原三仓报告只读消费，旧报告保持原样。[六项未知检查的根因与支持范围评审](worklog/2026-10-20-needs-attention-review.md)已完成；随后[Workouts 导入顺序归一](worklog/2026-10-20-workouts-import-order.md)通过正反例、冻结矩阵及三组 Docker 开发校准，原判定和正式指标保持不变。[Goddd mock/context 有限适配](worklog/2026-10-20-goddd-context-adaptation.md)已接入固定变体与可追溯的适配选择，旧校准及新正反例通过；真实候选副本的测试保留通过，但 proxying.go 的范围缺口继续 NeedsAttention。原实验记录与指标保持不变。后续[命名期限常量及构造器读取设计](worklog/2026-10-20-goddd-routing-scope.md)已比较方案并验证结构原型，生产规则未修改。

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
