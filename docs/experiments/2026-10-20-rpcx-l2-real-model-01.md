# L2 首轮真实开发实验：三个异构仓库

- 日期：2026-10-20（Asia/Taipei）
- 状态：唯一真实批次、独立自动参考与离线汇总已完成；原判定三项 NeedsAttention，人类复核未完成
- 固定协议：[l2-dev-eval-v0.1.0](l2-dev-eval-v0.1.0/README.md)，原文不回写
- 运行版本：173f26e，源码/二进制及数据身份均按发布材料复核
- Campaign：l2-dev-eval-v0_1_0；数据为三个已见开发切片，不含留出

## 授权与启动

用户明确回复“授权按协议运行一次”，授权读取 .env 中的 DEEPSEEK_API_KEY，向 https://api.deepseek.com/models 查询一次，并向既定 chat/completions 端点发送三个 L2 开发切片、迁移规范、SDK/common 与工具交互；最多 120 次生成准入、240 次工具、750 万 Token 保守占用，每任务 10 分钟，外部 30 分钟后取消并最多 60 秒收尾，不自动重试或追加额度。

运行前核对协议 freeze、545 份源码集合、二进制、本地镜像与尚不存在的输出目录；实际 --check 通过。启动器只解析指定鉴权字段，不执行 .env，不采用环境变量更换模型，也不显示或保存 Key。启动器首次语法检查发现两处多余字符，在执行前修正；当时尚未创建启动记录、读取凭证或发送请求，不计一次生成尝试。

真实子进程在 **2026-10-20 10:16:21（Asia/Taipei）**启动。唯一一次本次能力 GET 的声明 C=1,048,576，与协议一致；原文 SHA-256 与此前观测一致。实际 Plan 与生成前 Manifest 的原文摘要匹配。没有新建第二个 Campaign 或修改运行中候选。

启动记录、控制台与原始 UTC 时间：runs/l2-dev-eval-v0.1.0-launch/。唯一实际批次：runs/l2-dev-eval-v0.1.0/。本轮只报告所纳入的三个任务，不用旧校准或其他真实 Run 填补失败。

## 实际执行与成本

唯一一次批次退出 0，耗时 414.611 秒；三个 Run 均为 Completed，原判定全部 NeedsAttention，无执行失败/取消/未启动。退出 0 只表示批次交付完整，不等于迁移正确或自动成功。原 Candidate/门禁/预算均不回写，不追加真实请求。

| Target / 原 Run | 原判定 | 模型 / 工具次数 | 已知 Token | Run 耗时 |
| --- | --- | --- | --- | --- |
| goddd / rpcx-agent-2744869177 | NeedsAttention | 37 / 65 | 616,392 | 234.422 秒 |
| translation / rpcx-agent-1269740581 | NeedsAttention | 22 / 39 | 241,906 | 388.714 秒 |
| workouts / rpcx-agent-2398064069 | NeedsAttention | 22 / 40 | 202,592 | 179.740 秒 |

共 81 次准入、144 次工具尝试；已知输入 1,043,095、输出 17,795，合计 1,060,890 Token。在途与未知保守占用均为 0，未触及两级次数/Token 限额，无账本拒绝。缓存命中 990,848、未命中 52,247，已包含在输入数内，不另加一次费用；没有实际价格或账单证据，货币成本未知。Run 时间存在重叠，不能把三项耗时相加当批次墙钟。

Goddd 轨迹包含两次可修复工具错误：一次文本替换不唯一，一次测试编译失败暴露漏改调用点；Agent 随后读取/修改并重新验证。Workouts 一次读取路径多拼了 rpcx-v2，随后纠正。Translation 无工具错误。以上都是同一 Run 内的工具反馈修复，不是重试整个任务或人工编辑候选。

## 原门禁的依据与缺口

三个候选的依赖、保护路径、构建、原测试执行和可信业务行为均通过；Translation 实际覆盖 default/rpcx_alternate，Workouts common 前置模块检查通过。原判定未被测试全绿或 Agent 的完成自述覆盖。

