# L2 首轮真实开发实验协议 v0.1.0

- 发布日期：2026-10-20（Asia/Taipei）
- 协议 ID：l2-dev-eval-v0.1.0；Campaign / Enrollment ID：l2-dev-eval-v0_1_0
- 类型：development_evaluation，已见开发集；不属于 M5 留出评测
- 状态：协议已冻结，真实生成待本次明确授权；尚无本轮迁移结果

本协议在观察真实候选之前固定任务、限制、判据与失败处理。协议发布后不原地改输入、提示、模型、额度、判据或指标；有必要变化时另起版本，并保留本轮所有记录。增加一次尝试也属于新实验，不能作为本轮的免费重试。

## 问题与材料身份

本轮观察：三个不同业务结构的切片能否在既定预算内迁移；独立参考是否发现门禁放过的错误；完成、失败与未知各有什么证据和成本。样本只有三个且作者已见，只报告开发观察，不外推生产成功率或可靠性概率。

[batch.json](batch.json)是实际运行配置；[enrollment.json](enrollment.json)固定任务顺序及基线、规范、参考数据与工具身份；[implementation.json](implementation.json)固定实际源码集合、构建工具链、二进制与镜像身份。[freeze.json](freeze.json)保存本目录其余文件的原文字节 SHA-256，不包含自身。源码摘要不包含本协议目录，避免自引用；其确切范围与算法见 implementation.json。完整源码文件摘要表另保存在忽略目录 runs/l2-protocol-source-files.json，可从所述 Git 路径集合复算。

能力依据是此前唯一一次获授权查询的[原文](models-observed.json)与[解析记录](capabilities-observed.json)，查询时间 2026-10-20 02:38:42（Asia/Taipei），URL 为 https://api.deepseek.com/models。本协议没有进行第二次查询。旧响应用于审查预留值；正式启动仍查询一次当前声明并保存新原文，不将旧材料冒充本次查询。

## 任务、构建范围与一次尝试

| 顺序 / Target | 数据 / 宿主 Profile | 必需范围 | 规范 |
| --- | --- | --- | --- |
| 1 / goddd | rpcx-l2-goddd-v1 / rpcx-goddd-t3-v1 | default | [v0.3.0](../../../scenarios/rpcx/goddd/specs/v0.3.0.md) |
| 2 / translation | rpcx-l2-translation-v1 / rpcx-translation-t3-v1 | all-required：default 与 rpcx_alternate，同一候选 | [v0.4.0](../../../scenarios/rpcx/translation/specs/v0.4.0.md) |
| 3 / workouts | rpcx-l2-workouts-v1 / rpcx-workouts-t3-v1 | trainings/default 与只读 common 前置模块 | [v0.5.0](../../../scenarios/rpcx/workouts/specs/v0.5.0.md) |

N 固定为 3；每 Target 最多一个 Run，不挑最好结果。未启动、基线红、超时、预算不足、模型/设施错误都保留在 N 中。Translation 的两个配置不增加分母。留出一个名额仍待独立准备，本轮不创建或访问留出样本。

Agent 使用该源码版本中的原 Runner 提示、工具定义、预算提醒及错误反馈，不临时改提示或人工修补候选。每个 Run 独立对话与工作区。Agent 能通过工具读取本任务代码、规范与只读本地 SDK/common；参考探针、答案、校准补丁、其他任务、批次账本和评测清单均不在其工具根内。初始提示和后续动态消息以原 Agent 记录为准，不能用事后重建的提示替代。

## 模型、预算与时间

| 项目 | 固定值 |
| --- | --- |
| 模型 / 生成端点 | deepseek-flash / https://api.deepseek.com/chat/completions |
| 能力端点 | https://api.deepseek.com/models，仅一次应用层 GET，无自动重试 |
| 生成模式 | 文本 Chat Completions，非流式，thinking disabled，无服务端工具；temperature 等未配置参数沿用服务默认并记录为未显式控制 |
| 上下文 C / 输出 O | 期望 C=1,048,576；请求 max_tokens=4,096 |
| 单请求预留 Q | C+O=1,052,672 Token |
| 每任务额度 | 最多 40 次准入、2,500,000 Token；Loop 最多 40 次 Complete 尝试、80 次工具调用 |
| 全批额度 | 最多 120 次准入、7,500,000 Token；不追加额度 |
| 并发 | Target=2，执行命令槽=2；按清单顺序接纳 |
| 单 Run 时间 | 10 分钟，包含准备、基线、生成、等待执行槽及独立门禁；不是 10 分钟纯模型时间 |
| 外部停止 | 启动进程 30 分钟后发送 SIGINT；停止新生成并尝试收尾，60 秒后仍未退出则终止本次宿主子进程；记录不完整及残留，不重跑 |

