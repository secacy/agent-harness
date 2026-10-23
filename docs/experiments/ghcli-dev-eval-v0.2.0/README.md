# GHCLI 新生成开发评测协议 v0.2.0

- 日期：2026-10-23（Asia/Taipei）。
- 协议 / Campaign / Enrollment ID：ghcli-dev-eval-v0_2_0。
- 类型：schema 2 / development_evaluation / development，一个 Target、一次尝试。
- 状态：本地材料固定，未取得本版外发授权，未查询或生成；冻结后不原地修改。
- 实现基线：75554dc；采用 [ADR-011](../../adr/011-capability-contract-binding.md) 的显式 model-contract-v1。
- 前置：[能力契约分离验收](../../worklog/2026-10-23-capability-contract.md)；本版[准备记录](../../worklog/2026-10-23-ghcli-v02-preparation.md)。

## 问题与解释范围

[v0.1.0 唯一启动](../2026-10-23-ghcli-dev-real-model-01.md)已因附加元数据改变整份响应身份而在生成前停止，0 Target / 0 Run / 0 生成。它的协议、失败记录和授权使用事实保留。本版选择新的协议、实现及输出身份，不续跑、覆盖或删除旧记录。

本轮仍只观察：从公开冻结 GHCLI 基线出发，一条新的 Agent 轨迹能否在既有限制内交付候选，原门禁如何判定，独立参考与实际助手复核是否支持，成本与缺口是什么。N 固定为 1，是已见、用于门禁开发的有限应用切片；不宣称未见留出、稳定成功率、任意仓库适用性或因果改进。旧四任务与 1/4 指标不回写，不与本轮或旧失败启动合并分母。

## 固定材料与输入

| 材料 | 责任 |
| --- | --- |
| [batch.json](batch.json) | 一个实际 Target、模型、额度、时限和独占输出目录 |
| [enrollment.json](enrollment.json) | 生成前输入/参考身份、显式绑定策略、设置摘要和四份材料摘要 |
| [dataset.json](dataset.json) | 来源版本、许可证、保护路径、default 构建配置及已见暴露事实 |
| [reviewers.json](reviewers.json) | 实际助手身份、关系与复核判据，不预签结果 |
| [implementation.json](implementation.json) / [source-files.json](source-files.json) | 当前源码原文身份、独立导出副本、离线构建 harness/summarize 二进制 |
| [environment.json](environment.json) | 本机 Go 与当前只读核对的 Docker 镜像/context/engine、资源设置 |
| [models-observed.json](models-observed.json) / [capabilities-observed.json](capabilities-observed.json) | 复用 2026-10-23 10:56+08:00 旧启动的公开能力响应作历史期望，不是本版查询 |
| [expected-plan.json](expected-plan.json) | 历史能力与本地实测环境构造的期望，不是实际运行记录 |
| [launch.py](launch.py) | 固定材料预检、仅执行模式解析 Key、12 分钟中断及最多 60 秒收尾，一次启动防重放 |
| [freeze.json](freeze.json) | 本目录其他文件的原始字节摘要，不包含自身 |

输入仍为 rpcx-l2-ghcli-v1 / rpcx-ghcli-t3-v1 / default，迁移规范 v0.6.0。来源、应用/SDK/规范/参考摘要见 dataset 与 enrollment。提示、工具、业务门禁、参考和原测试要求均沿用当前发布，不给生成者已保存候选、包装器名称或参考答案。

源码及二进制副本固定在 runs/ghcli-dev-eval-v0.2.0-preparation/snapshot/，实际输出为 runs/ghcli-dev-eval-v0.2.0，启动记录为 runs/ghcli-dev-eval-v0.2.0-launch。后两目录必须不存在；路径、引擎或实现变化需另发版本。本版不重复打包旧交付归档。

## 能力绑定与停止条件

本版显式固定 capability_binding=model-contract-v1，使用 Protocol.SettingsSHA256 构造期望设置摘要。运行 ID、查询时间和能力原文摘要不参与设置匹配；provider、endpoint、model、context、请求输出限制及输入/额度/工具/时限/并发/资源等其余 Plan 设置仍严格匹配。未知策略拒绝。

运行前必须取得一次当前 Models 响应。所选模型必须唯一存在、支持文本输入/输出，正数能力上限必须合法且支持固定输出请求。上下文上限即使增大也停止，不擅自增大预留或额度；无关附加字段、其他模型信息及排版变化允许继续。服务输出容量只需支持固定请求限制，未使用的输出容量不参与设置匹配。相同别名及能力字段不能证明服务权重或行为不变。

