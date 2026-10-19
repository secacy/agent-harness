# L2 轮次离线汇总

`Summarize(ctx, Request)` 在单候选参考之后读取发布记录，输出新的 `round.json` 和 `summary.md`。当前支持一个 Campaign、每 Target 一次尝试的三个已发布 L2 Profile；不运行模型、候选命令或门禁，不修改原 Campaign/Run/参考/复核。多 Run 恢复及通用离线审计仍归 M4。

输入分两部分：Manifest 固定轮次 kind、Campaign/计划摘要、按计划顺序纳入的 Target 集合，以及 Profile/构建/基线/规范/参考数据/参考工具身份；Request 指向该清单、Campaign 目录、尚不存在的输出目录和可选的逐 Target 参考/复核文件。Request 所有路径为绝对路径，参考/复核文件另带实际 SHA-256。纳入清单不是完整实验协议，也不授权模型调用。真实开发轮次的清单应在生成前固定；开发校准可以在既有受控运行上创建，必须标 `development_calibration`。受控模型 Campaign 不能通过更换 kind 冒充真实评测。

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
