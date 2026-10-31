# P1 独立真值与人工复核准备

日期：2026-10-31，Asia/Taipei。状态：已完成现有材料调查和复核清单准备；四个样本的本轮人工复核均未完成。本文不是冻结协议、人工签署或 P1 实验结果。当前阶段见[开发入口](../README.md)，验收依据见[需求](../REQUIREMENTS.md)。

## 1. 采用的独立依据

复用已有 [L2 独立参考](../../scenarios/rpcx/l2/evaluation/README.md)，不把生产门禁 verdict、Agent 说明、历史候选结果或参考补丁相似度当作真值。参考入口只接受固定输入、候选及其摘要、规范、执行后端和独占证据目录；不接受组别、原放行决定或预期结果。

参考为生产副本装入单独编写的行为检查，剥离候选测试，并另行执行候选的原测试。它使用独立模块声明比较和 Go 实际构建/测试事件，不调用场景生产 Evaluate、类型分析、测试完整性规则、quality.Decide 或生产行为探针。[语义边界测试](../../scenarios/rpcx/l2/evaluation/events_test.go)约束直接导入及 gate 函数复用范围；共享 SDK、Go、执行后端、文件身份设施及开发者仍可能带来共同盲区。它是实现与输入上的分离，不等于独立人员或盲审。

四个样本均为已见开发输入。历史[固定四任务](../experiments/l2-fixed-eval-v0.1.0/reviewers.json)及[后续开发观察](../experiments/l2-current-dev-v0.1.0/reviewers.json)明确使用助手复核，且声明没有独立人类或盲审保证。因此它们可以支持本轮准备和选择校准对照，不能替代本轮明确要求的逐仓人工复核。

旧[消融草案](2026-10-31-baseline-ablation.md)中的 8 次生成、B/C 共用候选、提交后普通构建测试决定 A/B 放行，以及旧预算总额均不适用于当前 P1。当前主实验为至少 36 次独立生成，A/B 正常完成提交即按策略放行；独立真值在放行决定保存后评测所有固定候选，包括未放行者。

## 2. 当前来源身份

以下是调查时当前材料身份，非本轮正式冻结；实现或材料改变后须重新生成冻结清单，不能直接沿用旧实验的实现/工具身份。来源上游 commit 表示准备切片的溯源，不表示当前输入等于上游整仓。完整来源及暴露说明可从[历史数据清单](../experiments/l2-current-dev-v0.1.0/dataset.json)核对。

| 样本 | 输入/上游来源 | 规范 | 本轮拟用 Profile | 活跃参考数据 / 报告版本 |
| --- | --- | --- | --- | --- |
| Goddd | rpcx-l2-goddd-v1；marcusolsson/goddd `7c414addc7a127df0b4ea38653be6a7c6380a37a` | [v0.3.0](../../scenarios/rpcx/goddd/specs/v0.3.0.md) | rpcx-goddd-t3-v1 | rpcx-l2-reference-goddd-data-v2 / rpcx-l2-reference-goddd-v1 |
| Translation | rpcx-l2-translation-v1；evrone/go-clean-template `d1f46523e9a8435713aabb70222c6189564d4501` | [v0.4.0](../../scenarios/rpcx/translation/specs/v0.4.0.md) | rpcx-translation-t3-v2 | rpcx-l2-reference-translation-data-v2 / rpcx-l2-reference-translation-v1 |
| Workouts | rpcx-l2-workouts-v1；ThreeDotsLabs/wild-workouts-go-ddd-example `8ecfcdf05b1462c4757bd2dcac9086c78e9f7791` | [v0.5.0](../../scenarios/rpcx/workouts/specs/v0.5.0.md) | rpcx-workouts-t3-v1 | rpcx-l2-reference-workouts-data-v1 / rpcx-l2-reference-workouts-v1 |
| GHCLI | rpcx-l2-ghcli-v1；cli/cli `49f72234acd346666a4646a0f5a427fb3543debc` | [v0.6.0](../../scenarios/rpcx/ghcli/specs/v0.6.0.md) | rpcx-ghcli-t3-v1 | rpcx-l2-reference-ghcli-data-v3 / rpcx-l2-reference-ghcli-v3 |

Translation v2 Profile 与 v1 使用同一输入/规范，但使用当前测试结构范围策略；两个配置 default 与 rpcx_alternate 均必需，仍计一个任务。GHCLI v1/v2 参考为历史归档，不得用旧 automatic=pass 代替当前 v3 参考。

