# Translation 诊断反馈开发观察 v0.1.0

- 日期：2026-10-24（Asia/Taipei）。
- 协议 / Campaign ID：translation-diagnostics-dev-v0_1_0。
- schema 2 / development_evaluation / development；一个已见 Translation Target，一次尝试。
- 状态：本地材料准备并固定；未获得本版真实外发及消费授权，尚未启动。
- 设计与取舍：[单任务观察设计](../../worklog/2026-10-24-translation-diagnostic-observation.md)；已有机制：[诊断首版](../../worklog/2026-10-24-generation-diagnostics.md)。

## 问题与观察边界

观察真实 Agent 是否使用只读 baseline 和 migration_check、反馈后发生什么编辑，以及最终测试保留/迁移约束是否有充分依据。保留自主选择，不追加必须调用工具、旧错误位置或人工修复建议；提示、工具说明和预算通知沿用固定二进制。原样从认证 v1 基线新建候选，不从旧错误补丁开始，不预埋删除断言来制造修复机会。

若工具未使用、使用时已 preserved、收到反馈后没有后续请求或候选仍未知，均作为有限结果保留。一次已见开发观察不能证明成功率提高、普遍有效或诊断的因果作用；基线可读、提示、工具和模型波动不单独归因。旧 0/4、旧 Translation 结果不合并分母，严格留出与原总体验收条件不变。

## 固定输入与实现

任务为 rpcx-translation-t3-v1、rpcx-l2-translation-v1、规范 v0.4.0。default 与 rpcx_alternate 是同一候选的两个必需配置，均须验证。完整业务基线、SDK、规范及当前独立参考身份在 dataset/enrollment 固定。Agent 只见当前业务 workspace、该业务的只读 baseline、纯规范、rpcx-v1/v2；旧候选、oracle、expected、参考/门禁源码、协议元数据和账本不进入工具根。diagnostics 宿主目录不可由文件工具读取；工具只回传有限结构事实。

生产代码基线为 2a8c8ee。源码与二进制导出在 runs/translation-diagnostics-dev-v0.1.0-preparation/snapshot/；构建参数、文件和二进制摘要由 implementation/source-files 绑定。tools.json 保存本 Profile 的完整模型接口工具集合：原仓库工具由实际 API 生成，migration_check 的声明从固定 Go 源码解析；使用保存的受控轨迹核对请求输入摘要，而不是手写另一套说明。

| 文件 | 作用 |
| --- | --- |
| [batch.json](batch.json) | 单 Target、诊断专项上限、模型、预算、时限、资源与独占输出 |
| [enrollment.json](enrollment.json) / [expected-plan.json](expected-plan.json) | schema 2 准入、参考、预定复核人、期望设置与 model-contract-v1 绑定 |
| [dataset.json](dataset.json) / [reviewers.json](reviewers.json) | 已见暴露事实、来源/许可、固定输入及实际助手复核职责 |
| [implementation.json](implementation.json) / [source-files.json](source-files.json) | 实际源码导出、Go 工具链及 harness/summarize 二进制身份 |
| [environment.json](environment.json) | 本机只读核对的 Go、镜像、Docker context/engine 与资源限制 |
| [models-observed.json](models-observed.json) / [capabilities-observed.json](capabilities-observed.json) | 2026-10-24 旧批次保存的能力原文与解析结果，仅作历史期望；不是本版查询 |
| [tools.json](tools.json) / [analyze.py](analyze.py) | 固定工具集合与只读编辑/请求/诊断轨迹重放 |
| [test_analyze.py](test_analyze.py) | 保存的受控轨迹与明确标记的合成变体校准；不产生真实效果样本 |
| [launch.py](launch.py) / [freeze.json](freeze.json) | 一次启动、环境/材料预检、停止收尾及其余成员的原始字节摘要 |

## 模型、费用和停止

| 项目 | 固定上限或条件 |
| --- | --- |
| 模型 / 外发端点 | deepseek-flash / https://api.deepseek.com/chat/completions |
| 能力访问 | 启动时仅一次 https://api.deepseek.com/models 鉴权 GET，无自动重试 |
| 模式 | 文本、非流式、thinking disabled、max_tokens=4096，无服务端工具，无生成重试；未显式设置采样参数，沿用服务默认并记录模型返回身份 |
| 历史期望 C / 请求 O / 保守 Q | 1,048,576 / 4,096 / 1,052,672 Token |
| 两级额度 | 全 Campaign 与唯一 Target 同为最多 40 次生成准入、2,500,000 Token，含未知请求的整笔保守占用 |
| Loop | 最多 40 次 Complete 尝试、80 次工具；migration_check 专项两次尝试，含无效参数，超额拒绝仍占总工具尝试 |
| 时间 | Run 10 分钟（基线、生成、独立门禁均在内）；宿主 12 分钟 SIGINT，最多尝试 60 秒收尾后 SIGKILL 自有进程组 |
| 执行 | Target/命令槽各 1；Docker linux/arm64、2 CPU、2 GiB、128 PID，禁网、只读输入及隔离缓存 |
| 尝试 | 一个 Target，最多一个新 Run；不恢复、手工修补、追加额度或另起尝试替代失败 |