全批 Token 上限取三个 Target 上限之和，以避免共享池额外压缩单任务额度；代价是相较 600 万草案多批准 150 万。250 万是保守占用上限，不保证能完成 40 次；已知占用超过 1,447,328 时已无法再预留 Q，未知调用保留整笔 Q，不按零结算。金额未获价格与账单证据，不把 Token 上限称为货币费用上限。

正式入口先 Discover，所选模型 C 与 expected_context_tokens 不一致时保存能力材料、退出 2，不创建 Campaign 或发送 POST。服务允许输出须至少为 4096，否则 Discover 拒绝。其他模型、显示名或无关元数据变化可使原文摘要变化，不单因此拒绝；新原文、时间与实际 Profile 由本次 Plan 绑定。字段缺失、无唯一模型、上界无效、HTTP/解析错误均停止，不猜测或沿用旧上界。模型别名可能变更底层实现，该检查不证明权重固定或服务计量正确。

一次 Complete 最多发起一次应用层生成请求；无自动重试、无重定向重放。请求前持久化预留、调用后结算；未知用量占满预留，非法用量触发停批。单任务失败继续其余任务，共享控制/账本故障则停止接纳并取消在途。原单任务错误、Run 失败与质量 Rejected/NeedsAttention 分开报告。父取消与强杀的未决记录保持不确定，M3 不恢复续跑。

## 执行环境与发布顺序

固定镜像为 golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd，linux/arm64。每容器 CPU=2、内存=2048 MiB、pids=128；禁网、只读根及源挂载，临时空间和销毁规则沿用已固定执行器。禁止临时拉取镜像或回退到 trusted-local。共享两个槽位限制执行命令数，不宣称限制整个宿主内存。宿主已检查 Go 1.26.5 / darwin arm64。

生成前必须依次完成：

1. 核对本协议所有材料摘要、源码集合、既有二进制及本地镜像身份，确认输出目录 runs/l2-dev-eval-v0.1.0 尚不存在。缺失/漂移先停止；不改目录名来绕过失败记录。
2. 离线执行配置检查。`--check` 不读取凭证或访问 API，也不证明本次服务、Docker 或业务基线已经通过。
3. 取得本次生成的明确授权后，只读取 .env 中的 DEEPSEEK_API_KEY 用于既定端点鉴权；模型固定在配置，不使用环境变量切换模型，不将整个 .env 当 shell 脚本执行。
4. 正式批次唯一一次当前能力 GET、容量核对和 Docker 初始化成功后，独占保存能力与配置材料；Campaign 保存实际 Plan 与控制记录。
5. 任务接纳前发布静态快照、round-manifest.json、round-request.json；PlanSHA256 取实际文件原文，查询时间/实际 Plan 身份不预填。发布失败零任务接纳。
6. 每 Target 实际认证并构建/测试基线，通过后才调用模型；提交后由独立质量门禁检查，保存候选、补丁、命令证据、审核材料与原始终态。质量判定和执行状态均保留。

从仓库根运行的离线命令如下；二进制不入 Git，使用前核对 implementation.json：

```sh
runs/harness-l2-dev-v0.1.0 batch --config docs/experiments/l2-dev-eval-v0.1.0/batch.json --check
```

授权后由受控宿主进程在相同命令中移除 `--check`，设置仅用于鉴权的环境变量并实施上述外部停止规则。授权前不运行该步骤。控制台、退出码、起止时间、实际 Git revision、二进制身份和授权范围另存 runs/ 下的启动记录；错误或部分材料不删除、不覆盖。

## 独立参考与源码、交付复核