| 样本 | 业务基线 TreeSHA256 | 参考 freeze.json SHA256 |
| --- | --- | --- |
| Goddd | `3f2f404c1947f4076dfcc1f54c03fc08a7475a44f30db8398848ef48dda7a610` | `5cd44c1d8c00f147809d41545086ce1c32ae7cfcc0f6bb47b867c8a7601715c7` |
| Translation | `e520d248568e98e6a91556041a27ed23e916198436678e561c590d2756b57784` | `1687c993a76ff6682111f0f6eb6b9662e4bc193b7e92d3d4baa2c2fdcf54acbd` |
| Workouts | `4f269c1ee06787cd3b08cfc68de33eb9a6ac9afb3ac2922738f8b15b3659d670` | `400e7c32a2e6b9cac3936aa6bd5e2f1c4911d96aeca44fc25a20ff58dab6f669` |
| GHCLI | `c299869d906bc9bb084e1cad4c35d8b6820879488536117ba9bdacd0f7ab131c` | `ec6280258bb5fb95566d671d88a48d32ede2f859cc9f29baf63b58362f72e12d` |

本次读取并逐件核对四份参考 freeze.json 成员摘要、规范摘要及当前 Profile 的一致性；上表基线摘要来自发布的 Profile，正式准备仍须重读实际树验证。当前独立参考工具摘要为 `6f6aa8aace731f23f76cf3cfba7fdd800881f4b30217e16fca97f5fbaad38f3b`，按现有 toolData/TreeHash 的材料集合重算：evaluation 非测试 Go 源码、gate 非测试 Go 源码及 l2.ExecutionMaterials。正式冻结应调用 [PublishedIdentity](../../scenarios/rpcx/l2/evaluation/identity.go)，保存其实际返回及四仓完整输入、SDK、common、规范、Profile 和执行环境身份；仅有本表不足以证明已冻结。

## 3. 运行前四仓人工复核清单

复核对象是各仓规范、原输入、独立参考及其覆盖和未知处理，不是对尚未生成的候选预签 pass。实际人逐仓给出接受、需修订或无法判断，记录姓名/身份、实际 RFC3339 时间、与开发及校准的关系、检查过的材料/代码定位及理由。允许同一实际人复核四仓，须如实披露关系；助手可以整理材料，不能代签或把自身审查称为人工复核。

### Goddd：HTTP 请求到路线 RPC

材料：[规范](../../scenarios/rpcx/goddd/specs/v0.3.0.md)、[参考测试](../../scenarios/rpcx/l2/evaluation/data/goddd-v2/routes_test.go.txt)、[候选复核说明](../../scenarios/rpcx/l2/evaluation/data/goddd-v2/review.md)。

- 核对 HTTP requestRoutes → booking → 日志/指标装饰器 → RoutingService 的真实路径，父值、取消和较短期限确实到达 RPC。
- 核对每次 RPC 从准备实际调用时起独立计算 30 秒并释放；发送前延迟对照不能错误使用 HTTP 请求发出时刻衡量 RPC 期限。
- 核对 fetch_routes、From/To、各航段字段及顺序，未知货物无 RPC，远端失败/空结果保留既有非 nil 空路线和 HTTP 行为，不额外要求规范未授权的 error API。
- 核对原 22 项测试/子例及断言人工审查方法；自动测试身份与成功状态不足以证明断言未削弱。等价 helper 不能仅凭结构不同判为语义失败。

### Translation：同候选双配置

材料：[规范](../../scenarios/rpcx/translation/specs/v0.4.0.md)、[参考测试](../../scenarios/rpcx/l2/evaluation/data/translation-v2/translation_test.go.txt)、[候选复核说明](../../scenarios/rpcx/l2/evaluation/data/translation-v2/review.md)、[配置选择实现](../../scenarios/rpcx/l2/evaluation/configurations.go)。

- 核对 default/rpcx_alternate 两个实际活动适配实现及互斥标签、原生成 mock 参与情况，不能以 default 成功代替第二配置。
- 核对真实 usecase、两层 tracing 到 RPC 的当前 ctx、取消/期限/值及独立 30 秒；已取消请求不发 RPC、不 Store。
- 核对 Original/Source/Destination、Translation 内存字段和用户 ID、成功后 Store 顺序、远端/Store 错误链及零结果、History；不能把 JSON 标签表现当作内存业务契约。
- 核对每配置原 26 项测试/子例断言、表数据及 helper 的审查方式，mock、LICENSE、go.sum 受保护；双配置合计仍为一次尝试。

