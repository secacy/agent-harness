# M3.4：Translation 双配置迁移判定与 Runner 接入

- 日期：2026-10-19。
- 状态：在本文声明范围验收；代码基线 f8c1024。双配置开发校准和确定性 Runner 交付完成，尚非真实模型或独立参考评测。
- 前置：[双配置基线](2026-10-19-translation-baseline.md)。
- 交付目标：两个必需配置分别构建、测试、分析和观察行为；漏迁移 alternate 不能借默认配置通过而放行。确定性 Runner 能生成和交付同一 Target 的双配置证据，不调用真实模型。

## 实现前的取舍

| 问题 | 方案与实际得失 | 本轮选择 |
| --- | --- | --- |
| 两配置如何组织 | 拆成两个 Target 易复用单配置 Runner，但可交付两份相互矛盾补丁，还会把同源样本计两次；放在一个 Run 要给每阶段增加配置身份 | 一个 Target/Run/候选，宿主 Profile 固定必需配置集；每个配置单独证据，所有配置通过才可放行。配置缺失保留 unknown，不以未执行当作不适用 |
| 如何复用 Goddd | 全复制模块缓存与解析规则会分叉；立刻统一业务规则 DSL 又把场景约束伪装成通用规则 | 抽取已有离线材料装配到 L2 宿主层，复用证据/文件/模块声明及测试事件小函数；Translation 保留自己的生产范围、适配与探针。Goddd 的公开 API 与旧 Profile 身份保持兼容，不重写既有门禁流程 |
| Agent 如何检查配置 | 增加自由 -tags 参数会把必需集合交给 Agent；仅最终门禁跑两配置会让修复反馈缺少 alternate | 宿主传入固定配置集，go_check 一次请求顺序检查全部配置，提示每个结果；不接受模型自选 tag/命令。共享执行器仍逐命令取槽，单次工具与 Run 时间预算仍生效 |
| 类型证明如何扩展 | 删除原 ctx 预检查可迎合现分析器，但破坏保真；把任意 guard/初始化视为安全又会扩大错误放行 | 只增加经类型身份认证的 ctx.Err 立即返回前置检查，和无副作用请求/响应局部声明形态；未知控制流/绑定保留缺口。原业务映射、Store 顺序由有限结构与真实行为共同检查 |

规范另发 v0.4.0，数据仍为 rpcx-l2-translation-v1，旧规范/数据不可改。生成 mock、LICENSE、go.sum 受保护；所有原测试和表驱动数据保持原语义，候选测试可做导入/API 适配，可信验证使用认证基线适配测试与独立编写探针。原生成 mock 不因迁移需要而重生成。

## 验收契约

- 正确两配置迁移可 ReadyForReview；只漏 alternate 超时必须 Rejected，引用 TIME 条款和 alternate 证据。上游保留的 15 项原测试绕过传输；发布基线另有 11 项植入测试，其中 RoundTrip 已检查期限，不能把这个反例描述成全部 26 项测试通过的假成功。
- 只改默认导入/Call 的候选在 alternate 构建失败；缺配置执行/活动文件或原测试事件不足不能放行。
- 等价 helper 重构可通过行为但保持 NeedsAttention；父 ctx 断链、改 mock 或删断言有明确依据。
- 每配置真实观察请求值、父期限/取消、返回释放、参数/结果映射、错误链与 RPC→Store 顺序，正例包含两配置真实约 30 秒到期。
- 基线两个配置都通过后才准入模型；受控 alternate 基线故障零模型调用。Agent go_check 覆盖两个配置，候选和报告只属于一个 Run。
- 本地候选矩阵、真实 Docker 主对照/Runner、共享模块及旧门禁回归分别记录；工具材料与配置身份进入证据，不把手写校准当作独立 oracle 或模型效果评测。

## 实际接入与证据组织

新规范与发布数据分离：v0.4.0 摘要为 `d45937d461cf056a82aba50741a27ba3c991c73967047e0d4e723cc70feb5e5f`；基线仍为 rpcx-l2-translation-v1，不改写夹具、依赖归档、选择清单、旧规范或 oracle。新 Profile rpcx-translation-t3-v1 的实际 JSON 字节摘要为 `4d71ed028b28ebe7a66ca1e2bc10a328d4990ebd5bd606eb35db50a8ec7e3279`，已有三个 Profile 的身份保持不变。沿用 ADR-005 的数据/工具分离和 ADR-007 的宿主 Profile 所有权，本步不另建规则 DSL 或动态配置插件。

