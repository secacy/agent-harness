# Translation 诊断观察 01：主动核对后扩展测试，额度耗尽未提交

- 日期：2026-10-24（Asia/Taipei）。
- 状态：唯一生成尝试、轨迹核对、固定参考、实际助手复核及收尾完成；原 Run 失败，没有最终质量判定。
- 协议：[translation-diagnostics-dev-v0.1.0](translation-diagnostics-dev-v0.1.0/README.md)，提交 `1dcd0a0`；受测生产源码基于 `2a8c8ee`。
- 范围：一个已见 Translation 开发任务，一次原样迁移；default/rpcx_alternate 是同一候选的两个必需配置。
- 材料：[结果入口](../../runs/translation-diagnostics-dev-v0.1.0-result/README.md)、[原 Campaign](../../runs/translation-diagnostics-dev-v0.1.0/campaign/summary.md)、[原运行汇总](../../runs/translation-diagnostics-dev-v0.1.0-result/round-original/summary.md)、[轨迹分析](../../runs/translation-diagnostics-dev-v0.1.0-result/trajectory/analysis.json)。运行材料不随 Git 提供。

## 授权、身份与实际执行

用户针对本版回复“授权按本协议运行一次”。按冻结启动器执行一次 Models GET 和唯一生成尝试，读取 .env 的 Key 仅作鉴权。没有第二次生成、自动重试、额度追加或手工修补候选。协议成员不回写，授权/启动记录在结果及 launch 目录另存。

实际从 **16:42:45.736207+08:00** 至 **16:45:56.657636+08:00**，宿主墙钟 **190.922 秒**，退出 1；未触发宿主中断或强杀。Campaign Completed 表示调度收尾完成，其中唯一 Run `rpcx-agent-1249678547` 为 **Failed / limit_reached**：40 次模型调用全部用完，未产生最终提交，未进入运行时独立门禁，质量 verdict 为空。

本次模型为 deepseek-flash，C=1,048,576、O=4,096、Q=1,052,672；实际 Models 原文 SHA-256 为 `b4a502059eb2dafad6380e0a57b2debf31219ae859cba9f0e019d48ae473be2f`，查询时间为本次新值，model-contract-v1 核对通过。实际 Plan 摘要 `c11ab8bc1fcb473df06458bd856d77b365a73af188c1b168292dd2cbc327adca`，诊断专项上限保持 2，未改工具说明或强制 Agent 调用。

## 工具使用和实际轨迹

分析器重放成功 replace_text，并核对全部 **40 次** Messages+Tools 输入摘要与实际准入账本；工具确实被提供，诊断提出/执行/成功均 **1 次**、错误 **0 次**。反馈进入第 35 次及后续模型请求。没有未回答的工具调用。

Agent 在 baseline 根读取 go.mod，并搜索原测试名称；没有在该根阅读全文原测试。它在首次编辑前已从 workspace 读取完整原测试及两份生产适配器，所以不能把“没有再从 baseline 读取测试全文”写成未见原测试。

| 调用 | 已核对事实 |
| --- | --- |
| 1—20 | 探索仓库、规范和 SDK；两次猜测不存在文件路径得到工具错误，之后继续定位 |
| 21—28 | 更新模块、两个实现和测试导入；默认实现显式 release，alternate 使用 defer release |
| 30—33 | 首次构建发现 alternate 缺独立期限常量；修正后双配置 build/test 通过 |
| 34 | 自主调用 migration_check，返回 preserved、零 finding；只确认原测试结构，明确没有执行测试或证明生产语义 |
| 35—36 | 收到反馈后继续读取；剩余 5 次调用时声明要加“临时探针，随后移除”，实际新增 TestProbeParentContext |
| 37—39 | 新探针先错误接收 WithValue 的两个返回值，修正编译后，又因期限判断错误在两配置失败 |
| 40 | 已收到“最后一次调用，优先最终回复”提示，仍修改探针；保留错误的 >29 秒条件，未再测试、移除探针或最终提交 |

诊断对应候选摘要 `5cdd98d36c6e1d3c47f6a71ab1504122cb524b7dd29750bdd3b9812f3ed747be`；最终部分候选为 `bd72710c0e069c22ee2b5587be8fa5961aa5afc65b60834c562f422b7442f828`。诊断后共有三次成功测试文件编辑，均来自后续请求，且没有第二次诊断。由于原结果无 finding，这些编辑不是针对已定位违规的修复；仅有时间先后，不能归因为诊断造成新增错误。

本次没有 unknown/violated→修复→preserved 的机会，不能据此评价诊断修复能力。工具在当时报告 preserved 与最终新增测试失败并不矛盾：原测试结构保留、额外测试本身正确、整个套件通过，是不同检查对象。

## 最终部分候选的独立参考与复核

生成停止后，先读取完整补丁、全部变更文件和规范，保存 initial 源码记录，再运行冻结 Translation data-v2 参考。参考在独立副本中执行 19 条 Docker 命令，两配置结果一致：

