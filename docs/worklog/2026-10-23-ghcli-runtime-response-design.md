# GHCLI 运行时响应契约：明确违规与未知边界

- 日期：2026-10-23。
- 状态：单元 A 已验收；单元 B 已完成生产门禁与离线观察审计的范围验收；单元 C 的完整 Runner/Campaign 交付待验收。
- 代码基线：7b58a75。
- 前置：[离线反例](2026-10-23-ghcli-response-regression.md)、[独立参考 v3](2026-10-23-ghcli-reference-v3.md)。
- 目标：运行时能依据实际响应/错误观察拒绝已证实的行为变化，同时保留未覆盖修改的 NeedsAttention。

## 1. 是否值得继续，以及本步交付边界

当前门禁已经用 request_scope=unknown 阻止原样错误候选自动放行，因此这不是紧急补救错误放行。收益在于把“需要人工判断的额外修改”进一步归因为“有实际反例的返回值违规”，减少维护者重新构造重定向条件的工作，并使新 Run 的拒绝理由直接可核查。

如果当前优先级只是演示已有完整项目，可以停在参考 v3；该增强属于有限支持扩展，不是 M3—M5 整体验收完成的前提。继续实施时，只交付本场景的动态反例发现和证据归因，不同时扩展静态等价证明、通用控制流分析或自动修补。

成功标准：原样候选在新运行中由 NeedsAttention 变为有 BEHAVIOR-001 证据的 Rejected；通过新增探针的候选仍须满足全部既有检查。尤其是含未知 helper 或额外修改的候选，即使这些探针全通过，也仍然 NeedsAttention。

## 2. 已有实现提供了什么

- [evaluate.go](../../scenarios/rpcx/ghcli/evaluate.go)在构建和依赖通过后，先做类型检查与 request_scope，再执行 behavior；request_scope=unknown 不会跳过动态检查。这正好允许在范围证明不足时找到独立反例。
- [behavior.go](../../scenarios/rpcx/ghcli/behavior.go)剥离候选测试、注入可信探针，通过受限 Docker 执行，保存源码、命令及日志，归入 semantic_behavior。
- 现有探针覆盖普通响应、传输错误、Close 错误、父取消与实际到期，尚未覆盖 CheckRedirect 拒绝时的响应＋错误。
- [gate.Observe](../../scenarios/rpcx/gate/events.go)解析测试事件和 GHCLI_FINDING；当前标记提取未要求来自同一个已失败的必需测试，失败分支也不要求完整终态。behavior.go 仅把“失败但无 finding”降为 unknown。不能直接加一条输出标记，就声称业务归因已充分。
- [quality.Decide](../../internal/quality/decide.go)已支持明确违规优先 Rejected，同时保留未知缺口；证据绑定非法返回 error。本步无需修改聚合原则。

## 3. 方案权衡与选定方向

目标排序：不误拒设施故障、不把有限通过扩大为完整正确、审核依据可复核，其次才是减少执行时间与代码量。

| 方案 | 对当前反例的作用 | 成本与不足 | 选择 |
| --- | --- | --- | --- |
| 保持现状，仅在独立参考/人工复核发现 | 已能阻止自动放行，零生产改动 | Run 的门禁报告仍只能说明未知，归因要等后处理 | 可接受的停止点；本次选择增强即时归因 |
| 静态识别错误分支清空响应 | 执行便宜，可定位具体赋值 | 仅匹配语法无法证明该分支可达、响应确非空；等价重排、别名和 helper 需要更多语义分析 | 不选作首步；没有可信路径证明时不能据此判违规 |
| 运行时调用独立参考 v3 或复制其执行结果 | 复用完整实现，能发现当前错误 | 运行时与评测共用判据，掩盖共同盲区；完整参考还有额外构建与两次实际到期成本 | 不采用，保持两层执行及材料边界 |
| 扩展现有运行时探针，并收紧 GHCLI 本地事件归因 | 以实际函数返回值确认差异；同一命令可复用当前构建与隔离 | 有限样本不能证明所有输入；仍需明确事件可信边界 | **选定作为待验证方案** |

