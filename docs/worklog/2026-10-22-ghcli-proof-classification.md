# GHCLI：区分父上下文违规与证明不足

- 日期：2026-10-22。
- 状态：已验收，范围为父上下文证据分类与保存候选开发回归。
- 对应工作：[四任务真实评测](../experiments/2026-10-22-rpcx-fixed-real-model-01.md)后的开发校准。
- 代码基线：a110168。

## 问题与目标

真实生成的 GHCLI 候选通过构建、原测试和运行时行为检查，但类型分析把 `WithTimeout(parent, 30*time.Second)` 中的局部变量 `parent` 记为 `fail / request_parent_not_preserved`。原规则只证明直接使用 HTTP 请求参数的 `Context()`，其否定分支却把所有不匹配形式当作明确违规。候选使用请求上下文的局部别名和冗余 nil 分支，未被现有证明器支持，不等于已证明丢失父上下文。

目标是修正证据分类：已证明保留为 pass，明确脱离请求取消链为 fail，其余为 unknown。保持已有放行范围，不通过识别这一份补丁扩展自动放行。原实验、候选、判定、指标、协议和归档包保持原样。

## 方案权衡

优先目标是拒绝必须有正面违规证据，同时不把既有错误样本放行。

| 方案 | 对当前候选的行为及代价 | 选择 |
| --- | --- | --- |
| 所有未识别父上下文统一改 unknown | 消除误拒，但直接 `Background()` 也会失去明确违规结论 | 不采用；已有类型身份足以证明这类错误 |
| 新增别名、nil 分支和返回资源的数据流证明 | 有机会证明实际候选，但还涉及资源释放及结构边界；为本次分类修正引入额外的正确性义务 | 暂缓；以后需独立固定正反例再扩展支持范围 |
| 分开判定已证明保留、已证明脱离、其余未知 | 实际候选保持 unknown，需要人工关注；直接脱离取消链仍拒绝，原可证明写法仍通过 | 采用；最小修改恢复既定证据契约，不增加自动放行形式 |

明确违规限定为类型身份认证的标准库 `context.Background()`、`TODO()` 或 `WithoutCancel(...)` 直接作为派生父参数；不按函数名字或出现 `Background` 字符串拒绝。别名、辅助函数、自定义同名方法等不扩展数据流，标记 `request_parent_unproven`。未识别父参数不阻止独立识别常量超时不等于 30 秒的明确违规。

这是既有三值证据契约的纠错，沿用现有 ADR，不新增 ADR。协议结构和 VerifierVersion 保持兼容，实现身份由实际工具材料 SHA 区分；不得修改历史报告的工具摘要来替换旧实现。

## 实现与验收计划

1. 将父上下文证据分类抽为局部规则；保持原 pass 证明形式，保留明确违规的原因码。
2. 构造类型检查回归，覆盖直接请求、真正脱离、局部别名及 nil 分支、辅助函数、同名伪装和未知父参数伴随错误常量超时。
3. 在新目录使用真实实验保存的同一候选重新执行运行时门禁；应变为 NeedsAttention，不能直接放行。同时构造直接 Background 父参数的变体，确认迁移完整性仍 fail、总体仍 Rejected。两者均不调用模型或离线参考。

开发输入为已在生成后助手复核中打开的公开规范、实际候选及运行时实现。本次继续使用其进行开发校准，明确不是未见评测；不读取私有控制答案或独立参考探针源码来调整规则。

验收同时核对原候选及历史判定摘要不变、新报告绑定同一候选及不同工具材料、实际命令与容器回收。先运行受影响包的测试和静态检查，再按实际依赖范围补回归；尚未执行的检查不视为通过。

## 实际实现与验证

`worker/rules.go` 新增父参数的三值分类，类型身份认证后才认定标准库的取消链脱离。unknown 使用 `request_parent_unproven`，明确违规保留 `request_parent_not_preserved`；继续检查可求值的常量超时，因此未知父参数伴随 31 秒上限仍 fail。没有扩展别名证明、返回资源证明或结构归一化。

