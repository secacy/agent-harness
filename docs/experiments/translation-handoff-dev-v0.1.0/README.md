# Translation 末轮交接开发观察 v0.1.0

- 日期：2026-10-24（Asia/Taipei）。
- 协议 / Campaign ID：translation-handoff-dev-v0_1_0。
- schema 2 / development_evaluation / development；一个已见 Translation Target，一次尝试。
- 状态：准备并固定本地材料；本版尚未获得真实外发与消费授权，尚未启动。
- 已确认[观察设计](../../worklog/2026-10-24-final-response-observation.md)；受测[末轮交接机制](../../worklog/2026-10-24-final-response-handoff.md)。

## 本次问题与解释边界

检查真实模型在 final-response-v1 下是否有效交接、原独立门禁能否完成，以及候选质量是否获得完整依据。生成从认证 v1 基线开始，保留自主规划与提前提交；不提供旧候选、错误位置、参考补丁或额外收尾建议。

若提前提交，记录实际交付与质量，末轮行为记未观察；不为触发末轮延长对话或追加尝试。若在边界前失败，保留原因。末轮到达、本地尝试、预算准入、收到回复、正常提交、门禁完成和有效自动成功分别记录。一次已见开发观察不能证明成功率提高或因果收益，旧结果不合并分母。

## 固定任务与实现

任务 rpcx-translation-t3-v1，业务基线 rpcx-l2-translation-v1、规范 v0.4.0；default/rpcx_alternate 均必需，计同一个任务。完整来源、许可、曝光、输入、参考和工具身份见 dataset/enrollment。Agent 只见本 Run workspace、只读 baseline、rpcx-v1/v2、纯规范，以及有限 migration_check 反馈；oracle/expected、协议、账本、门禁和参考源码不进入工具根。

生产源码起点 d4e5b2b，导出副本、二进制和构建参数在 implementation/source-files 固定。宿主策略 final-response-v1 从第一轮预算通知生效：最多前 39 次模型调用允许工具，第 40 次仅回复；工具额度恰好耗尽也提前仅回复。模型有效 stop 才提交当前候选；违规工具请求不执行，截断、预算拒绝、取消或服务错误不自动变成提交。末轮没有额外 Token、重试或墙钟保障。

沿用 baseline 可读及诊断专项两次，不要求必须诊断，不调整业务质量规则或追加针对上轮错误的提示。提示、工具、边界控制都来自冻结二进制；完整普通工具 schema 由实际 API/固定源码提取，末轮工具是 nil，不能假定所有请求都使用 tools.json。

| 文件 | 作用 |
| --- | --- |
| [batch.json](batch.json) / [expected-plan.json](expected-plan.json) | 一个 Target、策略、预算、时限、资源及期望实际计划 |
| [enrollment.json](enrollment.json) / [dataset.json](dataset.json) / [reviewers.json](reviewers.json) | schema 2 准入、已见分区、来源、固定参考及实际助手复核职责 |
| [implementation.json](implementation.json) / [source-files.json](source-files.json) | 源码、导出、工具链和 harness/summarize 二进制身份 |
| [environment.json](environment.json) | 只读实查本机 Go、Docker 镜像/context/engine 及资源限制 |
| [models-observed.json](models-observed.json) / [capabilities-observed.json](capabilities-observed.json) | 旧协议保存的能力材料，仅作历史期望；不是本版查询 |
| [tools.json](tools.json) / [analyze.py](analyze.py) / [test_analyze.py](test_analyze.py) | 普通工具集合、只读轨迹与请求重放、受控记录/明确合成变体校准 |
| [launch.py](launch.py) / [test_launch.py](test_launch.py) / [freeze.json](freeze.json) | 一次启动、本地预检、停止收尾及其余成员原始字节摘要 |

## 模型、资源与停止

| 项目 | 固定条件 |
| --- | --- |
| 外发 | deepseek-flash；一次 https://api.deepseek.com/models 鉴权 GET，生成仅 https://api.deepseek.com/chat/completions |
| 模式 | 文本、非流式、thinking disabled、max_tokens=4096，无重定向/自动重试/服务端工具；采样未显式设置，沿用服务默认 |
| 历史 C / O / Q | 1,048,576 / 4,096 / 1,052,672 Token；实际运行前核对当前声明，不随意缩小预留 |
| Campaign / Target | 同为最多 40 次生成准入、2,500,000 Token，含未知消耗整笔保守占用 |
| Loop / 工具 | 40 次 Complete 尝试、80 次工具；migration_check 两次专项尝试包含无效参数并计入总工具额度 |
| 时间 | Run 10 分钟，含基线/生成/独立门禁；宿主 12 分钟 SIGINT，最多尝试 60 秒收尾后 SIGKILL 自有进程组 |
| 执行 | Target/命令槽各 1；Docker linux/arm64、2 CPU、2 GiB、128 PID、禁网、只读输入、隔离缓存 |
| 尝试 | 一个 Target、一次启动、不恢复/手工修补/追加额度；失败也不重放本版启动 |