运行时探针依据公开规范第三段、标准库在本地工具链下的实际行为及原基线编写。可以复用通用文件/执行设施，不导入 evaluation，不读取其私有探针、期望标签或报告，不让运行时测试加载参考 v3 的代码文件。已有参考结果可用于开发对照，但不充当运行时证据。两层检验相同公开义务仍有共同盲区，分开实现不等于统计独立。

## 4. 最小实现形态

### 探针与执行

在现有 probes/boundary_test.go.txt 中新增 TestHarnessApplicationReturns，采用四个固定子场景：success、transport-error、redirect-declined、use-last-response。继续使用内存 transport；不访问外网、不创建真实 HTTP 服务。

| 场景 | 必须实际观察的契约 |
| --- | --- |
| success | 状态/头与可读 Body 保留，交付前未关闭，调用方 Close 的错误与释放责任保留 |
| transport-error | 错误链保留，不凭空生成响应 |
| redirect-declined | CheckRedirect 返回普通错误后，invokeHTTP 同时保留响应及错误；响应状态/头保留，原 transport Body 已由 HTTP 客户端关闭 |
| use-last-response | ErrUseLastResponse 产生非空响应且无错误，Body 可读且由调用方关闭，Close 错误保留 |

先用原 v1 基线校准这四项纯行为契约，再验证 v2 正负对照；不能让 v1 去满足迁移后的 SDK/期限全部条件。测试避免读取预期为空的 Body，防止普通差异退化成 nil panic。观察不到 transport 调用或设置失败时不伪造业务 finding。

同一受限命令执行旧探针和新探针，新增父/子测试进入宿主必需事件集合；保留旧实际到期检查、命令超时及资源限制。暂不拆为第二条 Docker 命令，接受明确反例仍可能等待原有约 30 秒检查完成的成本。只有测量证明这种延迟妨碍使用时，再单独权衡短路或快慢分组。

### 本地归因，不扩大公共解析器

在 ghcli 包增加一个本地事件归约函数，输入原始 go test -json、退出码及固定测试/规则目录，输出状态、逐项观察与 findings。不重构所有场景的 gate.Observe；其他场景的语义保持。

归约约束：

1. 测试必须属于认证探针所声明的固定包和必需测试集合；run/终态及包终态数量合法，重复、缺失、skip、损坏 JSON 均不能通过。
2. 明确 fail 要求命令正常结束且退出非零、必需事件完整；finding 来自同一已运行并失败的叶子测试，规则属于该测试允许集合。父测试可因子测试失败而 fail，但不能独自提供业务归因。
3. 新 finding 使用固定原因码，例如 response_missing_on_redirect_error、error_chain_changed、body_closed_before_caller；绑定 BEHAVIOR-001。旧探针的 CTX/TIME/BEHAVIOR finding 也按其具体测试限制，不允许任意规则跨测试挪用。
4. 任一失败叶子无可归因 finding、finding 出现在通过的测试、未知规则/原因码、panic/看门狗输出或退出状态矛盾，整组保留 unknown。即使另一个测试已有违规标记，也不把不完整这次执行算作充分证据；先采用保守策略。
5. 全部必需事件通过且退出 0，才令 semantic_behavior=pass。新增 finding 的观察值只记录有限状态、存在性及固定标识，不归档任意请求头、正文或错误全文。

标记和 Go 测试事件仍与候选处于同一进程。上述约束能识别普通错配及设施故障，不能认证恶意候选伪造的所有 stdout。抗任意事件伪造不属于本次保证；若出现这种证据，需重新设计隔离观测通道，不能靠添加 nonce 或日志格式宣称解决。

### 状态与最终判定

