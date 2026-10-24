# 生成阶段诊断反馈：测试结构首版设计

- 日期：2026-10-24。
- 状态：设计已获确认，已在本单元声明范围实现并验收；没有真实模型效果实验。
- 代码基线：`8ac2c65`。
- 前置能力：[基线对照与提交前自检](2026-10-24-generation-baseline-review.md)。
- 推进记录：先提交方案供用户确认；用户回复“可以，进入下一步”后开始实现。设计、代码、测试和实际结果合成一个工作提交。

## 1. 问题、目标与首版范围

[四任务观察](../experiments/2026-10-24-rpcx-current-dev-real-model-01.md)中，Translation 削弱原断言但构建和测试通过，Workouts 的方法期限错误也未由原测试发现。现在 Agent 已能读取只读基线，但还没有机器生成的规范诊断反馈。

目标是在**同一次 Agent Loop 内**支持“请求检查当前候选→获得条款及位置→自主修改→再次检查”，沿用原调用、工具和时间预算。模型正常提交后，仍由原独立门禁决定最终质量。

首版只反馈 **Translation / Workouts 的原测试结构完整性**，包括所有标签下的测试文件。复用现有宿主 API 适配与 testintegrity 比较规则，不执行候选、不做超时探针或类型加载。Goddd 有多套适配选择，GHCLI 原测试全受保护，本步暂不接入；这不是所有 Profile 通用可用的工具。

这是一个有限的反馈能力，不会同时修复两类真实错误：Translation 的条件变化可被定位，但仍只能标 unknown；Workouts 当前测试结构正确，因此该检查不会发现方法期限错误。明确接受这个覆盖缺口，先验收反馈链路，再决定是否投入生产边界诊断。不能用本步机制测试声称自动成功率提高。

## 2. 为什么选择这个首版

关注顺序：诊断真实可核对、保留 unknown 含义、候选与记录绑定可靠、同一预算内可修正，然后才是覆盖面和吞吐。

| 方案 | 当前样本上的作用 | 选择理由与代价 |
| --- | --- | --- |
| 继续增加提示 | 可强调原断言和逐调用期限 | 已增加基线对照提示；没有新机器观察，本步不继续加长 |
| **按需测试结构诊断** | 已有比较器能定位原声明变化、测试删除、直接跳过或纯断言删除 | 不运行代码、不引入第二套判定；可先验证未知反馈是否有用。选择，但只覆盖测试保留 |
| 同时接入类型/超时分析 | 可发现已支持形态的错误时长，报告其他边界缺口 | analysis 对缺少直接 WithTimeout 往往只能报 unknown；不能直接保证诊断 Workouts 的真实违规。还需隔离加载、配置、执行资源及更多费用，待首版后权衡 |
| 将完整 Evaluate 作为工具 | 可返回已有行为失败和更多检查 | 每次重复完整基线、可信副本、双配置及真实到期检查；容易把内部材料/结论整体暴露，反馈开销可能挤占十分钟 Run。本步不采用 |
| 提交后自动启动修复轮次 | 模型无需主动选择检查 | 需要保存各轮候选/验证、累计预算和恢复边界，不能再次调用 Loop.Run 就重置计数。本步不采用 |

若有限检查只能给出笼统 unknown、缺少可定位声明，本单元不验收“可操作诊断”；先补位置/公开原文对照，不把 unknown 升成 fail。若反馈已准确定位但模型仍反复改错，应再考虑生成策略或更强检查。是否有效需要新实验，不能靠挑选已见候选决定。

## 3. 调用与职责

拟新增工具 `migration_check`，参数严格为 `{}`，表示当前宿主已启用的测试结构检查。模型不能指定输入目录、Profile、检查规则、材料或输出位置。不把它加进通用 repotools；由 rpcx Runner 的小型工具组合器分派，其他工具继续交给现有 Executor，internal/agent 保持不变。

首版使用宿主显式启用选项，默认关闭；只允许上述两个 Profile，其他 Profile 启用时在创建 Run 前拒绝。单 Run、Campaign 的可信宿主适配和计划身份需传递同一选项，不增加一个可绕过实际计划绑定的隐式默认。CLI 若涉及配置字段，应同步严格解析和准入；冻结旧协议继续使用旧配置。本次不设计动态插件或通用规则 DSL。

建议首版每 Run 最多 **2 次 migration_check 尝试**，作为开发起点：一次修改前诊断、一次修改后核对。模型可以不用，也可带未解决项提交。每次尝试均占原 MaxToolCalls；无效参数或专项次数耗尽也占工具尝试，不进入检查。专项额度不追加 API/Token 额度，不自动补一次模型请求。启用状态及该上限进入实际任务/计划身份，工具描述明示剩余约束；参数与字段的最终 Go 命名在实现时确定。