构造回归由原 6 种扩为 17 种，除父参数外继续覆盖错误时长、资源释放缺口、调用上下文辅助函数及接收者重赋值。自定义同名 `Context`/`Background` 不伪装成标准库，别名 Background 也不因数据流未支持而自动放行。新增可显式启用的保存候选 Docker 测试，拒绝与原候选重叠的输出路径，独占创建新产物目录，并在结束时核对原候选摘要。

保存候选 `39390941b07031512fd3eac31104acfcb281409e545819d94cf158ecb4c74de9` 在 `runs/ghcli-proof-classification-docker-01/saved-candidate/` 得到 NeedsAttention：六项检查 pass，迁移完整性与 request_scope unknown。类型报告仍完整加载，识别唯一 `invokeHTTP / http.do` 调用和 30 秒常量，父参数为 `unknown / request_parent_unproven`。原报告 Rejected 不改。

负例仅在新副本把唯一 `WithTimeout(parent, outboundCallTimeout)` 改成 `WithTimeout(context.Background(), outboundCallTimeout)`，候选摘要为 `814387941cb446de9b5ac5b402ad553bb51426f0ced0bc3ebf475f8767b20007`。构建和原测试仍通过，迁移完整性与行为检查 fail，总体 Rejected；材料在同目录 `detached-parent/`。这证明没有用“测试通过”替代父上下文约束。

原工具材料摘要为 `fddd1ddf43ab2e5b2b0ec9835f18f4bc50a5272814aba1400c136d42f026b5b2`，两份新报告均绑定 `a5567e8f755bbf7b9b8a4ad915e10efb2cf5f0006a04df0937b00e87f0cfea8f`。新旧实现及结果可区分，未替换旧报告或冻结协议的身份。

| 验证 | 实际结果 | 证据（工作区相对路径） |
| --- | --- | --- |
| 受影响包最终竞态检测 | ghcli 与 worker 两包通过，36 项测试/子测试 pass；显式 Docker 用例在此命令中 skip | `runs/ghcli-proof-classification-race-final.jsonl` |
| 质量、审计、共享分析、Runner 与轮次汇总消费端回归 | 五包通过，516 项测试/子测试 pass，25 项 skip；跳过项不计入执行通过 | `runs/ghcli-proof-classification-consumers.jsonl` |
| 保存候选及直接脱离变体的真实 Docker 门禁 | 父测试及两个子测试通过；原候选 NeedsAttention，负例 Rejected；12 条实际命令 | `runs/ghcli-proof-classification-docker-01.jsonl` 与两份 `report.json` |
| 记录到的容器回收 | 实时查询 Docker 可用后，12 个精确 ID 均不存在 | `runs/ghcli-proof-classification-docker-01/container-cleanup.json` |
| 受影响包静态检查 | go vet 通过 | `runs/ghcli-proof-classification-vet-final.log` |

首次受影响包检查因回合中断只留下部分日志 `runs/ghcli-proof-classification-first.jsonl`，不视为完整通过；随后普通检查及最终竞态检测均完成。Docker 运行启动后，测试代码补入输出路径重叠拒绝及复用 JSON 写入工具，生产规则未再变化；最终竞态编译覆盖测试代码，真实运行的输入/输出路径确实不重叠。该补充不改变新报告的工具材料身份。

消费端回归完成，退出 0。另核对 16 个历史文件（原 Run 报告/补丁/类型报告、原轮次汇总与冻结协议文件）的字节摘要不变；原候选由测试完整目录摘要核对。新报告与原报告的候选、基线及规范身份一致，工具材料身份不同。164 个本地文档链接目标、Go 格式及 git diff --check 通过。日志与结果汇总在 `runs/ghcli-proof-classification-summary.json`，运行产物不入 Git。

## 结论与下一步

误拒分类修正已经在规则和完整门禁路径验收，没有扩大自动放行形式。本次没有模型调用、独立参考重跑或新效果指标；原真实实验的有效自动成功仍为 1/4。

当前仍不能自动证明实际候选的父参数别名和返回资源/辅助函数结构，所以保留 NeedsAttention。后续若要减少这类未知，需要单独设计有边界的数据流与资源释放证明，并补正反例；不能仅凭这次候选的行为测试通过改成 ReadyForReview。后续真实效果评测须使用新实现身份及新协议，不能复用旧冻结身份来覆盖旧结果。