参考实现和冻结材料采用 enrollment.json 中固定身份：Goddd data-v2、Translation data-v2、Workouts data-v1，ToolSHA256 为 ad6681e9020f8f250b9457cf7c7b34ac490d4ebaa2d4566aae51330105d0b939。参考在生成结束后对实际候选运行，不接收原 gate verdict/expected/Agent 自述作为判据；依赖与参数按原 Request 从认证基线及实际 Run 身份取得，使用相同 Docker 后端，依序执行，不调用模型。

评测覆盖所有存在且可认证的候选，不仅挑 ReadyForReview；没有完整候选或无法绑定时记录不可评/未知，不从 N 删除。参考父 context 每 Target 最多 15 分钟，既有每命令超时/计时容差和必需事件不变；超时不重试、不放宽窗口。参考产物写新目录，不改原候选或门禁。

| 参考范围 | 必须观察 |
| --- | --- |
| Goddd | 模块/保护路径/基线及候选构建、原测试、HTTP→booking→装饰器→RPC 的映射/空结果/错误链、ctx 值与取消、短父期限与逐次 30 秒默认期限及实际到期 |
| Translation | default 和 rpcx_alternate 均有实际选择文件、构建、原测试、映射/用户身份、Store 次序、History、tracing、ctx/期限及实际到期；缺任一配置不得完整通过 |
| Workouts | common 自身构建/测试；真实 callback ctx、条件余额、余额→trainer→本地提交次序、参数/空确认/错误链、成功远端副作用、两客户端分别派生与实际到期 |

自动检查结果为 pass/fail/inconclusive：可认证候选上的确定违反规范保留 fail 与条款；参考编译不适配、缺事件、计时不足、设施或输入认证问题保持 inconclusive，不直接当候选语义错误。等价 helper/WithDeadline 按实际行为与源码证据判断，不要求与参考补丁同形。

另行填写三个复核项，不预填通过：

- test_preservation：逐处比较受影响测试、断言、skip/build tag、替身及事件，确认没有只保留测试名却弱化断言。
- change_coverage：检查全部生产 diff、调用链与未被自动探针覆盖的分支；将每处行为改动对应规范条款，对无法证明处保持未知。
- delivery_evidence：将交付报告中的依据、实际命令/配置/日志、已知缺口和候选身份逐项核对，检查是否夸大验证或遗漏未解决问题。

实际人类复核者填写 candidate_review 的身份、RFC3339 时间、材料位置及逐项理由；只有真实活动计时才记录人工耗时。未安排或未完成时保持未知，不用助手替人签署通过。助手可辅助定位差异和反例，但作者已见与同一开发者的共同盲区须披露，不称为独立人员盲审。自动已确定 fail 不因缺人工复核而被抹掉；自动 pass 仍不等于完整 pass。

## 汇总、指标与结论限制

保留原 Manifest/Request；将独立参考与实际复核的路径/原文摘要填入新的 Request 副本，写入新的 evidence_dir，由既有 summarize 生成报告。身份/摘要冲突时轮次 Valid=false，不发布效果比例；缺失参考/复核保留未知。实际 Plan、Run、候选、规范与参考必须相同，不用旧校准结果改名顶替。

- N=3；R 为不同实际 Run 数；Q 为取得原质量判定的 Target 数；A 为 ReadyForReview，H 为 NeedsAttention。
- S 为原 Run Completed、ReadyForReview、完整独立复核 pass 的数量；有效自动成功 S/N。未知复核时报告 S、u_s 与区间 [S/N,(S+u_s)/N]，不只报下界冒充完整结论。
- k 为放行后完整复核 fail，u 为放行后仍未知。A=0 时错误放行率无定义；u=0 时 k/A；否则报告 k/u/A 及 [k/A,(k+u)/A]。区分迁移/测试错误与交付依据错误，同一候选只计一次。
- 需人工确认同时报告 H/N、H/Q；Q=0 时后者无定义。不把该比例换算成人工时间。
- 成本分列实际已知输入/输出、未知保守占用、在途预留、准入/拒绝数、Loop 尝试/工具数、墙钟和失败原因；Campaign 与 Target 不能重复相加。缺账本不按零计费，货币账单与未实际记录的人工耗时均未知。

所有失败、超时、取消和部分产物写入单独的本轮结果记录，保留原始记录不回写协议。若需要调优，先完整报告本轮，再在开发集开新版本。此轮没有未见留出、独立人员盲审或统计功效，不能代替 M5。