Translation/Workouts 当前的测试适配与 CompareAdapted 调用位于各自 Evaluate 内。实现时提取**同一宿主测试比较函数**供诊断与最终门禁调用，保留基线认证、允许导入和 Plan 身份；不得复制一份宽松策略，不读取 cases/manifest/oracle。改动会改变两场景工具材料摘要，必须核对历史消费兼容和原校准；旧冻结输入与报告不改。

## 4. 返回值与模型能得到什么

使用原比较器的 `preserved / violated / unknown`，避免把它们改名成 ReadyForReview。每项包含原原因码、TEST-001、基线/候选相对路径、物理行号及声明名称；需要 API 适配位置时明确树身份。重复项不丢失，完整结果留档。

例如 Translation 条件重写只能反馈：

```text
test_structure: unknown
TEST-001 / original_declaration_changed
Original declaration changed beyond the trusted API adaptation.
Compare TestTranslationRoundTrip at the reported baseline and workspace locations.
This result does not establish whether the changed assertion is weaker or equivalent.
```

不能生成“已证实 29 秒变 0 秒”的新规则或虚构运行观察。Agent 可通过 baseline/workspace 查看实际原文并决定修正。Workouts 测试结构 preserved 时，说明范围仅限原测试结构，生产 deadline、测试真实执行、业务行为均未检查。比较器发现明确违规仍返回其原 violated，不把所有变化都当成错误。

检查成功执行时，即使结果 violated/unknown，`ToolResult.IsError=false`，由结构化状态表达诊断。参数错误、专项次数耗尽、候选输入超出可读取边界返回可修复工具错误；候选语法不受支持按比较器原 unknown 规则处理。可信基线/规则漂移、输入读前后不一致、证据发布失败属于宿主错误，返回 Go error 终止本 Run，保留诊断，不能让模型靠重试绕过。

回复不回传可信程序、探针源码、固定参考、expected verdict 或完整 Evaluate 内部报告，只含从公开规范与本候选计算的白名单字段。返回文字与路径视为工具数据，遵循既有提示注入边界。复用 16 KiB 反馈上限，按 finding 为单位生成合法 JSON，保留绑定和状态并标明总数/省略数；不得截断 JSON 后冒充完整报告。完整报告在宿主目录，不新增整个目录的 Agent 可见根。

## 5. 快照、预算和记录

调用期间 Loop 同步等待，不并发编辑候选。先认证本 Run 的输入及启用配置，读取完整业务候选并固定快照，然后从快照收集完整测试集合（包括未激活标签文件），执行宿主比较。复用每文件 1 MiB、10000 文件、合计 16 MiB 的读取边界；不接受候选自己提供的文件清单。

每次执行使用新序号目录，例如 `generation-diagnostics/check-0001/`：保存候选快照、候选文件清单、基线测试/允许适配测试、完整报告和本次工具返回。绑定 Run/Target、Profile、规范、完整基线/候选摘要、比较器/适配材料摘要、Go 版本、tool_call_id 和序号。基线主体复用已有不可变副本；候选非测试代码不参与比较，但完整快照使报告针对的候选可重建。

写完材料、复核基线/依赖及当前候选仍与本次读取一致后才返回结果。确认候选漂移时撤回可消费报告，保留故障诊断；失败记录不能标成有效检查。后续修改使本次结果只代表旧候选，报告与提示明确该绑定。最终候选即使摘要相同，独立门禁仍重查，不复用生成诊断替代质量证据。

不启动后台 goroutine 或重置 Loop。读取、比较前后检查原 context；静态 AST 比较本身没有可中断接口，不承诺强制硬超时，依靠输入大小上限和原 Run 时限做合作式退出。如实测静态检查仍能长时间阻塞，停止扩大首版而另评执行边界。没有容器、额外生成请求或实际到期等待。

Run 报告拟新增可选的生成诊断引用清单（旧报告缺省为空），区别于 Validation。每项绑定目录/报告及返回内容摘要；artifacts/agent.json 保留实际反馈。归档审计核对清单、原请求/工具返回、快照和材料身份，纳入审计实际读取的依赖并在收尾复核；审计不重新判定测试语义。篡改或跨 Run 引用必须被发现，不能仅凭可选字段有摘要就宣称已审计。原旧报告消费应继续通过。

## 6. 确认后的实施顺序与验收

