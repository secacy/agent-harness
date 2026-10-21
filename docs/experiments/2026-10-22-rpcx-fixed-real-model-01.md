# 四任务固定样本真实评测 01

- 日期：2026-10-22（Asia/Taipei）。
- 状态：唯一批次、四项独立自动参考、实际助手复核与轮次汇总完成；交付归档验证见文末。
- 协议：[l2-fixed-eval-v0.1.0](l2-fixed-eval-v0.1.0/README.md)，运行发布 revision `1e334e0`；原协议文件不回写。
- 类型：schema 2 `formal_evaluation` / development；三个已见开发样本＋一个隔离准备后已校准样本，不是严格未见留出或独立人员盲审。
- 实际材料：[本轮入口](../../runs/l2-fixed-eval-v0.1.0/README.md)、[完整汇总](../../runs/l2-fixed-eval-v0.1.0/round-final/round.json)、[分层原始计数](../../runs/l2-fixed-eval-v0.1.0/strata.json)。

## 授权与固定条件

用户在看到本版实际传输范围和限制后回复“授权”。该次许可覆盖读取 `.env` 的 DEEPSEEK_API_KEY、一次 Models GET、向既定 DeepSeek 生成端点发送四个切片代码/规范/允许的只读依赖及工具交互；最多 160 次生成准入、320 次工具、1000 万 Token，单 Run 10 分钟，全批 40 分钟后停止并最多尝试 60 秒收尾，不重试、不追加额度。

启动器先核对工作区、762 份源码、协议 freeze、二进制/归档、Go 和现存镜像/context/engine，再执行真实二进制。只解析 Key，不执行 `.env`，只传递明确列出的必要环境变量。一次当前 Discover 的原文摘要仍与期望一致，C=1,048,576、O=4,096、Q=1,052,672；未放宽 schema 2 设置摘要。模型别名不证明权重固定，Token 不代表账单金额。

生成前的 Enrollment、Manifest、Request 与四份协议材料副本均先于首次预算预留发布。实际启动和结束为 **02:32:25—02:43:44 +08:00**，宿主墙钟 **679.186 秒**，退出 0。没有触发外部停止，没有配置/基线/执行失败或任务跳过；退出 0 只表示执行交付完成。

## 逐任务结果

助手项顺序为原测试保留 / 变更覆盖 / 交付依据。自动参考 pass 后仍要结合实际复核，原门禁始终不改。

| Target | 原门禁 | 独立自动参考 | 实际助手三项 | 完整参考/复核 | 主要观察 |
| --- | --- | --- | --- | --- | --- |
| Goddd | NeedsAttention | pass | pass / inconclusive / pass | inconclusive | 必要 ctx/API 适配之外新增 mock nil 回调保护；原未初始化回调的 panic 变为空返回，缺该额外行为的批准/验证依据 |
| Translation | NeedsAttention | pass，两配置 | pass / pass / pass | pass | 原生产结构与测试形式检查未知；逐行复核及两配置参考支持当前候选，但不回写为自动放行 |
| Workouts | ReadyForReview | pass，含 common 与两调用真实到期 | pass / pass / pass | pass | 本轮唯一有效自动成功；callback ctx、条件余额/调用顺序及错误行为保持 |
| GHCLI | Rejected | pass，含两个入口真实到期 | pass / pass / fail | fail，类别 delivery | 静态证明未识别冗余 nil-parent 分支，却报明确 parent 丢失；候选迁移行为通过，原拒绝依据存在误判 |

全部四个 Run 的 Agent 退出原因为 completed，每个任务只有一次尝试；没有挑选最好候选。完整复核 fail 的 GHCLI 是**交付依据错误**，不是确认迁移/测试语义失败。Goddd 的未知也没有被行为探针通过抹掉。

## 指标与分层

固定总 N=4、R=4、Q=4、A=1、H=2、Rejected=1、S=1。完整参考为 2 pass / 1 inconclusive / 1 delivery fail；放行项无未知。轮次 Valid=true、ExecutionComplete=true。

| 集合 | N / A / H / S | 有效自动成功 S/N | 错误放行 k/A | 需确认 H/N（本轮亦为 H/Q） |
| --- | --- | --- | --- | --- |
| 全部固定样本 | 4 / 1 / 2 / 1 | 1/4 = 25% | 0/1；未知放行 0 | 2/4 = 50% |
| seen-development | 3 / 1 / 2 / 1 | 1/3 | 0/1；未知放行 0 | 2/3 |
| isolated-prepared-calibrated | 1 / 0 / 0 / 0 | 0/1 | 无定义，A=0 | 0/1 |