### Workouts：仓储 callback ctx 与双客户端

材料：[规范](../../scenarios/rpcx/workouts/specs/v0.5.0.md)、[参考测试](../../scenarios/rpcx/l2/evaluation/data/workouts-v1/workouts_test.go.txt)、[候选复核说明](../../scenarios/rpcx/l2/evaluation/data/workouts-v1/review.md)、[参考矩阵](../../scenarios/rpcx/l2/evaluation/workouts.go)。

- 核对 decorated Handle → 仓储 UpdateTraining → callback 的路径，RPC 来源是仓储提供的 callback ctx；外层 Handle ctx 不能代替它。
- 核对 users/trainer 两次各自派生和释放、两次之间加入延迟的期限起点对照、各自真实约 30 秒到期，以及较短父期限、取消和重复调用。
- 核对零余额跳过 users、余额 → trainer → 本地提交顺序、参数和空确认、错误链；trainer 失败保留此前已发生的余额副作用，不引入补偿/重试/回滚。
- 核对 trainings 原断言、领域/command/decorator 改动和 common 只读边界；common 三项测试及自身构建须真实执行。

### GHCLI：HTTP 响应消费与所有权

材料：[规范](../../scenarios/rpcx/ghcli/specs/v0.6.0.md)、[v3 参考协议](../../scenarios/rpcx/l2/evaluation/data/ghcli-v3/protocol.json)、[主探针](../../scenarios/rpcx/l2/evaluation/data/ghcli-v3/probes_test.go.txt)、[响应返回探针](../../scenarios/rpcx/l2/evaluation/data/ghcli-v3/response_return_test.go.txt)、[真实到期探针](../../scenarios/rpcx/l2/evaluation/data/ghcli-v3/command_expiry_test.go.txt)。

- 以原 HTTP request parent 为上下文契约；不能把上游 command context value 传播的诊断观察变成规范未要求的自动判据。
- 核对请求 URL/方法/头/body、命令操作顺序、错误/状态/输出，以及调用方继续读 body 时的取消和较短期限；派发返回不能立刻取消仍在消费的 body。
- 核对 response 与 error 同时返回、可解包错误链、CheckRedirect 拒绝、ErrUseLastResponse、Read/Close 错误和 body 关闭责任；每次独立 30 秒及无 body/传输错误/Close 后释放。
- 核对支持包与原测试全部保护，候选修改仅限业务 RPC 集成和必要模块元数据；参考的有限应用切片不能证明整个上游或真实网络协议等价。

### 待实际填写的逐仓回执

| 样本 | 当前结果 | 实际复核者 / 时间 | 材料身份与具体定位 | 理由 / 缺口 |
| --- | --- | --- | --- | --- |
| Goddd | 未复核 | 待填写 | 待填写 | 尚无本轮人工签署 |
| Translation | 未复核 | 待填写 | 待填写 | 尚无本轮人工签署 |
| Workouts | 未复核 | 待填写 | 待填写 | 尚无本轮人工签署 |
| GHCLI | 未复核 | 待填写 | 待填写 | 尚无本轮人工签署 |

填写回执必须基于实际审查，不能因模板齐备、助手读过源码或旧校准通过自动改为已复核。正式协议冻结时保存实际回执及其摘要；如复核要求修改参考，应在运行前完成、重新校准和冻结，保留旧版本。

## 4. 运行后逐候选判断与交付分离

预运行四仓复核认证真值方法，不能证明 36 个尚未产生的补丁均正确。每个可取得的固定候选仍需运行独立参考，并审查测试保留及自动参考未覆盖的生产改动。无候选、参考不适配、设施失败、缺观察或无人复核的事项保留未知。部分工作区可以评估并单列，但不能当作正常提交补计成功。

现有 [evaluation.Assess](../../scenarios/rpcx/l2/evaluation/review.go)把自动参考、test_preservation、change_coverage、delivery_evidence 合成完整交付结果。P1 需要候选正确性与说明准确性分列，因此保持原契约不变，由 P1 专用消费层同时输出：

- 候选真值：已认证的自动参考 + test_preservation + change_coverage，取 pass/fail/inconclusive；明确违规才记 fail，未覆盖或设施问题记 inconclusive。缺人工变更覆盖/测试复核不能仅凭 automatic=pass 变为候选 pass。
- 交付说明：delivery_evidence 单列，按实际提交说明、检查日志和组策略评价；A/B 未执行独立门禁本身不是交付违规，不能拿 C 的审核材料要求制造额外隐含门禁。
- 原系统放行：独立保存三组策略决定及依据，不从真值或交付复核倒推，不回写原 verdict。

