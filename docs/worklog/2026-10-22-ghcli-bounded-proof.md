# GHCLI：有限上下文来源与响应资源证明

- 日期：2026-10-22。
- 状态：第一单元来源与证据链已在声明范围验收；Body 资源及局部剩余比较的原型见第 9 节，尚未接入生产门禁和完整范围归约。
- 代码基线：2db2fdb。
- 对应能力：[M2 证据判定](../MILESTONES.md#m2证据驱动的判定)的开发扩展，不是新的效果评测。
- 前置：[分类修正](2026-10-22-ghcli-proof-classification.md)已完成；[当前交付](../DELIVERY.md)保持有效。

## 1. 问题与目标

分类修正后，真实实验保存的 GHCLI 候选为 NeedsAttention：六项检查 pass，migration_completeness 和 request_scope 为 unknown。本工作研究能否在有限、可解释的范围内减少未知，不以该候选必须放行为验收条件。

实际缺口分三部分，不能只增加一种别名匹配：

| 位置 | 实际修改 | 现有规则为何不充分 |
| --- | --- | --- |
| invokeHTTP 的父参数 | `parent := request.Context()`，随后有 `parent == nil` 的 Background 兜底 | requestParentStatus 只识别直接请求参数的 Context 调用；尚无来源、重赋值与可达性证明 |
| invokeHTTP 的取消函数 | 错误和无 Body 路径立即 cancel；成功路径把 cancel 装入新增 releaseBody，在 Close 时释放 | inspectCalls 只支持 RPC 之前登记的局部 defer；没有资源转移证明 |
| 整个 outbound.go | 新增常量和 releaseBody；dispatchHTTP 增加 nil 分支；invokeHTTP 增加结果处理 | normalBoundary 不能解释这些变化。即使类型检查通过，完整剩余结构仍不相同 |

读取范围为公开规范、认证基线、运行时门禁和已暴露的真实候选；不读取私有控制答案或独立参考探针源码。真实候选路径见[原实验](../experiments/2026-10-22-rpcx-fixed-real-model-01.md)，摘要为 `39390941b07031512fd3eac31104acfcb281409e545819d94cf158ecb4c74de9`。

优先级依次为：错误不能因放宽证明而通过、结论有具体证据、减少合理写法的未知、实现与维护成本。未知本身不是规则缺陷；把未知当违规或当通过才违反现有契约。

## 2. 方案选择与代价

| 方案 | 对当前候选和项目目标的影响 | 决定 |
| --- | --- | --- |
| 保持未知，由审核人确认 | 当前工程交付已经成立，新增复杂度为零；每次遇到相同局部形式仍需复核 | 保留为默认退路；新方案无法证明时采用 |
| 只追踪父上下文别名 | 能解释局部来源，成本小；资源转移和范围检查仍未知，不能解决整个候选的交付缺口 | 作为首个实现单元，不声称独立带来自动成功 |
| 因行为探针通过而忽略新 helper 或 Body 包装 | 可以快速放行，但有限请求无法证明其他分支；误改错误返回、提前取消或遗漏资源可能被掩盖 | 不采用 |
| 立即引入 SSA、一般指针/别名分析和跨函数摘要 | 支持面可能更广；仍要自行定义响应所有权、接口动态目标及路径效果，不是引入库即可获得证明 | 当前不采用；只有一个具体边界，尚无支持一般分析的收益证据 |
| 类型身份上的有限来源追踪，加受限资源状态与完整剩余比较 | 能逐项解释当前缺口；接受循环、未知 helper、资源逃逸等继续未知，以及维护有限语言子集的成本 | 采用职责划分；资源部分先做最小原型，达不到边界则只交付来源追踪 |

不选择“一次写完通用分析器”，原因是当前实际收益只有一个已见边界，且证明责任主要来自响应 Body 的生命周期。反过来，也不把所有函数内语句都视为安全：只追踪已明确列出的赋值、判断和退出形式。

延续 [ADR-003](../adr/003-typed-migration-analysis.md) 的 AST/go/types 和有限证明、[ADR-004](../adr/004-analysis-execution-boundary.md) 的执行边界，不新增 ADR。若原型需要一般跨函数分析才能成立，应重新选择方案。

## 3. 函数内来源追踪契约

按实际语句顺序处理，以 types.Object 区分变量，按类型选择区分标准库函数和同名业务方法。最小内部状态为“原请求来源 / 明确脱离 / 未知”，另记是否已证明非 nil 及其来源位置。临时状态不作为通用框架暴露。

支持以下有限形式：

1. 本函数原 `*http.Request` 参数的真实 Context() 返回值；直接表达式和直线 `:=` / 单对象 `var` 初始化可使用同一来源规则。局部别名可以沿声明顺序传递，不允许依靠变量名匹配。
2. 别名区间没有重赋值、取址、闭包捕获、go/defer 间接使用或传给未知函数。请求对象的重绑定和可能改变其来源的操作也要检查，不能只检查别名本身。无法核实影响时标 unknown。
3. 特例只允许 `if parent == nil { parent = context.Background() }`，或交换 nil 两侧的同一比较，无 init/else、额外语句或其他条件。只有 parent 的同一对象已由真实 Request.Context() 证明非 nil，且此前没有失效写入时，才记录该分支不可达。未知 context 参数不获得这个豁免。
4. 常量仍用 go/types 求值，必须为每次调用 30 秒；派生 ctx 必须是本次真实 SDK/受认证接口 Call 的实际参数，不能仅证明“函数里存在一个正确 WithTimeout”。

本机 `go doc net/http.Request.Context` 明确其结果始终非 nil。这是标准库方法身份成立后的事实，不是从探针通过推断的规律。实现时也应记录实际工具链身份；可能为 nil 的 Request 指针在调用该方法时的异常行为不能被归约成任意可用 context。

明确脱离的直线别名（例如从 Background、TODO、WithoutCancel 得到）可给 fail；混合分支或不受支持的表达保持 unknown。未知父参数伴随已证明错误的 31 秒时长，仍保留超时违规；一个维度未知不能覆盖另一个维度的明确失败。

## 4. 响应资源的有限证明契约

这里需要区分两个 context：调用方新建的 RPC callCtx，以及基线 dispatchHTTP 已为响应 Body 建立的上下文。不能笼统要求“所有 cancel 都 defer”或“所有 cancel 都在 Body.Close”：基线本来就隔开了 RPC 返回与 Body 消费生命周期。

### 4.1 首单元保留的释放模式

来源追踪只组合现有局部 defer 模式，不扩大资源支持。是否允许这一模式，仍需现有范围与行为检查确认 dispatchHTTP 保留基线的请求父 context、deadline 复制及 cancelBody；函数退出时结束 RPC context 不代表 Body context 同时结束。

### 4.2 需要原型验证的返回 Body 模式

第二单元限定在原 invokeHTTP、原 dispatchHTTP 和同文件一个新增、未导出的 Body 包装类型，使用固定控制流角色；不按 releaseBody、cancel 等文字匹配。

派生资源按路径具有“持有 / 已释放 / 已转交 / 未知”状态。只有已识别的 RPC 返回后错误分支、无响应或无 Body 分支、带 Body 的返回分支获得新支持；循环、goto、未知调用处理资源、多个 RPC、多个可选包装器和接口动态转交均为 unknown。每个可达退出都必须有解释。

| 路径 | 需要证明的事实 |
| --- | --- |
| RPC 返回错误 | 对应取消函数调用后返回原响应和原错误；资源未转交 |
| 成功但响应或 Body 为空 | 对应取消函数调用后返回原响应和 nil；不产生包装器 |
| 成功且 Body 非空 | 不提前取消；将原 Body 与同一取消函数装入包装器，把包装后的 Body 留在同一响应中返回 |
| 包装器 Read | 直接保留原 io.ReadCloser 的 Read；不新增截断、替换或错误转换 |
| 包装器 Close | 在调用原 Body.Close 前登记同一取消函数的 defer，原样返回 Close 错误；无额外可观察操作 |

只支持嵌入原 io.ReadCloser、附带一个 context.CancelFunc 字段和上述 Close 的包装器。类型身份、字段绑定、构造位置和完整用途必须可枚举；不接受字段外泄、重写 Read、异步释放或调用任意 helper。复用基线 cancelBody 的完整结构证明 Body 上下文保持原父取消和 deadline；同名但已改写的类型不受信任。

原 RPC 错误可能伴随非 nil 响应；不得把返回 `response.Response, err` 改为 `nil, err` 后称等价。短路 nil 判断的顺序也要保留，避免新增解引用。新包装器可能改变动态类型，因此这里仅证明公开规范规定的 Body Read/Close、错误和所有权契约，不宣称对任意反射、类型断言或外部消费者等价。

**停止条件：**若无法完整枚举资源用途、退出路径及包装器效果，则保留 unknown；不得凭现有行为探针补齐静态证明。明确提前取消、丢失原错误或在受支持路径缺释放等 fail，必须引用实际已证明可达的操作；单纯找不到形状依然是 unknown。

## 5. 范围检查与证据传递

normalBoundary 当前独立按语法删去派生语句，没有足够信息支持更宽的资源归约。扩展后仅允许归约由同一输入、工具和构建配置证明的节点，随后仍比较完整剩余结构。

拟采用以下边界，而不增加共享 analysis.Report 的字段：

- GHCLI worker 与它自己的 inspect 消费端使用本地、严格解码的版本化信封，内部包含既有 analysis.Report 和 GHCLI 专用证明。其他场景不接受此信封；保留已有 typed-analysis.json 供现有消费者使用，新增 boundary-proof.json 作为独立证据。
- 证明项包括类别、pass/fail/unknown、原因、相关规则、源码节点及绑定。节点使用文件摘要、字节区间、AST 类别和定义位置关联对象，不序列化内存地址；同一对象的用途须能覆盖加载的完整生产包。信封版本、字段、状态与原因白名单在消费端校验，拒绝尾随 JSON。
- 宿主复核输入/工具摘要、配置、活动文件、区间边界和节点摘要。缺项、重叠或互相矛盾的归约不能通过；协议伪造或材料不匹配按验证错误处理，不伪装成业务 unknown。
- 第一单元只输出来源证明和保留旧释放模式的事实。第二单元才允许范围检查消费已通过的资源证明；无证明时走旧范围检查或保持 unknown。

只在比较用的 AST 副本移除已证明的别名/不可达 nil 分支、30 秒常量及专用导入、派生/释放结构和已证明的包装器。每次移除记录原节点与规则。Dial/Call 适配也须绑定真实 SDK 对象，不能把同名业务方法一并改写。

归约后的 invokeHTTP 返回响应、错误与操作顺序必须与基线一致；dispatchHTTP 除已证明不可达的新增 nil 分支外保持基线。新增类型、常量的全部用途若不能解释，就不消去声明。其他函数、方法、导入和生产声明全部继续比较；语义指令单独保留校验，不能当普通注释忽略。

保留原八项质量要求，不关闭 request_scope 或 semantic_behavior。迁移完整性引用来源/期限/资源证明；范围检查引用局部归约和剩余结构证据，均关联实际规范条款。局部 pass 不等于最终 ReadyForReview。

## 6. 实现顺序与验收矩阵

两项可验收工作分别提交，不按文件或微小函数拆分：

1. **函数内来源与证据链：**新增局部分析、GHCLI 信封及严格消费、来源正反例、旧资源模式组合和真实隔离 worker 对照。交付能解释别名的分析器；真实保存候选仍因 Body 转移和 request_scope 为未知。
2. **Body 资源与范围闭环：**先以构造程序验证资源状态和剩余比较，再接 worker、scope、证据和完整 Docker Evaluate。原型越界则停止扩展，保留首项交付，不引入一般跨函数引擎。

下表是设计预期，不是已经运行的测试结果。除专门反例因素外，各样本其余前提应充分，不能用无关 unknown 隐藏是否击中规则。

| 组别 | 对照 | 必须观察到的结果 |
| --- | --- | --- |
| 来源正例 | 直接请求、单级/多级直线别名、标准库导入别名、不同局部名 | 来源 pass；原直接模式不退化 |
| 不可达分支 | 已证明非 nil 的请求 context 加限定 nil 兜底 | 来源 pass；记录分支依据和对象 |
| 明确脱离 | 直接及直线别名的 Background/TODO/WithoutCancel | 来源 fail，组合候选 Rejected |
| 写入与逃逸 | 请求重绑、parent 重赋值、取址、闭包、go、传入未知 helper | 不得沿用过期来源 pass；支持范围外为 unknown |
| 未知与伪装 | 普通可空 ctx 的 nil 兜底、自定义同名方法、混合分支、遮蔽 | 不得获得请求非 nil 特例；unknown 或独立明确 fail |
| 期限 | 未知来源加 31 秒、变量期限、错用另一个 ctx | 31 秒 fail；其他缺证据不得 pass |
| 资源正例 | 错误/空响应/空 Body 释放，成功包装，原 Read 与 Close 错误 | 第二单元资源 pass，所有退出可解释 |
| 资源负例 | RPC 前取消、成功返回前取消、明确错误分支漏释放、错 cancel 字段 | 支持范围内有可达违规证据为 fail，不得自动放行 |
| Body 负例 | 吞 Close 错误、提前关 Body、改 Read、替换返回响应或错误 | 资源/行为或范围检查阻断，不能被归约消失 |
| 越界资源 | 资源交给未知 helper、循环、多个包装器、未知接口分派 | unknown，不强求给违规结论 |
| 剩余结构 | 改 URL/header/状态处理、dispatch deadline、原 cancelBody、新增无关声明 | 不得得到整体放行；已知违规与范围未知分列 |
| 协议与身份 | 错摘要/配置/节点、缺证明、重复冲突、未知版本、尾随 JSON | 错误协议拒绝；必需证明缺失不产生 pass |
| 完整门禁 | 保存原候选、真正脱离变体、受支持构造正例和未知 helper | 原候选是否可放行取决于全部证明；负例 Rejected，未知例 NeedsAttention，正例确有 ReadyForReview |

第一单元至少验证受影响 worker/消费端、类型与状态绑定，并用真实 Docker 类型加载核对正例和来源负例。第二单元再完整运行含真实到期的 Evaluate、补丁重放、审核导航和精确容器回收。冻结 L1、既有 GHCLI 对照以及 Runner/审计消费者按实际影响回归，跳过项单列。原输入与历史结果逐项摘要核对，输出使用新目录。

不以工作耗时或测试项数量决定通过；任何新增错误放行先撤回对应证明范围。若需要支持任意 helper、指针逃逸或路径合并，先重新比较收益和成本。

## 7. 本轮实际工作与边界

本轮读取了当前 worker、scope、Evaluate、质量/条款映射、原认证基线、保存候选和分类修正报告，确认父参数、资源转移、整文件比较是三个独立问题；用本机 Go 文档核对 Request.Context 的非 nil 契约。没有读取私有控制答案、执行候选、调用模型或修改生产 Go。

交付为本设计及当前文档入口更新。本文中的资源状态、信封和正反例仍是待实现方案；没有新通过率、没有扩大现有支持范围。原候选、规范、Profile、冻结实验和归档保持。第一单元下一步实施；原 M3—M5 的人员与严格未见留出缺口仍保留。

文档检查通过：四份变更文档的 201 个本地链接目标、26 个锚点与代码围栏有效，git diff --check 通过；变更路径仅为本设计、文档索引、里程碑和交付说明。检查材料保存于 runs/ghcli-bounded-proof-design-01/verification.json。未运行 Go 测试、Docker 或模型，故不将设计矩阵记为功能验收。


## 8. 第一单元：函数内来源与证据链

- 实现基线：f150795；日期：2026-10-22。
- 范围：来源追踪、旧局部释放模式组合、跨进程证明及严格消费，不实现 Body 所有权转移或新的 scope 归约。

### 实际实现与具体取舍

worker/source.go 在派生的原位置之前顺序处理语句，按 types.Object 维护原请求、局部 context 的来源和依赖。只接纳直线新声明和已证明非 nil 的限定分支；未支持语句涉及已追踪对象时保守失效，不尝试一般路径合并。标准库 Context、Background、TODO、WithoutCancel 继续按真实包/方法对象确认，不以变量名或字符串判断。

新来源事实与调用完整结论分开：来源可为 pass，调用仍因期限或释放无法证明而 unknown。保存候选因此从“父来源不明”推进到“父来源已证明、释放仍未知”，没有获得新的整体自动放行。来源未知时仍可独立拒绝已证明的 31 秒常量。

组合检查发现原规则会略过任意 var 初始化和额外 defer，也只在顶层识别重赋值。为避免新来源 pass 建立在过宽的旧检查上，本单元限定派生与 RPC 之间为对应 defer、无初始化的 var 和空语句，排除嵌套/部分短声明重赋值、地址与闭包逃逸，要求 Call 直接位于当前语句。额外效果的正当性无法证明时保持 unknown；不把这些形态一律写成业务违规。

协议采用设计中的 GHCLI 专用信封 schema 1，原 analysis.Report/schema 2 不变。生产端与消费端保持各自小型 wire 定义，因为 worker 按独立 go run 文件在隔离工具根编译，直接导入宿主模块会扩大依赖与可见边界；真实 Docker 编解码对照覆盖二者兼容。信封包含完整类型报告、每个调用的来源状态/原因/规则、原节点与定义位置、文件和节点摘要。宿主校验版本、配置、活动源清单、生产覆盖、调用一一对应、节点 AST/范围/摘要、状态与原因及重复证明，拒绝未知字段、尾随 JSON 和身份不符。

原 typed-analysis.json 保留，boundary-proof.json 单独认证落盘，并进入 migration_completeness 的证据引用。协议失败或保存失败走原 Evaluate 错误路径，无质量 verdict；不会降格成可用证明。输入/配置/工具绑定由同一类型报告承载，不额外重复建一个账本或通用证明框架。rule-map、Profile、规范及 quality.Policy 不改，scope 继续使用原检查。

### 结果与边界

保存候选的新完整门禁仍为 NeedsAttention：六项检查 pass，migration_completeness/request_scope unknown。来源证明为 pass，链条包含 request_context、context_alias、unreachable_nil_fallback；调用原因变为 unsupported_local_flow，明确剩余缺口在释放结构。直接 Background 派生父参数的变体仍 Rejected，来源与迁移完整性 fail，行为检查也 fail。

两份新报告的工具材料身份为 a5e3870d9313c28df9633abb03aa8ee5936bbd25961d5561a20a21d4a5dbbf6d，输出在 runs/ghcli-source-proof-01/docker-gate/。原候选摘要仍为 39390941b07031512fd3eac31104acfcb281409e545819d94cf158ecb4c74de9。只运行已有候选和本地构造变体，不读取 .env，不调用模型，不重跑独立参考，不修改原效果指标。

| 验证 | 实际结果 | 证据（runs/ghcli-source-proof-01/ 下） |
| --- | --- | --- |
| 最终受影响包竞态检测 | 两包 122 项测试及子测试 pass，两个显式 Docker 用例 skip，无竞态报告 | core-race-final.jsonl |
| 类型加载及信封真实对照 | 直接请求、别名/nil 分支、脱离、helper 四组分别 pass/pass/fail/unknown；五项测试及子测试通过，实际四条 Docker 命令 | docker-inspect-final.jsonl、docker-inspect-final/ |
| 保存候选完整门禁 | 原样 NeedsAttention、脱离变体 Rejected，来源分别 pass/fail；三项测试及子测试通过，含原到期行为检查，实际十二条 Docker 命令 | docker-gate.jsonl、docker-gate/ |
| 消费端回归 | quality、audit、analysis、Runner、round 五包通过，516 项 pass、25 项 skip；跳过项不算执行通过 | consumers.jsonl |
| 静态检查 | 两个受影响包 go vet 通过 | vet.log |
| 容器回收 | 在 Docker server 可用的前后两次确认之间，20 个精确 ID 均不存在，包含较早一轮四条类型检查 | container-cleanup.json |
| 历史身份 | 登记的 59,589 份数据、协议、原运行及解包归档文件摘要均不变；不含缓存 | history-before.json、history-after.json |

第一次普通检查 first.jsonl 通过时尚未补齐新正反例。随后 second.jsonl 捕获构造程序含显式空语句导致规则返回 unknown；承认 EmptyStmt 无效果后，third.jsonl 及最终竞态检测通过。没有用失败轮次或重复执行累计覆盖数。较早 docker-inspect.jsonl 在补严工具链版本格式校验之前执行，最终四组以 docker-inspect-final.jsonl 为准；消费者和完整门禁使用最终生产规则。

第一单元已验收：可交付有限父来源分析、独立证据和原局部释放模式的组合。资源原型和 Body/完整范围证明仍是下一单元；旧正式实验有效自动成功保持 1/4。不在这一轮重跑完整 L1 正式矩阵，公共分析、quality、规范和冻结数据均未修改。汇总保存在 runs/ghcli-source-proof-01/summary.json，运行产物不入 Git。

收尾检查：六份变更 Markdown 文档的 250 个本地链接目标、34 个锚点及围栏有效，10 份变更 Go 文件格式正确，git diff --check 通过；记录在 documentation.json。代码、测试和文档作为本工作单元一起提交。


## 9. Body 资源与剩余比较的原型（2026-10-23）

- 基线：ea9c624。
- 本次交付：可审查、可复现的有限分析原型、构造正反例及动态见证。生产 worker、scope 和原八项门禁不改；不新增真实模型实验。

### 为什么先独立验证，以及选择了什么

本工作首先回答两个实际问题：能否不引入一般控制流/跨函数引擎就解释当前资源形式；解释之后能否保留原 RPC 参数和错误返回用于剩余比较。仅为真实候选加模板放行无法回答前者；直接接入生产则会在路径规则尚未验证时，同时承担协议、材料和范围归约的变化。

因此新增 ownership 原型包，位于当前门禁 go:embed 模式之外，且不被生产入口导入。它仍使用 Go 对象、标准库类型/方法和选定的实际派生/调用；不拿变量名或函数文本充当身份。接受的代价是本轮不给候选新的门禁结论，后续接入仍需真实包加载、证据认证和完整范围比较。接入时把有限检查迁到隔离 worker，不引入插件系统或长期维护两份规则。

原型不做一般路径求解，而是只接受一条直线 RPC 后的两个分支和成功转交：错误分支原响应/原错误返回前释放；空响应或空 Body 分支释放并原响应返回；成功路径把原 Body 和同一 cancel 装入唯一包装器。包装器必须只嵌入真实 io.ReadCloser、持有一个真实 context.CancelFunc，并有唯一 Close 方法：先登记字段 cancel 的 defer，再原样返回原 Body.Close 错误，Read 使用原嵌入方法。

这是一种可封闭的有限模式。闭包/未知参数传播、其他方法、包装器/字段的额外用途、未知 helper、重新绑定和结构外控制流不在证明范围。可识别的无释放退出、吞 RPC 错误和提前取消记 violated；未支持 Close 写法仍 unknown，不能仅因没识别出就宣称违规。所有用途须在输入类型信息内被消费，其他文件中的引用也阻止 proven。

原型的前置条件包括正确的父来源/期限/SDK 边界。这些在本包未被再次证明；生产接入必须与上一单元绑定，不能单凭本地 Result 通过质量聚合。

### 实现与可观察结果

代码、构造输入及命令见[原型说明](../../scenarios/rpcx/ghcli/ownership/README.md)。SDK 类型与本机动态见证使用公开夹具的实际 client.go，而非改写 SDK；不读取私有控制答案或独立参考探针源码。

静态检查给出四条路径：rpc_error → released，empty_body → released，response_body → transferred，body_close → released。proven 后只对选定的 RPC 调用/返回区间生成比较视图，移除被绑定的 ctx 参数，保留原请求、操作名和响应参数，返回原响应和原错误。比较视图经复制/重解析生成，测试逐次核对输入 AST 未变。

这个局部视图故意不是整文件归约：改变操作名或请求构造后，资源模式仍可能 proven，但比较视图必须保留该业务差异；新增未使用的响应字段也不会仅因资源证明通过而获得整文件认可。完整 scope 仍需比较函数前缀、dispatchHTTP、原 cancelBody、导入及其他声明。

动态见证将八组自建源文件复制到新模块，go.mod 仅 replace 到原本地 SDK，禁用模块网络。正确模式、等价 helper 和返回 error 别名的模式各通过四项实际测试，覆盖传输错误（含非空响应）、空响应/空 Body、成功后读取和 Close 错误/顺序、Body 消费时父取消。五种错误变体分别触发指定的断言失败：错误分支漏释放、空 Body 漏释放、RPC 前取消、吞 Close 错误、吞 RPC 错误。编译/设施错误不被视为反例成功。

等价 helper 的动态见证通过而静态仍 unknown；吞 Close 错误的变体动态失败、静态也保留 unknown。这说明运行观察和有限证明各自给证据，不能相互替代或把所有 unknown 变成 fail。

对保存候选，仅解析和 type-check outbound.go，使用实际 SDK 和标准库类型，不执行候选，也不装载完整业务包。原型识别了该文件的资源模式，调用/返回比较视图与公开基线的对应区间相同。候选文件 SHA-256 为 8f060e7d060f1887f06537ef3e6730d75826466f1edf259cb64d65622b499663，与原目录摘要 39390941b07031512fd3eac31104acfcb281409e545819d94cf158ecb4c74de9 是不同粒度，不能混用。此观察不是一次新 Evaluate，当前门禁仍为 NeedsAttention。

### 验证与接入结论

| 验证 | 实际结果 | 证据（runs/ghcli-ownership-study-01/ 下） |
| --- | --- | --- |
| 原型与现有 GHCLI/worker 最终竞态回归 | 三包通过，153 项测试及子测试 pass、4 项 opt-in 用例 skip，无竞态报告；原型自身为 31 项 pass | core-reviewed.jsonl |
| 本机动态见证与保存边界研究 | 10 项测试及子测试 pass；八个自建模块实际执行 32 个动态测试，其中三组正例共 12 pass，五组反例命中指定失败，不能将内部失败计作候选成功 | study-reviewed.jsonl、native-reviewed/results.json |
| 保存边界的局部静态结果 | 资源模式 proven，四路径有去向，RPC/返回局部视图与原基线一致，文件字节不变；整包与完整 scope 未验收 | saved-reviewed/result.json |
| 静态检查 | ownership 包 go vet 通过 | vet-reviewed.log |
| 当前门禁材料与入口隔离 | 与上单元保留的 13 份 GHCLI 工具材料逐件相同，harness 的依赖不包含 ownership | isolation.json、harness-deps.txt |
| 历史身份 | 上单元登记的 59,589 份数据、协议、原运行与归档材料摘要均不变 | isolation.json |

评审发现初稿会把未证明的错误别名当作吞错；修正为仅对错误分支明确返回 nil 记违规，其他未识别返回保持 unknown，并补返回 error 别名的静态边界对照和实际等价动态见证。较早 first/core-first/core-final/core-accepted/study-first/study-final 记录保留，不与最终 reviewed 轮次重复计数。本轮 Go 依赖枚举初次尝试写宿主默认模块 stat cache，出现权限诊断；改用工作区内的 GOMODCACHE 后退出 0，stderr 为空，依据最终 harness-deps.txt 确认隔离。动态见证中的预期失败都有实际测试事件，未把编译或环境失败当作有效反例。

当前方案的结论限于上述有限语言子集：可以继续实现 worker 资源证据和完整剩余比较，不需要因此引入一般 SSA 或跨函数引擎。本轮没有 Docker、模型请求或独立参考运行，也没有重跑现有完整门禁/真实到期测试；当前实现材料相同，原 NeedsAttention 与正式实验 1/4 保持。汇总见 summary.json，运行产物不入 Git。

下一工作单元应一次交付生产闭环：真实活动包中复用有限资源检查；将资源证明、节点和用途绑定到候选/工具/配置并严格消费；只消去有证明的节点；对 dispatchHTTP 新 nil 分支单独证明标准库 Context 返回非 nil；保留完整剩余结构和语义指令；补协议负例、构造三类判定及原样候选/变体完整 Docker 门禁。任一必要证据不足时继续 NeedsAttention，不以本原型的 proven 替代它们。实际效果指标、原协议、原候选与归档保持。

本研究作为独立的可复验工作提交，表示可行性验证完成，不表示第二单元的生产闭环已验收。收尾检查通过：六份变更 Markdown 的 210 个本地链接目标、30 个锚点与围栏有效，四份新增 Go 文件格式正确，git diff --check 通过；结果保存于 documentation.json。
