# 首轮 L2 NeedsAttention：根因与支持范围评审

- 日期：2026-10-20
- 状态：根因评审与下一单元设计已完成；生产规则尚未修改
- 基线：6bb1945
- 对应里程碑：[M3.4](../MILESTONES.md#m3多仓库批量闭环)
- 输入：[唯一真实批次](../experiments/2026-10-20-rpcx-l2-real-model-01.md)，三个原始 NeedsAttention 不回写

## 本次目标与范围

先解释每项 unknown 缺了什么证明，再选择是否值得扩展。优先维持错误不放行、原结果可追溯，其次减少无意义人工确认；不以把这三个开发候选变成 ReadyForReview 为验收目标。本次不修改生产门禁、Profile、规范、提示或原候选，不请求真实模型，也不代填人类复核。

已读取原 scope/test-integrity/analysis Comparison、三个完整补丁及规范。原未知共六个检查项：Goddd 的 request_scope/test_preservation；Translation 的 migration_completeness/request_scope/test_preservation；Workouts 的 request_scope。初步归因包括导入序列、模板未覆盖的必要 context 适配、允许但未建模的超时写法、共享常量、失败提示字面量；不能统称“测试不足”。

## 诊断方法与预期区分

使用已保存材料和隔离副本，尽量只改变一个因素。现有有限结构比较实现可以复制到 runs/ 的诊断驱动中，但只复用适配/指纹函数，不建立新的质量判定。复制材料记录实际摘要；不在场景源码目录添加临时文件，避免改变宿主材料身份。

1. Workouts：只排列导入，比较现有 syntax 指纹；再改 RPC 方法名作为反例，确认单因素结果不是靠删除全部 AST 差异获得。
2. Goddd 测试：只把原候选 stub 的未使用 `_` 改为宿主适配采用的 ctx，消费原 adapted tests 比较；原断言或 helper 逻辑变化仍保留差异。
3. Translation 测试：只还原失败提示字符串，比较原 adapted tests；保持断言条件、调用不变，识别字符串保守策略的代价，不在正式规则中忽略任意字符串。
4. Translation 分析：直接核对 worker 的前缀限制和原 Comparison；必要时在副本仅将派生/defer 移到纯局部构造之前，再经现有 Inspect 观察诊断变化。这是构造对照，不是新迁移候选或原 Run 重试。
5. 生产范围未知：将具体 AST 差异与宿主 adapt/syntax 对照，分别列出可局部证明、需要类型绑定/控制流证明及继续人工确认的部分。

本次对照只能解释给定样本的触发原因，不构成通用语义等价证明。下方记录实际证据与方案选择，正式规则扩展另作可验收单元。

## 逐项结论：什么没有被证明

| 原 unknown | 实际触发点 | 分类与本轮结论 |
| --- | --- | --- |
| Workouts / request_scope | trainer_grpc.go 的导入从 context、rpcx、time 排成 context、time、rpcx。AST 指纹保留导入数组顺序；users 文件新增 time 在比较前被既有逻辑剥离，所以只有 trainer 被标记 | AST 表示噪声。仅恢复导入顺序即匹配既有范围；没有发现这个 unknown 所指文件还残留其他未建模结构差异 |
| Goddd / test_preservation | stubRoutingService.FetchRoutesForSpecification 新增参数为 `_ context.Context`，宿主 adapted-tests 为 `ctx context.Context`；该形参未在原 helper 体使用 | 未使用形参名的等价表达未被支持；仅恢复名称后整份原测试集合被 preserved。不是原断言已删除，也不是新增 context 导入被拒绝 |
| Translation / test_preservation | TestTranslationRoundTrip 中 t.Fatal 的唯一字符串由 v1 default deadline missing 改为 per-call 30s deadline missing，原条件未改 | 诊断字符串策略保守；仅恢复文字后 preserved。不能据此在正式规则中忽略所有字符串、所有 testing 调用或实际断言条件 |
| Translation / migration_completeness | alternate 在请求/响应局部构造之后才派生超时。worker.boundary 只允许可选 ctx.Err guard 后紧接 WithTimeout/defer，再处理局部构造 | 类型分析形态未覆盖；RPC 同函数/操作配对已成功，Changes 为空。不是漏迁移某个配置、剩余 v1、动态操作或服务端期限已被证明错误 |
| Goddd / request_scope | mock/mock.go 的函数类型字段与 FetchRoutesFn 调用增加 ctx，但宿主 adapt 只给方法/接口和特定方法调用适配，漏掉该字段与调用。proxying.go 另有 30 秒命名常量、构造器 `_ = ctx` | 必要 ctx 适配模板不完整，加上常量/无效使用语句超出范围。普通注释已被此处解析器忽略，不是原因；超时本身的类型分析与行为均已通过 |
| Translation / request_scope | default 使用 rpcCtx 而非原 ctx；alternate 的期限前缀不在 syntax 预期位置；共享 translation_types.go 新增超时常量。此解析器还保留普通注释，v1→v2 文案也参与指纹 | 同时包含绑定/语句位置/新增声明及普通注释噪声，不能用一个“忽略格式”补丁消除所有问题。需要将可证明的期限变换与其余业务结构分别核对 |

这里未定位到上述 unknown 所代表的、确定违反规范的业务变化；这不是对整个候选正确性的证明，也不把 unknown 叫作 Rejected 误报。原独立自动参考只支持其列明行为，人类三项复核仍未完成。Goddd 的 ctx 传播本来就是规范要求的新增行为，不应把“与基线不同”一概说成未经批准变化。

关键实现位置：[Workouts 指纹](../../scenarios/rpcx/workouts/structure.go)、[Goddd 宿主适配](../../scenarios/rpcx/goddd/adapt.go)、[Translation 指纹](../../scenarios/rpcx/translation/structure.go)、[类型边界](../../scenarios/rpcx/analysis/worker/rules.go)、[原测试声明比较](../../scenarios/rpcx/testintegrity/compare.go)。

## 最小对照的实际证据

全部产物位于 runs/needs-attention-review-01/，不入 Git。Workouts/Goddd 的驱动复制现有适配/指纹实现，仅改包名；Workouts 去掉不使用的质量聚合函数及其 import。实际源文件摘要和复制方式已保存。测试对照直接调用原 CompareAdapted，使用 Run 已落盘的 adapted-tests、完整原/候选测试和相同宿主 Plan。它们只解析 AST，不执行候选。

| 对照 | 实际结果 | 证据 |
| --- | --- | --- |
| Workouts 原样 / 仅导入排序 / 排序后改 RPC 方法名 | syntax 不匹配 / 匹配 / 仍不匹配；没有删除正文来制造相同指纹 | workouts-syntax.json、workouts-syntax.log |
| Goddd 原测试 / 仅 `_`→ctx / 再改一处原断言条件 | unknown / preserved / unknown | test-probe.json |
| Translation 原测试 / 仅恢复失败提示 / 再改期限断言条件 | unknown / preserved / unknown | test-probe.json |
| Translation alternate 原样副本 | complete，调用 unknown / unsupported_timeout_shape，复现原诊断 | translation-analysis/evidence-original/ |
| 仅把派生/defer 放在原纯局部构造之前 | complete，调用 covered / direct_timeout_boundary | translation-analysis/evidence-derive_before_pure_locals/ |
| 上述副本将 30 秒改 5 秒 | complete，调用 violation / timeout_duration_mismatch | translation-analysis/evidence-wrong_duration/ |
| 上述副本改从 Background 派生 | complete，调用 unknown / unproven_parent_context | translation-analysis/evidence-wrong_parent/ |
| Goddd 生产模板对照 | 导出两个宿主 expected 文件；字段函数类型/调用 ctx 缺失、候选常量与 `_ = ctx` 差异明确 | goddd-adapt.json、goddd-*.diff |

四项类型对照使用原 Inspect 和固定禁网 Docker 镜像、认证的离线依赖，分别保存输入/工具/命令/报告身份；同一缓存只作串行编译缓存。没有运行候选 init、业务或测试，也没有重跑原 Run。移动语句仅用于解释分析器的位置限制，不证明任意移动都保持所有可观察语义，正式支持不能通过实际改写候选来迎合模板。

测试反例的 unknown 表示“不证明原断言保留”，不是已经输出 violated/Rejected；方法名反例也只是范围指纹不匹配。这里没有对这些构造副本调用质量聚合，不把局部信号升级成最终判定。

诊断驱动最初要求 Goddd 的负例片段唯一，但原文件有两处相同条件，驱动自身定位断言失败；失败信息与空 stdout 已保留为 test-probe-first-error.json、test-probe-first.stdout.log。随后显式选第一处条件完成两组共六个测试对照。没有原候选或生产规则修改，也不计为一次模型/迁移重试。

## 方案取舍与选择

当前目标顺序：不降低放行所需证明强度；消除能局部证明无关的差异；最后才是支持更多代码形态。三个候选已用于诊断，之后只能作为已见开发对照，不能改名充当留出或用新规则宣称原实验已经成功。

| 方案 | 在本轮材料上的具体结果与代价 | 选择 |
| --- | --- | --- |
| 继续接受所有 unknown，全部交人审核 | 不增加验证器复杂度，但 Workouts 仅导入排序也要占用人工确认；不会解决真实证据揭示的可避免噪声 | 保留为超出新范围时的默认行为，不作为唯一后续工作 |
| 行为探针通过就忽略结构/test unknown | 三例可能立即放行，但已知删断言样本能通过自动行为参考，且探针不覆盖任意业务改动 | 不采用，违反必需检查与 SCOPE/TEST 约束 |
| 一次性做通用 AST 语义等价、全调用图或 LLM 裁判 | 可覆盖常量/helper/参数名/语句位置，证明与验证成本远超一个导入排序问题；还会引入新盲区和成本 | 不采用 |
| 逐类限定证明、分开验收；先消除导入顺序噪声 | Workouts 只需保持 import 身份不变后归一顺序；保留全部正文比较和其他门禁。Goddd/Translation 仍可继续 NeedsAttention | 采用；接受首单元不消除所有未知、不保证任何真实候选一定获放行 |

首个实现单元选 Workouts 的导入排序，而非同步开放共享常量、任意 ctx 改名、语句移动和失败提示：前者不需要扩展 RPC 控制流或测试断言语义，已由单因素对照完全隔离；后几项分别需要对象绑定、受限表达式/声明证明或诊断参数边界。把它们一起改会无法知道是哪里增加了放行范围。

不统一忽略普通注释的原因是 Translation 将 build tags 等语义指令与普通注释一起解析；必须先单独保存并核对指令，才能安全剥离其他文本。Goddd/Workouts 已忽略普通注释，不能把三个场景的实现当成同一问题直接替换。

## 下一实现单元的具体边界与验收

**范围：**只在 Workouts 的现有 AST 指纹中，将已有 import 的顺序作确定性归一；保留完整包路径、显式别名/空白/点导入标记与原声明集合，其他 AST 不做无条件忽略。现有 directives 拒绝、原测试/构建、类型身份/30 秒/释放、可信业务检查均不变。不额外引入 DSL、配置或通用等价引擎；出现明确重复后再决定是否抽共用函数。

1. 正例：同一声明集合在一个 import 块内换序、标准库/第三方分组变化仍可比较；ctx 重用的现有正确形态保持。
2. 反例：新增/删除 import、改变别名或导入路径、改为点/空白导入、语义指令变化、RPC 方法/参数/错误处理变更不得借排序得到结构通过；错期限/父 ctx/提前 cancel 仍由原必需检查阻断。排序不能删除未知声明或隐藏 helper。
3. 运行既有 Workouts 正/反/未知矩阵，保留原预期；对原真实候选的新检查只能写入单独的“新验证器开发校准”目录，实际工具材料身份另存。不得改原 Run、替换原门禁或重新计算原成功率。
4. 验证完成前不保证 ReadyForReview；若只有排序差异且所有其他检查仍充分才允许新判定放行。仍存在结构/执行证据缺口时保留 NeedsAttention。

后续再分别评审：Goddd 已认证 mock 的必要 ctx 传播及未使用参数名；Translation 派生之前的纯局部前缀；共享 30 秒常量和派生 ctx 使用的绑定证明。测试失败提示暂保留 unknown，不能只为清除这一例把所有文字参数忽略。

## 版本与证据边界

本轮没有改任何 Profile。现有 Profile 固定输入/配置、验证器标识和宿主依赖身份；具体比较器源码由场景的材料摘要及实验源码 pin 另行绑定。不能把相同 Profile ID 当作整套比较器源码从未变化的证明。

后续仅做上述表示修复且不变更 Profile 内容时，原内容不需重签，必须保存不同的实际工具材料与源码身份；若改变已发布 Profile 的验证器标识或契约字段，则按 [ADR-007](../adr/007-host-owned-task-profiles.md) 另起 ID。新的真实实验无论如何都要发布匹配代码/二进制的协议，不能修改 v0.1.0 的 freeze 或复用其旧源码摘要。历史 Run、材料和判定继续由原身份解释，不自动升级。

此处是既有证据/版本约束的应用，不新建 ADR；扩大控制流证明或改变长期验证策略时再单独决策。

## 本单元结论

六项 unknown 已有逐项证据与下一单元验收边界；生产代码、原三项 NeedsAttention 及复核状态均未改。四个类型分析容器已回收，原 5907 份 Campaign 材料保持摘要；摘要与驱动身份见 runs/needs-attention-review-01/verification.json。

本轮只进行了诊断对照，没有实现新归一规则、执行真实模型、补全人类复核或宣称 M3 完成。下一步实施并验收上述 Workouts 导入顺序修复，再根据独立证据决定下一类扩展。

文档验收：五份文档的 101 个本地链接目标与代码围栏完整性检查通过，git diff --check 通过；代码及冻结协议未改。本轮使用上述诊断对照验证归因，没有运行项目 Go 回归；规则实施后的回归另行验收。检查摘要见 runs/needs-attention-review-01/document-checks.json。

后续实施及验收见 [Workouts 导入顺序归一](2026-10-20-workouts-import-order.md)，本记录中的诊断结果与原实验身份保持不变。