| Target | 门禁保留的未知 | 已有证据及解释 |
| --- | --- | --- |
| Goddd | request_scope、test_preservation | mock/mock.go 与 routing/proxying.go 有 unmodeled_production_change；调用链、构造期 ctx 移除、新常量与新增测试需要进一步复核。typed RPC 对应及期限分析为 pass，不把未知说成已发现迁移错误 |
| Translation | migration_completeness、request_scope、test_preservation | default 类型分析 pass，rpcx_alternate unknown；两个适配器及新增共享超时常量所在 translation_types.go 需要复核。原测试改了 SDK 导入及一条期限失败提示，AST 结构规则保留未知 |
| Workouts | request_scope | trainer_grpc.go 记录 unmodeled_production_change；其余检查包括 typed RPC、原测试结构和两客户端语义均通过。两个客户端各自从传入 ctx 派生期限，未改 callback/业务命令 |

这些是门禁原始 findings 的解释，不是一次已完成的人类复核或事后放宽规则。详细条款、位置和证据仍以各 Run 的 artifacts/review.md、evidence/ 下报告为准。助手已读取三个完整补丁作辅助分析，未填写任何人类通过记录。

## 独立参考与指标

三个独立自动参考均 pass，输入检查均为 InputsUnchanged=true。Goddd 8 条命令、82.381 秒；Translation 19 条命令、200.644 秒，两个配置的 selection/build/candidate_tests/behavior/expiry 全部 pass；Workouts 12 条命令、145.833 秒，包含 common 与两客户端分别实际到期。参考依序执行，没有模型请求，不把其耗时或命令数加作原 Agent 的额度。

自动参考通过不能替代完整人类源码/交付复核；本轮没有人类签署的复核。自动生成的 Review 模板原样保留，三项完整参考均 inconclusive，没有用助手辅助笔记替人签署通过。

独立汇总 CLI 实际消费原 Plan/Run/候选、固定 Manifest 和本次新参考，得到 Valid=true、ExecutionComplete=true。这两个字段表示来源绑定及运行记录完整，不表示候选或审核材料内容已全部正确；下述断链仍是明确交付缺陷。

| 指标 | 本轮结果 |
| --- | --- |
| N / R / Q / A / H | 3 / 3 / 3 / 0 / 3 |
| 有效自动成功 | 0/3；无自动放行，因此人类复核后也不会把这些原 NeedsAttention 改计为自动成功 |
| 错误放行率 | 无定义（A=0），不填 0%；确认错误放行数 0 不代表门禁总体可靠 |
| 需人工确认 | H/N=3/3，H/Q=3/3；不能直接换算审核耗时 |
| 自动参考 / 完整参考 | 3 pass / 3 inconclusive |
| 失败分布 | 原执行失败 0、质量 Rejected 0；另有同一 Run 内工具错误 3 次及 Harness 审核定位断链 6 项 |

全部已纳入任务保留，分母没有因未知或质量未放行缩小。三例都未放行意味着本轮尚不能测出一个有定义的错误放行率；不能据此解释为“错误放行风险为零”，也不按需要挑选下一次最佳结果补进本轮。

三个原补丁均在新建 Git 工作区的干净基线上成功重放，候选全树摘要分别与原 Run 一致；没有修改原补丁或候选。重放及逐任务轨迹分析见 runs/l2-dev-eval-v0.1.0/audit/。

## 已确认的交付缺陷

450 个本地审核链接中发现 6 个源码位置链接失效：Goddd 的 mock/mock.go、routing/proxying.go，Translation 的三个 webapi 文件，以及 Workouts 的 adapters/trainer_grpc.go。它们均被拼为 ../workspace/demo-orders/...，但实际模块分别是 goddd、translation、trainings。变更表的基线/候选链接和结构化报告仍可读。

原因定位在 scenarios/rpcx/runner/review.go 的 renderValidation 源码位置渲染：该分支残留硬编码 demo-orders，而 renderReview 的变更表已使用 Input.RepositoryName。首次断言式链接审计因此失败，随后改为记录实际缺陷，保存完整缺失路径；没有修改候选、门禁、原审核文件或把缺链隐藏成审计通过。此项属于 Harness 交付缺陷，不是模型迁移编译错误。

5907 份 Campaign 原材料在离线核对时保持原摘要。后续修复应让位置渲染使用实际模块名，并覆盖三种 Profile 的源码定位链接；必须在另一个工作单元验证，不能回写本轮原记录。本轮没有人工签署的 candidate_review，故完整复核仍未知；已确认的链接错误另列，不因复核待完成而省略。