候选层消费仍应复用原 Assess 的严格身份、schema、必需配置/模块和结果一致性验证，不能通过直接解 JSON 绕过这些检查。专用实现可显式增加候选判断接口，或者在先通过严格原始记录验证后按受限规则计算两个结果；不得把修改后的虚构 Review 当作真实人工回执归档。具体实现由本轮设计确定。

## 5. 隐藏材料与循环论证防护

生成者只可见本 Run 的 workspace、业务 baseline、规范、两个 SDK，以及 Workouts 的 common。不公开 evaluation 源码/参考测试、expected、校准补丁、其他 Run、参考输出、复核意见和历史答案。被测生产门禁也不读取独立参考材料、真值或复核标签。

独立参考在生成结束且放行决定落盘后，用新证据目录与隔离副本运行；固定候选成员和摘要在运行前后核对。参考 Request 不包含组别与 verdict。人工判断候选正确性时先看候选/规范/独立证据，尽可能隐藏组别及放行结果，再看实际交付材料评估说明；无法盲审时记录原因及实际关系。

机制验证至少应检查实际注册工具根和实际模型请求，而不是只看配置：A 无 migration_check、迁移专用结构化自检或 final-response-v1；B/C 有且只有已声明反馈；任何组均不可读参考路径。构建容器只挂本候选及声明的只读依赖/cache，不挂整个项目、宿主证据根或其他 Run。这里要求本轮边界确实生效，不扩展为 P4 全套越权实验。

## 6. P1 专用结果聚合验证方案

以冻结的 36 个 attempt 身份为唯一分母，逐项关联计划、组、任务、重复编号、生成终态、是否正常提交、固定候选身份、独立参考/实际复核、原放行决定、trace 与预算引用。汇总不得只扫描成功目录；缺文件仍输出对应项及缺口。

应覆盖以下有实际语义差异的机制用例：

| 用例 | 必须观察的结果 |
| --- | --- |
| A/B 正常提交但候选独立失败 | 按策略放行，计确认错误放行；提交后 build/test 不能偷偷撤销放行 |
| C 正常提交 + ReadyForReview，但独立失败 | 计确认错误放行，不能把 ReadyForReview 当真值 |
| C 拒绝或证明不足，但独立 pass | 计误拒，并保留拒绝/证明不足的类别 |
| 候选 pass、交付说明 fail | 候选正确性保持 pass，说明单列 fail；完整交付结果可单列失败 |
| 候选 unknown 或复核缺失 | 不计 pass 或确认错误；报告未知数及潜在范围 |
| 四仓预运行回执缺一、助手或校准记录冒充人工 | 正式实验启动条件不满足；不伪造回执 |
| 参考/复核摘要、Run、候选、配置或成员冲突 | 拒绝消费该证据，保留身份错误；不能当候选代码错误或静默删除 |
| Translation 缺 alternate、Workouts 缺 common/任一客户端期限证据 | 候选结论不能完整通过 |
| 0 放行、0 真值通过、全 unknown | 对应零分母 N/A，保留预定 N 与未知计数 |
| 未启动、未提交、超时、拒绝准入、模型/工具失败 | 仍在预定分母；无有效候选时明确不可评，不删行 |
| 重复 Run、账本 call 重复、候选跨组复用 | 拒绝冒充独立生成；按实际调用去重金额和 token |
| 已结算/在途预留/未知占用/费用缺失 | 分列，缺金额依据保持未知；不拿预留当已消费或 token 当账单 |

自动成功为已放行且候选真值 pass / 全部预定尝试；错误放行为已放行且真值 fail / 全部放行；误拒为候选真值 pass 但未放行 / 全部真值 pass 候选。未知不能填零。报告每组每任务的三次原始计数、均值与范围、任务及组总计；36 次独立生成不采用旧 B/C 共享生成的费用处理。结果说明四个已见仓库、每格三次及共享开发设施带来的限制，不用其证明普遍效果。

## 7. 尚待完成

四仓实际人工回执、本轮完整运行协议和模型/金额授权尚无；本次未执行正式生成或参考候选实验。独立参考旧机制和既有校准材料可复用，但本轮策略开关、来源消费、候选/交付分列、全分母聚合及执行隔离仍须实现并验证。当前状态是复核材料已准备，P1 验收未完成。