当前原文不归一化或覆盖为历史文件：models.json、capabilities.json 保存本次查询，实际 plan.json 和账本保留本次 source_sha256/verified_at，Manifest 绑定实际计划完整字节。离线汇总要求当前 models.json 摘要等于实际 Profile.SourceSHA256、capabilities.json 的 Profile 与计划相同；原文缺失/篡改或 Profile 冲突不发布有效指标。

## 一次尝试、预算与时间

| 项目 | 固定限制 |
| --- | --- |
| 模型 / 端点 | deepseek-flash / https://api.deepseek.com/chat/completions |
| 能力查询 | 本版启动时一次 https://api.deepseek.com/models 鉴权 GET，不自动重试 |
| 模式 | 文本 Chat Completions，非流式，thinking disabled，无服务端工具，无生成自动重试 |
| 期望 C / 请求 O / 单次保守预留 Q | 1,048,576 / 4,096 / 1,052,672 Token；O 实际编码到请求 |
| 全批及单任务额度 | 40 次生成准入、2,500,000 Token，含未知用量整额保守占用；Loop 最多 40 次 Complete 尝试、80 次工具调用 |
| 并发与资源 | Target 1 / 命令 1；每容器 2 CPU / 2 GiB / 128 PIDs，镜像固定实际 ID |
| 时间 | Run 10 分钟；宿主 12 分钟中断，最多尝试 60 秒收尾，再停止进程；不承诺底层 I/O 阻塞时必能回收 |
| 尝试与恢复 | 一个 Target、最多一个 Run；不自动重试、不续跑、不手工修补、不追加额度 |

C/Q 是服务声明下的保守条件，不是估算分词或金额账单上限。请求前必须预留并落盘；缺 usage 或断连等未知成本保留整笔占用，不记零。源码/材料/环境/关键能力漂移、基线红、预算耗尽、非法回复、执行/发布失败或超时均停止并保留原记录，不因失败挑选最好结果或修规则重跑本版。

## 可见范围、参考、复核和指标

Agent 只获得本 Run 的业务副本、纯迁移规范、rpcx-v1/v2 和允许的只读模块依赖。旧候选、门禁/参考源码、控制答案、协议元数据、账本、复核和其他任务不进入工具根；不能修改 SDK、可信检查或基线。四份协议材料在外部访问前认证为宿主副本；实际计划落盘后、Target 启动前发布 enrollment、manifest、request 和 protocol-materials。

固定独立参考为 rpcx-l2-reference-ghcli-data-v2，数据/工具身份在 enrollment 固定。只有本次生成结束后才在独立副本执行，不向生成对话反馈参考答案，不用旧候选替代失败任务。参考错误与失败保留，不自动改规则重跑。

实际复核者 codex:/root 为既有门禁开发助手、当前准备者和协调者，不是独立人类或盲审。先检查候选/规范，再结合参考核对原测试/断言/skip/tag、全部生产变更与调用链、报告依据及未解决缺口；记录实际 RFC3339 时间、文件和理由。不得预填 pass，探针通过不能抹去额外业务变化的未知。

N=1 固定保留所有计划失败；Target 创建前停止时分列计划数与实际 Run 数，不伪造 Skipped 或候选 verdict。S/N 仅在原 Run Completed、原门禁 ReadyForReview、参考及实际复核均支持时计成功；生成前停止只报告计划未交付，不冒称完成候选评测。错误放行 k/A 中 A 为所有原放行数，A=0 无定义；复核未齐时报告 k/u 及区间。H/N 与 H/候选数、执行失败、准入/拒绝、输入/输出 Token、未知/在途占用、工具数和墙钟分列；缓存输入不重复计。金额账单及人类活动耗时未知就标未知。

生成后保留原补丁/报告/审核入口与账本/日志，在新目录提供独立参考、实际助手复核和离线汇总；补丁在新基线上重放，原 Run 文件摘要保持。只读审计确认材料一致性，实际容器按冻结引擎/精确 ID 核对；审计 pass 不等于迁移正确。无候选时参考/复核/重放不适用；未完成纳入时不补造有效轮次。

## 本版外发授权

固定材料和离线 --check 不授予 API 权限；此前许可仅覆盖 v0.1.0 的一次启动，已使用。本版需另获具体授权后，才读取 .env 的 DEEPSEEK_API_KEY、发出一次鉴权 GET，并向既定生成端点发送当前任务代码、规范、SDK/允许依赖及工具交互。只解析 Key，不执行 .env、不打印或保存凭证。未获本版授权时不执行 execute，不沿用旧许可自动启动。
