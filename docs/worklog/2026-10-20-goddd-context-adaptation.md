# Goddd：mock context 传播与未使用形参的有限适配

- 日期：2026-10-20
- 状态：有限适配与证据发布已实现并验收；原真实候选副本仍 NeedsAttention
- 代码基线：4308f45
- 对应里程碑：[M3.4](../MILESTONES.md#m3多仓库批量闭环)
- 前置：[未知项根因评审](2026-10-20-needs-attention-review.md)、[Workouts 导入顺序验收](2026-10-20-workouts-import-order.md)
- 当前结论：采用 Goddd 宿主从认证基线生成的有限适配变体，保留旧写法；不扩大共享测试比较器的默认规则

## 问题、目标与范围

首轮真实 Goddd 候选的两项 unknown 中，本单元只处理两个已定位的差异：

- `mock/mock.go`：宿主已给 `(*RoutingService).FetchRoutesForSpecification` 加 ctx，但没有给函数类型字段 FetchRoutesFn 及其调用加 ctx。真实候选同时修改了字段签名和转发调用。
- `booking/service_test.go`：宿主给 `(*stubRoutingService).FetchRoutesForSpecification` 加名为 ctx 的参数，真实候选使用 `_ context.Context`。原 helper 不读取这个新增参数，原请求构造和返回逻辑未改。

目标是使这两种合法适配能被逐项解释，同时保留原结构检查对其余改动的约束。不以让真实 Goddd 候选整体放行为验收目标：`routing/proxying.go` 的新增超时常量和构造器 `_ = ctx` 仍超出当前范围，继续要求人工确认。它们不随本单元被忽略。

输入仍是固定 Goddd 基线和规范 v0.3.0，规则依据为 CTX-001、TEST-001、SCOPE-001。生产方法丢弃请求 ctx 不在支持范围；对测试 helper 的命名处理不能推广为生产 ctx 可以不用或以 Background 代替。

## 已核实的兼容约束

[旧正确候选](../../scenarios/rpcx/goddd/cases/patches/correct.patch)只迁移 mock 的公开方法签名，仍保留 `FetchRoutesFn func(RouteSpecification)` 和 `s.FetchRoutesFn(rs)`。该候选的 request_scope 已在冻结校准中通过。[现有适配器](../../scenarios/rpcx/goddd/adapt.go)生成的正是这一形式。

认证基线及真实候选中，FetchRoutesFn 只在该字段声明和这次转发出现，未发现字段赋值或调用方初始化。这解释了新签名当前无需联动其他文件，也暴露验证限制：原业务测试没有证明这个回调的新 context 传播，实施时须新增直接对照。保留旧形式只维持既有测试替身的支持范围，不把它解释为生产链路全部保留了 ctx。

不能直接替换唯一宿主模板，否则旧正确候选会从 pass 退化为 unknown；也不能改冻结补丁/manifest 来掩盖退化。

## 方案取舍

目标顺序：原断言和业务结构可审查、错误不放行、旧范围不退化，最后是支持更多表达。当前只需要支持两个已认证位置，不追求任意 Go 程序等价。

| 方案 | 当前材料上的具体影响 | 选择 |
| --- | --- | --- |
| 只把唯一模板改成显式转发及 `_` | 最少分支，但旧 mock 及原 ctx helper 不再匹配；改 expected 会丢失既有兼容范围 | 不采用 |
| 在共享 testintegrity 中默认忽略未使用参数名，生产比较器忽略 context 差异 | 多仓自动获益，但会改变所有场景的证明范围；生产 ctx 必须实际转发，不能统一忽略 | 不采用 |
| 给共享 Plan 增加通用参数改名/绑定规则 | 能显式开关，但需要身份、位置、绑定、报告与多场景回归；只有一个新增且未使用参数，不足以证明这项接口复杂度值得 | 暂缓；多个场景出现相同需求时重评 |
| 用类型加载做一般 alpha 等价和回调传播分析 | 可覆盖局部改名、遮蔽和闭包；当前两个已知位置可以用严格 AST 模板解决，新增加载、协议和信任边界代价更高 | 暂缓 |
| Goddd 从认证基线生成两个固定生产形式、两个固定测试适配集合 | 仍比较完整结构，不改原候选；多保存少量适配材料，接受其他等价写法仍 unknown | 采用，并以原型/反例验证后实施 |

固定变体由宿主在消费候选前生成，不从候选反推期望、不读取参考补丁或 oracle。原型可用手写补丁作测试输入，正式门禁不允许如此。旧形式与新形式只在约定位置不同，不建立“同名函数都适用”的规则。

## 拟实现的边界

### 1. mock 生产适配：完整文件二选一

保留当前 adapt 生成的 canonical 文件，再从该文件构造 forwarded 变体；只适用于 `mock/mock.go`：

1. 定位唯一类型 RoutingService 的唯一 FetchRoutesFn 字段，确认原匿名参数/返回结构与认证基线一致。
2. 在字段参数首位加匿名 `context.Context`，原 RouteSpecification 参数和 Itinerary 返回类型完整保留。不往全匿名参数列表里混入命名 ctx，避免非法 Go 声明。
3. 只定位接收者 `*RoutingService` 的 FetchRoutesForSpecification 方法，将原接收者的 `FetchRoutesFn(rs)` 改为 `FetchRoutesFn(ctx, rs)`。保留 `FetchRoutesInvoked = true`、语句顺序及其余文件内容。
4. 比较原候选整个 mock 文件的结构指纹，只有完整匹配 canonical 或 forwarded 之一才证明这个文件；其他路径仍采用原规则。不得分别忽略字段和调用，也不得从两个模板拼取相等片段。

宿主变体构造中唯一性、旧结构或命名条件不成立，是宿主支持契约不成立，返回明确错误，不能回退到任意名称匹配。候选解析失败、部分迁移、额外逻辑或未知声明仍不提供结构通过。既有 directives 拒绝保持。

仍不支持：Background/TODO/另一 ctx 转发、只改字段或只改调用、额外 helper、其他函数类型字段改写、任意回调初始化联动。没有一般调用图或反射证明。

### 2. 测试适配：新增未使用形参的两个固定集合

保留现有 canonical 测试集合。仅对 `booking/service_test.go` 中唯一 `(*stubRoutingService).FetchRoutesForSpecification`，额外生成 blank-context 集合，将宿主新增第一参数的名字 ctx 改成 `_`。

前置证明：认证原声明没有这个 ctx 参数；原参数、接收者、返回类型与函数体全部保留；新增参数类型正是宿主引入的默认 `context.Context`；原声明中不存在标识符 ctx。扫描可保守拒绝同名字段、局部变量、嵌套函数等，不为接受遮蔽而重建名称解析。这个位置的条件满足时，名字变化不会把原表达式重新绑定到新增参数。它不是对任意未使用参数或任意新名字的归一。

两份完整集合分别调用已有 `testintegrity.CompareAdapted`，候选仍是原始字节。不开共享 Plan 新字段，不先重写候选再把归一后的哈希冒充原候选身份。

选择规则：

- 唯一或两份一致的 preserved，证明整套原测试匹配其中一个受信适配。不能逐函数从不同报告拼凑 preserved。
- 已有 violated 不能被另一份 unknown 覆盖；存在明确 violated 时保留违规证据。若竟出现 violated 与 preserved 冲突，不自动放行，按宿主证明冲突停止并诊断。
- 全部 unknown 时仍返回 unknown，同时保留两份缺口。比较器/材料错误不伪装成候选质量 unknown。
- 选择的 adapted-tests 实际文件必须与报告 Expected 绑定一致，也用于可信生产副本中的原测试执行。两份比较报告和变体来源均保存，报告的原 Candidate 摘要不变。

具体函数可留在 goddd 包：生成固定 mock 变体、生成固定 blank-context 测试集合，以及组装两份 CompareAdapted 结果。它们是本场景的普通函数；不增加注册表、插件、DSL 或通用流程引擎。

### 3. 证据、失败与质量聚合

建议保存 `adaptations/` 下的固定变体源码及 `adaptations.json`：基线/材料摘要、变体 ID、相对路径、声明身份、修改位置、expected 文件摘要、使用的条款。scope 材料记录每个文件命中的固定形式；测试保存 canonical/blank-context 两份比较报告，以及最终采用的适配身份。实际文件名可在实现时按已有 save/copyFiles 接口落实，不改变规则含义。

Evidence 必须引用新落盘材料，并绑定本次 baseline/candidate/spec。不同变体的选择只影响相应检查，不改变 quality.Policy 的必需集合、优先级或状态含义；全局 request_scope 仍汇总所有生产文件。

仅实现这两处后，原真实候选的 test_preservation 预计可由 unknown 变 pass，mock 文件的结构缺口预计可消除；整个 request_scope 预计仍 unknown，因为 proxying.go 的常量和 `_ = ctx` 尚未被证明。这是待完整门禁验收的预期，不是本轮已取得的新判定。

## 可行性原型的实际结果

本轮生产代码、测试、Profile 及冻结数据均未改。原型放在忽略目录 `runs/goddd-context-design-01/`：复制既有 adapt/syntax，仅改包名及摘要函数引用；从认证基线构造变体，用已有 CompareAdapted 比较原测试字节。它不执行候选、不调用 Docker/模型，也不读取 .env。

| 对照 | 实际观察 |
| --- | --- |
| 旧正确候选的 mock / 真实候选的 mock | 分别只匹配 canonical / forwarded，均可在有限二选一中解释 |
| 只改字段 / 只改调用 | 两种模板都不匹配 |
| 转发 Background / 改 rs 请求 / 改 Invoked 标记 / 改其他回调 | 两种模板都不匹配 |
| 原 ctx helper / 真实 `_` helper | 分别只有 canonical / blank-context 的完整测试集合为 preserved |
| 改 rs.Origin / 追加 Background 调用 / 改名 requestCtx | 两套比较均 unknown |
| `_` helper 删除断言 / 无条件 Skip | 至少一套比较保留 violated，不因另一套 unknown 丢失已知违规 |

共八个 mock 对照、七个完整测试集合对照通过原型断言。results.json 保存每份匹配/比较结果，reports/ 保存两套原测试比较报告；expected-forwarded/ 和 expected-unused-tests/ 保存实际变体。首次五个测试对照及后补删除/Skip 对照分日志保留，不把两轮重复样本合并计数。该原型只验证当前设计的可表达性，尚未实现报告发布、质量聚合、唯一性全部防御或合成绑定反例，不能算运行时验收。

## 实施顺序与验收条件

下一单元把两处作为同一项 Goddd 有限适配工作实现并验收，不按小函数拆提交。先实现宿主变体和证据，再接范围/测试检查，最后跑完整门禁；无需真实生成。

| 必须证明的边界 | 验收方式 |
| --- | --- |
| 旧范围不退化 | 六项冻结 Goddd 校准的原检查、判定及条款保持；保留 mapping-helper 的 unknown，不改原 freeze/manifest |
| mock 的完整配对 | 旧、新形式为正例；字段/调用只改其一、错父 ctx、改 rs、Invoked、其他类型/方法/返回/新增函数及语义指令均不得结构通过 |
| 新回调确实转发 | 独立构造带值/取消的 ctx，直接调用新 mock 并观察回调收到同一 ctx 与 rs、标记及结果；不以编译成功代表传播成功 |
| 未使用参数证明严格限定 | ctx/`_` 为正例；原有参数改名、改类型、调换参数、其他 helper、函数移动、闭包或遮蔽同名、原 body 有 ctx 标识符时不获得该适配 |
| 原断言仍受保护 | 删除/Skip 保留 violated；断言条件、helper 返回、共享表数据和 init 改动不得 preserved；不拼凑两份测试报告 |
| 证据仍指向原字节 | 原 Candidate 摘要与原文件一致，Expected 对应保存且实际执行的 adapted-tests；两份报告及选中身份可重算；保存失败无最终 verdict |
| 整体范围诚实 | 在独立开发目录消费原真实候选副本，预期 test_preservation pass、request_scope 仍 unknown；常量和构造器改动不能被新规则吞掉 |
| 设施和回归 | Goddd/testintegrity 及相关 Runner 回归、竞态/静态检查；旧正确和新有限形式、已知错误、仍未知三类至少做隔离 Docker 对照；未执行项不得记通过 |

所有新测试对照均属于已见开发校准，不追加真实模型、不修改首轮原 Run 或指标。原 5907 份 Campaign 材料前后核对；容器及资源按已有执行边界回收。

## 身份与改选条件

不修改规范或宿主 Profile 内容。实施后保存实际新工具材料/源码身份，历史 Run 继续由其原版本解释；如确需改 Profile 字段或验证器契约标识，按 [ADR-007](../adr/007-host-owned-task-profiles.md) 另起 ID。共享 testintegrity 默认契约保持原样，符合 [ADR-002](../adr/002-evidence-bound-quality.md) 的独立证据和必需检查约束，不另立 ADR 重复它们。

若支持位置或变体数明显增长、出现 callback 初始化联动，或必须接受遮蔽/任意参数名，停止增加模板组合，重新比较宿主显式规则与隔离类型证明。若只是这个真实候选仍 NeedsAttention，不据此扩大常量或控制流规则；它们需自己的反例与取舍。

本轮交付为设计和可行性证据。生产规则实施、完整校准、真实候选副本的新门禁结论均待下一单元，不宣布 M3 完成。

收尾核对：原 Campaign 的 5907 份文件摘要未变，四份变更文档的 99 个本地链接目标及代码围栏检查通过，git diff --check 通过。源码、冻结材料与原协议没有变化。本轮未运行项目 Go 回归或 Docker，不能据此声称完整门禁通过；原型与检查材料摘要见 runs/goddd-context-design-01/verification.json。

## 实际实施与验收

本节记录基于 ffc1846 的后续实现；上文原型结果及设计时的验收状态不回写。

新增场景内 variants.go 和 adaptation_evidence.go：从已认证基线生成固定变体，严格核对字段/方法、原声明不使用 ctx 和完整适配声明；两份测试报告采用同一输入、规范和工具身份，不能跨报告拼凑通过。原 CompareAdapted 和共享 Plan 不变。

Evaluate 在命令验证前保存四份固定适配、两份完整原测试比较、最终选中报告及 adaptations.json；每份文件逐一登记 Evidence，绑定规范、基线及候选。adaptations.json 记录声明身份、条款、文件摘要、逐生产文件匹配和选中的测试形式。adapted-tests 保存实际选中集合，可信生产副本使用同一份集合。

新测试覆盖完整文件匹配、成对变更、host 结构错误、原 ctx/局部/闭包/字段同名绑定、候选改名/逻辑/init、删除断言/Skip、报告冲突与原始摘要、四处发布故障。隔离验收另直接观察 mock 收到同一 ctx（含值及取消状态）、rs、Invoked 标记和原返回结果，不将该宿主探针注入候选测试。

所有材料位于 runs/goddd-context-adaptation-01/，不入 Git；不请求真实模型或读取 .env。最终验证结果如下：

| 验证 | 实际结果 | 证据 |
| --- | --- | --- |
| Goddd/testintegrity 竞态回归 | 两包、148 项测试及子测试通过，无竞态；3 项显式验收跳过，另表执行 | core-race.jsonl |
| 六项冻结本地校准 | 1 ReadyForReview、4 Rejected、1 NeedsAttention，原 manifest 检查、判定及条款全部保持 | matrix.jsonl、matrix/ 各 evaluation.json |
| 新有限形式 Docker 正例 | ReadyForReview；两份测试报告、选中 blank-context、forwarded mock、原测试、类型和可信行为均通过，包含实际到期 | docker-variants/forwarded-blank-2753371499/ |
| 独立 mock 直接传播 | 活跃及已取消的 ctx 均传给回调，同一对象、值、取消、rs、标记和返回结果通过；测试事件已核对 | 上述目录 mock-probe/、mock-probe-execution.json |
| 错误期限 Docker 反例 | 5 秒期限仍 Rejected，结构适配不能替代期限验证 | docker-variants/wrong-duration-1006014749/ |
| 未知 mock Docker 反例 | 回调使用 Background，完整文件不匹配；行为探针通过仍 NeedsAttention | docker-variants/wrong-mock-context-353229406/ |
| 旧正确候选 Docker | 仍 ReadyForReview，保留 canonical 形式 | docker-legacy/correct-2195436263/ |
| 原真实候选副本 Docker | test_preservation pass、mock 缺口消除；仅 proxying.go 仍有范围缺口，最终 NeedsAttention | real-candidate-calibration/ |
| 项目回归 | 25 个有测试包、1037 项测试及子测试通过，7 个包无测试；37 项显式验收跳过，不算作通过。包含既有 T3 三类交付与 Runner 回归 | project-tests.jsonl |
| 静态检查 | Goddd 的 go vet 通过 | vet.log |

11 份校准报告的 Evidence 摘要、输入绑定、四份固定变体、原候选测试字节及选中/可信执行测试集合均已逐一核对。原 Campaign 的 5907 份文件摘要未变，45 个 Docker 容器均已不存在；阶段审计见 artifact-audit.json。这里没有重新评测或替换原实验，旧判定及正式指标保持。

原真实 Goddd 候选摘要仍为 `9ac51f3a432cb8b62038f5e13860e525bb33b1d70670506a74fb672e49a32682`，新工具材料摘要为 `a1940825c5daa73b31236e07b580d62368ccdff765f5e0436729fe98acfafe9d`。新校准的选中适配、测试报告和未知项只解释新材料身份下的检查，不将旧 NeedsAttention 改计为自动成功。

最终审计 verification.json 逐项核对上述材料、当前工具源码、原 Campaign 摘要与容器回收；规范、Profile、冻结校准数据、共享 testintegrity 及原实验协议均未变。Go 格式、git diff --check、六份文档的 138 个本地链接目标及代码围栏检查通过，文档摘要见 document-checks.json。不同轮次的重复测试不相加计为项目覆盖。

本单元按已声明的两处固定适配验收，没有消除 Goddd 的全部范围缺口，也没有完成人类复核或 M3 总体验收。下一步评审 proxying.go 的命名期限常量和构造器兼容参数，再决定证明方案；不将 `_` 测试参数规则推广到生产 ctx，也不为获得放行追加真实 Run。

后续取舍、局部证明边界及最小原型见 [Goddd routing 范围证明](2026-10-20-goddd-routing-scope.md)。
