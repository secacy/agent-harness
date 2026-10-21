# 四任务固定样本评测协议 v0.1.0

- 日期：2026-10-22（Asia/Taipei）。
- 协议 / Campaign / Enrollment ID：`l2-fixed-eval-v0_1_0`。
- 执行格式：schema 2，`formal_evaluation`，`split=development`，每 Target 一次尝试。
- 状态：本地材料固定；本次外发与消费待授权，生成尚未执行。该版本不满足原严格留出及人员验收要求。

## 本轮要回答什么

在当前支持范围和同样单任务限制下，四个固定输入能否自主迁移、门禁是否错误放行、哪些结果仍未知、成本和失败原因是什么。原三个开发样本已参与调优；GHCLI 由隔离 Agent 准备，随后已做接入和校准。它们不构成严格未见留出，不推断任意仓库或 SDK 的生产成功概率。

本协议的任务、规则及预算在真实生成前固定。有实质变更就发布新版本；原失败/偏离保留，不原地换样本、调门禁、增加预算、挑最好 Run。方法取舍及前置验收见[范围记录](../../worklog/2026-10-21-m5-freeze-readiness.md)。

## 固定材料及执行位置

| 材料 | 用途 |
| --- | --- |
| [batch.json](batch.json) | 可执行的四任务、模型、两级额度、时限及 Docker 配置 |
| [enrollment.json](enrollment.json) | 生成前纳入、逐任务输入/规范/Profile/参考/工具身份、计划设置摘要、四份材料和预定复核者 |
| [dataset.json](dataset.json) | 实际输入身份、同源组、分层、已见范围、保护路径和全部必需构建范围 |
| [reviewers.json](reviewers.json) | 助手复核身份、实际角色关系及三项判据；不预签结果 |
| [implementation.json](implementation.json) / [source-files.json](source-files.json) | 762 份源码/场景/脚本的实际身份、从导出副本构建的二进制及源码归档身份 |
| [environment.json](environment.json) | 实测工具链、平台、现存镜像和 Docker context/engine、固定资源及网络策略 |
| [models-observed.json](models-observed.json) / [capabilities-observed.json](capabilities-observed.json) | 2026-10-20 已授权查询的历史能力材料，仅用来固定本轮期望身份 |
| [expected-plan.json](expected-plan.json) | 使用历史能力和本机环境构造的期望计划，用于离线验证设置；不是已执行的实际 Plan |
| [freeze.json](freeze.json) | 本目录其余材料的原文字节 SHA-256，不包含自身 |

实际生成位置固定为仓库根下 `runs/l2-fixed-eval-v0.1.0`，必须不存在。源码/二进制归档及准备证据保存在 `runs/l2-fixed-eval-v0.1.0-preparation/`，不入 Git。归档中的源码包含离线输入与依赖，不含 `.env`、Git 元数据、缓存或历史运行；准备过程只核对新应用元数据/摘要，未打开业务、规范正文或控制答案。

计划绑定包含规范化绝对输入路径和当前 Docker engine；本协议是本工作区的执行配置。更换工作区或引擎需在生成前另发协议，不修改本版摘要绕过校验。之后交付原始记录时可以生成新离线复核 Request，不重写原 Plan 或旧证据；可复算保存结果不等于能重复得到同一模型补丁。

## 任务、分层和一次尝试

| 顺序 / Target | 数据 / Profile | 必需范围 | 报告分层 |
| --- | --- | --- | --- |
| 1 / goddd | rpcx-l2-goddd-v1 / rpcx-goddd-t3-v1 | default | seen-development，N=3 的一项 |
| 2 / translation | rpcx-l2-translation-v1 / rpcx-translation-t3-v1 | all-required：default 与 rpcx_alternate，同一候选 | seen-development，N=3 的一项 |
| 3 / workouts | rpcx-l2-workouts-v1 / rpcx-workouts-t3-v1 | trainings/default，另含只读 common 前置模块 | seen-development，N=3 的一项 |
| 4 / ghcli | rpcx-l2-ghcli-v1 / rpcx-ghcli-t3-v1 | 有限应用切片/default，认证的分片依赖 | isolated-prepared-calibrated，N=1 |