| 观察 | semantic_behavior / 执行 | 最终含义 |
| --- | --- | --- |
| 完整执行、同测试有效 finding 确认响应丢失 | fail | 在材料有效且验证正常结束时 Rejected；保留 request_scope 等其他未知 |
| 四种新契约通过，完整旧行为检查也通过 | pass | 不改 request_scope；其他项未知仍 NeedsAttention |
| 缺事件、skip、无归因的测试失败、候选导致的探针编译不适配、watchdog/panic | unknown | 该检查不产生明确拒绝；若无其他有效违规，则 NeedsAttention |
| Executor 返回设施错误、父取消、证据写失败 | 返回 error，沿用 Runner 失败处理 | 无完成的质量判定；不是候选业务 Rejected |
| 候选/规范/基线或证据摘要不匹配 | 验证错误 | 不发布可信完成判定 |

修改 request_scope 的消去规则、接受未知 helper 或让“探针通过”覆盖范围 unknown，均不在本次实现内。保留响应的正例也不预先承诺 ReadyForReview：只有完整八项检查满足时才可放行。

## 5. 证据、审计与版本

沿用 semantic_behavior、BEHAVIOR-001 与现有规则映射，不改变公开规范。继续归档实际 probe.go.txt、行为命令和双日志；拟增加 behavior-observations.json（独立 schema 1），记录必需集合、逐测试终态、原因码、命令/日志摘要及规范/基线/候选/生产副本/探针/工具身份。由宿主从同一份原始事件生成，不接受模型传入结果。

生产副本在注入前须与剥离测试后的候选逐文件一致；注入后只多出固定可信探针。保存对应清单并核对执行前后副本和 SDK，不能仅给报告填上候选摘要。所有新增材料登记 quality.Evidence，并被 semantic_behavior/finding 引用。审核材料指向规范映射、具体测试观察及原始日志，不把运行时探针的结论包装成精确静态源码路径证明。

审计接入属于完成条件：复核观察材料与命令/输入/原始事件的关联，识别删记录、改日志及跨 Run 引用。对带新观察材料的报告按其声明格式核验；旧报告维持旧格式，不凭当前源码追溯要求新材料。具体审计代码与可信副本清单的复用位置，在第一实现单元核对，不能默认通用摘要校验已经涵盖这些语义关联。

本次规划保持现有 Evaluation schema、八项 Policy、Profile 与 VerifierVersion 家族，使用实际 MaterialsSHA256 区分实现；新增局部材料自带格式版本，不改 sourceEnvelope。若实现发现消费者必须改变既有结构契约，则先调整版本方案，再实现；不得原地修改已发布 Profile。新实验要冻结新的实际实现身份，旧报告和参考 v3 数据不回写。

长期原则已由 [ADR-002](../adr/002-evidence-bound-quality.md) 和 [ADR-005](../adr/005-versioned-evaluation-data.md)覆盖；当前仅作场景内增强，不新增 ADR。若以后引入跨场景执行框架或新的防伪信任边界，再单独决策。

## 6. 实现顺序与可测验收

| 单元 | 交付 | 验收与停止条件 |
| --- | --- | --- |
| A：探针与本地归因 | 四种观察、固定必需集合、事件归约及证据格式 | 基线/正负对照、标记与测试错配、重复/缺失/skip、无 finding 失败、panic/watchdog/编译失败；出现假拒绝先修归因，不继续集成 |
| B：门禁与审核/审计 | 同一受限命令、可信副本身份、观察材料、条款和链接 | 真实 Docker：原样候选 Rejected；完整正例不回归；未知 helper 探针通过仍 NeedsAttention；删改/跨候选材料不被认可 |
| C：完整交付 | 受控模型通过现有 Runner/Campaign 交付新 Run | 预算包装、补丁重放、质量报告、审核导航和离线审计一致；实际容器清理；不重跑真实模型来代替机制验收 |

B 的对照至少包含：原样响应丢失、保留响应、吞错、提前关闭 Body、改变 Close 错误、等价变量改名/分支重排，以及未支持 helper。等价写法可以保留范围 unknown，但新行为检查不得误判语义违规。旧 CTX/TIME/资源违规和既有可放行样本都需回归。

