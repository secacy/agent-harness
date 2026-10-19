# M3.4：开发评测协议与独立参考设计

- 日期：2026-10-19。
- 状态：Goddd、Translation 双配置与 Workouts 回调的单候选独立参考在声明范围内已验收，见第 10—12 节。轮次汇总机制及受控 Campaign 关联已验收，见第 13 节；完整人工复核及协议发布待完成，未运行真实模型评测。
- 对应里程碑：[M3](../MILESTONES.md#m3多仓库批量闭环)。
- 代码基线：009d56a；前置：[三仓确定性 Campaign](2026-10-19-m3-4-heterogeneous-campaign.md)。
- 本轮交付：确定首轮任务纳入、独立参考的边界、未知结果和指标口径，为实现提供验收条件。

## 1. 当前阶段与目标

M3.4 已有三个开发切片的离线基线、运行时门禁、单仓库交付及确定性异构批次。剩余目标是取得真实生成候选，并回答迁移是否符合规范、门禁是否错误放行，以及完成或失败各花了多少。

接下来有两条准备工作：本记录负责参考标准与评测口径；供应商能力上界认证、真实预算配置及批次命令另作工作。两者可独立推进，均就绪后才运行首轮真实开发批次。本轮没有创建 oracle 工具、可执行评测协议或付费命令。

## 2. 目标优先级与方案选择

优先保证判据先于真实生成结果确定、错误与未知可区分、同一候选可离线复核，其次控制只有三个样本时的实现和人工成本。

| 方案 | 在当前项目上的得失 | 选择 |
| --- | --- | --- |
| 以原门禁或其行为探针再次通过作为参考 | 可以立即统计，但原探针遗漏的分支仍会遗漏，无法检查错误放行 | 不采用 |
| 所有环节重写成另一套自动验证器 | 实现隔离更彻底；会重复材料认证、执行、事件解析和结构分析，且仍不能自动证明任意业务重构正确 | 首版不采用，优先把独立实现投入关键语义 |
| 独立行为测试，加逐条源码/交付复核；共用受控执行和材料基础设施 | 能用另一条证据路径验证 ctx、期限及业务事件，源码复核补足测试与覆盖边界；需要人工记录，不能仅凭自动测试通过判为正确 | 首版采用；接受人工复核成本与共同基础设施盲区 |

“独立”先指参考判据和关键检查实现独立于运行时门禁：不调用三场景 Evaluate、analysis、testintegrity 或 quality.Decide 来形成参考结论，不复制其 probes，也不读取 case 的门禁预期充当正确性答案。允许复用 execution、冻结输入读取、L2 离线依赖装配及摘要/文件操作。L2 装配代码间接使用 gate 的文件函数，因此按实际复用函数核对边界，不以“整个依赖图不含 gate”作为虚假的独立性条件。

同一开发者/助手仍可能写出相同理解错误；报告记录作者与复核者身份及已见材料，不声称独立人员盲审。若没有人工逐条复核，完整参考结论保持 inconclusive，可另外展示自动参考通过数。留出样本的独立准备要求不因此解除。

## 3. 首轮任务与尝试口径

拟议协议标识为 `l2-dev-eval-v0.1.0`，本轮仅保留设计名称。参考材料、工具、模型能力与预算参数齐备后再发布带完整摘要的协议，不能用空摘要或待定数值宣称已冻结。

| 开发任务 | 固定数据 / Profile | 必需构建范围 |
| --- | --- | --- |
| l2-goddd-booking | rpcx-l2-goddd-v1 / rpcx-goddd-t3-v1，[规范 v0.3.0](../../scenarios/rpcx/goddd/specs/v0.3.0.md) | default |
| l2-clean-translation | rpcx-l2-translation-v1 / rpcx-translation-t3-v1，[规范 v0.4.0](../../scenarios/rpcx/translation/specs/v0.4.0.md) | default 和 rpcx_alternate；一份候选、一个 Target |
| l2-workouts-cancel | rpcx-l2-workouts-v1 / rpcx-workouts-t3-v1，[规范 v0.5.0](../../scenarios/rpcx/workouts/specs/v0.5.0.md) | trainings/default 和只读 common 前置模块；一个 Target |

首轮为一个 Campaign、三个 Target，每 Target 一次尝试，不自动重试，也不挑选最佳 Run。纳入任务总数 N=3；Run 数另列，不能因以后加入重试仍混用分母。并发与执行槽位拟沿用已验收的 2/2；模型、每 Run 调用/工具/时间限制、Token 额度及批次预算在能力认证工作中确定，并在运行前写入协议。脚本模型的 90/10 上界和 120 Token 结果不能用于真实配置。

协议发布前做结构和环境准入；错误配置未准入时不创建效果轮次。协议发布并开始执行后，基线失败、输入读取失败、未启动、超时、额度不足、生成/设施/发布失败都留在 N 中并单列原因。若整批被人工取消，标记该轮未完整执行，保留全部任务，不把已完成子集当完整轮次。故障注入、手写候选校准、人工修复和事后诊断重跑另表记录，不回填原轮次的成功；再次真实生成必须另立轮次，保留旧结果和全部成本。

三个来源均已参与开发，不提供严格未见估计。`l2-heldout-01` 尚未准备，不在 N 内。M4 冻结正式协议时另行复核留出依赖；本设计不替代 M5 的正式评测。

## 4. 参考标准检查什么

先检查候选生产代码的迁移契约，再结合源码复核检查测试保留、未覆盖变更和交付依据。参考不要求与 correct.patch 相同，也不因 helper 超出门禁模板就认定迁移错误。

| 范围 | 独立行为与复核依据 |
| --- | --- |
| 公共 ctx/期限 | 记录实际 RPC 收到的 value、父取消/过期、较短和较长父期限；无父期限时逐次 30 秒；返回后子 ctx 释放而父 ctx 未被取消。参考后端协作等待实际 ctx.Done，验证真实到期；失败/重复调用另测，避免只检查源码前缀 |
| Goddd | 从 HTTP 请求经过 booking 和两个装饰器到 RPC；用不同于门禁的数据检验路线/航段顺序与完整映射、未知货物零调用、远端错误及空成功结果的既有 HTTP/空路线行为。对照请求 ctx 与构造器保存 ctx，定位 CTX-001—003、TIME-001—003、BEHAVIOR-001 |
| Translation | 两配置分别运行，从 usecase 经两层 tracing 到真实适配器；检查各字段、可解包错误、RPC 成功才 Store、Store 错误零结果/错误链、History；独立核对活动源文件及受保护 mock 实际参与测试，定位 BUILD-001、CTX-001、TIME-001—002、BEHAVIOR-001 |
| Workouts | 可信仓储给 callback 独立 ctx，与 Handle ctx 有不同值/期限/取消来源；观察 users→trainer 的事件、参数、零余额分支、错误链及成功才提交。第一调用延迟后第二调用仍重新计时；trainer 失败后保留已发生远端余额效果，定位 CTX-001、TIME-001—002、BEHAVIOR-001 |
| 硬约束与原测试 | 独立比较依赖/保护路径；每配置构建并完整运行候选测试，核对原测试及子例事件；源码复核原断言、表数据、helper、标签及调用链，记录允许的 API/ctx 适配。common/SDK 使用认证输入；仅有相同测试名不足以通过 TEST/PATH/DEP 条款 |
| 交付与范围 | 在候选参考结论写入后，复核审核报告的规范、候选、配置、命令/日志归属及 SCOPE 条款；区分“候选违反迁移契约”与“交付声称已证明但证据不足”。发现可证实的错误放行依据时单列原因类别 |

参考生产副本删除候选 `_test.go`，安装新编写的参考测试；候选原测试在另一个副本执行。参考测试与生产摘要绑定，不能把测试替换后的文件树当作原候选全树摘要。源码复核记录具体文件/变更、条款、证据和结论，无法证明的分支标未知；不要求证明任意程序等价性。

时间容差、必需测试事件和超时/清理上限在参考实现时先用已知对照校准，作为材料发布；真实生成后不根据候选表现放宽。计时缺乏分辨率、进程超时或后端不能回收均留下证据缺口，不能只凭超过等待时长判定违反 30 秒契约。v1 与迁移后不做整体等价比较：请求 ctx 传播是新要求，v1 没有相同取消行为。

## 5. pass、fail 与 inconclusive

自动参考、源码复核、交付复核分别保存结果；完整独立复核由三部分聚合。

| 观测 | 处理 |
| --- | --- |
| 所有适用必需项有通过证据，源码及交付复核完成 | pass；含义限于本协议列明的切片与契约 |
| 认证候选下出现可定位条款的确定违规 | fail，保留反例、观测和日志；同时有未知项也不抹掉已确认违规 |
| 必需事件缺失、跳过、复核未完成、计时不足、参考测试不能适配合法重构 | inconclusive；不把测不到或 oracle 自身编译失败当候选错误 |
| 认证的候选自身在必需配置编译失败 | BUILD 条款 fail；与“装入参考测试后才编译失败”分开记录 |
| 输入摘要漂移、报告绑定不符或证据损坏 | inconclusive，并标记评测有效性错误；不允许靠无效输入宣布 fail/pass |
| Docker/工具/取消等设施问题 | 受影响项 inconclusive；仅保留已认证快照上仍可独立复核的确定发现，不把命令非零一概当语义 fail |

参考工具不会修改原 Run 的 verdict。`NeedsAttention + 参考 pass` 表示正确但未自动放行，不计有效自动成功，也不自动当作门禁误判；`ReadyForReview + inconclusive` 表示放行结果尚未证实。等价 helper 必须能在源码复核后得到 pass，不能被参考检查复制的 AST 模板永久挡住。

## 6. 最小工具与材料边界（待实现）

拟新增 `scenarios/rpcx/l2/evaluation/` 存放活动工具，参考数据另行版本化，沿用 [ADR-005](../adr/005-versioned-evaluation-data.md)。先做显式场景选择及数据结构，不引入规则 DSL 或通用评测插件。实现提供两个可分别调用的步骤：

1. 单候选参考：输入受认证的任务/规范、候选路径及摘要、空证据目录、执行器；输出逐项参考结果与材料/工具/输入身份。接口不接受原 gate verdict、校准 expected 或 Agent 自述。
2. 轮次汇总：读取已发布 Campaign/Run 报告与参考/复核记录，严格以 Target/Run/候选摘要关联；输出逐任务明细和计数。缺记录保留未知，跨 Run 引用或摘要冲突报有效性错误；不重跑或覆盖原门禁。

报告至少保存协议/数据/规范/Profile/候选/工具身份、配置、逐测试/条款结果、命令/双日志摘要、后端/容器和清理结果。人工复核保存作者、时间、材料范围、理由与可选实际活动耗时；无人计时则耗时未知，不从候选数量推算人工成本。

参考材料、expected 和校准补丁仅供离线宿主读取，不装入 Runner 的 Agent 根目录，也不向运行时门禁提供。工具源码可以公开审查；可见性保证是执行输入隔离，不宣称在同一开发者上下文中实现了真正的信息盲审。

## 7. 指标：先计数，再算比例

N 为已纳入的 Target 数，R 为实际 Run 数，Q 为取得候选质量 verdict 的 Target 数，A 为 ReadyForReview 数，H 为 NeedsAttention 数。首轮每 Target 一次，N=3，通常 R=3；未启动可以使 R<N。无 verdict 的任务单列，不能从 N 删掉。三个构建来源计三项，Translation 两配置不增加分母。

- **有效自动成功：**S 为原 Run Completed、ReadyForReview 且完整独立复核 pass 的 Target 数，报告 S/N。u_s 为满足上述运行/放行条件、但完整复核未知的数量；复核未完整时给出可能范围 [S/N, (S+u_s)/N]，不把下界叫完整效果结论。未完整交付或 NeedsAttention 即使参考 pass 也不进入 S；它们不因补齐参考就有自动成功资格。
- **错误放行：**k 为 ReadyForReview 且完整复核 fail 的数量，u 为放行后仍 inconclusive 的数量。A=0 时不可计算；A>0 且 u=0 时为 k/A；u>0 时报告 k、u、A 及 [k/A, (k+u)/A]，不是统计置信区间。fail 分列迁移错误和交付依据错误，同一候选只计一次；再分别列自动参考与人工复核的结果，不能把自动参考通过当完整 pass。
- **需人工确认：**同时报告 Target 比例 H/N 与候选判定比例 H/Q；Q=0 时后者不可计算。列出已知缺口类型，不将比例换算为审核耗时。
- **成本和失败：**保留每个 Target、全部 Run 与 Campaign 的准入/拒绝数、已知 Token、未知保守占用、耗时及失败原因；使用 Loop 尝试数与账本准入数的既有含义。未知占用不是已知实际消费，既不能按零计算，也不能与已知消费冒称供应商账单。费用缺价格依据或使用明细则标未知。Run/Target/Campaign 的汇总是同一费用的不同视角，不能再次相加。

算例仅用于口径检查：三项任务分别为 ReadyForReview/参考 pass、NeedsAttention/参考 pass、执行失败，则 N=3、Q=2、A=1、H=1、S=1、k=u=0；自动成功 1/3，错误放行 0/1，人工确认分别 1/3 与 1/2。若第一项复核未完成，则 S=0、u=1，自动成功范围 [0,1/3]，错误放行范围 [0,1]。这不是实际项目结果，小样本 0/1 也不构成普遍可靠性证据。

## 8. 实现顺序与进入真实评测的条件

1. 实现一个候选的参考接口和 Goddd 的独立行为检查/人工复核记录；先用 correct、missing-timeout、mapping-helper、删断言及保护路径等对照验证。单场景证明参考能发现确定违规、接受经复核的等价重构、保留测量和设施未知。
2. 沿相同报告契约补 Translation 两配置与 Workouts callback/两客户端；添加三仓校准汇总，另做协议/候选绑定及指标汇总的机制测试。供应商能力与真实 batch 入口可同期另作工作，不依赖参考实现完成。
3. 发布完整协议及参考材料，确认模型配置和预算准入后再运行真实开发批次；保留全部任务、候选和原 verdict，生成单独 experiments 记录。

| 进入条件 | 必须验证的对照 |
| --- | --- |
| 参考没有直接沿用门禁结论 | 参考接口无 verdict/expected 参数；调用与材料审查确认不使用原语义探针或规则函数；参考输出完成后再关联原 gate |
| 正确、违规、未知可分 | 三仓 correct 参考 pass；明确语义/保护/测试违规 fail 且有条款；mapping-helper 经人工源码复核可 pass；参考编译失败、缺事件、超时、未复核保持未知 |
| 不靠名称或参考补丁字节判定 | 测试期构造另一种合法改写和同名错误候选，按实际条款/证据判定；校准 expected 只参与运行之后的对比 |
| 配置、身份和失败不漏计 | 漏 alternate、换候选摘要、错 Run、缺参考报告、未启动/预算拒绝均有显式结果；输入损坏不冒充候选语义失败 |
| 指标可从原记录复算 | 覆盖 A=0、Q=0、已知错误+未知、NeedsAttention 参考 pass、重复/缺 Run；保留 N 并核对算例，重复引用不重复计费 |
| 发布与输入隔离 | 参考材料/工具各有真实摘要，旧数据不改写；真实轮次前核对 Agent/门禁可见材料，清单无留出输入 |

停止/调整信号：合法 helper 普遍因 oracle 无法适配而未知，或新增 oracle 只能通过复制门禁模板工作时，先缩小参考自动化范围并补源码复核，不为凑齐三个样本再建一套同样的分析器。没有复核者时报告未知及自动观察，不能为得到一个成功率把未知改为 pass。

## 9. 本轮结果

已读取三份规范、场景门禁说明、L1/T3 参考接口、L2 材料装配和 M3 指标定义。确认旧 M3 worklog 的“尝试数”与里程碑的 Target 分母需要澄清，本记录以每 Target 一次及 N/R 分列落实；明确参考未知不能直接记错误放行。

本轮仅完成设计与文档维护。102 个本地链接目标、16 个文档锚点、代码围栏及 git diff --check 通过；以一次性算例核对已知/未知、零分母及未完整交付不计潜在成功的公式。没有运行 Go 测试、Docker 或真实模型实验。参考实现、校准、真实模型配置和付费批次均未执行，模型预算数值仍待能力核实；没有读取 .env。

## 10. Goddd 单候选参考实现与验收

本单元基于 65da32b 实现[单候选参考 API](../../scenarios/rpcx/l2/evaluation/README.md)。本节是后续实现记录，第 9 节保留设计提交当时的事实。

### 实现选择与报告边界

新增 evaluation 包，先只接受认证的 Goddd 基线/SDK 和规范。Request 绑定 Target、Run、候选路径及全树摘要，不接受运行时质量结论。Go 的 `mod edit -json` 独立解析基线和候选模块声明，按规范变更 require/replace 后比较其余配置；不复用门禁的模块迁移规则。原生产/测试候选、剥离候选测试后安装独立参考的生产副本分别保存。源码与数据摘要分开，当前参考数据 v2 的 freeze 摘要为 `5cd44c1d8c00f147809d41545086ce1c32ae7cfcc0f6bb47b867c8a7601715c7`；早期校准数据 v1（freeze `25471d9a65936cf18af69416f141d9f975235e04ad629e4db5563d75e3f93abb`）保留。旧 fixtures/spec/cases/oracle 不修改。

参考测试从公开 HTTP 入口经过真实 booking 与两个装饰器观察 RPC，用不同业务数据检查两条路线的航段/时间/顺序、空成功与远端错误；构造器 ctx 故意已经取消且与请求有不同值。新测试覆盖短/长父期限、取消/过期、父取消到后端、释放及重复调用；正常默认期限另实际等待约 30 秒。数据固定 250ms 的 deadline 测量容差、1s 的启动观察窗口及 29—33s 的实际到期窗口。默认期限以上达后端的时刻为观察依据；过早的期限仅在无法由请求发送到后端间的任何合法调用起点解释时确认违规，否则记测量不足。测量超窗或安全停止同样 inconclusive，不根据候选结果放宽容差。

Automatic 由六项自动检查聚合；缺事件、参考测试自身编译失败和设施错误保持 inconclusive。新参考的源代码边界测试禁止直接复用场景验证、analysis、testintegrity、quality/checks 或 gate 的语义函数；允许共用文件、身份与离线模块/执行基础设施。实际共享源码随报告保存，ToolSHA256 表示这些列明材料，不声称是整个执行环境或全 Harness 的摘要。

每次输出 review-template.json，测试保留、生产改动覆盖及交付依据全部待复核。Assess 校验原报告字节、Target/Run/候选身份、复核者/时间/理由/定位，输出新的 Assessment。未知项不抹掉稳定输入下的确定违规；输入绑定失效则保留未知。`development_calibration` 与真实 `candidate_review` 分列；工具没有冒充独立人员完成复核，也没有在没有交付材料的校准上预填 SCOPE 通过。

### 失败与修正

首轮真实校准日志 `runs/m3-4-goddd-reference/local-first.jsonl` 整体失败。参考工具把库工程的 `go build -o <directory> ./...` 返回“no main packages to build”误作候选构建失败。修正为丢弃构建输出，并增加认证基线的同参数构建：基线本身失败属于参考执行错误，不能作为候选 BUILD 违规。新故障注入分别覆盖基线失败和候选失败；原失败日志保留，不将其算作迁移结果。

收尾新增的材料篡改对照在 `regression-race.jsonl` 捕获一个登记错误：materials 根被加到了 SDK 复制列表，遗漏在最终快照核对列表之外。已修正，最终回归实际覆盖材料与日志篡改后不得保留通过状态。

参考源码审查还发现 v1 按 HTTP 发送前时刻推算期限，存在把 RPC 前合法延迟误判为 TIME-002 的风险。这是参考判据错误，不能通过改正确候选来消除。保留已使用的 v1，另发参考数据 v2，依据后端观测及可能调用起点区间区分确定违规与未知，并新增发送前 400ms 延迟的正确控制。v2 全部重新校准；此前报告留在原目录，不冒称已用新数据重验。

### 实际校准与限制

六份既有校准补丁仅供测试宿主构造输入，expected 不传入 Evaluate。正确与 mapping-helper 的自动参考通过；missing-timeout、background-fallback、protected-license 分别得到 TIME-001、CTX-003、PATH-001 的确定违规。deleted-assertion 的测试事件与行为自动通过，模板整体仍保持 inconclusive。助手进一步核对其 `cargo_test.go`，确认 TestConstruction 删除 RoutingStatus/NotRouted 原断言，记录 TEST-001 的开发源码发现并经 Assess 得到 fail；人员字段明确标注助手开发校准检查，其他复核项仍未知。该记录不代表独立人工复核或真实模型成绩。

两个新增测试期变体再检查参考没有照搬结构模板：使用 WithDeadline 的等价迁移自动通过；把期限改为 5s、但保留 correct 运行名称的变体由实际 deadline 观测得到 TIME-002。名称和参考补丁字节均不是候选判据。

| 验证 | 结果 | 证据 |
| --- | --- | --- |
| 当前 v2 本地校准 | 八个候选符合预期：自动 4 pass / 4 fail；正确、helper、WithDeadline 的完整结果均因复核未完成保持未知；删断言样本另有明确标注助手身份的开发源码发现 | `runs/m3-4-goddd-reference/v2-local.jsonl`、`v2-local/*/reference/reference.json` |
| 当前 v2 Docker 主要对照 | correct、missing-timeout、mapping-helper 分别自动 pass / fail / pass；同一工具材料摘要，与本地一致；通过组含实际约 30 秒到期及发送前延迟控制 | `runs/m3-4-goddd-reference/v2-docker.jsonl`、`v2-docker/*/reference/reference.json` |
| 当前工具及相关回归 | evaluation、L2 baseline、Goddd 三个有测试包的竞态回归：117 项测试及子测试通过、2 项显式验收跳过，无失败或竞态；包含身份、材料/日志漂移、未知事件、oracle 编译失败及复核绑定对照 | `runs/m3-4-goddd-reference/v2-regression-race.jsonl` |
| 源码复核记录的实际消费 | deleted-assertion 的自动行为通过；助手检查发现 TestConstruction 原断言被删，绑定当前 v2 报告后 Assess 得到 fail，kind 为 development_calibration，其他复核项仍未知 | `v2-local/deleted-assertion-1693037896/reference/review-codex-calibration.json`、`assessment-codex-calibration.json` |
| 容器回收 | 本轮七份 Docker 报告记录的 54 个容器 ID 均已不存在，Harness 标签容器清单为空；其中包括保留的早期 v1 校准 | `runs/m3-4-goddd-reference/docker-container-audit.json` |

早期轮次与当前 v2 的结果分开保存，不累加重复测试数。最后的工具材料摘要为 `10008bad317540465060239199538d0523f9ff197b615ff0197075962815db22`；本地与 Docker v2 报告一致。go vet、Go 格式、106 个本地链接目标、17 个文档锚点、代码围栏及 git diff --check 通过，v1/v2 数据逐文件摘要与发布清单一致。没有读取 .env 或调用真实模型，运行材料不入 Git。

本单元验收的是 Goddd 单候选自动参考、未知处理及复核记录接口；不能把待复核模板称为完整独立通过，或由手写对照计算真实模型效果。下一步扩展 Translation 双配置参考，再接 Workouts；轮次统计和真实模型能力/批次入口仍按第 8 节推进。

## 11. Translation 双配置独立参考

本单元目标是在同一候选摘要下，分别取得 default 与 rpcx_alternate 的选中文件、构建、原测试和独立行为证据。它们仍是一个 Target，不增加效果指标分母。实现与验收结果在本节后续补录。

### 实现前的选择

复制 Goddd 全流程最直接，但会复制输入认证、日志绑定和漂移检查，之后容易只修正一份；立即引入可配置评测插件又会扩大可信配置与验收范围。因此仅提取内部的固定场景描述和共用执行流程，保留 Evaluate 的 Goddd 入口，新增显式 EvaluateTranslation。场景描述由宿主源码固定，不允许候选或普通请求增删必需配置、测试与规则。

Translation 的行为参考从规范与冻结业务接口另写，通过实际 usecase 及两层 tracing 进入 RPC，并观察 Store/History；不复制现有 Translation 门禁探针或类型模板。共用文件操作、离线模块装配及执行器；报告归档真实工具源码。代价是仍共享基础设施和 SDK，且同一作者不能提供信息盲审。

两配置分别保留 go list 的实际活动/忽略源文件和 mock 参与信息，以及各自的原测试/行为/真实到期结果。不能用默认构建推断 alternate，也不能把一次测试的事件复用为另一次配置的通过。顶层自动项只聚合，Assess 另校验必需配置完整、配置身份和聚合一致。候选自行构建失败是 BUILD 违规，注入参考后才编译失败或缺事件仍为未知；原测试断言、未覆盖源码和交付依据继续待复核。

验收包括正确、alternate 漏超时、只改默认接口、Background、等价 helper、保护 mock 与删断言；再增加合法 WithDeadline 和同名错误期限，检验参考不依赖补丁字节或名字。机制测试注入缺 alternate 事件、oracle 编译失败、配置选择错误及报告删除配置，完整复核不得因此通过。先运行可信本地校准，再用禁网 Docker 复验主要对照；不读取 .env 或请求模型。

### 实现与判定边界

新增 `EvaluateTranslation`，沿用 Request、Report 与待填写 Review。内部固定场景描述只提取认证、执行与归档流程；没有动态注册插件或用户规则。报告 `Configurations` 固定 default/rpcx_alternate，每项保存 tags、实际活动/忽略实现、mock 参与信息、selection/build/candidate_tests/behavior/expiry。选中文件不符合规范归 BUILD/PATH；顶层 build 同时聚合 selection。Assess 拒绝缺配置、缺检查、错标签、缺选中文件证据或顶层与配置结果不一致的报告。Goddd 的旧报告结构与入口保留。

独立测试从实际 usecase 经 tracing、WebAPI 与 RPC 观察完整 Translation 字段、用户/ctx、RPC→Store 次序、远端与 Store 错误链、零结果、History、短/长父期限、前置取消、父取消、派生释放和重复调用。每配置另实际等待约 30 秒到期；采用固定 250ms 期限容差、1s 准备观察窗口和 29—33s 到期窗口，不能可靠判断的调度情形保持未知。基线与候选均实际构建；Translation 基线还分别跑完整测试作执行控制。候选测试与注入参考的生产副本分开，不能用候选自行新增的断言替代参考。

### 发现与修正

首次 `go test` 因沙箱禁止写用户 Go 缓存而未执行测试，随后使用 runs/go-build，无需读取凭证。源码审查发现早期参考 v1 用 JSON 中转检查内存实参，会把只添加 JSON 标签的等价候选当作映射错误。该夹具的传输不经过 JSON，这属于参考假设错误。v1 材料与已产生报告保留，发布 v2，直接观察内存字段，并新增 tagged-fields 对照；v2 同时统一参考源码格式，不改变旧发布。

v1 freeze 为 `afeb051cd0d7d71eb3a0795a803147b0d8dd1de35c6454a229960be5bbb3c471`，当前 v2 为 `1687c993a76ff6682111f0f6eb6b9662e4bc193b7e92d3d4baa2c2fdcf54acbd`。旧 Goddd 数据、所有场景 fixture/spec/cases/oracle 均不修改。校准名称/预期只存在测试宿主；EvaluateTranslation 不读取补丁或原门禁。WithDeadline 与带标签改写用于检验不依赖正确补丁字节，同名 5s 期限用于检验不依赖名称。

针对参考自身的错误，另从 tagged-fields 的稳定副本构造 `runs/m3-4-translation-reference/v1-tagged-counterexample/`，只装入冻结 v1 探针执行 default 快速叶子。它实际报 BEHAVIOR-001；同一候选的 v2 两配置及真实到期均通过。该反例仅检验旧探针，不伪装成完整参考 Run，也不修改 v2 原证据。命令、原始日志、候选身份与两版参考关系保存在 counterexample.json。

### 校准结果与后续边界

当前 v2 本地十个候选全部符合预期：自动参考 5 pass / 5 fail。correct、mapping-helper、WithDeadline 和 tagged-fields 可通过两配置行为及真实到期，但仍缺完整源码/交付复核，不能计作完整独立成功。alternate-missing-timeout、default-only-api、background-fallback、protected-mock、同名 5s 期限分别保留 TIME-001、BUILD-001、CTX-001、PATH-001、TIME-001 发现；alternate 失败不抹掉 default 的证据。

删断言候选自动 pass，两个配置均保留原测试事件。助手对实际基线/候选 `internal/repo/webapi/translation_rpcx_test.go:27` 核对，确认 TestTranslationRoundTrip 的 method/request 断言被删除。另存 `review-codex-calibration.json`，作者明确标注助手开发校准检查，只有 test_preservation 为 TEST-001 fail，另外两项仍未知。Assess 消费后得到 fail；原模板和自动报告不回写，该记录不代表独立人工复核或真实模型成绩。

| 验证 | 结果 | 证据 |
| --- | --- | --- |
| v2 本地完整校准 | 十个候选符合预期，5 pass / 5 fail；两个通过批次分开保存，不重复计数 | `runs/m3-4-translation-reference-v2-smoke.jsonl`、`runs/m3-4-translation-reference-v2-local.jsonl` |
| 相关四包竞态回归 | 159 项测试及子测试通过，4 项显式验收跳过，无失败/竞态；包含双配置缺事件、编译/选择/取消故障及报告篡改 | `runs/m3-4-translation-reference-regression-race.jsonl` |
| 发布 v2 后的 evaluation 竞态回归 | 62 项测试及子测试通过，2 项显式验收跳过，无失败/竞态；不与上一行重复累加 | `runs/m3-4-translation-reference-v2-evaluation-race.jsonl` |
| Goddd 共用流程实测 | correct / missing-timeout 保持自动 pass / fail；正确候选含真实到期 | `runs/m3-4-translation-reference-goddd.jsonl` |
| 原断言复核记录的消费 | 自动通过但明确源码违规，Assess 为 fail；kind=development_calibration，其他复核项未知 | `runs/m3-4-translation-reference/v2-local/deleted-assertion-1690693593/reference/review-codex-calibration.json`、`assessment-codex-calibration.json` |
| 静态检查 | evaluation 的 go vet 通过 | `runs/m3-4-translation-reference-v2-vet.log` |
| 当前 v2 禁网 Docker 对照 | correct / alternate-missing-timeout / mapping-helper 分别自动 pass / fail / pass；两配置及关键检查状态、候选摘要与本地一致 | `runs/m3-4-translation-reference-v2-docker.jsonl`、`v2-docker/*/reference/reference.json` |
| 容器回收 | v1/v2 六份 Docker 报告记录的 112 个容器均已不存在 | `runs/m3-4-translation-reference/docker-container-audit.json` |

本地与 Docker 当前 v2 归档使用同一工具摘要 `82b8d1a365a2fa844e65e191888d0d18381cb8c7e8fe08424ba8623d2bdd21f7`，已逐份核对候选、工具、参考清单及命令日志摘要。汇总 `runs/m3-4-translation-reference/summary.json` 明确标注 development_calibration，不产生真实模型指标。早期 v1 的本地与 Docker 校准另留原目录和日志，不与当前 v2 相加；旧探针对标签的反例也单列，不覆盖历史报告。

本单元验收 Translation 双配置单候选参考与复核记录消费；原断言、生产改动覆盖及交付材料仍需实际复核，不能据自动 pass 宣称完整独立通过。没有读取 .env、发起真实模型请求或加入付费入口，运行材料不入 Git。下一步按第 8 节补 Workouts callback/两客户端参考，再完成三仓校准及轮次汇总；真实模型能力、批次命令与真实开发评测另行推进。

活动 Go 源码与 v2 参考源码格式、四个版本的参考数据清单、git diff --check 均通过；五份进度/使用文档的 88 个本地链接目标、14 个锚点与围栏检查通过。历史归档与冻结场景输入不改写。

## 12. Workouts 回调与两客户端独立参考

- 日期：2026-10-20；基于 3716310，实施与验收结果在本节补录。
- 目标：补齐第三个开发切片的单候选参考，不在本单元混入轮次统计或付费实验。

### 实现前的取舍与验收条件

仅直接调用两个适配器，能测 ctx/期限，但观察不到 Handle 错误捕获外层上下文、余额为零分支和提交次序；复制运行时探针则无法形成另一条证据路径。因此从真实 decorated Handle 进入自编仓储控制，给回调独立的值/取消/期限；真实 RPC 后端记录参数、空确认、逐调用 deadline、余额远端效果和本地提交。选择保留少量直接适配器对照检验重复调用与错误释放，不能替代 Handle 主路径。只读 common 从认证输入复制，单独保存构建和全部三项测试证据，不从 trainings 成功推断 common 通过。

两个客户端分别执行约 30 秒实际到期，避免合并成一条 40 秒命令导致第二项永远测不到；报告必须同时保留两组必需事件。额度是本地验证时间，不涉及模型费用。既有文件/执行/离线模块装配继续复用，行为参考从规范及冻结业务接口独立编写，不调用 Workouts 门禁、分析器或原探针。

校准包括 correct、trainer 漏超时、Background、外层 ctx、零余额多调、吞错、helper、保护路径与删断言；另构造 WithDeadline、两调用共用期限和同名错误期限。前两者的行为合规性不按生产模板判断；源码/交付复核未完成仍未知。机制注入 common 缺事件/失败、第二客户端缺到期事件、参考自身编译失败及输入/日志漂移，不能获得完整通过。先可信本地，再禁网 Docker 主要对照；不读取 .env 或请求模型。

### 实际实现与首轮修正

新增 EvaluateWorkouts，宿主固定 trainings/common/SDK 与 v0.5.0 的身份，沿用单候选 Request 和三个待复核项目。Workouts 的自动检查新增 local_modules；LocalModules.common 记录只读快照摘要及独立 build/tests，Assess 拒绝缺项或聚合不一致。输入认证之后才执行 common，认证 common 自身失败属于评测执行问题，记录 inconclusive 和 error，不当作候选违反 TEST/BUILD。

生产参考副本剥离 trainings 的候选测试，注入新编写的 adapters_test，common 与两个 SDK 仍从认证基线只读复制。真实命令从 Handle 经 command decorators、独立仓储的真实 callback、生产适配器到两个 SDK 后端。仓储只对副本执行 callback，callback 成功且返回完整取消状态才提交；记录成功的余额远端效果，不模拟跨远端回滚。原领域与其他改动的覆盖仍须源码复核。

default 快速检查包括两角色/时限下的条件余额、零余额、两个远端错误、仓储/已取消前置条件、回调的值/取消/期限、两客户端父取消、重复派生及分次计时。分次对照在 users 后端成功返回前等 500ms，再以 trainer 自己的调用区间判断期限，不能按整个 Handle 开始时间放行。users/trainer 两个 expiry 分别发命令，各自最多 40 秒测试时间、60 秒执行时间；第二个未执行或异常时，expiry 仍未知。固定 250ms deadline 容差、1s 准备观察窗口、29—33s 实际到期窗口及 34s 安全停止，不根据候选表现放宽标准。

首轮机制测试 `runs/m3-4-workouts-reference-mechanisms.jsonl` 失败：尚未执行 common 测试时组合结果含空 Tests map，JSON omitempty 将其省略，Assess 读取后的 nil 与重算的空 map 不相等，误报聚合不一致。修正为空时不创建 map，保留失败日志；`boundaries-fixed.jsonl` 重跑通过。该问题没有把未知放行为成功，但会阻止有效未知记录被消费。实现审查同时去重 common 自身命令的只读挂载，避免 Docker 重复 destination；早期 smoke 与 local/docker 报告另留，不覆盖，最终验收使用 accepted-local/accepted-docker。

当前参考数据独立发布为 rpcx-l2-reference-workouts-data-v1，freeze 摘要 `400e7c32a2e6b9cac3936aa6bd5e2f1c4911d96aeca44fc25a20ff58dab6f669`；报告保存实际使用的数据、工具源码、规范、基线、候选、common、命令与双日志身份。旧 Goddd/Translation 数据及所有场景 fixtures/specs/cases/oracle 未修改。Workouts 采用第七个 local_modules 自动项，旧两个场景的六项结构仍可消费。

### 验收结果与限制

accepted-local 的十二个候选均符合校准预期，自动 4 pass / 8 fail：correct、helper、WithDeadline 与删断言自动通过；trainer 漏超时、Background、外层 ctx、零余额多调、吞错、保护路径、回调共用期限与同名 5s 期限均有对应 TIME/CTX/BEHAVIOR/PATH 条款。共用期限变体仍从正确 callback ctx 派生一个 30s，但两个适配器不再单独派生；参考通过第二次调用的实际期限观察发现 TIME-001，而非检查它是否长得像正确补丁。

删断言样本在 `adapters/cancellation_rpcx_test.go:83` 删除余额 method/request 比较失败时的 t.Errorf，保留原测试名和空 if 条件。助手核对实际基线/候选后另存 development_calibration 的 review，test_preservation 为 TEST-001 fail，作者明确标注不是独立人工复核，其他两项未知。Assess 得到 fail，原自动报告和待填模板不覆盖。其余自动通过样本也仍需实际源码与交付复核，不计完整独立成功。

| 验证 | 实际结果 | 证据 |
| --- | --- | --- |
| 最终可信本地校准 | 十二个候选符合预期，4 pass / 8 fail；通过组各含 users/trainer 两次实际到期 | `runs/m3-4-workouts-reference-local-final.jsonl`、`accepted-local/*/reference/reference.json` |
| 最终禁网 Docker 主要对照 | correct / outer-context / mapping-helper 分别自动 pass / fail / pass，候选摘要及关键检查状态与本地一致 | `runs/m3-4-workouts-reference-docker-final.jsonl`、`accepted-docker/*/reference/reference.json` |
| 最终三包竞态回归 | evaluation、L2 baseline、Workouts 共 140 项测试及子测试通过，4 项显式验收跳过，无失败/竞态；包含第二 expiry 异常、common 缺事件/执行失败、输入漂移及报告篡改 | `runs/m3-4-workouts-reference-race-final.jsonl` |
| 共用流程真实回归 | Translation correct 两配置通过，包含两次实际到期，自动通过配待复核模板仍未知；Goddd 机制回归在上一行内 | `runs/m3-4-workouts-reference-translation.jsonl` |
| 删断言记录消费 | 自动 pass，经有源码定位的助手开发复核得到 fail，不冒充独立人员 | `accepted-local/deleted-assertion-1469139847/reference/review-codex-calibration.json`、`assessment-codex-calibration.json` |
| 容器回收 | 早期及最终六份 Docker 报告中的 68 个容器均已不存在 | `runs/m3-4-workouts-reference/docker-container-audit.json` |
| 静态检查 | evaluation 的 go vet、活动 Go/新参考源码格式、git diff --check 通过；五份文档的 88 个本地链接和 14 个锚点有效，五个参考数据版本清单一致 | `runs/m3-4-workouts-reference-vet-final.log`、`document-check.json` |

最终工具源码摘要 `18025a8c5846f21b8319f6a9ca451e0b8499fecec5ced5e991e2c84104c03978` 与 accepted-local/accepted-docker 相同，当前源码与归档材料一致；候选、common、工具、参考与每条日志摘要已逐份核验。汇总在 `runs/m3-4-workouts-reference/summary.json`，明确标 development_calibration，不生成真实模型效果率。早期 smoke/失败机制和修正前回归均保留，后续通过项不与这些轮次重复计数。

本单元完成第三个场景的单候选自动参考，尚未完成跨 Campaign/Run 的校准关联及指标汇总、完整人工复核或真实模型评测。不读取 .env，不调用模型，不开放付费命令，运行产物不入 Git。下一步完成三场景轮次汇总，以固定身份关联原门禁、参考及复核，验证零分母、未知区间与失败任务不漏计；供应商能力及批次命令仍可另行推进。

## 13. 三场景轮次汇总

- 日期：2026-10-20；基于 c27b76f。本单元实现发布记录的离线关联，不生成新候选、不更改原门禁，也不发布尚缺模型能力材料的正式实验协议。

### 实现前的取舍

把门禁读取加入 evaluation 包会破坏参考输入边界；只导出 CSV 让人手工拼表又无法稳定检查 Target/Run/候选身份。因此新增 l2/round，位于单候选参考之后，读取固定纳入清单、一个 Campaign 的原始计划/索引/Run 报告、可选参考及复核。参考包不导入该汇总器。使用现有记录类型读取交付结构，但不调用门禁判定函数或重跑参考。

首版只接受每 Target 一次尝试，清单与 Campaign plan 的任务集合、Profile/构建/基线/规范必须一致；清单另固定预期参考数据与工具身份。少一个任务、重复 Run/引用、跨 Target/规范/候选报告或摘要冲突属于轮次有效性错误，输出诊断但不发布效果比例。缺文件/未启动/未复核保留原 Target 与未知项；缺终态或成本材料时不能把存在的部分汇总当完整轮次。结构冲突与记录缺失分开，不让缺证据自动变成错误放行。

汇总重用 Assess 合成已绑定参考与另行复核；自动 pass 不代替完整 pass。开发校准与真实评测用不同 kind，真实评测拒绝消费 development_calibration 复核。分类迁移/测试违规与交付依据违规，同一候选只算一个错误。统计 N/R/Q/A/H/S、未知成功及错误放行区间，零分母保存 null；不外推统计置信度。Campaign/Target 的账本只是同一费用不同视角，展示两级但总计只取一次；缺成本标未知，不当零消费。人工时间与货币费用暂无证据，保持未知。

验收包括协议算例、A/Q=0、自动通过但待复核、NeedsAttention 的正确候选、执行失败/Skipped/取消、缺报告/参考/复核/账本、跨 Run/候选/规范、重复引用、成本重复及证据漂移。再以已有三仓确定性 Campaign 的原 Run 身份运行参考并实际关联，不把旧校准报告改名冒充同一 Run，也不将手写候选结果称为真实模型效果。

### 实际实现与证据边界

新增 `scenarios/rpcx/l2/round` 与只读命令 `scenarios/rpcx/l2/cmd/summarize`。Manifest 固定来源/参考身份，Request 的参考文件和可选 Review 另有摘要；严格关联原计划、Campaign/Target 索引、Run 终态、交付报告/验证的候选与规范、实际候选树，再消费参考材料、命令双日志与 Assess。汇总只读取交付结构，不调用具体场景的门禁判定实现，也不使 evaluation 包反向读取 gate verdict。参考包原源码与冻结数据均不修改。

输出保存原始来源文件的实际摘要与副本、缺文件标记，以及本汇总包自身生产源码摘要；结束前核对重复读取、文件/树/副本漂移。该摘要不代表整个 Harness 或环境身份，普通可信宿主材料没有签名认证。原记录路径不是任意文件读取权限，Campaign 内部引用及参考日志需保持在声明根内。真实 kind 禁止消费 calibration Review，也拒绝 controlled 模型 Campaign 改 kind 冒充真实效果。

缺少原 Run 报告时仍保留原索引中的 verdict，因此 A/Q 和 N 不会因缺文件下降；同时 DeliveryVerified=false，不计确认或潜在自动成功。R 表示已发布索引引用的不同 Run，缺 run.json 单列缺口，不声称其终态已认证；发现孤立未索引 Run 的审计归 M4。正常已记录的执行失败可使批次记录完整终结，但不会成为候选质量通过。取消、Skipped 或必需终态材料缺失则 ExecutionComplete=false。

Cost 使用与 summary 一致的预算快照，核对两级总数、Run 绑定、准入/拒绝、已知输入/输出与未知占用，Target 明细不再次叠加。Loop 的 ModelCalls 尝试数和工具数从可获得的 Agent 记录另列；无 Agent 记录不臆测零次。缺账本成本为 null；金额和人工活动耗时没有证据，保持未知。此处仅做聚合所需的交叉检查，不实现预算恢复或完整账本重放审计。

首轮构建发现一个未使用 import，已删除；此前没有产生可执行的汇总。其后的机制算例和缺失/冲突对照通过，没有通过更改分母使失败消失。汇总器后续补充源码归档、Loop 尝试数、上下文收尾与副本漂移检查，最终 CLI 用同一已发布参考写入新目录复验；原轮次与参考不回写，不重复运行模型或候选。

### 受控实测与验收

选用已有 `runs/m3-4-heterogeneous/heterogeneous-local-3562736416/campaign`，原计划摘要 `f2a721a6c297188bb3b0033cbc7711a73fdce9fb762cda72fc2c584b6d2cf60d`。三个原候选均来自确定性脚本模型，不是实际供应商生成。以原 Target/Run ID 对其已交付候选重新执行 Goddd、Translation、Workouts 参考，三项自动通过，保留 Review 缺失。没有把先前用 calibration-correct 身份生成的参考改名冒充本次 Run。

最终离线命令得到 N=3、R=3、Q=3、A=3、H=0、S=0、确认错误放行=0、未知放行=3；成功和错误放行均报告 [0/3,3/3] 的未知范围，不报告完整成功率或错误放行率。共有 6 次准入，已知输入/输出 90/30 Token、总量 120，无未知/在途占用；这是受控模型用量声明，不是供应商账单。原三份 gate verdict 与 Campaign summary 的字节保持不变。pending Review 不因自动参考 pass 被转为完整 pass。

收尾新增的归档篡改测试在 `runs/m3-4-round-accepted-race.jsonl` 失败：测试以 macOS 的 /var 别名查找 reader 用 /private/var 保存的键，取到空路径，未能注入预期篡改。测试改为同样的 canonical 路径后重跑；没有放松生产完整性检查，失败记录保留。

| 验证 | 结果 | 证据 |
| --- | --- | --- |
| 汇总与单候选参考竞态回归 | 两包 107 项测试及子测试通过，4 项显式验收跳过；未把后续重复测试数相加 | `runs/m3-4-round-race.jsonl` |
| 最终汇总竞态回归 | 34 项测试及子测试通过，1 项实测入口显式跳过，无竞态；最后校准/真实 Review 类别负例另绕过 controlled 入口约束，确保确实测试 Review 边界 | `runs/m3-4-round-final-verified.jsonl` |
| 原三仓 Campaign 实测 | 三个独立参考按原 Run 身份执行，关联成功；全部完整参考保持未知，没有新模型调用 | `runs/m3-4-round-published-first.jsonl`、`runs/m3-4-round/published-first/` |
| 最终 CLI 实际消费 | 不重跑候选/参考，读取同一套文件发布新的有效校准报告；N/A/未知区间及成本符合前述计数 | `runs/m3-4-round-cli-final.log`、`runs/m3-4-round/accepted-final/summary.md`、`round.json` |
| 来源及材料复核 | 207 项原始来源/归档对应，当前生产源码与工具归档一致，全部摘要复核通过 | `runs/m3-4-round/verification.json` |
| 静态及文档检查 | go vet、Go 格式、git diff --check 通过；七份文档 114 个本地链接目标、15 个锚点及围栏通过 | `runs/m3-4-round-vet-final.log`、`verification.json` |

最终汇总器工具摘要 `81dee2a13f25c40e9e9b758a6cb775d351833eebd50b39f37420100878bee867`，仅覆盖 round 包的生产源码。实际来源文件和原 Run 目录均不覆盖；终态汇总最后发布，新目录独占，不提供旧轮次恢复。没有运行新的 Docker 命令、读取 .env、调用模型、准备留出或发布正式真实实验协议。本单元验收的是离线关联和口径机制，不据此宣布 M3 完成。

下一步核实真实供应商能力上界与预算配置，设计并实现受预算约束的 batch 入口，再补全并发布真实开发实验协议。实际生成后仍需源码与交付复核；留出仍待独立准备，M5 未启动。旧工作记录中的后续计划保留为当时事实，不将当前机制校准回填为真实模型效果。