后续进展：[源码定位修复](../worklog/2026-10-20-review-source-links.md)已验证新渲染可解析三仓的 6 个位置；原审核文件未覆写，本轮判定未改动。该修复不改变上述实验发生时的交付缺陷或自动成功率。

## 收尾核对、限制与下一步

最终复核 545 份源码/场景文件、已冻结协议及实际生成二进制均未变；5907 份原 Campaign 文件在独立参考与汇总后仍保持原摘要。未修改提示、门禁、规范、参考工具、候选或原审核材料，没有追加请求、重跑或合并候选。此次仅增加实验记录及进度文档，未重跑 Harness 单元/竞态回归；此前协议发布时的 37 项测试仍是前一工作单元的结果，不重复计作本轮测试。

记录到精确 ID 的 93 个基线/门禁/参考 Docker 容器均已不存在；按 Harness 标签与本批次挂载检查无残留，未删除其他任务容器。Agent go_check 仍仅保存文本反馈，没有逐命令容器 ID，所以 93 不是整个实验容器总数。9077 份本轮运行产物经 Key 字节匹配检查未检出本次凭证；不显示 Key 或将其写入检查报告。最终材料检查见 audit/final-verification.json。

计数、预算、原判定及参考身份交叉复核通过；修改文档的 114 个本地链接目标、Markdown 围栏与 git diff --check 通过。这不覆盖已明确记录的原审核材料 6 个断链；二者分开记录，不将新文档检查通过当作旧交付缺陷已修复。

本轮证实真实批次能完成三种异构任务的生成、独立判定及证据交付，且成本可归属；没有证实有效自动成功或降低审核成本。门禁对这些表达形式全部保留 NeedsAttention，加上真实交付断链，说明应先处理可审查交付与支持边界，再决定是否开启新协议。下一工作建议先修复位置链接并验证三 Profile，再对本轮 unknown 的有限支持范围做方案权衡；不直接加额度、放宽规则或重跑到放行。

M3 保持实现中：有了首轮真实开发观察，但完整人类复核、可计算的错误放行观察和独立留出仍有缺口。M4/M5 不因这一次批次完成而自动启动。协议及作者已见、模型别名、同一开发者/工具共享基础设施等限制继续成立。

后续已完成[源码定位修复](../worklog/2026-10-20-review-source-links.md)及[六项未知检查的根因评审](../worklog/2026-10-20-needs-attention-review.md)。单因素对照定位了导入顺序、未使用参数名、失败提示文字及超时前缀位置等支持缺口；尚未修改生产判定规则。原批次材料、三项 NeedsAttention、指标和未完成人类复核状态保持不变。

## 原始证据入口

以下均相对 runs/l2-dev-eval-v0.1.0/；运行产物由 Git 忽略：

- campaign/summary.md、campaign/summary.json、campaign/budget/budget.json：原逐任务终态、原判定与 81 次请求的预留/结算。
- campaign/targets/<id>/runs/<run>/artifacts/：原 agent.json、report.json、candidate.patch、review.md；evidence/ 保存实际质量检查及输入/候选副本。
- enrollment.json、round-manifest.json、round-request.json：生成前发布的静态快照、实际 Plan 绑定和原待参考请求，均未覆盖。
- references/<id>/ 下的 reference.json、review-template.json、logs/、materials/：本次实际候选的独立自动参考及未填写的复核模板。
- round-with-references-request.json、round-with-references/ 下的 summary.md、round.json：新请求及实际离线汇总，保留缺人类复核的未知状态。
- audit/analysis.json、*-replay/、original-campaign-files.json、delivery-checks.json、final-verification.json：轨迹辅助分析、重放、原材料摘要、6 项断链和最终资源/凭证核对。

启动记录与 stdout/stderr 位于 runs/l2-dev-eval-v0.1.0-launch/；驱动和日志保存在 runs/l2-dev-eval-{launch.py,reference.go,reference,audit.py,final-audit.py,summarize} 及同前缀日志。参考驱动源码/二进制和汇总器二进制摘要已记录到 final-verification.json；驱动只组装原有发布 API，不更改冻结参考实现。