C 至少包含一个完整可放行正例、一个明确返回值违规和一个未知修改。对照保持同一冻结输入，生成使用可控脚本；本次不扩大失败隔离/恢复边界。若新材料不能通过审计或链接到原始证据，模块测试通过也不得宣布该能力交付完成。

## 7. 本轮完成情况

已核对实际门禁执行顺序、探针、事件解析、质量聚合和版本约束，完成上述取舍与验收设计。没有修改运行代码、参考数据或候选，没有执行 Go/Docker/模型实验；表中结果均为未来验收条件。下一步先实施 A，验证事件归因和正负对照，再进入门禁及交付集成。

文档检查：本轮四份文档的 210 个本地链接目标、29 个锚点及代码围栏检查通过，git diff --check 通过；记录保存于 runs/ghcli-runtime-response-design-01/documentation.json。同步修正场景 README 中已过时的活跃参考 v2 描述为 v3，未更改任何冻结材料。

## 8. 单元 A：返回值探针与事件归因

- 实现基线：e7f280e；日期：2026-10-23。
- 交付：[返回值探针](../../scenarios/rpcx/ghcli/probes/returns_test.go.txt)、[本地事件归因](../../scenarios/rpcx/ghcli/observations.go)和[机制/校准测试](../../scenarios/rpcx/ghcli/observations_test.go)。

新增探针单独存放，复用同一 invokeHTTP 边界，便于在原 v1 上单独校准纯返回值义务；单元 B 再与旧探针一起安装到同一生产副本、由同一行为命令运行。本单元没有修改 behavior.go 的执行路径，没有产生新 Run 或更新原质量判定。

四项契约只输出固定 BEHAVIOR-001 原因码，不输出任意响应或错误正文。归因函数核对固定包、测试目录、父子生命周期和退出状态；只有完整必需事件中，同一失败叶子报告了其允许原因，才输出 fail/findings。标记错配、缺失/重复事件、skip、panic、编译失败、无归因失败等均输出 unknown，且清空明确 findings。保守策略也覆盖“一项已有有效反例，但另一项执行不完整”的情况。

局部 schema 1 先保存包、检查状态、逐项终态/原因与失败诊断；这是归因结构，不是已绑定输入的完整证据。单元 B 仍须增加命令/输入/探针身份及可信副本清单、保存并引用材料、接入审计。现有旧探针的 Rule/Detail 标记也要在 B 中适配固定原因目录，不能直接交给只接受 Rule/Code 的新归因函数。

本地执行只使用固定、已审查的源码片段、标准库与两个本地 SDK，禁止依赖下载；不接受任意候选。本次校准不宣称具有 Docker 隔离或完整迁移正确性。

| 对照 | 实际观察 |
| --- | --- |
| 原 v1 基线 | 四种契约通过 |
| 原样 v2 错误候选 | response_missing_on_redirect_error |
| 移除清空响应分支的 v2 对照 | 四种契约通过 |
| 吞掉返回错误 | transport-error 与 redirect-declined 均报告 error_chain_changed |
| Close 丢弃底层错误 | success 与 use-last-response 均报告 close_error_changed |
| 提前关闭 Body | success 与 use-last-response 均报告 body_closed_before_caller |

首轮 first.jsonl 有 4 项测试及子测试失败，其中三个 v2 子进程因沙箱无法读取默认 Go 缓存而编译失败，父测试随之失败；原 v1 与原样 v2 子项当轮通过。失败结果保留，没有把编译失败算成契约违规。后续使用 runs/go-cache 作为 GOCACHE；calibration.jsonl 的 36 项测试及子测试通过。补充提前关闭 Body 对照后，整个 ghcli 包的 race.jsonl 共 103 项测试及子测试通过、2 项 Docker 专用测试跳过，无竞态报告。跳过不计 Docker 验收；go vet 通过。