Model-contract-v1 核对所用模型和相关设置，允许无关原文展示信息变化，但保留本次原始响应身份；关键能力、输入、环境或材料漂移即停止，不再次 GET、换模型或改预留。当前查询不是证明服务权重/行为不变。Token 额度的保证依赖服务遵守上界与单请求契约，不是金额账单保证；断连/缺 usage 保留未知占用，非法 usage/共同账本故障停批。

镜像、context/engine 的实际值见 environment.json。预检不拉镜像、不创建执行容器，不回退 trusted-local。输出 runs/translation-diagnostics-dev-v0.1.0 和启动收据 runs/translation-diagnostics-dev-v0.1.0-launch 必须不存在。材料或输出不符合时保留拒绝，不自动绕过；启动收据独占创建，执行模式一次，失败也不重放。

## 准入、可见性与分析

四份 schema 2 材料在外部访问前核对，实际 Plan 发布后、Target 启动前保存纳入清单、协议副本与请求。generation_diagnostic_limit=2 参与设置摘要；关闭或改变专项上限都不能在本版静默接受。baseline/诊断不是门禁通行凭证，最终 Evaluate 仍独立执行。

分析器只读取已停止写入的 Run、明确提供的账本与 tools.json。它核对运行身份、原始来源稳定性、基线与最终候选摘要；按 ToolCallID 对应实际 ToolResult，只重放成功 replace_text 的唯一文本替换；每个诊断点核对其完整快照。提出但无结果的调用列为未回答，不计作已执行诊断。

每个已保存的模型回复均核对此前 Messages+Tools 的 Go JSON 接口摘要与已准入账本记录；这不是 HTTP 字节摘要，也不证明服务端理解。tool_configured 只表示输入和工具清单启用诊断；tool_provided 需有已核对的实际请求/回复，没有时记 null，不能把基线失败称作工具已提供。反馈产生后没有新的模型回复时，记未观察到继续处理。同一批模型回复中预先安排、排在诊断之后执行的编辑，不能记成看过反馈后的修复。分析器列同文件关联与具体定位，是否改变同一断言/声明由源码复核确认，不用文件名关联冒充语义证明。

缺文件、摘要不符、不可唯一重放或来源漂移时发布分析失败并停止推导相应行为链，不能用模型总结补齐。工具被提供但未调用、已 preserved、unknown/violated 后无改动、改动仍失败以及无候选，都正常保留。独立归档审计与质量参考另行完成，分析器不取代它们。

## 独立验收与报告

固定参考为 rpcx-l2-reference-translation-data-v2，数据/工具身份在 enrollment 中冻结。生成结束后，所有可认证原候选进入独立副本参考（父时限 15 分钟）与实际助手源码复核，覆盖双配置；执行失败的部分候选可作诊断，但不能事后算原自动成功。无可认证候选记不可评。参考设施错误/缺证据为 inconclusive，不能改称候选语义 fail，不调整规则重试。

实际复核者 codex:/root 参与过开发、旧候选复核、准备及协调，不是独立人类或盲审。先看原候选/规范再结合参考结果，按测试保留、全部变更覆盖和交付依据三项保存实际时间、位置、理由与未解决项；未知不预填 pass。特别核对原断言/表数据/helper，诊断 preserved 或参考自动 pass 不代替完整复核。

固定计划 N=1，实际 Run R=0/1 单列；S 仅在原 Run Completed/ReadyForReview 且完整参考/实际复核 pass 时为 1。A=0 时错误放行率无定义；有未知时报告 k/u/A 及区间，不把未知记正确。执行失败、Rejected 与 NeedsAttention 分列，不以一次比例推断泛化。

主报告展示：baseline 读取、诊断提出/执行/成功/错误、反馈后的请求与编辑、诊断状态变化及最终约束；同时报告实际准入/拒绝、已知/未知/在途 Token、总工具、Run/宿主墙钟和记录内命令耗时。当前没有逐次诊断时间戳，精确诊断耗时与增量成本为未测量；实际金额及人类活动时间未知就标未知。

生成结束后保留原记录；新结果目录保存参考/实际复核、离线汇总、轨迹分析、来源保全、补丁重放、审核导航、审计和资源回收。按固定 context/engine 与精确 ID 核对容器，审计 pass 不等于迁移正确。完成一次结果后停止，再讨论研发选择。

## 本地核对与本版授权

```sh
python3 docs/experiments/translation-diagnostics-dev-v0.1.0/launch.py --check --freeze-sha256 <freeze.json 的实际 SHA-256>
```

--check 仅做本地只读核对，不读取凭证、不查询模型或创建容器。--execute 先要求干净 Git、固定材料和独占路径，独占记录后仅解析 .env 的 DEEPSEEK_API_KEY 作鉴权，不执行 .env 或打印/保存密钥。此前许可已消费，必须另获本版一次 GET/生成/外发授权后才执行。

轨迹分析在实际原批次停止后运行，账本路径使用本次已确认位置，输出新目录且位于输入外：

```sh
python3 docs/experiments/translation-diagnostics-dev-v0.1.0/analyze.py --run <本次原Run目录> --ledger <本次budget.json> --tools docs/experiments/translation-diagnostics-dev-v0.1.0/tools.json --output <新的分析目录>
```
