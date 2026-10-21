# L2 轮次离线汇总

`Summarize(ctx, Request)` 在单候选参考之后读取发布记录，输出新的 `round.json` 和 `summary.md`。当前支持一个 Campaign、每 Target 一次尝试的三个已发布 L2 Profile；不运行模型、候选命令或门禁，不修改原 Campaign/Run/参考/复核。多 Run 恢复及通用离线审计仍归 M4。

输入分两部分：Manifest 固定轮次 kind、Campaign/计划摘要、按计划顺序纳入的 Target 集合，以及 Profile/构建/基线/规范/参考数据/参考工具身份；Request 指向该清单、Campaign 目录、尚不存在的输出目录和可选的逐 Target 参考/复核文件。Request 所有路径为绝对路径，参考/复核文件另带实际 SHA-256。纳入清单不是完整实验协议，也不授权模型调用。真实开发轮次的清单应在生成前固定；开发校准可以在既有受控运行上创建，必须标 `development_calibration`。受控模型 Campaign 不能通过更换 kind 冒充真实评测。

生成前固定静态任务时使用 `Enrollment`：只有 schema_version、id、kind、targets，不填写动态 CampaignID/PlanSHA256。`Validate(plan.Targets)` 核对全部任务的顺序、输入身份和已发布参考的规范/数据/工具摘要；`Bind(planJSON)` 对实际计划原文取摘要并生成原格式 Manifest。它不预测时间或 JSON 格式，不选择候选，也不认证真实供应商或人工复核身份。