证据保存在 runs/ghcli-runtime-response-a-01/；最终竞态日志包含六种输入的源码/探针摘要、结构化观察及原始子进程事件。未读取 .env、调用模型、执行 Docker 或修改冻结规范/参考数据。源码材料采用既有嵌入方式，因此当前实现材料摘要会变化，不能把旧材料摘要用于未来实验；这不代表新探针已经进入生产执行路径。

下一步进入 B：接入旧/新探针的完整目录与受限命令，绑定可信副本及执行身份，完成报告和离线审计正反例，再用真实 Docker 验证 Rejected、可放行与未知边界。

## 9. 单元 B：生产门禁、审核材料与离线审计

- 实现基线：8f736a8；日期：2026-10-23。
- 范围：接入已有 Evaluate 行为阶段、保存完整观察材料、复用审核链接并增加离线审计；完整 Runner/Campaign 交付仍归 C。

### 接入与证据归属

新旧探针安装到同一个剥离原测试的生产副本，以同一条既有受限命令运行；保留原有真实到期检查和资源/超时限制。旧探针改为固定 Rule/Code 标记，各子场景分别限定允许的原因与条款；新旧事件由同一个本地归因函数消费。行为检查仍在 request_scope=unknown 时执行，未修改静态消去规则或质量聚合原则。

新增 behavior-observations.json：包含 schema 1、规范/基线/候选绑定、实际工具材料身份、命令与双日志摘要、候选生产文件投影、注入后文件清单、两个 SDK 清单及逐测试观察。注入后的实际目录必须等于候选生产投影加两份固定探针；原 command 方法继续在执行前后核对业务副本和 SDK。所有材料登记为 quality.Evidence，semantic_behavior 及具体 finding 引用观察、探针、命令/日志和 rule-map。

审核渲染沿用现有证据导航，无须新建报告页面：具体原因能链接到测试观察、原始日志和规范映射。测试确认 response_missing_on_redirect_error、对应测试身份、BEHAVIOR-001 及三种链接都能呈现。

### 离线核对与兼容

[ValidateBehaviorEvidence](../../scenarios/rpcx/ghcli/behavior_evidence.go)不执行候选。它核对固定证据清单、摘要与绑定、命令所属原始 Run 目录、实际候选/可信副本/SDK、归档工具中的探针字节，并从原始 stdout 重新计算观察和 semantic_behavior。仅修改结论，或改日志后仅重算外层摘要，都不能通过。证据读取按固定顺序，避免多个材料缺失时因 map 遍历顺序产生不同诊断。

[Runner 审计适配](../../scenarios/rpcx/runner/audit_behavior.go)以已经固定摘要的归档工具中是否包含 behavior_evidence.go 识别新格式；主动声明该观察材料的报告也必须接受检查。新格式已执行行为检查时，删除观察材料或引用不能降级为旧格式。旧材料不追溯要求新增文件，构建前置失败导致行为阶段 blocked 的记录也不伪造观察。归档迁移继续使用原始执行目录身份，读取路径由 audit.View 限制在当前归档内。

局部 schema 1 的必需集合和原因语义构成该格式的消费契约；未来若改变它们导致旧观察不能重算，须另起局部格式版本。Evaluation、Profile、VerifierVersion 家族及公开规范保持既定契约，通过实际 MaterialsSHA256 区分实现；独立参考和历史报告不修改。

### 验证记录

首次 Docker 命令因沙箱无法访问本机 socket 失败，记录在 docker.jsonl；随后在已授权的本机 Docker 权限下使用新目录执行。首次 focused.jsonl 的失败来自两个测试构造问题：对 pass 样本设置 pass/替换不存在标记没有实际改动，以及审核断言未处理 HTML 转义。修正测试后通过，不将这些失败解释为已证实的生产缺陷。另一次目录细化编辑残留旧代码导致编译失败，保留 final-focused-race.jsonl；清理后定向检查通过。