纳入总 N=4，同时报告两个分层的原始计数；Translation 多配置不增加分母，五组 GHCLI 手工校准不增加分母。未启动、基线红、超时、预算耗尽、非法回复、设施或发布失败均保留在纳入 N 内。无候选时记录不可评，不以删除 Target 提高成功率。旧开发实验、手工对照和本轮生成分表。

每 Target 只创建一次尝试；不自动重试、不人工修补、不续接恢复、不替换原候选。固定源码中的提示、工具、预算提示和门禁原样使用。每个 Run 独立对话，只开放本任务业务输入、迁移规范及只读 SDK/允许的前置模块；oracle、参考、对照、其他任务、复核者和账本不进入 Agent 可见根，门禁不读 oracle。

## 模型、预算和停止规则

| 项目 | 固定值及含义 |
| --- | --- |
| 模型 / 生成端点 | deepseek-flash / https://api.deepseek.com/chat/completions |
| 能力端点 | https://api.deepseek.com/models；正式启动时一次应用层 GET，无自动重试 |
| 模式 | 文本 Chat Completions，非流式、thinking disabled，无服务端工具；未显式设置的 temperature 等参数沿用服务默认，不声称权重/随机性固定 |
| 期望上下文 C / 请求输出 O | C=1,048,576；max_tokens=4,096；来自所附历史材料，不声称已重新查询当前服务 |
| 每请求预留 Q | C+O=1,052,672 Token；实际当前 Profile 不符合期望设置则零生成停止 |
| 单 Target | 40 次准入、2,500,000 Token；Loop 40 次 Complete 尝试、80 次工具调用；Run 总时限 10 分钟 |
| 全批 | 160 次准入、10,000,000 Token；工具上限合计 320 次，不追加额度 |
| 并发 | Target=2、执行命令槽=2，按固定清单接纳 |
| 外部停止 | 宿主子进程启动满 40 分钟发送 SIGINT，停止新生成；最多再等待 60 秒收尾，仍未退出则终止该自有进程组，保留部分记录与残留诊断，不重跑 |

全批额度等于四项单任务上限之和，避免共享池额外挤压各任务；40 次不是保证次数。已知/在途/未知占用使剩余额度不足 Q 就拒绝，未知费用保留整笔 Q；不把 1000 万 Token 当成货币账单上限。预留先持久化再调用，合法 usage 按实结算，非法 usage/共同账本故障停止新增请求。单任务故障不主动取消无关任务。

**本版采用完整能力响应身份锁定。** 启动时仍必须查询当前服务，`expected_context_tokens` 比较 C，Discover 核对输出能力；schema 2 的计划设置摘要还包含能力响应原文 SHA-256。即使只改空白、其他模型或展示信息，也可能使本轮拒绝，接受这一保守成本。变化时保留新能力材料和停止记录，零生成、不自动改协议或二次查询；如要采用变化后的能力，另发版本并重新授权。模型别名无法证明底层权重和服务计量固定。

## 环境及生成前检查

使用已存在的 `golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd`，linux/arm64。每容器 2 CPU、2048 MiB、128 PID，禁网、只读根/源挂载、按 Run 隔离可写缓存；不自动拉镜像或回退 trusted-local。宿主 Go 1.26.5 / darwin arm64，实际 context/engine 以 environment.json 为准。两个槽位不保证限制全宿主内存或 Docker daemon 资源。

从仓库根进行无凭证、无网络、无仓库命令的本地配置核对：

```sh
runs/l2-fixed-eval-v0.1.0-preparation/snapshot/bin/harness batch --config docs/experiments/l2-fixed-eval-v0.1.0/batch.json --check
```

启动前另核对 freeze、源码/归档/二进制摘要、现存镜像/引擎身份和输出位置。取得该次明确授权后才读取 `.env` 的 `DEEPSEEK_API_KEY`，不将 `.env` 当 shell 脚本执行，不用环境变量换模型。执行相同命令去掉 `--check`，由受控父进程实施上述停止规则，保存授权、起止时间、退出码、实际发布 revision 与二进制身份。

