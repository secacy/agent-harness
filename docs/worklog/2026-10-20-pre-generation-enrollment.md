# 生成前的计划与评测纳入清单绑定

- 日期：2026-10-20
- 状态：已验收（受控模型与本地执行范围）
- 基线：05b326c
- 对应里程碑：[M3.4](../MILESTONES.md#m3多仓库批量闭环)

## 问题与目标

已有 round.Manifest 绑定实际 PlanSHA256，但 batch 的能力查询时间在启动时才形成，不能预填一个真实 Plan 摘要。事后从结果创建清单又不能说明任务和参考判据在生成前已经固定。本次把静态纳入与运行时绑定分开：提前固定 Target 顺序、基线/规范与参考身份；读取实际计划后发布最终 Manifest，并在发布成功前禁止任务接纳。

本单元不发布完整实验协议、不决定最终预算、不访问真实模型或 .env。静态纳入文件不是模型调用授权，Kind 也不认证服务或复核人员身份。

## 方案权衡

优先顺序：首个生成请求前完成绑定、只依据实际落盘计划、控制文件失败时零任务接纳，其次减少 API 改动。

| 方案 | 对当前代码的具体影响 | 取舍 |
| --- | --- | --- |
| 运行结束后补 Manifest | 无需改调度器，但可按结果改分母/参考，不能实现生成前固定 | 不采用 |
| CLI 预测 Plan 的 JSON 字节并提前计算摘要 | 不改 Campaign，但重复 UTC 规范化、JSON 格式及换行规则；两处漂移会绑定错误计划 | 不采用 |
| 拆成 prepare/execute 两个持久化阶段 | 可先审完整动态计划，但要定义旧准备状态、过期材料、重复执行和清理，接近 M4 恢复工作 | 暂缓 |
| Campaign 发布实际计划后执行可信宿主准备函数，再接纳 Target | 宿主直接读取真实文件，通用层不依赖 rpcx/evaluation；代价是新增一个明确的初始化边界 | 采用，初始化失败停批，原单 Run 不变 |

Campaign.Config.Prepare 接收实际 plan.json 的绝对路径与摘要，在任何 Target 目录或账本创建前执行。只交付不可共享修改的 Artifact 值；准备结束后复核计划字节，删除/篡改即失败。初始化错误保存 Failed，父 context 取消保存 Cancelled，均带结束时间与诊断且零任务接纳。该函数是可信宿主组装，不是 Agent 工具或任意插件；不响应取消或阻塞的宿主 I/O 仍不具备硬终止保证。未来需要多阶段可恢复准备时再比较两阶段方式。

## 数据与边界

- 新增静态 Enrollment：schema_version、id、kind、按计划顺序的 TargetPin；没有 CampaignID 或 PlanSHA256 占位值。通过版本化参考材料元数据核对 data/spec/tool 身份，不读取候选或门禁结果。
- batch 可选引用 `enrollment: {path, sha256}`。本地检查时验证文件原文、目标集合及当前宿主参考身份；有漂移则在任何 API 请求前拒绝。省略该字段保留原普通批次行为，不声称已做评测纳入。
- 实际计划发布后，宿主根据同一静态快照写入 enrollment.json、round-manifest.json 和可供 summarize 使用的 round-request.json。输出均位于批次根目录，Agent/门禁工具根不增加这些路径。
- 参考元数据接口只返回版本/规范/数据/工具摘要。参考源码与探针不进入 Agent 消息或运行时质量门禁；候选产生后仍由原独立参考与复核形成结论。
- 现有 Manifest、旧报告和归档数据保持兼容；不把受控机制测试标为真实开发效果。清单发布顺序由可信宿主和受控请求观测证明，不提供第三方签名或对抗宿主事后篡改的保证。

## 验收计划

1. 三种参考发布身份可在没有候选/命令执行时取得，并与原参考报告的身份算法一致。
2. 静态清单拒绝重复、漏项、额外项、换序、输入/规范/参考身份漂移与未知版本；绑定严格使用给定实际 Plan 原文字节。
3. Campaign 准备阶段读取到已发布计划；失败、取消或篡改计划时 Execute/底层模型调用为零，保留失败状态。
4. 模拟 HTTP 的首个 POST 到达时，磁盘清单已与真实 Campaign 计划摘要一致，静态原文已保存，Agent 消息不含评测材料；发布失败不启动生成。
5. 用自动发布的 Request 执行离线汇总，未复核保留未知；旧不带 Enrollment 的命令、原轮次机制及已发布证据读取不退化。

## 实际实现

Campaign 仅新增可选 Prepare 边界；参数是实际文件 Artifact，通用层没有导入参考、清单或 rpcx 代码。准备返回后复核计划原文，父取消与材料错误分别保存 Cancelled/Failed 及结束时间；此时未创建 Target 或账本。没有多阶段恢复、插件注册或重试。

evaluation.PublishedIdentity 从已认证的发布材料取得规范、数据与工具摘要，算法与实际参考报告相同；没有候选参数或命令执行。工具生产源码增加身份接口会改变 ToolSHA256，三个静态示例按新身份生成；数据发布、规范、候选补丁、oracle 和历史报告不改。旧 Manifest 的消费不要求匹配当前工具，仍以其声明和归档材料绑定。

round.Enrollment.Validate 核对完整目标顺序、输入声明及当前参考身份；Bind 只对实际计划原文取摘要。CLI 本地读取并核对带摘要的源文件，固定一份内存快照；后续发布使用该快照。实际计划落盘后保存静态原文、动态 Manifest 和绝对路径的汇总 Request。三个文件成功后才接纳任务；缺省 enrollment 的普通批次保留原流程。

模板见 [batch 配置](../../examples/rpcx-l2-batch.json)与[静态清单](../../examples/rpcx-l2-enrollment.json)。它们固定当前三仓身份，但保留模型占位符及示例预算，不是完整实验协议，也不是本次生成授权。未给 Prepare 增加 Agent 工具或门禁反馈，首个模拟 POST 的消息中未出现清单文件或参考版本。

## 验证与实际产物

| 验证 | 实际结果 | 证据 |
| --- | --- | --- |
| Campaign、round、evaluation 完整竞态回归 | 三个包、157 项测试及子测试通过，无竞态报告 | runs/enrollment-core-race.jsonl |
| 最终 Campaign 竞态回归 | 33 项测试及子测试通过，覆盖最后补齐的初始化父取消终态及原调度/预算对照 | runs/enrollment-campaign-race-final.jsonl |
| 新增命令聚焦竞态 | 7 项测试及子测试通过；真实基线/门禁联调、静态身份漂移、发布失败与首 POST 前文件观测 | runs/enrollment-command-race-first.jsonl |
| 完整项目回归 | 25 个测试包、950 项测试及子测试通过，退出 0；包括模板身份、旧 batch/单 Run、原门禁与参考汇总 | runs/enrollment-project-tests.jsonl |
| 模板准入 | 三个 Target 的本地 --check 通过；无凭证和网络 | 本轮命令记录，另有项目回归中的模板身份测试 |
| 离线汇总 CLI | 自动发布 Request 的副本成功消费，保留失败与未知 | runs/enrollment-summarize-cli.log |
| 静态检查 | Campaign、evaluation、round、harness 的最终 go vet 通过 | runs/enrollment-vet-final.log |

受控批次保留在 `runs/enrollment-cli-artifacts/batch-cli-641812884/batch/`：2 个 Target，1 个 Completed / Rejected，1 个 Failed / input_invalid。模拟模型原样提交候选，唯一生成响应声明 25 Token，不是实际供应商费用。服务收到首个 POST 时，静态快照、Manifest、Request 均已存在，Manifest.PlanSHA256 与实际 plan.json 字节一致，静态副本与配置中的源摘要一致。

自动 Request 在测试内直接执行 Summarize，通过。CLI 复验首次手工修改其副本时将 evidence_dir 写成相对路径，CLI 按接口拒绝，错误与副本保留为 enrollment-summarize-cli-first.log、round-cli-request-relative.json；改为绝对新目录后通过，原自动 Request 未改。最终 round-cli/round.json 为 Valid=true、N=2、R=2、一个执行失败、两个参考 inconclusive、自动成功 0；没有放行，错误放行率无定义。失败任务未从分母删除，未知没有改为通过。

项目回归启动后补齐了准备期间父取消的 Cancelled 记录；最终 Campaign 全包竞态覆盖这项调整。各轮重叠测试不累计成覆盖数。未运行本轮真实 Docker 故障注入，未读取 .env 或调用真实模型。

最终材料检查通过：116 个本地文档链接目标、Markdown 围栏、Go 格式及示例清单摘要一致；git diff --check 通过，冻结数据/规范/补丁/oracle/归档无改动。测试计数和日志摘要保存在 runs/enrollment-summary.json，运行产物不入 Git。

## 本单元结论与下一步

生成前绑定在上述受控范围验收：先固定任务与参考身份，再从实际计划发布动态清单，材料成功后才接纳任务。发布失败或取消不会启动 Target；未复核结果保持未知。该能力不是完整实验协议或真实迁移效果结论，M3 整体仍未完成。

下一单元先明确批次预算：当前三个 Target 上限合计 750 万 Token，批次示例只有 600 万，共享竞争会影响哪些任务完成，需要明确选择总额截断还是保证各任务额度。随后冻结真实开发实验的输入、模型、预算、次数、失败处理与指标口径，再取得该次生成的明确授权。