验证产物均位于 runs/ghcli-runtime-response-b-01/。实际结果见下表及最终验收说明。本轮未读取 .env、调用模型或修改冻结规范/参考；开发对照不计新的迁移效果指标。


| Docker 对照 | 完整门禁结果 | 行为检查 |
| --- | --- | --- |
| 仅在原边界补显式期限 | ReadyForReview | pass |
| 原样响应丢失候选 | Rejected | fail，response_missing_on_redirect_error |
| 恒等 helper | NeedsAttention | pass，静态范围仍未知 |
| 吞掉错误 | Rejected | fail，error_chain_changed |
| 提前关闭 Body | Rejected | fail，body_closed_before_caller；原资源检查也保留违规 |
| 丢弃 Close 错误 | Rejected | fail，close_error_changed |
| 60 秒错误期限 | Rejected | fail，actual_expiry_changed 等原期限原因 |
| 仅变量改名 | ReadyForReview | pass |
| 等价分支重排 | NeedsAttention | pass，未扩大静态支持范围 |

完整矩阵分为 docker-verified.jsonl 的七种输入及 docker-equivalent.jsonl 的两种等价写法；共九种输入，没有挑选最优候选。最终源码另在 docker-final.jsonl 单独复验原样候选及八项审计负例，仍为 Rejected。早期矩阵的工具材料为 dba292d7a62a0c7cf84bfe6cb1a563fef335060252c535cfaa6e046f257dd9b6；最终源码复验为 3319f7f4d04cccc963618a357e6d31edc74345e6ffe9105517e7f835ec00e064，分别按实际身份保留。后补的目录归属检查、按子测试细化目录和确定性读取顺序由最终定向竞态检测覆盖；final-recheck.json 用最终读取器逐份重算了全部十份报告，均通过。

审计机制测试覆盖删除记录/引用、改变观察、修改日志并重算摘要、跨候选、跨 Run、改变生产投影/探针/报告、改变实际可信副本/SDK。Runner 格式选择测试区分旧记录、新格式缺材料、主动声明材料和未执行的 blocked 阶段。真实 Docker 报告已由同一离线消费函数复核；完整 Campaign 审计和生成的审核导航仍作为 C 的验收项，不以这次独立 Evaluate 代替。

| 最终检查 | 结果与证据 |
| --- | --- |
| ghcli 与 runner 包级竞态回归 | 252 项测试及子测试通过，26 项条件测试跳过；package-race.jsonl |
| 最终归因/审计/审核/校准竞态检测 | 82 项测试及子测试通过，1 项已有条件测试跳过；final-mechanism-race.jsonl；不与包级重复计数相加 |
| 真实 Docker | 九种输入及最终原样复验全部符合预期；三份 docker 日志、各 evaluation.json 与 results.json |
| 最终离线复核 | 十份报告通过；final-recheck.json |
| 静态检查 | ghcli/runner 的 go vet 通过；vet-accepted.log |
| 精确容器回收 | 十份报告的 60 个唯一容器 ID 全部不存在；container-cleanup.json |

回收核对脚本首次大小写敏感匹配未识别 Docker 的小写 `error: no such object`，其原始 stderr、空 existing 集合和退出码保留在 container-cleanup-first.json。按大小写无关方式重读相同原始结果后，逐 ID 均确认已移除；没有因此删除或操作其他容器。

B 在上述门禁、观察材料及离线消费范围验收。下一步 C 用脚本化模型经预算包装、仓库工具、Runner/Campaign 交付三种判定，验证补丁重放、审核导航与实际 Campaign 审计；不需要追加真实模型调用，也不改变历史实验指标。本单元代码、测试及对应文档作为一个工作单元提交。

本轮五份文档的 247 个本地链接目标、31 个锚点及代码围栏校验通过，git diff --check 通过。最终日志、十份复核结果、回收证据及其摘要汇总在 runs/ghcli-runtime-response-b-01/summary.json；运行产物不入 Git。