1. 提取两个场景共用的原测试比较入口，先证明诊断与门禁消费同一规则且历史材料可读。
2. 接入宿主开关、专项次数、工具组合器、逐次快照、有限回复、报告与审计。
3. 用脚本模型经真实编辑工具验证诊断→修正→重新诊断→提交后的独立门禁；完成代码、测试及本记录后统一提交。

| 验收 | 必须观察到什么 |
| --- | --- |
| 支持与未知边界 | 必要 API 适配 preserved；删除原测试/纯断言为原 violated；条件/helper 重写仍 unknown，附准确条款和位置 |
| 已见真实候选只作回归 | Translation 原样候选反馈 unknown 并定位声明；Workouts 原样测试 preserved 且明确未检查生产期限，不伪造覆盖 |
| 可修复闭环 | 脚本模型删除一个原断言→读取 violated→恢复→第二次 preserved；最终质量仍取独立门禁，脚本测试不计自主效果 |
| 次数与失败 | 第三次、无效参数、不支持 Profile、缺少剩余工具预算；零隐式模型请求、不重置计数；模型预算耗尽也不自动补验证轮次 |
| 记录与隔离 | 两次候选快照均可重建；旧结果不改；响应始终合法、截断可见；越界根/自选规则/参考读取被拒绝 |
| 漂移及发布故障 | 输入/候选漂移或材料写失败无有效成功结果；已有诊断保留；取消不导致额外请求或后台工作 |
| 审计与兼容 | 摘要破坏、报告/返回不一致、跨 Run 引用可定位；新批次审计通过，旧归档仍可读；原两场景校准不改变分类含义 |

设计阶段仅核对现有代码、文档和接口，当时没有实现以上机制或运行这些验收。本轮不新增 ADR：当前是局部工具和反馈试点，长期选择仍需实施证据。确认后若发现只有复制整套门禁才能接入，先报告范围变化并重新权衡，不把它悄悄扩大为通用工作流。

设计阶段文档校验：3 份变更 Markdown 的 189 个本地链接目标、27 个锚点及围栏通过，git diff --check 通过，记录见 runs/generation-diagnostics-design-01/documentation.json。当时未运行 Go 测试或真实模型，文档等待设计确认后与实现一并提交。


## 7. 实际实施

首版使用 `runner.Config.DiagnosticLimit`，Campaign Target 同名字段，以及 batch Target 的 `generation_diagnostic_limit`，默认 0，只接受受支持 Profile 的 2。通用 Agent Loop 和 repotools 未修改；Runner 的小型分派器只处理 migration_check，其他工具委托原 Executor。单仓库可从 Go API 启用，现有单仓库 CLI 仅面向旧 demo 入口，因此没有添加对其无适用 Profile 的命令行开关。batch 严格解析、实际计划、Run 输入及冻结协议设置摘要均包含同一值；新增协议负例确认不能静默改变诊断设置。

Translation/Workouts 的 CompareTests 共用原 adapt、CompareAdapted 和 Plan；最终 Evaluate 同样调用这个入口。TestMaterials 保存 data.go、structure.go、比较入口、testintegrity 及 gate 源码，诊断材料进入自身报告身份。最终门禁继续采用其完整工具材料身份，不要求两种报告的材料集合相同。Profile、规范、独立参考数据及原分类规则均未更改。

每次成功检查先保存完整业务候选、基线/允许适配测试、工具和响应，再复核输入/候选及发布 report.json，最后进入 Report.Diagnostics 清单。候选读边界错误可反馈模型修复；宿主材料漂移、候选读后漂移、写入失败和取消返回 Go error。无效参数计入两次专项尝试；第三次不执行比较，但仍占原工具尝试。反馈采用 finding 前缀的二分选择，保证 16 KiB 内始终是完整 JSON，并显示省略数；没有静默截断状态或摘要。

审计核对计划设置、tool_call_id/顺序、实际 Agent 反馈、文件清单、规范/基线/候选、测试集合及工具身份；它重算反馈投影，不重跑语义比较。所有材料经原 audit.View 读取和收尾复核。最后补充了“已启用但基线失败、Agent 从未启动”的合法早退：无需虚构 agent.json，模型调用为零，真实 Campaign 审计仍通过。

## 8. 实际验证与问题修正

运行材料统一位于 `runs/generation-diagnostics-01/`，不入 Git。