`internal/task.BuildConfiguration` 只描述宿主固定 ID/tags。Translation Profile 使用 all-required，并包含 default 与 rpcx_alternate；普通 Target 不能改为单配置。Runner 启动前在两配置分别 go list、build、test，preflight/baseline.json 的 configuration_tests 保留每配置的 26 项测试身份，不把它们合成虚构的单配置通过。Agent 一次 go_check 顺序运行两配置，普通构建失败仍继续给出另一配置反馈，取消则停止后续命令；所有配置共用两分钟工具期限，并逐命令经过原执行槽。

`translation.Evaluate` 复用已有 checks.Evaluation / quality.Decide，包含九项必需检查。依赖、保护路径、测试结构与生产范围检查同一完整候选；构建配置、构建、原测试执行、类型对应、可信行为分别生成两行配置结果后聚合。fail 优先，blocked/unknown 不能被后续 pass 覆盖。配置 JSON、实际命令及日志、类型分析和比较均保存为绑定规范/基线/候选的证据。任一必要基线或设施错误返回空判定，不假装是候选违规。

类型分析仍每次只分析一个配置，保存未激活源文件的缺口；宿主仅承认另一必需配置负责的那份已知互斥文件，且要求当前配置恰有一个 translate 对应、无其他变更和额外缺口。两行共同参与最终判定，不能用默认配置替代全仓库覆盖。生产结构与独立行为同时约束边界，超出 helper/控制流证明范围仍为 NeedsAttention。

共用离线依赖后端移动至 L2 宿主层，Goddd 以别名和薄包装保留已有 API。文件/模块声明/测试事件的小函数移动至 gate；Goddd 业务规则与旧 pin 不变。两个门禁都保存实际共用源码和依赖装配/解包源码，避免只保存薄包装而漏掉真实实现身份。缓存仍是宿主只读执行材料，不进入 Agent 文件根。

七组手写校准使用独立 manifest/freeze，发布摘要 `9b3bc011c8c933452d1fd2094e8d17dd408c58ae85de64df2afa1870c3c10b6f`。预期细化到每种配置的五项检查和关键条款；TIME/CTX 违规还必须有可信探针输出的具体条款归因，不以整体检查挂载的一串条款替代实际观察。这不是独立 oracle，不计入真实模型效果分母。

## 联调发现与修正

1. 原测试比较器以包目录＋方法名分组，把原生成 mock 中不同接收者的同名方法混在一起。方法身份增加接收者类型的结构摘要，普通函数/测试身份保持不变；新增不同 mock 接收者和方法体变化对照。不修改原 mock 或裁掉相关测试。
2. 单配置类型报告天然将另一实现列为 inactive_build_file。只为固定互斥文件提供跨配置对应规则，原比较报告与缺口仍落盘；额外文件、操作不符、缺少候选对应等仍不放行。
3. 初稿用 blocked 同时表示“尚未聚合”和真实阻断，可能让后续 pass 覆盖前一配置的 blocked。改为单独记录是否已聚合，补充 blocked/unknown/fail 在后续 pass 下仍保留的对照。
4. alternate 缺超时并非“所有基线测试仍通过”：15 项上游原测试通过，但新增 RoundTrip 检查已失败。保留全部发布测试，不削弱断言。可信适配测试明确失败后仍执行独立快速探针，分别保存业务失败与 TIME-001 的实际观测，不把前者冒称为期限证明。另一组 Background 兜底候选才是两配置全部 26 项基线测试通过而被独立探针拒绝的例子。
5. 最初测试适配误将原 mock 的同名 Call 当作 RPC 添加参数，已收窄到认证的两份适配器；测试导入另行做必要迁移。删断言反例最初未真正删除可归因的纯断言，改为确实移除原请求映射断言，测试仍可编译通过，由结构比较拒绝。
6. 类型前缀只增加已认证 context.Err 前置返回及局部构造，用户函数和通道接收仍为 unknown。取消绑定额外使用、提前释放、错误期限等原对照继续保留。尾部业务不由这个局部证明承担。

早期失败与修复轮次保留在 runs/m3-4-translation-gate：local-first/local-second、structure-first/structure-final、local-rest.jsonl 等不是成功验收。local-rest 首次因默认 Go 编译缓存写入受沙箱限制而未能编译，改用仓库 runs 下缓存重跑。项目回归的 localhost 模拟服务也受端口权限限制；保留原失败记录，再单独复验相应包，不能将未实际执行的 HTTP 测试记为通过。

## 实际验收结果

