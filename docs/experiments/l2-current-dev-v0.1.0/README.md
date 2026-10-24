# 当前实现四任务开发观察 v0.1.0

- 日期：2026-10-24（Asia/Taipei）。
- 协议 / Campaign ID：`l2-current-dev-v0_1_0`。
- 格式：schema 2 / development_evaluation / development，每 Target 一次尝试。
- 状态：本地执行材料固定，未获得本轮真实外发及消费授权，尚无新生成。
- 依据：[历史回顾](../2026-10-24-gate-value-review.md)、[准备取舍](../../worklog/2026-10-24-current-development-observation.md)。

## 目标与可比边界

观察当前已提交实现对四个固定输入产生的新候选：有效自动成功、错误放行、未知原因、失败分布及实际记录成本。四项全部已参与开发/校准，不是严格未见留出；生成者仍从冻结基线开始，不能看到旧候选或评测答案。

输入与单任务预算沿用旧四任务设置，生产门禁包含之后的分类/有限证明/响应契约修正；共用 Recorder 已验收。GHCLI 参考使用 v3，而旧四任务与后续 GHCLI 使用 v2。模型服务的权重、默认采样、供应商计量和实际运行环境不保证跨轮相同，因此新旧结果不能归因为某一个修改，也不合并分母。本轮只报告一次尝试的有限开发观察，原 1/4、后续单任务 0/1 及其未知/误拒保持。

## 材料与位置

[batch.json](batch.json)是执行配置，[enrollment.json](enrollment.json)固定输入/规范/Profile/参考/计划与四份材料。[dataset.json](dataset.json)记录四项来源和暴露，[reviewers.json](reviewers.json)固定实际助手身份与判据，不预填结果。[implementation.json](implementation.json)、[source-files.json](source-files.json)固定实际源码导出与二进制；[environment.json](environment.json)固定本地 Go、平台、现存镜像与引擎。

[models-observed.json](models-observed.json)、[capabilities-observed.json](capabilities-observed.json)原样使用 2026-10-23 已获授权查询的历史材料，只作本轮期望；不是当前服务的新查询或保证。[expected-plan.json](expected-plan.json)用于离线绑定核对，不冒充实际 Plan。[freeze.json](freeze.json)对本目录除自身外的文件逐件取原文字节摘要。

当前源码快照和独立构建的 harness/summarize 位于 `runs/l2-current-dev-v0.1.0-preparation/snapshot/`，不含凭证、Git、历史运行或协议文档。协议在 Git 另行固定，避免递归身份。不重新打完整交付包；实际执行后另保存原始结果。

实际批次目录固定 `runs/l2-current-dev-v0.1.0`，启动收据目录固定 `runs/l2-current-dev-v0.1.0-launch`，均须不存在。配置绑定本工作区路径与现存 Docker 引擎，更换环境须另版。任何实质变更另版，不能见结果后调整本版再重跑。

## 任务与候选

固定顺序：Goddd/default、Translation/all-required（default 与 rpcx_alternate）、Workouts/trainings/default（含只读 common）、GHCLI/有限应用/default。N=4，全部 seen-development，同时逐 Target 报告。Translation 多配置不增加 N，两个 GHCLI 历史候选不进入本轮。

每 Target 最多一个 Run、一次原候选；不自动重试、恢复、人工修补或替换。基线红、预算耗尽、API/协议/设施/发布失败及无候选均保留纳入记录，不删除以改善比例。材料/能力在接纳前拒绝时保留启动拒绝，不冒充完整生成实验。

生成对话仅见自己业务副本、迁移规范、SDK 和允许的只读依赖。账本、原基线写入口、参考、oracle、expected、旧候选和其他 Target 不进入工具根。提示、工具及预算反馈使用固定二进制内的既有实现，本轮不加候选专属提示或给出人工修复建议。门禁独立执行，不读取参考答案。

## 模型、预算与停止

| 项目 | 固定限制 |
| --- | --- |
| 端点/模型 | https://api.deepseek.com/chat/completions / deepseek-flash |
| 能力查询 | https://api.deepseek.com/models，一次应用层鉴权 GET，无自动重试 |
| 模式 | 文本、非流式、thinking disabled，无服务端工具、无自动生成重试；未设采样参数沿用服务默认 |
| 能力期望 | C=1,048,576；请求 max_tokens O=4,096；Q=C+O=1,052,672 |
| 单 Target | 最多 40 次准入、2,500,000 Token；Loop 40 次 Complete 尝试、80 次工具；Run 总时限 10 分钟 |
| 全批 | 最多 160 次生成准入、10,000,000 Token；工具合计至多 320 次；不追加 |
| 并发 | Target=2、命令槽=2 |
| 宿主停止 | 启动后 40 分钟 SIGINT，停止新增生成；最多再尝试 60 秒收尾，仍未退出则 SIGKILL 自有进程组；不重启 |