| 验证 | 实际结果 | 证据 |
| --- | --- | --- |
| 最终诊断机制竞态回归 | 23 项测试及子测试通过；包含两 Profile、次数/参数、快照、读写边界、快照后漂移/取消、审计篡改、早期基线失败及已见候选消费 | mechanisms-accepted.jsonl |
| 首轮六包竞态回归 | campaign 46、audit 109、Translation 21、Workouts 35、Runner 147 项通过；共 29 项条件测试跳过。CLI 首轮因 localhost 监听权限失败，整轮退出 1，不能计作全通过 | packages-first.jsonl |
| CLI 允许本机监听的回归 | 60 项通过、1 项条件跳过，示例身份检查失败；更新可变示例后，两个受影响父测试及子测试共 14 项通过。不将两轮重复计数相加 | cli-final.jsonl、cli-affected-final.jsonl |
| 协议/轮次竞态回归 | 199 项测试及子测试通过，1 项条件跳过；包括新增 diagnostic-limit 设置漂移负例 | round-final.jsonl |
| 真实 Translation 修复交付 | 同一 Loop 的 violated→恢复原断言→preserved→第三次拒绝→正常提交；实际双配置构建/测试及独立门禁后 Completed / ReadyForReview，实际 Campaign 审计 pass | repair-accepted.jsonl；repair-accepted 下批次和 audit（首轮材料另留 repair） |
| Workouts 原正确候选交付 | 实际构建、原测试和语义门禁通过，Completed / ReadyForReview；不等于未知 helper 的证明扩展 | workouts.jsonl；workouts-delivery |
| 历史真实批次兼容 | 新读取器只读审计原四任务 Campaign，consistency=pass；原三个 NeedsAttention 与一个 Rejected 保留 | historical.jsonl、historical-audit/audit.json |
| 静态与文档检查 | 最终结果另存 vet-final.log、documentation.json；未将它们当作迁移效果数据 | 本轮收尾记录 |

首轮修复批次为 `repair/diagnostic-repair-4157647086/campaign`，Run 为 rpcx-agent-2337988810；最终修复批次为 `runs/generation-diagnostics-01/repair-accepted/diagnostic-repair-3874741159/campaign`，Run 为 rpcx-agent-3453970230。受控模型准入 4 次、工具 9 次，模型替身声明已知用量 80 Token，无在途/未知占用。这不是供应商消费：模型的编辑和恢复内容由测试宿主预先编写，未请求真实模型。它证明预算、反馈、修正、最终验证与审计能够协同，不能证明自主修复率。

两份已见原样候选只读回归保持原完整摘要：Translation 返回 unknown，并给出 TestTranslationRoundTrip 声明位置；Workouts 返回 preserved，反馈明确生产期限未检查。没有把 Translation 条件改写自动升级为已证实削弱，也没有将 Workouts 的测试结构通过解释成方法期限正确。原候选内容和原报告没有回写。

首轮新增测试曾错误读取 audit.Report.Findings，编译失败；更正为实际 Violations 接口后通过，日志 focused-first.jsonl 保留。沙箱 localhost 权限错误通过允许本机模拟服务的环境重跑；该回归没有访问真实供应商。另发现 examples 中工具身份仍为旧值，而 gate/l2/evaluation 源码在本单元未改；只更新可变开发示例的当前 ToolSHA256 及 enrollment 文件摘要，冻结实验协议不改。示例相关失败与最终通过分别保留。

完整修复批次启动后，补充了反馈裁剪的二分实现、data.go 材料依赖和早期失败审计分支，最终 mechanisms-accepted 覆盖这些收尾改动。导航收尾又发现新链接误用 textValue，把 code 标签写进 URL；改用原 link 函数并增加链接测试，保留原批次，再运行最终受控修复批次。原批次的质量/审计结果不改，两个断链不记作通过；没有重新运行真实模型。

## 9. 交付结论与后续边界

按已确认的首版范围完成：两个 Profile 可在同一 Loop 内按需获得测试结构反馈、保留每次候选并自主选择后续编辑，旧预算与最终独立判定继续生效。没有新增自动重试、提交后修复轮次或独立参考反馈。

真实自动成功率、断言保留率和审核成本是否改善仍未证明；Workouts 的生产期限错误仍超出本工具范围。下一步应先设计生产边界诊断的目标、证据与成本，或者冻结一次受限生成观察来判断现有反馈是否被使用；两者选择需先讨论确认，不自动启动或复用已消费的真实实验许可。

最终收尾：新修复批次 203 个审核链接均可解析，两个逐次诊断入口存在；审计 pass、inputs_unchanged=true，原首轮材料保持。最终链接单测通过，go vet 通过；5 份文档的 266 个本地链接、40 个锚点及围栏通过，git diff --check 通过。源码与结果摘要见 runs/generation-diagnostics-01/summary.json。