| 验证 | 结果及限制 | 证据目录 runs/m3-4-translation-gate/ 下 |
| --- | --- | --- |
| 项目代码目录回归 | 首轮 20 个包通过，cmd/harness 与 deepseek 因沙箱禁止 localhost 监听而失败；这两个包在允许本机端口的环境完整重跑通过。合并的是 22 个不同包、746 项测试/子测试，不把首轮失败记为退出 0 | project-tests.jsonl、http-permission-retry.jsonl |
| 核心竞态检测 | task、repotools、测试结构、Translation、分析 worker 共 5 包、150 项测试/子测试通过，无竞态报告；最后的范围 finding 代码修正另经 Translation 21 项竞态对照通过，不与前轮重复相加 | core-race-final.jsonl、translation-race-verified.jsonl |
| 本地七组候选 | 1 ReadyForReview、5 Rejected、1 NeedsAttention，逐配置五项检查与 manifest 一致；TIME/CTX 反例有可信探针具体条款归因 | local-final.jsonl；local-final/ 下每组 evaluation.json |
| 发布前反例语言整理 | 仅将 protected-mock 故意追加的注释统一中文，同步发布前摘要后，该反例完整双配置复验通过。其余六份补丁不变；最终七组候选均重放当前冻结补丁并核对实际候选树，不能混用旧 protected 候选摘要 | protected-final.jsonl、matrix.json |
| Docker 三类主要对照 | correct / alternate-missing-timeout / mapping-helper 分别 ReadyForReview / Rejected / NeedsAttention；正确与 helper 两配置均真实等待约 30 秒到期。该轮早于补充独立期限归因的分支修正 | docker.jsonl、docker/ |
| Docker 期限归因补充 | alternate-missing-timeout 再次完整执行两配置，default 通过；alternate 的适配测试失败后继续运行独立快速探针，得到 TIME-001 缺少期限及 TIME-002 未释放的具体发现，最终 Rejected | docker-attribution.jsonl、docker-attribution/ |
| Docker Runner | Completed / ReadyForReview，双配置基线、实际编辑工具、两配置 go_check、完整独立门禁、补丁重放和审核链接均通过，模型为确定性替身 | runner-docker.jsonl；runner-docker/rpcx-agent-3810824127/artifacts/ |
| Goddd 共用实现回归 | 原正确与漏超时两组完整本地门禁通过，原规范、Profile、数据及校准补丁不改；不是重新执行全部旧场景 Docker 或 L1 正式矩阵 | goddd-regression.jsonl |
| 静态与材料检查 | 改动包 go vet、Go 格式、文档链接/围栏、git diff --check、已有三个 Profile 与旧冻结数据改动边界均通过 | vet.log、vet-supplement.log、summary.json |

七组校准只证明已声明场景的开发机制；不能据此报告真实模型成功率或错误放行率。未执行的条件测试、旧 L1 正式矩阵和留出评测不计作通过。本地模式仅用于这些可信夹具；实际隔离交付另以 Docker Runner 取证。

## 交付边界与下一步

Translation 已通过同一 Harness 完成“固定双配置基线 → 工具循环 → 单候选双配置独立判定 → 补丁与审核交付”。等价 helper 仍需关注，明确的父 ctx/期限/保护路径/测试违规有条款与证据；这不是通用语义等价证明。

未读取 .env、调用真实模型或开放付费 batch CLI。既有数据和历史实验保持不变；独立留出仍待准备。M3 不因第二个切片接入而整体完成：下一项按既有选择准备 Workouts 离线基线，之后再单独接入其回调/双客户端边界、补齐独立参考和异构批次评测。实际 API 预算上界、真实实验协议与授权仍按原条件执行。

原始日志、逐检查矩阵、当前候选重放核对、源码与运行材料摘要保存于 runs/m3-4-translation-gate/，运行产物不入 Git。最后 finding 代码仅使范围检查的发现名称与 unknown 状态一致，已单独通过结构与竞态对照，未声称此前的工具源码摘要等于该补充分支之后的字节。

Docker 收尾只读检查确认：本轮记录的 113 个容器 ID 均已不存在，Harness 标签下无残留容器。4 次 Agent go_check 的 ID 未进入工具文本，另外以执行成功回收与最终标签清单核对；不把未记录的 ID 算入 113。检查见 docker-cleanup.json。最终校验 148 个本地文档链接、围栏配对和 31 份变更 Go 文件格式通过；旧三个 Profile 的 JSON 内容与所有冻结输入未变。按一个完整工作单元提交，不拆为微小提交。