| 检查 | default / rpcx_alternate |
| --- | --- |
| 活动/忽略实现与生成 mock | pass / pass |
| 依赖、保护路径、生产构建 | pass / pass |
| 原 26 个测试/子测试实际执行 | 全部 pass / 全部 pass，无 skip |
| 候选完整测试套件 | fail / fail，仅新增 TestProbeParentContext 失败 |
| 独立请求、映射、错误/取消/期限、业务次序 | pass / pass |
| 实际约 30 秒到期 | pass / pass |

新增探针要求剩余期限不能大于 29 秒；刚开始的正确 30 秒期限通常约为 29.99 秒，会被该条件拒绝。最终参考实际报 `30s cap wrong`；源码谓词记录另存，不冒称它是一项新增生产故障注入。原 TestTranslationRoundTrip 的 [29,30] 秒条件完整保留，这次没有确认原断言削弱。

实际助手复核按原测试保留、生产变更覆盖、交付依据三项均为 pass：分别确认原声明/实际事件保留、两生产适配器在当前证据范围正确、原报告如实声明执行失败与未验收。**完整参考/复核仍为 fail**，因为候选测试套件已明确失败；三个复核项通过不抹掉该失败。复核者 codex:/root 参与开发、旧候选复核、准备和协调，知道原运行状态及轨迹，不称独立人类、盲审或严格未见。

第一次离线汇总请求误把部分候选参考附到没有原质量 verdict 的 Run，消费端按既有契约拒绝，失败材料保存在 round-final。随后使用新目录 round-original，只汇总原运行事实；部分候选的参考/复核由现有 Assess 单独关联为 fail。没有放宽汇总器、修改原报告或补造候选质量。原汇总中的 reference inconclusive 表示没有可消费的正式候选参考，旁路部分候选诊断失败另列。

## 原指标、成本与保全

固定 N=1、R=1、Q=0、A=0、H=0、S=0。原有效自动成功 **0/1**；错误放行率无定义。NeedsAttention=0 是没有交付候选，不能解释成没有审核缺口。执行失败 1，原质量 Rejected/NeedsAttention 均为 0；事后部分候选参考 fail 不改这些计数，也不晋升为自动成功。

| 项目 | 实际记录 |
| --- | --- |
| 生成 / 工具 | 40 / 63；其中 migration_check 1 |
| 已知输入 / 输出 Token | 542,122 / 5,423 |
| 已知总 Token | 547,545 |
| 未知 / 在途占用 / 预算包装器拒绝 | 0 / 0 / 0；停止来自 Loop 40 次调用上限 |
| Run / 宿主墙钟 | 190.301 / 190.922 秒 |
| 独立参考墙钟 | 180.097 秒，在生成批次外 |
| 参考记录内 19 条命令耗时合计 | 179.736 秒；生成侧检查及诊断无完整逐项耗时，不推算增量成本 |
| 精确诊断耗时、增量成本、金额、人类活动时间 | 无现成记录，未测量/未知 |

归档审计 consistency=pass、execution_state=terminal_with_failures、resource_state=recorded_removed，无 violation/gap；它认证记录自洽，不判迁移正确。补丁在新基线无模糊/偏移重放到相同完整摘要，15 个审核链接有效。35 个生成/参考容器精确 ID 在固定 context/engine 上均不存在，本 Campaign 精确 registry 标签也无遗留。

原批次 1,449 份非缓存文件、815 份当前/导出源码、两份固定二进制和 15 份协议成员保持不变。最终原运行汇总 valid=true、execution_complete=true、inputs_unchanged=true。所有参考、实际复核、分析与失败汇总另存；核对见[verification.json](../../runs/translation-diagnostics-dev-v0.1.0-result/verification.json)。没有新增产品 Go 改动或将本地机制测试计入效果分母。

## 结论与下一步

这次回答了“真实 Agent 会不会使用”：它主动调用一次诊断，并在后续请求中收到结果；没有回答“反馈能否帮助修复”，因为调用时原测试已 preserved。结果暴露另一项具体问题：本地检查通过后仍在有限剩余额度内扩展临时测试，修复不成且未提交，最后一次预算提示也未转化为提交行为。

建议下一步先设计**收尾阶段的范围控制与提交保障**，比较加强阶段提示、为最终回复保留调用、限制最后阶段写入等不同做法的收益与损失。重点是保留可审查成果并如实暴露未解决问题；不根据本次结果自动新增规则、自动采纳历史快照或追加生成。正式方案仍需确认后实现。

当前证据不足以立即扩大生产期限诊断、提高预算或宣称现有工具降低了错误率。旧 Translation/四任务结果与本轮分别保留，单个已见开发样本不能证明因果改善或泛化；原 M3—M5 的严格留出、人员口径和总体验收缺口保持。