[batch 配置](../../../../docs/command.md#受预算约束的批次入口)可引用静态清单的路径及 SHA-256，在 API 访问前核对并固定快照。Campaign 发布实际计划后、接纳任何任务前，由可信宿主保存 enrollment.json、round-manifest.json 和 round-request.json；发布失败停止接纳。新文件位于批次根目录，Agent/门禁可见根不增加。该顺序有模拟 POST 到达时的磁盘观测验证，不构成第三方签名或对抗可信宿主事后篡改的证明。设计与证据见[工作记录](../../../../docs/worklog/2026-10-20-pre-generation-enrollment.md)。

可从 CLI 消费同一 Request JSON：

```sh
go run ./scenarios/rpcx/l2/cmd/summarize -config /absolute/path/request.json
```

`request.json` 结构：

```json
{
  "manifest_path": "/absolute/path/manifest.json",
  "campaign_dir": "/absolute/path/campaign",
  "evidence_dir": "/absolute/path/new-round",
  "references": [
    {
      "target_id": "goddd",
      "report": {"path": "/absolute/path/reference.json", "sha256": "<actual sha256>"},
      "review": {"path": "/absolute/path/review.json", "sha256": "<actual sha256>"}
    }
  ]
}
```

Manifest 的 `schema_version=1`、`id`、`kind`、`campaign_id`、`plan_sha256` 和 `targets` 必填。每个 targets 项包含 `id/profile_id/build_config/baseline_sha256/spec_sha256/reference_version/reference_sha256/tool_sha256`，均应取固定发布材料的实际身份；不能用占位摘要执行。生成有效请求的完整开发示例见可选的 `TestPublishedCampaignAcceptance`。

## v2 协议契约

v1 继续读取原开发记录。v2 的 Enrollment 和 Manifest 都必须携带 `protocol`，可使用原两个开发 kind 或 `formal_evaluation`。当前只支持三个已见 L2 Profile、`split=development`、每 Target 一次尝试；尚无可用的独立留出 Profile。formal kind 是协议绑定格式，不是 M5 完成标志，不能代表未见样本。用合成服务检查这一格式也不算真实模型效果。

| protocol 字段 | 约束与用途 |
| --- | --- |
| `id` | 宿主发布的版本化协议标识；实质变更另起版本 |
| `split` / `attempts_per_target` | 当前仅 development / 1；未接入的 heldout 和多次尝试拒绝 |
| `plan_settings_sha256` | 使用 `PlanSettingsSHA256(plan)` 计算，冻结实际计划中的模型能力、输入、顺序、两级额度、Agent 限制、时限、并发与资源归属策略 |
| `execution` | 实际 `execution.Info`；Docker 必须是 sha256 镜像 ID 和明确 CPU/内存/PID 限额；在准备屏障比较真实后端，不靠标签猜镜像 |
| `reviewers` | 精确的 Target ID → 预定 Reviewer；缺人、空值或额外 Target 拒绝；不证明此人实际签署 |
| `materials` | protocol/dataset/implementation/environment 四项，每项含绝对 path 和实际 SHA-256，单文件为 1 MiB 以内的非空说明或身份清单；不在此直接塞整个源码包 |

准备顺序：宿主先审查实际协议和四份材料，使用已核实的模型能力及计划构造设置摘要，再发布 v2 Enrollment；之后沿用 batch 的 enrollment 文件/摘要引用。`--check` 在无凭证、无网络时检查静态任务及材料，并不确认尚未查询的实际 Profile/后端。实际启动取得新能力声明、初始化后端后，在任务接纳前比较设置与后端，再发布材料快照、Enrollment、Manifest 和 Request。材料加载后原文件的变动不会替换已固定副本。

设置摘要只排除动态 Campaign ID 和本次查询时间；不排除能力响应原文摘要。服务即使仅改变 JSON 空白也会使能力身份不同而停止生成，接受这项保守拒绝成本；不能把旧响应当作本次仍然有效的能力。镜像/限额在 execution 字段另行绑定。整个源码、当前二进制和说明是否一致仍由可信宿主在冻结时核实，局部设置摘要不是完整环境证明。

v2 汇总要求批次根目录中的 `enrollment.json`、`round-manifest.json`、`execution.json` 和 `protocol-materials/{protocol,dataset,implementation,environment}`。它从这些发布副本重新关联实际 Plan，核对 Request 指向的 Manifest 原文字节，复核四份材料及后端身份；不能仅改一份事后清单来重标旧 Campaign。原材料的绝对路径仅作来源记录，离线重算不再读取它们；迁移时保留批次根目录与 campaign 子目录关系，并重写新 Request 的路径。

生成前使用今天的宿主发布认证参考版本；离线重算使用归档的固定参考/工具材料，不强迫历史工具等于今天的发布。缺协议材料属于身份缺失，不发布指标；缺候选参考或实际复核仍保持未知。带结论的复核必须匹配预定 Reviewer；正式 kind 不接受 calibration 复核。原门禁判定、分母、未知区间和成本规则不变。支持 Campaign v1 及带已知 writer 协议的 v2 终态记录；这项格式检查不替代 `harness audit`。

完整机制与拒绝路径见 [protocol_test.go](protocol_test.go)、[CLI 联调](../../../../cmd/harness/protocol_test.go)和[工作记录](../../../../docs/worklog/2026-10-21-evaluation-contract.md)。本能力不提供来源真实性签名、自动独立性认证、留出准备或模型调用授权。实际留出、复核角色和完整协议尚未齐备时，M5 仍保持未冻结、未启动。

## 汇总与指标

关联先核对 plan、Campaign/Target 索引、Run 终态、原报告/验证的候选与规范绑定，以及实际候选快照；再核对参考的同一 Target/Run/候选/基线/规范和预定工具/数据、材料与命令双日志，最后调用 `evaluation.Assess`。它不会仅凭一份 pass 字符串计成功，也不会用新参考覆盖原 verdict。原来源文件按摘要归档到 inputs，sources 记录原路径和实际副本；读取结束前再次检查文件、树及副本是否漂移。tools 保存本汇总包实际生产源码，ToolSHA256 只表示这些材料，不等于全 Harness 或依赖环境身份。

- 缺报告、参考、复核、日志或账本：保留 Target、原判定和缺口。缺交付材料不计潜在自动成功；缺成本为 null，不能按零消费。已引用 Run ID 仍在 R 中，缺 run.json 另标 run_record_missing，不宣称已认证该 Run 的完整终态。
- 身份/摘要冲突、重复引用、越界路径、混入另一种参考或开发校准复核进入真实轮次：Valid=false、Metrics=null，返回错误。CLI 退出非零；输入/配置在创建输出前非法时不创建报告。计划是确认纳入范围的必需材料，缺计划也不发布比例。
- 自动参考通过但未复核：完整参考 inconclusive。参考或复核已确定失败时保留失败类别；迁移/测试与交付依据分别列出，同一错误候选总数只加一次。Assess 只验证结构和绑定，不认证复核者身份或定位内容是否真实；材料也不具备签名或对抗可信宿主篡改的保证。

Counts 保存 N/R/Q/A/H/S 及未知放行数。Metrics 以原始分子/分母保存上下界：成功 `[S/N,(S+u_s)/N]`，错误放行 `[k/A,(k+u)/A]`，需人工确认 H/N 与 H/Q。零分母为 null；这些是未知结果范围，不是统计置信区间。参考 pass 的 NeedsAttention 不算自动成功。执行失败/Skipped 不从 N 删除。ExecutionComplete 表示批次记录完整终结，允许有已记录的执行失败，不表示全部迁移成功；取消或缺必需运行记录时为 false。

Cost 读取与发布 summary 一致的原 budget 快照，核对 Target/Run 和费用汇总，分列准入、拒绝、已知输入/输出、未知保守占用和在途预留。总计只取 Campaign 一次，逐 Target 另列。这里是聚合所需的一致性检查，不是 M4 的持久化恢复/完整账本审计。货币费用和人工活动耗时没有当前证据，仍为未知。

机制测试：

```sh
go test -race ./scenarios/rpcx/l2/round
```

已有受控三仓 Campaign 的实测校准：

```sh
HARNESS_L2_ROUND_CAMPAIGN=/absolute/path/controlled-campaign \
HARNESS_L2_ROUND_OUTPUT=/absolute/path/new-calibration-root \
go test -count=1 -timeout=12m ./scenarios/rpcx/l2/round -run '^TestPublishedCampaignAcceptance$'
```

该测试在可信本地对既有候选重新运行三个独立参考，沿用真实 Target/Run ID，保留待复核；不重新生成候选、不调用 API、不计真实模型效果。产出的 Request 可再由 CLI 写入另一个全新 evidence_dir，检验实际消费而不重跑参考。
