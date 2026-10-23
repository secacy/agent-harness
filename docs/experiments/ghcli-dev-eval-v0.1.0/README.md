# GHCLI 新生成开发评测协议 v0.1.0

- 日期：2026-10-23（Asia/Taipei）。
- 协议 / Campaign / Enrollment ID：ghcli-dev-eval-v0_1_0。
- 类型：schema 2 / development_evaluation / development，每 Target 一次尝试。
- 状态：本地材料固定，未授权外发，未执行生成。冻结后不原地修改；变化另发版本。
- 前置：[新证明的完整 Harness 交付验收](../../worklog/2026-10-23-ghcli-harness-delivery.md)；本次选择理由与离线结果见[准备记录](../../worklog/2026-10-23-ghcli-generation-preparation.md)。

## 本轮问题与解释范围

新增父来源、Body 资源与完整范围证明已在旧候选及确定性交付中通过。本轮只观察：从原冻结基线出发，一次新的 Agent 轨迹是否能在既有约束内提交候选，当前门禁得到什么结论，独立参考及助手复核能否支持该结论，花费与缺口是什么。

仅选择发生门禁变化的 GHCLI；不重跑其余三个仓库。N 固定为 1，是已见、已用于门禁开发的有限应用切片，不是未见留出，也不证明任意仓库适用性。模型轨迹与旧实验不同，不能把新旧差异归因于门禁修改；不宣称受控因果比较或稳定成功率。旧四任务实验与 1/4 指标不回写，本轮不与旧轮合并分母。

## 固定材料

| 材料 | 责任 |
| --- | --- |
| [batch.json](batch.json) | 一个实际 Target、既有 DeepSeek 配置、额度和时间上限、独占输出目录 |
| [enrollment.json](enrollment.json) | 生成前纳入、公开 Profile/输入/参考身份、计划设置及四份协议材料摘要 |
| [dataset.json](dataset.json) | 样本来源、保护范围、default 配置及已见暴露事实 |
| [reviewers.json](reviewers.json) | 实际助手复核身份与判据，不预签结果 |
| [implementation.json](implementation.json) / [source-files.json](source-files.json) | 当前源码实际身份、导出副本、离线构建的 harness/summarize 二进制 |
| [environment.json](environment.json) | 本机 Go 与既有 Docker 镜像、context/engine 和资源参数 |
| [models-observed.json](models-observed.json) / [capabilities-observed.json](capabilities-observed.json) | 2026-10-20 查询所得历史原文与能力，不是本次 API 查询 |
| [expected-plan.json](expected-plan.json) | 历史能力与当前环境构造的期望计划，不是运行记录 |
| [launch.py](launch.py) | 本地完整预检、仅在 execute 模式解析 Key、12 分钟中断和 60 秒收尾；一次启动记录防重放 |
| [freeze.json](freeze.json) | 本目录其他文件原始字节 SHA-256，不包含自身 |

输入为 rpcx-l2-ghcli-v1 / rpcx-ghcli-t3-v1 / default；原应用切片 commit、许可证与基线/规范/SDK 摘要见 dataset。复用既有公开迁移规范 v0.6.0，不修改提示、工具或规则向新 Agent 暗示已保存补丁、包装器名称或可放行模板。

源码及二进制副本位于 runs/ghcli-dev-eval-v0.1.0-preparation/snapshot/；生成输出固定为仓库根下 runs/ghcli-dev-eval-v0.1.0，必须不存在。路径及 Docker engine 是本工作区的实际冻结条件；更换它们需在生成前另发版本。本轮不重复打包旧交付归档。

## 一次尝试、预算和停止条件

