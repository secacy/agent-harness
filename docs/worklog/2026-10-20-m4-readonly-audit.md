# M4.1：只读审计与未完成记录识别设计

- 日期：2026-10-20
- 状态：M4.1a/b 审计与 M4.1c 真实进程强杀已通过声明范围验收；M4 恢复、重试和清理待设计实现
- 代码基线：d9b15b9；当前已有 M3 交付/复核文档修改保持未提交
- 对应：[M4](../MILESTONES.md#m4运行可靠性与恢复)
- 推进边界：用户在上一轮明确“下一项为与 M3 并行的 M4 只读审计设计”后要求进入下一步，因此本步进入 M4.1 设计；不是 M3 总体验收或正式移交
- 当前结论：先检查既有记录是否自洽，再谈恢复动作；不执行候选、不调整账本、不覆盖历史，也不依据磁盘 Running 推断进程已死

## 1. 要解决的问题与本步范围

在 Harness 进程退出或被 SIGKILL 后，目录可能同时包含一个已完成 Run、未更新的 Target 索引和未结算请求。需要回答：哪些材料存在且身份一致，哪些完成声明不成立，哪些只是发布到一半，还有哪些缺证据而不能判断。审计结果随后可供恢复设计参考，本身不授予重新调用模型、释放额度或清理容器的权限。

本设计针对已有单机 Campaign v1、budget v1、rpcx Run v2 和受支持的报告结构。首版每 Target 至多一个 Run，支持当前两个 demo-orders Profile 和 Goddd/Translation/Workouts；不泛化任意业务插件或多 Run 恢复协议。单独 Run 的独立 CLI、恢复写入、重试、资源清理和 M5 冻结均不在 M4.1 范围。

输入是调用方已停止写入的本地 Campaign 目录及一个全新、互不重叠的输出目录。运行时不创建模型/Executor、不读 .env、不运行 Go/Git/Docker、不重跑质量门禁或独立参考。审计只写自己的报告目录，不在输入里创建锁、缓存、临时文件或“已审计”标记。

## 2. 从实际代码得到的约束

| 当前事实 | 实际位置 | 对设计的影响 |
| --- | --- | --- |
| Campaign 先发布 plan/campaign，再运行 Prepare，逐项发布 Pending 目录，再创建账本，随后启动 Target | [campaign/run.go](../../internal/campaign/run.go) | 初始化可能合法中断，不能要求任何非空目录都已有完整账本和全部 Target |
| Target 先落 Running，回调返回后才带 Attempt/Run 引用 | [Target 调度](../../internal/campaign/run.go)、[类型](../../internal/campaign/types.go) | 需扫描该 Target 独占 runs 子目录；不能把未索引 Run 自动视为跨 Target 或直接取 mtime 最新者 |
| Runner 创建目录后才写第一份 run.json；Phase 在步骤开始前发布 | [runner/run.go](../../scenarios/rpcx/runner/run.go) | 空 Run 目录或仅阶段记录是可解释的残片；Phase 不是阶段成功凭证 |
| 进入 Validating 后计算候选摘要，但该赋值未立即落盘；补丁在验证后的 finish 中导出 | [验证与 finish](../../scenarios/rpcx/runner/run.go) | 目前不能保证“验证前已有持久化候选 pin”；审计不能编造这个检查点，也不能从 patch 存在反推验证前发布顺序 |
| finish 先发布 report/review，再落 Run 终态；回调结束后 Target 才引用 Run；Campaign 最后发布 summary 与终态 | [runner 发布](../../scenarios/rpcx/runner/run.go)、[Campaign 收尾](../../internal/campaign/run.go) | report=Completed 而 run=Running、Run=Completed 而 Target=Running 都可能是合法发布前缀 |
| Campaign/预算写入含文件与目录 Sync；Runner writeAtomic 只有关闭、rename，没有 Sync | [campaign/storage.go](../../internal/campaign/storage.go)、[budget/store.go](../../internal/budget/store.go)、[runner/artifacts.go](../../scenarios/rpcx/runner/artifacts.go) | 首轮只验证进程 SIGKILL；不承诺整机掉电、文件系统损坏或持久化重排恢复。强化 Runner 持久化归后续实现，不能用审计补写代替 |
| 每 Target 账本绑定一个 Run；预留/结算分别落盘，invalid 用量保留 Q 的未知占用及 HaltReason | [budget/ledger.go](../../internal/budget/ledger.go)、[model.go](../../internal/budget/model.go) | 从逐 call 重算两级额度，未知或非法 usage 不能当零；缺账本不创建替代账本 |
| Docker 只有通用 agent-harness.execution 标签；名称和最终 ContainerID 不构成完整运行所有权日志 | [docker.go](../../internal/execution/docker.go) | M4.1 不靠标签清容器；中途创建但还没保存 ID 的资源不能靠审计补猜归属 |

这些是源码检查，不是本轮运行中断实验。尤其“单文件原子替换”不等于跨文件事务，“两次读取未变”也不等于证明原写者已经退出。

## 3. 关键方案与选择理由

| 问题 / 备选 | 具体得失 | 首版选择与代价 |
| --- | --- | --- |
| 扩展现有 L2 round，或单独审计 | round 依赖评测 Manifest/参考材料并用于计算效果；初始化中断可能连这些材料都没有。强塞进去会把合法残片当成评测配置错误 | 单独 audit，不要求 oracle/参考/评测 Manifest；代价是共用身份算法但不复用整个汇总流程 |
| 只认 Completed，或按实际发布顺序识别残片 | 前者容易实现，却会把报告先于终态的窗口误判为损坏 | 允许明确的发布前缀；缺少可绑定材料仍记证据不足，不用“可能中断”解释任意矛盾 |
| 先补齐记录再检查，或只读观察 | 补齐能简化后续读取，却可能伪造终态或让请求预算恢复 | 只读观察，原 Running 不改成 Cancelled，reserved 不改成 unknown。代价是不能直接拿审计结果当恢复许可 |
| PID/mtime 判死，或不推断活性 | PID 会复用、目录可迁移，mtime 不证明模型/容器已停止；当前也没有可靠写者协议 | 调用方先停写，报告 writer_state=not_checked。记录读前/读后摘要与目录成员；发现漂移记 inconclusive，不承诺捕获所有并发交错或 ABA |
| 立即引入事件日志/数据库，或读取当前快照 | 日志能保留顺序，但要同时改 Campaign、Runner、预算和恢复写者，无法先检验现有数据 | 先读取 v1/v2；不能证明历史曾经发生过什么、是否漏记一次调用或预留确实早于 HTTP。出现无法区分的恢复需求时再评估新协议 |
| Core 直接理解 rpcx 全部报告，或一个静态宿主检查点 | 前者让 internal 依赖场景；动态插件/DSL 又远超现有一个迁移领域 | core 检查 Campaign/Target/预算与通用 Run 归属，CLI 显式注入 rpcx 的只读 Run 检查函数。不做插件发现/规则 DSL，接受额外一个函数边界 |

本轮将这些取舍与验收放在 worklog；接口尚未实现验证，不为每个选择新增 ADR。若实现后审计/恢复边界形成长期协议，再记录已验证的决策。

## 4. 结果契约与只读边界

拟提供 `audit.Inspect(ctx, Request, RunInspector) (Report, error)`，放在 internal/audit；cmd/harness 组装已支持的 rpcx 检查函数，场景侧实现放在现有 runner 附近。audit 不导入 scenarios/rpcx。RunInspector 只接受受限只读 View 和已定位 RunRef，通过同一 View 读取文件/计算树摘要，纳入审计材料登记与漂移复核；宿主函数是可信代码，不声称这个 Go 接口能沙箱任意实现。

Request 仅包括 CampaignDir 和 OutputDir；两者绝对路径、双向不包含，输出必须尚不存在。core 从 plan 的合法 Target ID 构造路径，扫描固定 runs 目录。持久化路径只是待核对数据，不能成为任意宿主读取入口。

报告拟分开保存：

| 字段 | 含义 |
| --- | --- |
| consistency = pass / violated / inconclusive | 已定义的一致性规则是否全部可判定、是否有确定违规；不是候选质量 verdict |
| scan_complete、inputs_unchanged、writer_state | 必需规则是否检查完、所读内容/目录成员是否在两次检查间未变、写者活性是否检查；不能合并成“绝对可信” |
| execution_state | completed / terminal_with_failures / incomplete / unknown；按磁盘可见状态归纳，不改原状态、不宣称已确认进程死亡 |
| violations / gaps / observations | 确定的矛盾；无法完成的检查；合法未完成或已记录异常。每项有稳定 code、Target/Run、相对路径、可选 JSON 字段定位、原因和实际依据摘要 |
| sources、coverage、tool identity | 实际读取文件与目录成员身份、检查适用范围、审计版本/工具身份；未做项不能算 pass |

若既有确定违规又有缺口，consistency 仍为 violated，同时 scan_complete=false 并保留全部 gaps；没有违规但缺证据、未知版本、取消或漂移则 inconclusive。只有完整执行适用规则、来源稳定且无违规才为 pass。合法中断可以得到 pass + incomplete + observations；它仅表示当前记录符合前缀契约，不表示任务完成或可安全重试。

“确定违规”必须在最终漂移复核之后成立：每条发现绑定其依赖的文件、目录成员或缺失路径。依赖发生变化或无法复核时，该发现降为 `source_changed` / `source_unverified` gap，保留观察到的差异，不继续作为确定 violation。与漂移无关且依据已复核的违规仍可保留。例如旧 summary 与新账本不一致且账本随后再次变化，只能判证据不稳定；另一份稳定账本内部的算术错误仍可确定。`inputs_unchanged` 采用 true / false / null，null 表示取消、读取失败等导致无法完成复核，不能默认 true。

`execution_state` 只描述已核对的发布状态：完整 Completed Campaign 且所有 Target 尝试完成交付为 completed；完整终态但含 Failed/Cancelled/Skipped 或 Campaign 自身失败为 terminal_with_failures；合法的初始化/发布前缀为 incomplete；关键状态不支持、相互矛盾或无法读取为 unknown。单个 Run 已完成不能把整批提升为 completed，Rejected/NeedsAttention 也不等于执行失败。

输出使用独占新目录，先写 audit.json、audit.md 和所读材料索引，最后写发布完成标记及摘要；收尾失败 CLI 非零，不能仅凭部分 audit.json 存在认定报告发布成功。协议细节在实现时固定版本，不引入源目录修复选项。

### 路径与材料读取

- 根目录规范化一次；内部拒绝 `..`、绝对 Artifact 路径、符号链接及非常规文件，不跟随报告中的外部仓库/SDK路径、预算绝对路径、模型 endpoint 或命令 Args。输出路径检查考虑现存父目录的别名。
- 对旧记录里的 Directory/ledger_path：核对它们在“声明的原 Campaign 根”下的预期关系；实际读取一律由当前输入根与固定相对布局定位。这样允许归档迁移，不返回原机器去找缺失材料；原 Directory 字符串不被重写，也不是来源真实性证明。
- 每份 JSON 按下面的格式选择规则严格解码，检查必填字段，拒绝重复键/尾随内容；未知版本或暂不支持的 Profile 记 gap，不能用零值接着审完。
- 输入 JSON/单个被引用材料拟上限 16 MiB，单候选树沿用单文件 1 MiB、10000 文件/总计 16 MiB，单次审计拟 100000 文件/512 MiB；超过上限记无法完成，不截断后通过。忽略未引用 cache，不递归解析第三方依赖缓存或任意归档。
- 首次读取登记文件摘要及相关目录成员，结束前重读核对；新增/删改任何参与判断的记录都会使输入稳定性检查失败。调用方停写是使用前提；不抵抗恶意宿主同时替换目录，不保证全目录事务快照。

这些上限是开发起点，须用既有三仓归档确认容量，并测试边界；尚未通过性能或任意大批次验收。

### 格式选择与证据强度

当前 plan、Target、summary、rpcx report/task/profile 没有独立 schema_version，不能实现成“每个文件都必须有版本号”。campaign.json 的 schema_version=1 选择 plan/Target/summary 的既有契约；budget.json 自带 v1；run.json 的 v2 与受支持的 Profile ID 选择 rpcx report/task/profile 契约；嵌入的 Evaluation 再检查自己的 schema_version/verifier_version 组合。不为审计修改旧文件。父级版本材料缺失时，只检查能独立成立的边界及自带版本的材料，依赖父级的语义检查记 gap，不猜版本。

已支持格式中的必填字段缺失、重复键、尾随内容或类型不符属于格式违规；未知版本先报 unsupported_schema gap，不能拿旧格式的必填字段规则制造一批伪违规。同版本出现未知字段时保留 unsupported_format gap 并停止依赖该格式的判定，待显式扩展兼容表。权限错误、读取超限和未完成复核属于 gap；缺文件是违规还是合法前缀按下一节的前置关系判断。缺字段、显式 null 和合法零值必须分开处理。

本审计证明的是已读取材料之间的自洽及已配置 pin 的匹配。原文摘要若与记录一起被改写，可能仍完全自洽；没有外部可信锚点、签名或追加记录时，不能声称认证原始运行历史或检测所有篡改。测试中“修改文件但不更新引用”应被发现，“一致地重写整套自描述材料”则属于明示能力边界，不增加来源真实性结论。

## 5. 首版规则

### 5.1 Campaign、Target 与 Run

| 规则族 / 拟原因码 | 检查与分类 |
| --- | --- |
| identity_mismatch、plan_digest_mismatch | 已发布 campaign 的 ID、plan 原文字节摘要，Target ID 唯一性与清单一致；错误归属/摘要冲突为 violation |
| initialization_incomplete | 只有目录、plan 或部分 Pending/预算初始化材料，且无反证表明已经接纳生成：observation 或 gap，不捏造空账本；缺 plan 时无法确认任务全集，scan_complete=false |
| index_pending | Target=Running、独占目录内有一个可绑定的 Run：检查并展示该 Run，保留索引未完成 observation；不修改 Target、不把 Run 晋升为已完成 Attempt |
| run_identity_mismatch、unindexed_run | Run ID/Target/目录归属冲突为 violation；目录已创建但 run.json 未发布为未完成残片。多 Run 在当前协议不支持，列清单和 gap，不选最新或偷偷实现重试 |
| terminal_claim_incomplete | Completed Run 必须有匹配的结构化报告、候选/补丁摘要、验证/输入绑定和必需交付文件；缺失为 violation。单纯存在 Completed 报告不能替代磁盘 Run 终态 |
| failed_run_has_verdict | Failed/Cancelled Run 不得携带最终质量 verdict；允许保留部分候选、诊断和无最终 verdict 的验证材料 |
| stale_report | Failed/Cancelled Run 可能因诊断重写失败，留下旧 Completed 报告；不采用其 verdict，记陈旧材料及 gap，不能因文件存在认定成功。若更上层仍将其作为 Completed 交付引用，则检查该更强声明并报 violation |
| target_attempt_mismatch | Finished 的 Attempt 与其实际 Run 的身份/终态/报告摘要不符，或跨 Target 引用，为 violation；合法失败可能无 Run，必须符合现有缺身份分支契约 |
| result_rejected | `checkAttempt` 拒绝异常回调会保存 Failed / invalid_result、空 Run 身份及非空错误；即使目录中留有 Run，也不将它重新绑定为该 Attempt。不因残片存在就判 Target 索引矛盾，也不采用残片的成功 verdict |
| summary_mismatch、campaign_completion_incomplete | 有 summary 引用时核对原字节摘要、Target 次序/计数和预算副本；Campaign=Completed 必须有完整最终索引及汇总。可含 Failed Target，不要求全部迁移成功 |
| publication_pending | summary 已存在但 Campaign 仍 Running，或 Run 已终态但 Target 尚未更新：按关系检查后记未完成；不能拿孤立文件存在认定完成 |

失败终态缺少其声称完整交付的材料是违规；早期 Failed Campaign 没有 summary、尚未初始化全部 Target 则可能是控制故障的合法结果。具体缺失是否允许，依据“哪个层已经做出完成/引用声明”，而不是一律缺文件失败或一律中断放行。

还需覆盖控制写入失败：Campaign=Failed 时，summary 可能已采用内存中的 Finished 结果，而磁盘 Target 仍 Running。若对应 Run 和报告可绑定，按索引发布未完成记 gap，不伪报两份索引已经一致；Finished 身份互相冲突或 Campaign=Completed 声称完整时才按终态条件报 violation。不靠匹配 Error 自由文本来决定豁免，也不能以 Campaign=Failed 为由忽略预算、摘要或跨 Target 冲突。

`result_rejected` 来自 [checkAttempt](../../internal/campaign/storage.go) 的结构化拒绝结果及[现有负例](../../internal/campaign/run_test.go)，不是对原回调异常的逐项证明：原回调字段没有完整持久化。该状态下的残片单列为未获接纳材料，仍检查其内部可确定的违规；缺少将残片与拒绝原因绑定的证据时记 gap，不能靠 Error 文本恢复原回调，也不能以 invalid_result 豁免残片本身的摘要/身份错误。局部拒绝后 Campaign=Completed 可以合法，表示调度已完成，不能因此要求每个残片都获接纳。

合法中断必须满足已有发布动作的前置关系，首版明确检查以下最小矩阵；未列入支持范围的组合记 gap，不自动套用“可能中断”：

| 已稳定观察到的材料 | 必需前置材料 / 约束 | 不满足时 |
| --- | --- | --- |
| 任一 Target=Running/Finished/Skipped，或存在 Run / 预算绑定 / call | plan、Campaign、全部初始 Target 记录及已初始化预算快照；调度在这些发布完成后才开始 | 可确定缺失为 `prerequisite_missing`；读取失败、未知格式或漂移为 gap |
| Target=Pending/Skipped | 不能已有所属 Run、账本 Run 绑定或 call；单次尝试协议不会从 Running 回退到这两种状态 | `state_prerequisite_mismatch`，不能当作未索引 Run 的合法窗口 |
| 有 call 或 Account.RunID | 属于清单内 Target，且存在同 ID 的 Run 记录；尚未写 Target.Attempt 可以合法 | 身份冲突/确定缺失为 violation；不会据此要求已有最终报告 |
| rpcx Run 已进入 CheckingBaseline/Generating/Validating/Publishing | 分别核对已完成 prepare、基线检查及后续阶段所必需的持久化材料；Generating 只表示进入生成，不能据此要求已有 call 或预算 Run 绑定 | 精确业务材料清单由 M4.1b 按写入顺序固定；没有场景检查时记覆盖 gap |

这里只依据落盘状态判断前置条件，不根据时间戳推演任意全局顺序。合法发布前缀说明“存在与现有写入协议相容的解释”，不能证明实际中断原因。

rpcx 检查函数按已发布结构检查 task/profile 原字节 pin、实际基线/候选/依赖快照、规范、Run/报告/validation 的身份和已引用证据摘要；只比较已保存的 verdict 与绑定，不按当前新规则重算质量，不调用 quality.Decide 或业务探针。历史报告使用归档材料身份，不强行要求等于当前源码的工具摘要。

补丁首版只核对文件及既有 SHA 与候选 pin，不在审计里执行 Git 来重放补丁；因此不单独证明补丁应用后确实得到该树。Markdown 至少核对必需文件存在；发现项导航和文案质量不是首版必需审计规则。原六个断链及交付 fail 仍由 M3 记录，审计零违规不消除它们。

### 5.2 预算账本

按逐 call 重算 Campaign 和每个 Target 的 Totals，不信任已有累计值；运算检查非负与溢出。每条 call 的 Target、Run、唯一 ID、输入摘要和预留 Q 必须有效，Profile 与 Plan 一致，Q 为冻结配置的 C+O。没有 call 的拒绝不计作准入；Account 已绑定 Run 但还没有 call 也是合法状态。

| call 状态 | 占用重算 | 额外检查与结论 |
| --- | --- | --- |
| reserved | Calls+1，ReservedTokens+Q | usage 不得伪装成已结算；观察为未决请求，不释放、不认为 HTTP 未发送 |
| settled | Calls+1，KnownTokens+usage.Total | usage 必须存在、完整合法，输入/输出及缓存计数符合既有契约；字段缺失不能视为 0 |
| unknown | Calls+1，UnknownTokens+Q | 没有可信 usage，不按 0 处理；这是正常保守占用，不因成本未知自动算坏账 |
| invalid | Calls+1，UnknownTokens+Q | 保留原非法 usage/HaltReason并展示供应商/上界异常；正确记录异常本身不是漏扣账。若实际大于 Q，不能宣称本地保守占用覆盖实际费用 |

当前协议中 reserved/unknown 的 Usage 应为空、settled 应为合法值、invalid 应保留不合法原值；状态与值不匹配要定位为记录违规，不能自行重新分类或重算成另一种状态来消除问题。

两级均核对 `Known + Reserved + Unknown <= MaxTokens`、Calls<=MaxCalls、逐项与累计一致、各 Target 与 Campaign 总额一致。重复 call、账目丢项/少扣、归属或两级额度不符为 violation。invalid 原值可以为负或溢出，审计不能将其当可信 usage 加入累计，也不能为“修复负数”覆盖原记录。

Completed Campaign/Run 仍有其对应 reserved 请求，违反当前同步调用并结算后交付的契约；Failed/Running 保留 reserved 则可能是结算失败或进程中断，属于未决观察。Campaign 在已持久化公共账本故障下声称正常 Completed 为 violation。halt/unknown/invalid 对后续能否重试的处置留给恢复设计。

一个快照不能证明过去是否绕过包装器、是否预留先于发网、Revision 在历史上单调或同一请求曾否重复结算。首版核对当前正数 revision 与内容，不从次数/时间戳编造完整事件顺序或供应商账单认证。

## 6. 明确的合法中断前缀

以下表格是待测契约，不是已经实现的识别器。至少需要区分：

| 停止位置 | 可见材料 | 预期 |
| --- | --- | --- |
| Campaign 创建后、首份 plan 前 | 独占空目录/临时文件 | identity 未就绪，inconclusive；不当作完成或恢复新批次 |
| plan/Prepare 后，Target/预算初始化中 | plan、Running Campaign、部分 Pending；可能无账本 | 初始化未完成，不自动报预算为 0；检查能确认的部分 |
| Target Running 后、Run 第一记录前 | Running Target、空 runs 或一个空 Run 目录 | incomplete，未索引/初始化残片 observation |
| 基线检查后或 Agent 中 | Run Preparing/CheckingBaseline/Generating，诊断/部分工作树 | incomplete；缺尚未要求发布的报告不算已完成交付违约 |
| 已预留后、模型可能在途 | reserved + 绑定 Run，可能没有新 Agent 消息 | 保留 Q；不能推出未消费、不能释放额度 |
| 模型返回、结算未落盘 | 同上，可能有工具/消息材料 | 仍按 reserved；单靠这些文件不能认证供应商真实费用 |
| 验证中或验证返回、未发布终态 | Run=Validating/Publishing；可能已有 evidence/report | phase 只表示曾进入步骤；校验可见绑定，不认证完成，不改 verdict |
| report/review 后、Run 终态前 | report=Completed，run=Running | publication_pending；已有证据仍核对，整体 incomplete |
| Run 终态后、Target 引用前 | Run=Completed，Target=Running | Run 可局部完整，Campaign 未完成；不能丢掉已交付成果 |
| summary 后、Campaign 终态前 | summary 完整但 Campaign=Running | publication_pending；不是整个 Campaign 已完成 |

若 Completed 记录本身已发布但其必需材料缺失，不能再用上述前缀豁免。无法仅由当前快照区分“中断时没写”与“后来删除”，应只描述可见状态与证据范围，不伪造发生原因。

## 7. CLI、验收与实施单元

拟增加 `harness audit --campaign <root> --output <new-dir>`，不进入现有模型/后端初始化分支。拟定退出码：0 为报告完整发布且 consistency=pass，1 为发现 violation，2 为参数/输出配置错误，3 为审计无法完成或来源漂移，130 为取消。合法中断可退出 0，但终端必须同时显示 execution_state=incomplete 和 observation 数；0 不代表恢复完成、候选正确或可继续消费预算。若有 violation 同时有 gaps，退出 1 并保留不完整覆盖；报告写入失败优先作为操作失败，不将部分输出当成功。

| 实施单元 | 交付与验收 |
| --- | --- |
| M4.1a core（本轮范围验收完成） | 新只读遍历/版本解码、Campaign/Target/Run 归属和前缀、预算重算；构造合法/损坏快照对照。没有场景检查时 coverage 必须显示未完整审计，不能先宣称 pass |
| M4.1b rpcx 与 CLI（范围验收完成） | 接受归档的五种 Profile，绑定输入/候选/报告/证据；真实三仓归档应结构一致，原 Markdown 断链不被误解释为候选错误；未知版本保留 gap |
| M4.1c 故障验收 | 子进程运行受控任务，在持久化观察屏障处 SIGKILL，随后只读审计；固定模型、无真实 API、单独目录。先证明观察到屏障再杀，禁止固定 sleep 冒充命中窗口 |

必要对照包括：完整三仓、混合局部失败、早期控制失败、合法取消、预留未知/非法用量；篡改 plan/报告/候选/日志、跨 Target 引用、伪 Completed、重复 call、少扣两级账目、负值/溢出、缺 usage；未知版本/缺必需字段/重复键、路径越界/链接/FIFO/超大输入、审计中来源变化/输出失败/取消。对照逐项断言 code、位置、coverage 和退出码，不只检查“返回 error”。

第二轮评审补充五组必须对照：旧 summary / 新账本的读取漂移不能变成确定违规；稳定违规与无关漂移并存仍保留违规；invalid_result 留有完整或损坏 Run 时分别检查拒绝索引与残片；Running Target 缺账本、Pending/Skipped 却有 call 不得作为合法前缀；无独立版本字段的真实文件按父级契约读取，未知父级版本不套用旧结构。取消导致复核未完成时 inputs_unchanged=null。测试须固定期望分类，不能只根据实际返回结果生成期望。

SIGKILL 单列真实进程实验，先覆盖基线后、模型请求在途、report 后/Run 终态前、Run 后/Target 更新前四个关键窗口，再覆盖预算与初始化细分窗口。尚无可观察屏障的点在实现时增加最小测试缝隙，不能在设计阶段宣称已验证。对每轮审计比较输入全部参与文件的前后摘要与目录成员，重复审计使用全新输出、排除时间/目录差异后 findings 相同。

源码改写问题与只读审计区分：Runner 的持久化强度、验证前候选 pin、写者所有权和资源归属记录，属于后续 M4 恢复/清理实现的输入问题；本步只准确暴露其能力边界，不在审计过程中“顺手修复”。

## 8. 本轮检查与下一步

本节保留设计时检查及当时下一步；后续实现见第 10—11 节，当前下一步见第 12 节。

本轮已读取上述源码、原真实 Campaign/账本及受控混合批次，核对记录结构和实际发布顺序；未运行 Go 测试、SIGKILL、审计实现或候选，也没有查询模型服务。文档检查只验证本设计链接和变更边界，不代表 M4.1 已验收。

检查材料保存于 runs/m4-audit-design-01/inspection.json，明确标为设计输入检查。两个既有 Campaign 都是 Completed：真实批次为三 Target，受控混合为六 Target，均含匹配的 summary 原文摘要与预算副本；这支持“Campaign 完成允许局部失败”的输入契约，不能当作本轮中断实验。42 份所读源码/记录、阶段索引的 7344 份来源及 554 份代码/数据文件保持摘要。八份当前变更文档的 171 个本地链接、26 个锚点、代码围栏及 Git 差异检查通过，command.md、HEAD 与暂存区保持不变；结果为 verification.json。文件数量是核对范围，不是测试覆盖数量。

下一实现单元为 M4.1a：先交付 core 的只读与预算/前缀检查，用可解释的构造快照检验分类，再接 rpcx 强交付材料及 CLI。M4 以本设计明确并行推进，M3 未满的原人员口径、独立留出以及 M5 正式评测约定保持；本步不增加可运行命令到 command.md，不暂存、不提交 Git。

## 9. 第二轮设计评审

2026-10-20 再次对照 Campaign 校验/发布、Runner finish、预算结算和已有拒绝结果测试。保留只读观察、两级预算重算及静态场景函数边界；原设计有三项影响判定正确性的缺口：漂移未使依赖它的违规失效、未明确 invalid_result 的无身份拒绝与残片关系、合法前缀缺少反向前置条件。以上均已补入结果契约与规则，并列入必要对照。

另补格式选择和真实性边界，避免对没有版本字段的旧格式一律拒绝，或把一致的摘要链误称为历史认证。首版仍不增加事件库、签名、活性检测或恢复写入；现有三个实施单元保持，跨层规则用显式函数与表驱动测试即可，不实现通用状态机/规则依赖引擎。实现时每条发现登记直接参与判断的来源即可支持漂移降级。

本次仅做源码和文档评审，16 个本地链接目标、代码围栏、行尾空格及 git diff --check 检查通过。没有执行 Go 测试或中断实验；上述缺陷是设计遗漏，尚未实现，不能称为已复现的审计程序 bug。设计修订后可进入 M4.1a，以新增对照作为验收约束。


## 10. M4.1a：只读审计核心实现与验收

- 日期：2026-10-20；实现基于 d9b15b9 的工作区，未暂存、未提交。
- 目标：把前两轮设计中的归属、合法前缀、两级预算和来源稳定性落实为可独立调用的 Go API。
- 当前结论：core 在本节声明范围验收。没有接入 rpcx 完整交付检查、CLI、恢复写入或真实 SIGKILL；M4.1 和 M4 整体尚未验收。

### 实际实现与边界

新增 [internal/audit](../../internal/audit/README.md)。`Inspect(ctx, Request, RunInspector)` 只从当前 Campaign 根读取，将报告写入互不重叠的新目录；归档内原 Directory 仅用于核对逻辑关系，不访问旧机器路径。RunRef 传递固定 Target、Campaign ID、原根与 Run 信封，宿主函数经过同一个 View 读取并声明检查完成情况。新包不导入 scenarios，也不创建模型或执行后端。

职责分为受限来源读取、严格 JSON、Campaign 初始化/Target/Run/summary 检查、预算重算及报告发布。复用已有 Campaign、预算和 Target 数据结构；Run v2 信封保留在 core，业务材料留给场景。JSON 先检查版本与重复键，再按静态类型检查必填字段和精确字段名，避免普通 Unmarshal 静默吞掉缺字段、大小写差异及重复键。嵌入预算也单独识别版本；未知格式不会套用旧结构继续判断。没有引入规则 DSL、事件库或通用工作流引擎。

每项发现记录来源路径，报告的 sources 保存所读字节/目录成员摘要及复核结果。最终复核后，依赖变化或无法复核的违规降为 gap；其他稳定依据上的违规保留。稳定性失败不使用 true 零值，取消返回 null；覆盖不完整时不能为 pass。原写者活性固定为 not_checked。文件上限 16 MiB、最多登记 100000 项来源、首次保留内容总计 512 MiB；JSON 增加 128 层嵌套限制。上述是内容规模上限，复核会再次读取，不是总 I/O 上限；业务树的更小限制留给 M4.1b。

预算从逐 call 重新计算 Campaign/Target 两级 Calls、Known、Reserved、Unknown，检查负值、整数溢出、C+O、usage 和缓存计数、账户归属及限额；拒绝不计作调用。正确保留的 unknown/invalid 属于观察项，非法 usage 不当作可信消费参与累计。Completed 仍保留 reserved，或 Campaign Completed 却存在公共账本故障，分别报终态违规。零 call 的合法绑定保留，不要求制造一次请求。

合法前缀覆盖空 Run、索引滞后、summary 早于 Campaign 终态，以及无账本的早期初始化；已进入调度后缺前置账本或 Target 则不能豁免。`invalid_result` 的无身份失败与残片分别检查，残片不重新晋升为成功；失败 Campaign 的 summary/Target 发布滞后记缺口，不忽略确切冲突。core 只核对被引用报告的路径和原文字节摘要，报告内部绑定、候选树、日志与陈旧业务报告识别在 M4.1b 完成。有 Run 而未注入场景函数时必有 run_inspector_missing 和不完整覆盖。

报告发布为 audit.json、sources.json、audit.md，最后独占写入 published.json，后者绑定三份文件摘要。配置/取消/输出失败经 error 返回，材料违规与缺口经 Report 返回；输出中途失败不会发布完成标记。只有局部核心记录尚无 Run、且适用检查都完成时，才能在当前单元得到一致性 pass；业务回调机制测试中的 pass 使用明确的替身，不代表 rpcx 完整审计已实现。

### 实现检查中修正的问题

1. 识别到 plan、Target、summary 等无独立 schema_version，采用父级版本；对父记录无法解码时保留被阻断检查的 gap，不能把未检查部分计入完整覆盖。
2. 给缺失引用 Run 目录的分支补登记缺失路径，否则仅查内存扫描结果无法定位这一违规；空目录残片与引用缺失分别处理。
3. 补充目录成员、新文件出现、取消前未复核、嵌入未知版本和 FIFO 实例，确认不会假设所有变化都是文件内容变化，也不会阻塞打开 FIFO。
4. 初次 go vet 使用默认宿主 Go 缓存时遇到沙箱写权限错误，后续统一用 /tmp 下专用 GOCACHE；静态检查通过。首次全工作区 `go test -run '^$' ./...` 错将 runs 中历史参考/构造材料纳入编译，因这些材料不是当前模块源码、缺外部依赖而失败；保留 project-compile.jsonl。改为明确源码根 cmd/internal/scenarios 后，26 个包编译通过；这是编译检查，不是完整项目测试回归。

### 验证结果

产物保存于 runs/m4-audit-core-01/，运行产物不入 Git；不合并多轮重复计数。

| 验证 | 实际结果 | 证据 |
| --- | --- | --- |
| audit、budget、campaign 竞态检查 | 三个包、143 项测试及子测试通过；归档入口因未设置输入跳过 1 项 | core-race.jsonl |
| 最终 audit 竞态检查 | 82 项测试及子测试通过；归档入口跳过 1 项，无竞态报告；包含后续补充的发布阶段/零调用和读取限额对照 | audit-race-accepted.jsonl |
| 原三仓真实 Campaign，只读核心检查 | 0 violation；3 个场景函数缺口，consistency=inconclusive、execution_state=completed；所读来源及归档全文件/目录成员前后相同 | real-archive-accepted.jsonl、real-archive-accepted/audit.json |
| 六 Target 混合 Campaign，只读核心检查 | 0 violation；6 个场景函数缺口，consistency=inconclusive、execution_state=terminal_with_failures；归档前后相同 | mixed-archive-accepted.jsonl、mixed-archive-accepted/audit.json |
| 项目源码编译检查 | cmd/internal/scenarios 共 26 个包通过；没有运行其中全部测试 | source-packages-compile.jsonl |
| audit 静态检查 | go vet 通过 | vet-final.log |

构造对照逐项检查原因码及路径，覆盖成功/失败/取消/早期控制失败、预算少记/重复 call/非法 usage/溢出、跨 Target、摘要篡改、路径越界/符号链接/FIFO/超大输入、格式未知/缺字段/重复键、来源漂移和无关稳定违规并存、输出失败。相同来源重复审计结果相同；全新输出完成标记的三份摘要均核验。两个归档只作为既有格式与核心不变量回归，不是重新生成、重新判定或真实强杀；原实验质量结论与指标保持。

### 完成判断与下一步

M4.1a 在上述构造与归档范围完成；[模块说明](../../internal/audit/README.md)列出 API、结果语义和测试入口。尚未以实际 SIGKILL 证明各写入窗口，也未读取候选/日志完整证据，因此不能宣布 M4.1 已完成或“归档已完整审计”。下一单元 M4.1b 接五种 rpcx Profile 的冻结材料、已发布报告/证据身份和 CLI；随后 M4.1c 按真实持久化屏障验收。M3 剩余验收、原实验、受托复核角色及留出依赖保持，不新增付费运行，不暂存或提交 Git。

最终范围检查：既有 554 份代码/数据与阶段索引中的 7344 份来源摘要未变；九份文档的 177 个本地链接、27 个锚点、格式和 git diff --check 通过。HEAD 仍为 d9b15b9，暂存区为空。新模块源码/说明摘要、各轮测试统计及明确未实现项见 runs/m4-audit-core-01/verification.json；旧设计 verification.json 未覆盖。


## 11. M4.1b：rpcx 归档证据与审计命令

- 日期：2026-10-20；仍基于 d9b15b9 工作区，改动未暂存、未提交。
- 目标：使核心审计能核对五种已发布 rpcx Profile 的实际交付材料，并由独立命令消费旧 Campaign。
- 当前结论：场景函数和原生命令在本节范围验收。M4.1c 真实 SIGKILL、恢复、重试和资源清理尚未完成，不据此验收整个 M4。

### 选择与实现

场景函数放在现有 [runner/audit.go](../../scenarios/rpcx/runner/audit.go)，按固定 Profile ID 选择已发布布局，不调用 Profile 中会认证当前工具源码的运行时准备流程；历史 verifier 的实际源码摘要从归档自身重算。只读 [View](../../internal/audit/view.go) 是唯一材料读取入口，复用严格 DecodeJSON；RawMessage 只用于先选择嵌入版本，整个 JSON 仍先查重复键和尾随内容。来源中的未知格式得到 gap，不能靠默认字段继续检查。

支持两种 demo-orders、Goddd、Translation 和 Workouts Profile。逐层核对 task/profile/规范、完整基线布局清单、依赖副本、最终候选和补丁 pin、基线命令与必需测试记录、report/Run/预算绑定、已提交 Agent 退出记录、保存的 validation verdict、Evidence ID/摘要/输入绑定、检查和发现的 Evidence 引用、归档策略的检查集合与条款引用，以及命令清单/日志和归档验证器材料。证据里的输入清单必须对应当前归档内的实际基线/候选来源；不会回到其中保存的绝对目录读取。

历史工具材料按格式分别处理：T1/T2 重算归档策略紧凑 JSON 加探针字节摘要，T3 重算 verifier-materials 清单，L2 重算 evidence/tool 树；宿主依赖认证固定的 modules.json/tar.gz 原始材料，不解包或遍历未引用的模块缓存。单业务树最多 1 MiB/文件、10000 文件、16 MiB，并限制目录数和深度。Git 元数据、链接或路径越界不能成为读取入口。

**两个兼容性取舍在首次归档检查中得到具体证据：**

1. 旧 demo-orders Command.Log 是绝对路径，L2 是相对路径。统一禁止绝对字符串会误报已发布格式，因此只把“声明的原 Run/evidence 根内”的命令日志元数据转换成固定相对位置，再核对其 Evidence 引用；越界仍拒绝。普通 Artifact 路径继续只接受相对路径，不跟随外部日志路径。
2. 原混合批次的一个来源在 Plan 中使用 /var，Runner 持久化为 /private/var。只做字符串全等会把合法规范化误报成任务替换，因此输入身份比较固定 ID/Profile/配置/内容摘要，位置仍需绝对格式，但作为历史元数据保留；位置不同列 source_location_normalized observation。代价是本审计不证明两条历史路径曾指向同一物理目录；冻结内容和归档副本才是此处身份依据，不访问旧机器解析别名。

发布边界保留：Preparing 的部分文件可以尚未存在；进入后续阶段才要求已完成 prepare 的材料，Generating 及更晚阶段要求基线 pass；Running 下已出现报告是合法发布前缀。失败终态留有旧成功报告标 stale_report gap，不采用该质量结论；失败时未成功发布的可选诊断不伪装成完整交付。报告改变时，从报告取得的 Evidence/工具摘要发现也必须降级，不能只复核被引用文件而漏掉摘要声明的来源。

`cmd/harness` 在模型和后端初始化之前分派 `audit`。命令接受停止写入的 Campaign 和全新输出目录，不读取 .env、不创建客户端、不开容器、不重跑候选、不调用 quality.Decide。原生退出码为 0 一致性完整通过、1 有确定违规、2 配置错误、3 无法完成/漂移或操作失败、130 用户取消；输出同时显示 execution_state 和发现数量。合法未完成记录可以退出 0，但明确显示 incomplete。报告及发布完成标记沿用 M4.1a。

### 验证与修正记录

- 第一轮构造测试的 Workouts 样本路径错误：把实际业务模块名 trainings 当成来源切片名称 workouts。修正测试的来源选择，保留 first-tests.jsonl；未放松 Profile pin。
- 第一轮真实三仓归档已通过；首轮混合归档暴露上述绝对日志与来源别名误报，mixed-first 报告保留，修正后另建 accepted 报告，不覆盖原报告或失败材料。
- 构造材料只测试格式与摘要关系，不宣称运行过其中保存的命令。实际归档消费是只读回归；未生成新候选、重跑门禁或调用真实模型。
- 最初构建触及默认 Go module stat 缓存的沙箱写限制，最终统一使用 /tmp 专用 GOCACHE/GOMODCACHE 并禁用代理/校验服务；构建和检查通过，不新增依赖。

| 验证 | 结果 | 证据（runs/m4-rpcx-audit-01/） |
| --- | --- | --- |
| core、rpcx 审计、CLI 定向竞态检查 | 三个包、113 项测试及子测试通过，无竞态；两个未设置归档输入的可选测试跳过 | race-final.jsonl |
| 五 Profile 构造对照 | 五个自洽样本通过；规范/树/补丁/清单/预算/报告绑定、缺材料、Evidence 逃逸/错绑/引用缺失等负例按原因码拒绝 | 同上 |
| 发布前缀、未知 verifier、陈旧报告、来源漂移 | 分别验证 incomplete、inconclusive 与依赖失效降级；保留稳定依据上的违规 | 同上及 core 对照 |
| CLI 分派和退出码 | 无密钥、不可执行 Go/Docker 的 PATH 下完成检查；0/1/2/3/130、帮助与参数拒绝符合契约 | 同上 |
| 原三仓真实 Campaign，经原生 CLI | 退出 0；consistency=pass、completed；726 项来源，0 violation、0 gap | real-accepted/audit.json、native-cli-results.json |
| 原六 Target 混合 Campaign，经原生 CLI | 退出 0；consistency=pass、terminal_with_failures；415 项来源，0 violation、0 gap | mixed-accepted/audit.json、native-cli-results.json |
| 静态与源码编译 | audit/runner/harness 的 go vet 通过；源码目录编译结果另存，不等同全部项目测试 | vet.log、source-compile.jsonl |

原三仓的 NeedsAttention、六项未知检查、原六处审核链接缺陷及全部实验指标保持。这里的 pass 仅表示已支持归档契约中的结构和绑定自洽，不认证业务正确、历史未被整体重写或旧审核导航已修复。审计只核对补丁字节 pin，没有重放补丁证明它生成同一树；模块缓存和未引用材料不在覆盖范围。旧 a-only 审计报告仍保持 inconclusive，不回写为通过。

### 下一步

进入 M4.1c：运行受控子进程，在可观察的持久化屏障处真实 SIGKILL，然后以当前只读命令验收合法残片及未决预算。当前已经测试发布前缀的构造材料，尚不能把它们当作实际强杀证据。恢复写入和费用处置仍需另行设计。

最终材料检查：阶段索引 7344 项中，7342 项摘要保持；另两项是本轮有意修改的 CLI 分派源码与 Runner 说明，原运行归档及指标未改。原 554 份代码/数据中其余 552 项保持；新增/更新审计源码另有摘要清单。八份文档的 198 个本地链接、26 个锚点、Go 格式及 git diff --check 通过，HEAD 仍为 d9b15b9，暂存区为空。完整结果见 runs/m4-rpcx-audit-01/verification.json；旧 M4.1a 报告和验证结果不覆盖。


## 12. M4.1c：真实进程中断验收

本单元先覆盖基线通过后、受控模型请求在途、report/review 发布后但 Run 未终态、Run 终态后但 Target 索引未更新四个窗口，并增加 Campaign 初始化前缀。子进程真实运行 Campaign、Runner、预算包装器和离线 Go 门禁，模型由进程内确定性实现替代；不访问供应商。除初始化窗口外，串行批次先完成一个独立 Target，再中断第二个，以检查已完成成果不受影响。

现有 Model 和 Execute 接口可分别停在模型在途、Run 回调返回后；Campaign.Prepare 可停在计划和初始 Campaign 已发布但尚未创建 Target/账本时。另两个窗口不能靠读取磁盘后抢时间可靠命中。可选操作系统文件监控或轮询都存在后续写入已继续的竞态；因此采用两个包内观察点：基线返回后、报告与审核文件成功发布后。公共入口传 nil，CLI 不暴露故障参数；测试使用同一生产流程的包内入口，没有全局可变 hook。接受两个小型测试缝隙的维护成本，不添加通用事件总线或客户可配置的暂停协议。

父进程通过专用管道接到子进程阻塞确认，复读阶段/账本/报告条件并记录摘要后才向这个明确的子进程 PID 发 SIGKILL；等待系统确认被信号终止后，再启动只读审计。禁止固定 sleep 猜测窗口。报告后的磁盘 Run 仍 Running 是期望状态，不能采用内存中已计算的终态。模型在途窗口只证明进程内调用已获预留且尚未结算，不冒充真实网络供应商已消费。

计划断言：Campaign 保持 Running、Run/Target 状态符合各窗口、在途 Q 不释放、原完成 Target 文件/成员保持；两次新目录审计的发现相同且不修改源；报告后的 Candidate pin 未落 run.json 也不能产生伪违规；原生成流程的同批材料在无强杀时仍可完成。真实强杀覆盖只针对进程退出，不保证掉电、磁盘损坏或遗留容器回收，也不执行恢复/重试。


### 实际实施与验收

- 日期：2026-10-20；代码仍在 d9b15b9 工作区，未暂存、未提交。
- 结论：M4.1 只读审计与中断识别在声明范围验收。以下是受控 T1/T2、本地 Unix 执行、单机串行批次中的真实进程实验；不推广为任意平台、并发轨迹、供应商断连或掉电保证。

[Runner](../../scenarios/rpcx/runner/run.go)增加 baseline_passed 和 report_published 两个包内观察点，公共 Run 与 CampaignExecutor 都传 nil。模型在途和 Run 终态前后复用已有注入接口；初始化用 Campaign.Prepare。没有全局 hook、CLI 故障开关或新的状态格式，也没有修改预算持久化、质量门禁、重试/恢复流程。

[子进程验收](../../scenarios/rpcx/runner/audit_crash_unix_test.go)以独立测试宿主进程运行同一 Campaign/Runner/预算生产代码。使用 execution.Local 实际执行离线构建、测试与独立门禁，进程内模型提交未修改候选；正常完成的两次交付都是 Rejected，这里验证执行与交付记录，不声称产生成功迁移。模型声明的每次 20 Token 是受控机制数值，没有真实供应商消费。

子进程在操作已返回后通过专用 FD 管道发出检查点并阻塞，父进程确认管道 PID 等于自己启动的子进程，重新读取磁盘约束后才调用 Process.Kill；WaitStatus 必须确认为 SIGKILL。观察点中的内存 Record 不代替磁盘记录。非初始化案例中，一个 Target 已完整完成并被 Campaign 接纳，另一个在检查点停住；kill 作用于整个运行子进程，不是工作 goroutine 或执行容器。

| 检查点 | 强杀前实际磁盘状态 | 预算事实与审计结果 |
| --- | --- | --- |
| initialized | plan/Campaign 已发布，Target/预算尚未初始化 | 不伪造空账本；pass + incomplete |
| baseline_passed | 先前 Target 已完成；当前 Run=Running/CheckingBaseline，baseline=pass；当前 Target 无 Attempt | 仅前一 Run 一次已结算调用：Calls=1，Known=20；pass + incomplete |
| model_pending | 当前 Run=Running/Generating；实际预算包装器已绑定 Run 并落盘 reserved，底层受控模型尚未返回 | Calls=2，Known=20，Reserved=100，Unknown=0；强杀与审计均不释放/重分类 Q；pass + incomplete |
| report_published | report/review 已发布为 Completed；磁盘 Run 仍 Running/Publishing、无最终 Patch pin，Target 仍 Running | Calls=2，Known=40，Reserved=0；记 publication_pending，不能凭报告把 Run 晋升；pass + incomplete |
| run_terminal | 当前 Run 已 Completed，Target 仍 Running、没有新 Attempt | Calls=2，Known=40；保留局部完成成果，Campaign 仍 incomplete |
| 正常完成对照 | 默认公共 CampaignExecutor/Run 路径，不配置观察点；两 Target 均完成，summary 与 Campaign 终态已发布 | Calls=2，Known=40，Reserved=0；pass + completed |

每个强杀案例在系统确认子进程终止后读取全部源文件和目录成员（包含缓存），连续发布两份新审计报告，断言报告一致、源摘要/成员不变；非初始化案例还核对强杀前完成 Run 的全树摘要。随后使用新构建的原生 harness audit 再读相同六份材料，结果与 API 完全相同，命令退出 0；此时终态发布状态仍明确区分 incomplete/completed。审计本身始终 writer_state=not_checked，不把测试父进程掌握的死亡证明冒充通用写者活性协议。

| 检查 | 实际结果 | 证据（runs/m4-sigkill-01/） |
| --- | --- | --- |
| 首轮五检查点 | 五个真实 SIGKILL 案例通过；没有用伪造磁盘状态替代信号实验 | first.jsonl、cases/ |
| 最终强杀与正常对照，带竞态检查 | 五次 SIGKILL、一次正常完成，共 7 个测试及子测试通过；约 171.4 秒，无竞态报告 | accepted.jsonl、accepted/*/fault-evidence.json、control-evidence.json |
| 既有 core/rpcx/CLI 定向竞态回归 | 三个包、113 项测试及子测试通过；5 项默认关闭的归档/强杀/子进程辅助入口跳过 | regression.jsonl |
| 原生 CLI 消费六份新材料 | 六次退出 0，报告与 API 相同；五份 incomplete、一份 completed，原材料保持 | case-verification.json、accepted/*/native-audit/ |
| 静态检查与构建 | audit/runner/harness 的 go vet、审计二进制构建通过；源码包编译另存 | vet.log、build.log、source-compile.jsonl |

两轮强杀使用不同新目录，前一轮保留，不将两轮重复统计成更多独立检查点。验收工具默认关闭真实子进程实验，显式开启方式已补入命令说明；普通测试不会随机强杀自身或其他进程。运行产物保持在 runs，不入 Git。本单元没有读取 .env、使用真实 API、人工改写中断 Run/账本或清理不明归属的容器。

**完成与剩余边界：**M4.1 的方案、Go API、五 Profile 归档检查、CLI、构造反例与上述真实中断证据已经闭环。现有 Runner 仍只有关闭/rename，没有新增 fsync；当前证据针对进程 SIGKILL，不保证电源故障。预留后但尚未进入底层模型、响应返回但结算未落盘、候选验证开始前等更细窗口，以及 Docker/真实网络故障没有在本轮强杀中覆盖。已完成材料可被识别和保存，不代表未完成任务已经可恢复；账本仍不支持打开旧批次续用，Target 仍只允许一次绑定。

下一步为 M4.2 具体设计：确定停止旧写者的凭据、未决预留的保守处置、同一任务创建新 Run 的受控重跑及历史保留契约，再实现恢复/重试。该设计需重新权衡当前只绑定一个 Run 的账户模型和冻结旧记录的边界，不能直接把 Running 改成 Cancelled 或把 Reserved 清零。M3 原协议人员和独立留出缺口、M5 正式评测保持，M4 整体不提前验收。

最终交付检查：源码编译中 26 个有测试包通过、7 个无测试包跳过，没有运行全部项目测试。阶段索引 7344 项中 7340 项摘要保持，原代码/数据 554 项中 550 项保持；四项有意差异为此前 CLI 分派、Runner 说明与本轮两个生产流程文件。旧运行归档、冻结数据和此前 18 份审计源码摘要保持。八份文档的 202 个本地链接、28 个锚点、Go 格式和 git diff --check 通过；HEAD 仍为 d9b15b9，暂存区为空。汇总见 runs/m4-sigkill-01/verification.json，历史验收材料不覆盖。

后续推进：[M4.2 恢复设计](2026-10-20-m4-recovery.md)已记录同批次新 Run、累计预算、写锁和历史封存的取舍与待测契约；下一步 M4.2a。该设计不回写本节中断材料，也不使旧格式获得写恢复能力。