全批等于四项单任务上限之和，避免额外紧缩共享池干扰；这些是上限，不保证每任务能调用 40 次。请求前持久化双层预留，合法 usage 按实结算，断连等未知用量保留整笔 Q；非法 usage/共同账本错误停批。Token 额度不是货币账单上限，密钥在本系统外的消费不受其约束。

使用 model-contract-v1 绑定所选模型的相关能力与实际请求设置，保留每次 Models 原始响应身份。无关展示字段变化可接受，关键能力、预算、模式或材料漂移即零生成停止；不自动改期望、再次 GET 或换模型。能力材料历史性、模型别名与服务契约限制见 ADR-006/011；真实上界依赖服务遵守公开声明。

## 环境与启动检查

现存固定镜像 `golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd`，linux/arm64。每容器 2 CPU、2048 MiB、128 PID，禁网、只读根与输入挂载、隔离缓存，不自动拉镜像或回退 trusted-local。宿主工具与当前 context/engine 以 environment.json 为准；两个槽位不限制整个宿主或 daemon 总资源。

先用固定二进制执行 `batch --config <本目录/batch.json> --check`。完整启动器只读检查 freeze、当前源码/导出副本/二进制、Go、现存镜像与引擎、输出独占位置，不读取密钥、不调用能力/生成或创建容器：

```sh
python3 docs/experiments/l2-current-dev-v0.1.0/launch.py --check --freeze-sha256 <freeze.json 的实际 SHA-256>
```

执行模式为同一启动器 `--execute`，仅在取得本版明确授权后使用。先独占保存启动收据，再只解析 `.env` 的 DEEPSEEK_API_KEY；不把 .env 当 shell 脚本、不读取 Key 到报告、不按环境变量更换模型。干净 Git、所有摘要及环境不变才准启动；重复启动或输出占用拒绝。实际查询、Plan 和动态纳入材料在生成前落盘；期望 Plan 不替代实际证据。

## 独立参考与实际助手复核

四项固定参考为当前 PublishedIdentity：Goddd data-v2、Translation data-v2、Workouts data-v1、GHCLI data-v3；具体数据/工具/规范摘要在 enrollment 中固定。所有可认证候选都运行独立参考，不按门禁结论挑选。每项参考父时限 15 分钟，四项依次进行；各必需配置、事件、真实到期探针及单命令上限沿用固定实现，失败不调规则或重试。缺候选记不可评，设施/兼容问题与证据不足记 inconclusive，不能伪装成候选语义 fail。

预定实际复核者为 codex:/root，用户已委托后续复核由助手完成。该助手参与了开发、旧候选复核、协议准备和协调，四项输入/旧答案均已暴露；与 DeepSeek 生成对话分开，但不是独立人类或盲审，共享代码、SDK/设施和相关盲区。不可用时记录偏离与未知，不伪造签署。

生成后先按固定判据阅读原候选，再消费独立参考及证据；逐项检查测试保留、全部生产修改覆盖、交付依据，写实际 RFC3339 时间、身份、文件/条款/日志定位和理由。禁止预填 pass 或冒用用户签名。疑点诊断在独立目录记录为事后诊断，不修改冻结参考、候选或原质量结论；无法充分确认的修改保持未知。

## 统计、交付与停止条件

N 为事前纳入四 Target；R 为 Run 数，Q 为有原判定数，A 为 ReadyForReview，H 为 NeedsAttention，S 为原 Run Completed/ReadyForReview 且完整参考/实际复核 pass 的数量。报告 S/N、H/N 和 H/Q；放行后确认错误 k、未知 u：A=0 无定义，u=0 时 k/A，否则只报告 k/u/A 及未知范围 [k/A,(k+u)/A]，不是统计置信区间。完整参考不足时 S 单列未知潜在成功范围，不当成完整成功率。

执行 Failed 与质量 Rejected 分列，误拒依据与迁移/测试错误分列。固定四项全部展示，不挑最好 Run、不与旧轮合并。真实 Token/准入/拒绝/在途/未知消耗、Loop/工具次数、批次墙钟及原命令耗时分别记录，双配置时间不混为两个 Target。沿用历史回顾的成本解释，不把命令时间和当 CPU 或完整验证成本；人类活动时间、接入工时、金额无证据则未知。

实际原记录、补丁、规范、门禁、日志与预算保留；新目录放参考/实际复核和汇总 Request，不回写原材料。交付时核对来源保全、补丁重放、审核导航、审计和资源回收。若生成前停止或无可评候选，据实完成有限结果，不追加额度、另起尝试或拿旧候选填入。

本轮观察完成后停止，先报告真实结果再选择研发目标；不能以“比旧结果好”直接归因修正，也不为追求全通过而接着调整规则重跑。原严格留出、人员总体验收及审核成本缺口继续单列。