执行时保存当前 Models 原文，model-contract-v1 允许无关展示原文变化且保留真实身份；关键容量、模型、设置、输入、参考、源码、环境或材料不符即停止。元数据 GET 不计入生成次数。未知 usage 保守占用；非法 usage 或共同设施故障停批。费用保证依赖供应商遵守上界和单请求契约，Token 上限不是金额账单保证。

镜像、context/engine 的实际值见 environment.json。本地预检不拉镜像、不创建容器、不回退 trusted-local。输出 runs/translation-handoff-dev-v0.1.0 和启动收据 runs/translation-handoff-dev-v0.1.0-launch 必须不存在；启动收据独占，失败也保留。

## 请求、轨迹与原候选

分析器消费实际 plan.json、Run Input、agent.json、账本、普通 tools.json；核对策略/次数/输入一致，依预算通知与工具结果重建边界，再核对 FinalResponseAttempt 序号和消息前缀。普通请求使用完整工具，末轮 Go JSON 使用 Tools:null；null 与空数组不等价。

每次本地模型尝试列实际准入/拒绝/未准入、输入摘要、是否有助手回复；有模型错误但无回复仍可列准入和实际费用，取消/设施失败可能没有准入或拒绝记录。没有回复不能声称模型已理解反馈。模型接口摘要不证明 HTTP 字节或服务端接收，适配器未保存原始 finish_reason 时不编造原文。

提出的工具请求与有 ToolResult 的执行分列；末轮非法调用无结果时列未执行，绝不重放修改。成功 replace_text 必须在冻结基线中唯一匹配，逐次候选摘要、诊断快照及最终候选都要核对；不执行对话中的命令。go_check 仅提取已记录状态/配置和当时完整候选摘要，不能代替最终门禁或将旧检查转给新候选。

轨迹至少列：提前/边界提交或失败、最后成功编辑、后续本地检查、原门禁是否发布、诊断快照与最终候选关系。缺材料、身份不符、路径不安全、不可重放或来源漂移时保存分析失败，不用模型总结补齐。分析器与独立审计各自保留结果，均不替代质量复核。

## 参考、实际复核和指标

生成停止后，所有可认证候选在独立副本运行固定 Translation data-v2（父时限 15 分钟）并实际复核；两个配置都检查。先源码、后结合参考，核对原测试/断言/helper、全部生产改动、规范条款及审核依据；设施错误/缺证据为 inconclusive，不改称候选语义 fail。未知不预填 pass，不调整规则后重试。

实际复核者 codex:/root 参与开发/准备/协调并已见旧样本，单独于 DeepSeek 生成会话；不称独立人类或盲审。保存实际身份、RFC3339 时间、源码/规范/日志位置、理由与未解决问题。

计划 N=1，实际 Run R=0/1 单列。有效提交、门禁完成、ReadyForReview/Rejected/NeedsAttention、参考/复核和有效自动成功 S 分列。S 仅在原 Run Completed/ReadyForReview 且完整参考/实际复核 pass 时为 1；A=0 时错误放行率无定义，未知保留 k/u/A 及区间。只解释本次计数，不报告提升百分比、泛化或因果结论。

失败的可认证部分候选可事后评估，不晋升原 Run；无原 verdict 时走独立 Assess，不挂成自动成功。有候选但门禁超时、没有正常提交和生成前失败分别报告。已知输入/输出、未知/在途 Token、准入/拒绝、工具/诊断次数、Run/宿主墙钟和已记录命令耗时列出；参考/复核另列。精确增量成本、金额和人类活动时间无证据则标未知。

结果新目录保留源码复核、参考、原运行汇总、分析、审计、补丁重放、审核导航、来源保全和精确容器回收。原始材料及旧协议不改写。一次结果完成即停止，再讨论研发方向，原总体验收缺口继续披露。

## 本地检查与授权后启动

```sh
python3 docs/experiments/translation-handoff-dev-v0.1.0/launch.py --check --freeze-sha256 <freeze.json 的实际 SHA-256>
```

检查模式仅核对本地材料、实际 Docker 身份及离线配置，不读取凭证、访问模型或创建容器。执行模式要求干净 Git、独占目录及相同完整身份；取得本版一次外发/消费授权后才可 --execute。启动器只解析 .env 中的 DEEPSEEK_API_KEY 作鉴权，不执行 .env、不输出或保存密钥。

生成停止后，将实际路径传给本版分析器，输出必须是输入之外的新目录：

```sh
python3 docs/experiments/translation-handoff-dev-v0.1.0/analyze.py --run <原Run目录> --ledger <本次budget.json> --plan <本次plan.json> --tools docs/experiments/translation-handoff-dev-v0.1.0/tools.json --output <新分析目录>
```

旧单次许可已消费。准备与冻结不代表已授权生成；本版没有自动第二次尝试或补发收尾请求。