当前能力查询和执行后端初始化后，实际 Plan 须符合设置摘要与后端；在任何 Target 接纳前发布四份材料副本、Enrollment、Manifest 和 Request。实际 Plan 的查询时间/Campaign 原文字节摘要在运行时记录，不把 expected-plan 当作已执行证据。材料错配、能力变化或发布失败均不允许生成；部分诊断目录存在不算开始过迁移。各任务在基线认证、实际构建/测试通过之后才可调用模型。

## 参考与助手候选复核

对全部存在且可认证的候选运行所固定的独立参考，不只选择 ReadyForReview。Goddd data-v2、Translation data-v2、Workouts data-v1、GHCLI data-v2 的身份见 Enrollment；旧 GHCLI data-v1 不作活跃参考。参考不接收原门禁结论、Agent 自述或 expected 作为判据，不把参考通过改写为原 Run 放行。

参考每 Target 父时限 15 分钟，四项依次执行，使用同一固定 Docker 配置；每命令上限、全部必需配置、事件和计时容差保持既有参考实现。超时不重试、不放宽窗口。确定的规范违反记 fail；参考编译不适配、设施错误、缺事件或证据不足保留 inconclusive。没有完整候选不虚构可执行参考。

预定复核者统一为 `codex:/root`，即准备本协议的实际助手；它兼任运行协调，未向本轮生成 Agent 提供手工迁移补丁。生成器是各 Run 内的 DeepSeek 模型对话，复核者不修改原候选、不把参考反馈回本轮生成。三个旧开发样本及通用规则已见，GHCLI 准备/接入期间主流程只读元数据；准备/参考/门禁 Agent 已见内容，且共享文件系统/同模型家族，故不声称人类独立性、严格未见或盲审。实际记录时间和结论只能在活动发生后填写。

按 reviewers.json 检查三项：测试保留、全部生产变更覆盖、交付依据。先按固定判据阅读保存候选，再核对验证证据；保留文件/日志/规范条款定位及 RFC3339 时间。使用 `candidate_review` 格式时注明 assistant 方法，签实际助手身份，不冒用用户签名；缺证据保持未知，不预填通过。若预定助手不可用，记录偏离，不无声换复核者或伪造签署；需要新角色时先公开变更及影响，不把缺复核变成自动通过。

## 汇总、归档和局限

原 Manifest、Request、Run、门禁和账本保持原字节；在新 Request 副本加入参考/复核的实际路径及摘要，向新目录汇总。身份冲突时 Valid=false，不发布有效比例；参考不全则相关结果未知，不把两者混为一谈。

N 为事前纳入任务数；R 为实际不同 Run 数；Q 为有原质量判定数；A 为 ReadyForReview；H 为 NeedsAttention。S 为原 Run Completed 且放行、完整参考/复核 pass 的数量，报告 S/N 及未知潜在成功 u_s 的范围 [S/N,(S+u_s)/N]。放行后确认错误数 k、未知数 u：A=0 时错误放行率无定义，A>0 且 u=0 为 k/A，否则报告 k/u/A 和 [k/A,(k+u)/A]；不是统计置信区间。H/N 与 H/Q 同列，Q=0 时后者无定义。总 N=4 及两个方法分层均保留原始计数，不挑最佳表现。

成本分列已知输入/输出、未知保守占用、在途预留、准入/拒绝、Loop 尝试/工具数、墙钟及验证时间。Campaign 总账只计一次；金额与实际人类活动耗时无证据时未知。执行失败与质量 Rejected/NeedsAttention 分列；迁移/测试错误和交付依据错误分列，同一候选总错误数只加一次。

最终交付完整原始材料、保存候选/补丁、规范/日志、门禁/参考/助手复核、汇总与失败分布，以及本轮源码/二进制/环境和逐文件摘要。密钥不入包，不能只交本机 runs 链接索引。旧开发实验不改名，本轮只报告四个固定输入上的有限观察，原 M3/M5 严格留出及人员条件仍未满足。