| 项目 | 固定限制 |
| --- | --- |
| 模型 / 端点 | deepseek-flash / https://api.deepseek.com/chat/completions |
| 当前能力查询 | 启动时一次 https://api.deepseek.com/models 鉴权 GET；不自动重试 |
| 模式 | 文本 Chat Completions、非流式、thinking disabled、无服务端工具，无生成自动重试 |
| 历史期望 C / 输出 O / 保守预留 Q | 1,048,576 / 4,096 / 1,052,672 Token；O 实际编码到每个请求 |
| 单任务及全批额度 | 最多 40 次生成准入、2,500,000 Token（含未知用量的保守占用）；Loop 最多 40 次 Complete 尝试、80 次工具调用 |
| 并发 | Target 1、命令 1；每容器 2 CPU / 2 GiB / 128 PIDs，镜像固定为实际 ID |
| 时间 | 整个 Run 10 分钟；宿主 12 分钟后发中断，最多尝试 60 秒收尾，再停止进程；不承诺底层 I/O 阻塞时都能回收 |
| 尝试与恢复 | 一个 Target、最多一个 Run，不自动重试、不续跑、不手工修补、不追加额度 |

C 和 Q 是历史服务声明的保守条件，不是自己估算的上下文 Token，也不是货币预算。启动器必须先核对 freeze、全部源码、导出副本、二进制和实际环境，再发起唯一一次当前能力查询。实际 Profile 与期望计划不同（包括能力原文身份）时零生成停止，保留诊断，不缩小 Q 或修改本协议继续运行。预留前失败零调用；已准入请求缺 usage 时整笔未知占用不释放。

任何配置/材料漂移、能力不符、基线红、预算耗尽、非法回复、设施/发布失败、超时或人工中断均保留原记录。生成失败不从 N 删除，不挑最好结果，不因 NeedsAttention/Rejected 修改规则后重跑本轮。

## 可见范围与生成前纳入

Agent 只获得当前 Run 的 GHCLI 业务副本、纯迁移规范、rpcx-v1/rpcx-v2 及允许的只读模块依赖。已保存候选、校准变体、门禁/参考源码与控制答案、账本、协议元数据、复核记录和其他任务不进入其工具根；候选不能修改 SDK、可信检查或基线。

协议四份材料在任何外部访问前认证并固定；实际 plan.json 落盘后、任何 Target 开始前发布 enrollment.json、round-manifest.json、round-request.json 和 protocol-materials。实际 Plan 配置摘要及执行后端必须匹配本版期望。已保存候选不用于本轮生成或替代失败 Run。

## 生成后的参考、复核与指标

固定参考为 rpcx-l2-reference-ghcli-data-v2，数据/工具身份在 enrollment 中固定。只有本轮生成结束后，才在独立副本上运行既有参考；不读取控制答案调优候选/门禁，不把答案回传生成对话。参考失败或执行错误原样保留，不自动替换规则或重跑以改善结果。

实际复核者为 codex:/root，当前协议准备者和门禁开发助手，受用户既有委托完成候选复核；不是独立人类或盲审。核对原测试/断言/skip/tag、全部生产变更和调用链、规范条款、配置/日志/摘要、未解决缺口，记录实际 RFC3339 时间、文件位置和理由。不预填 pass，不因自动参考通过抹去额外业务变化的未知。

固定报告 N=1；原 Run 状态、质量与完整参考/实际复核分别记录。有效自动成功 S/N 仅在原 Run Completed、原门禁 ReadyForReview 且参考和实际复核均支持时计 S=1。错误放行 k/A 的 A 为全部原放行数；A=0 时无定义。放行复核未完成时报告已知错误 k、未知 u 与区间 [k/A,(k+u)/A]，不填完整错误率。需确认率 H/N、执行失败原因、已知输入/输出 Token、未知/在途占用、拒绝次数、工具数和墙钟均报告原始计数；缓存输入不重复累加。账单金额、真实人工活动耗时没有证据就标未知。

必须交付原补丁/报告/审核入口、原账本/日志、独立参考、实际助手复核、离线汇总和局限。原 Run 字节在评测后不变，补丁可在新基线上重放；审计检查记录一致性，实际容器按固定引擎/精确 ID 核对，不把审计 pass 当作迁移正确。

本协议固定和离线 --check 成功不授予 API 使用权限。此前授权仅覆盖旧实验一次；本次读取 .env 的 DEEPSEEK_API_KEY、鉴权 GET 及代码/规范/SDK/依赖/工具交互外发必须另获本次明确授权。程序不自动加载 .env；授权后只解析 Key，不执行其中代码、不打印或保存密钥。