一次尝试、四个固定样本和一个放行项不提供生产概率或统计稳定性。0/1 不能解释为“没有错误放行风险”；同样，GHCLI 的无放行不能解释为高可信。事后复核正确的 NeedsAttention 仍不计 S，Rejected 候选即使迁移行为正确也不计自动成功。

## 成本及失败分布

| Target | 模型准入 / 工具调用 | 已知 Token | Run 墙钟秒 |
| --- | --- | --- | --- |
| Goddd | 40 / 66 | 708,115 | 256.962 |
| Translation | 37 / 61 | 639,944 | 501.152 |
| Workouts | 22 / 38 | 206,331 | 186.348 |
| GHCLI | 21 / 31 | 331,577 | 230.654 |
| 全批 | **120 / 196** | **1,885,967** | **679.186（宿主总墙钟）** |

已知输入 1,861,376、输出 24,591；120 次预算请求均 settled，未知和在途占用均为 0，预算拒绝为 0。逐 Target 并发，Run 时间不直接相加为批次墙钟；独立参考、助手复核与打包耗时不混入生成批次墙钟。参考/复核没有额外模型调用。货币账单、实际人类审核耗时未知。

本轮执行失败分布为空；质量分布是 NeedsAttention 2、Rejected 1、ReadyForReview 1。Goddd 已知范围缺口、Translation 支持形态缺口及 GHCLI 判定依据错误分别保留。旧三仓实验使用旧实现且模型有随机性，不把本轮与旧轮的差异作为受控改进比例。

## 重要个案：证明失败被当成明确违规

GHCLI 在两处先取得 `*http.Request.Context()`，再写了冗余的 `if parent == nil { parent = context.Background() }`。固定 Go 1.26.5 的 `Request.Context()` 永不返回 nil，因此这两个 fallback 不会实际替换有效请求上下文。候选从原 HTTP parent 派生逐调用 30 秒、保留短父期限，并通过 body Close 管理资源；原测试、参考 parent/value/cancel、body 消费和两个实际到期入口均通过。

原 typed-analysis 却在 Call 处返回 `request_parent_not_preserved`，把 `migration_completeness=fail` 汇总为 Rejected。只证明当前分析器不识别形态，不能据此推导运行时已经丢失 parent；这正是本轮记录的误拒和 delivery_evidence fail。依据保存在 `reviews/net-http-request-context.txt`、`reviews/ghcli-static-proof-review.json`、`reviews/ghcli.json` 与原 typed-analysis。没有修改候选、分析器、参考或原门禁。

后续优化应优先修正“未能证明”与“明确违反”的分类边界，并把本次候选纳入新的回归工作；先保留本轮，再按新版本验证。删除冗余代码可能改善候选可读性，但不能以事后手工修补替代原实验。

## 审计、复核方法与交付

原 Campaign 的 **19,290** 份非缓存文件在参考、复核与汇总后保持字节不变，762 份实现源和 11 份协议材料均不变。四份补丁重放到冻结基线后均等于保存候选；681 个本地审核链接存在。原生审计 consistency=pass、execution_state=completed、violations/gaps=0。记录到的 141 个精确容器 ID 均已不存在；以实际注册表原文字节摘要查询本 Campaign 标签，也没有遗留容器。

资源标签辅助检查首轮误用紧凑 JSON 计算摘要，不能证明实际标签下无资源。错误诊断另存 `live-registry-invalid-compact.json`，校正为运行时的缩进 JSON＋换行原文字节后重新只读核对通过；不是一次生成/候选/参考重试。

预定复核者为 `codex:/root`，实际记录含时区时间、文件/证据和理由。主流程先读候选/规范再消费独立参考，已知原判定元数据，兼任协调及旧样本开发，不称盲审或独立人员。GHCLI 业务/规范正文在生成结束后为本次助手复核开放，访问另存记录；没有读取控制答案、向生成回传参考或利用结果调优本轮。准备/参考/门禁 Agent 和助手共享模型家族/文件系统，相关共同盲区仍在。

完整材料包为 [harness-fixed-evaluation-v0.1.0](../../runs/deliveries/harness-fixed-evaluation-v0.1.0/README.md)，包含协议、源码/二进制、原批次/账本/候选/日志、参考、实际助手记录、汇总/分层及执行驱动。缓存、Git 元数据及凭证不入包；原始材料不改名覆盖。包内离线核对结果见[验证记录](../../runs/deliveries/harness-fixed-evaluation-v0.1.0.verification.json)，不重新请求模型或运行候选。

本轮完成有限固定样本的真实观察和可审查交付，不满足原 M3/M5 严格留出、人员独立性或任意仓库泛化要求。下一项优化有了具体反例，不需要追加真实生成来替代这次结果。
