# 受预算约束的批次入口

- 日期：2026-10-20
- 状态：已验收（本机模拟服务及既有夹具范围）
- 对应里程碑：[M3.4](../MILESTONES.md#m3多仓库批量闭环)
- 基线：163a8ee。已有三仓 Campaign、独立参考与轮次汇总；本工作补齐真实供应商能力材料和命令组装，不运行真实模型实验。

## 目标与取舍

优先保证上界有来源、生成前材料落盘、每次生成经过账本，再考虑启动便利性。预算仍采用 ADR-006 的保守 `Q=C+O`；不把 Token 限额称为金额限额。

| 方案 | 当前场景中的差异 | 选择依据与代价 |
| --- | --- | --- |
| 在配置中手填 C 或抄文档示例 | 无额外 HTTP，但缩小 C 就能错误解锁预算；模型别名变化后难以识别依据 | 不采用；文档示例不是实际账户返回材料 |
| 导入预先捕获的模型文件 | 可离线准备，但需要额外认证来源、过期与模型别名政策 | 本次不采用；若供应商元数据端点不可用，再设计人工核实入口 |
| 每批生成前查询同源 `/models` | 获得本次可用模型及声明上界；新增一次鉴权 GET，依赖该端点可用 | 采用；缺少必要字段立即停止，不静默猜测默认值，也不自动重试 |

2026-10-20 查询官方 [Models API](https://api-docs.deepseek.com/api/list-models/)：`context_window` 表示输入输出合计容量，`max_output_tokens` 表示允许的输出上限，两字段可选。仅接受唯一匹配模型、正整数容量及明确支持文本的记录。调用配置中的 `max_tokens` 必须不超过服务声明输出上限和上下文容量；生成请求继续固定非流式、thinking disabled、无自动重试及不跟随重定向。协议依据为 [Chat Completions](https://api-docs.deepseek.com/api/create-chat-completion/)。服务遵守声明上界仍是条件，不声称本地代码能认证供应商内部计费或锁定别名实现。

命令留在 `cmd/harness`：`harness batch --config <JSON>`，组装已有 `campaign.Run` 与 rpcx 宿主适配器；不另建调度器或插件注册框架。JSON 中引用完整 Target（路径、摘要、宿主 Profile）、预算、Agent 次数/时间及执行配置；相对路径以配置文件目录为基准。首版只接 DeepSeek，保留原单仓入口。

`--check` 只校验本地配置，不读密钥、不访问网络、不创建运行目录，明确尚未核实模型容量。执行时才读取 `DEEPSEEK_API_KEY`，不自动加载 `.env`。元数据 URL 从 chat endpoint 的同源路径导出，禁止重定向；生产使用 HTTPS，本机模拟服务可用 HTTP loopback。

外层输出目录独占创建，保存解析后的配置、原始 `models.json` 与能力记录；实际 Campaign 放在其 `campaign/` 子目录，沿用现有独占创建规则。能力 Profile 的 SourceSHA256 绑定原始响应，Plan 和账本绑定同一 Profile。材料保存失败时零生成调用。失败目录保留且不可复用；不增加 M4 恢复语义。

退出码：0 表示所有 Target 完成交付（允许 Rejected/NeedsAttention）；1 表示能力查询、设施、执行或未启动任务失败；2 表示配置不合法；130 表示取消。输出同时列质量数量，不能由退出 0 推断迁移成功。

## 验收计划

1. 模拟 HTTP 检验同源 GET、鉴权、必需字段、重复模型、越界输出、重定向、取消及错误脱敏；不请求真实端点。
2. 命令检查覆盖未知字段、重复 Target、相对路径及非法限额；不创建产物或发送 HTTP。
3. 用真实适配器、基线和独立门禁运行本机受控批次；服务收到 POST 时账本必须已有预留，能力原文摘要一致，密钥不落盘。失败任务令 CLI 非零，但不改写其他交付。
4. 执行相关竞态检测、项目回归、静态检查及文档检查，再记录实际范围；不以模拟端点结果宣称真实能力认证或 L2 效果评测完成。

## 实际实现与验证

`internal/deepseek` 新增 Discover：只请求由 chat endpoint 导出的同源模型路径，响应限 4 MiB，禁止重定向；HTTP 错误不保存未知正文，能力原文若包含凭证则拒绝保存。唯一匹配、文本输入输出、两项容量、配置输出限制与整数溢出检查通过后，才生成带原文摘要及查询时间的预算 Profile。服务的输出能力上限另存，实际每次预留使用请求配置的 O。没有硬编码模型别名或容量。

`cmd/harness` 增加 batch 子命令及严格 JSON 读取，拒绝未知字段、多份 JSON、重复任务、摘要不匹配、路径重叠、非法资源与时间限制。新增 `runner.ValidateTarget` 复用宿主 Profile 与路径校验，避免入口另维护一份支持清单；实际磁盘内容与基线仍由 Runner 认证。路径根据配置位置解析，输出父目录必须存在；原单仓参数入口保留。

输出材料使用独占文件、文件同步与目录同步；外层目录也先同步父目录，再交给原 Campaign 独占创建子目录。能力或材料准备失败时不启动生成，已有目录不复用。命令将原 Campaign 的局部失败计数映射为非零退出码，同时显示独立质量计数；不重新判定候选。模板位于 [examples/rpcx-l2-batch.json](../../examples/rpcx-l2-batch.json)，带三个已发布输入及示例次数、Token、时间限制，模型名保留明确占位符。模板不是正式实验协议或此次模型调用授权。

### 发现的问题

- 首次聚焦检查 `runs/batch-cli-focused-first.jsonl` 因沙箱禁止 localhost 监听而失败；未将其算作实现通过。允许本机端口后，模拟服务及真实夹具联调通过。
- 项目回归发现新增路径测试直接比较 `/var/...` 与实际解析的 `/private/var/...`。实现已正确规范化路径，错误位于测试断言；修正为比较物理父目录。失败保留在 `runs/batch-cli-project-tests.jsonl`，修正检查见 `runs/batch-cli-path-fix.jsonl`，整个命令包随后通过。

### 已完成的机制证据

| 检查 | 实际结果 | 证据 |
| --- | --- | --- |
| 能力与 batch 聚焦竞态检查 | 两个包、38 项测试及子测试通过，无竞态报告；涵盖字段缺失/非法/溢出、重复模型、文本能力、超大响应、重定向、取消、鉴权及输出约束 | runs/batch-cli-focused-race-final.jsonl |
| 命令包回归 | 22 项测试及子测试通过；Docker 需显式启用的用例未执行 | runs/batch-cli-command-accepted.jsonl |
| 最终项目回归 | 25 个有测试的包、921 项测试及子测试通过，退出 0；其中未变化的包复用 Go 测试缓存，不与其他轮次相加计数 | runs/batch-cli-project-tests-final.jsonl |
| 最后配置分支竞态检查 | 16 项测试及子测试通过，覆盖路径断言修正、非法配置与最后的额度错误信息调整 | runs/batch-cli-config-race-accepted.jsonl |
| 模板离线检查 | 3 个 Target 通过，不读密钥、无网络请求、无运行目录 | 本轮 `harness batch --config examples/rpcx-l2-batch.json --check` |
| 静态检查 | deepseek、harness、rpcx/runner 的 go vet 通过 | runs/batch-cli-vet-final.log |

受控完整交付保留在 `runs/batch-cli-accepted/batch-cli-1333224595/batch/campaign/summary.md`：一个任务 Completed / Rejected，CLI 返回 0。混合批次为 `runs/batch-cli-accepted/batch-cli-1386199613/batch/campaign/summary.md`：正确记录输入缺失的执行失败，另一个任务仍 Completed / Rejected，CLI 返回 1。候选刻意原样提交，拒绝是预期结果。

两个批次各有 1 次元数据 GET 和 1 次生成 POST；服务收到 POST 时，磁盘账本已有 2123 Token 预留（受控 C=2000、O=123），Profile 的来源摘要与已保存原文一致。最终各结算为受控响应声明的 25 Token，无在途或未知占用；这些数字不代表真实 DeepSeek 容量或费用。输出树凭证检查通过。额度不足、能力缺失、取消、检查后输出位置被占用均未发送生成请求。

139 个本地文档链接目标、代码围栏、Go 格式与 git diff --check 检查通过。源码及运行日志摘要保存在 runs/batch-cli-summary.json，产物不进入 Git；原规范、数据集、候选补丁、oracle 和旧实验记录未修改。

## 结论与下一步

本单元完成受预算约束的实际命令组装：本地校验 → 同源能力查询 → 材料落盘 → Campaign → 两级预留与 Agent → 原独立门禁与交付。默认仍为 Docker；此次命令联调使用可信本地夹具，未重新进行 Docker CLI 实测、完整 L1 正式矩阵或真实供应商容量认证。

本轮未读取 .env、发送真实模型请求或运行 L2 付费批次，M3 仍在进行。下一步实际核实所选供应商/模型的能力材料，审查真实预算并发布完整开发实验协议，之后才运行三个开发 Target 各一次的真实评测。留出仍待独立准备，不计入已准备样本或开发分母。
