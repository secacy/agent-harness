# 评测协议契约：生成前纳入与离线重算

- 日期：2026-10-21。
- 状态：开发夹具及本机模拟 API 的协议链路已验收；完整正式评测未启动。
- 对应里程碑：[M5](../MILESTONES.md#m5独立评测与整体验收)。
- 目标：用已见开发夹具验证正式评测所需的协议绑定链路；不启动正式实验、不创建留出、不改写旧结果或 v0.1.1 工程包。

## 问题与方案选择

现有 Enrollment 固定参考身份，但没有固定完整计划参数、协议材料和复核人。Summarize 可以只接收一份事后 Manifest；因此新增一个 kind 字符串不足以成为正式纳入契约。

优先保证协议与实际执行一致、旧结果仍可读取，其次减少配置重复。比较三种方案：

| 方案 | 当前场景下的行为与代价 | 选择 |
| --- | --- | --- |
| 只增加 formal_evaluation | 旧 Campaign 可只改标签；预算、任务时限或复核人变化不能被发现 | 不采用 |
| 独立通用实验平台，另写调度与汇总 | 可表达任意数据集和多次尝试，但重复已有预算、输入和参考认证，引入尚无样本验证的插件接口 | 不采用 |
| 扩展现有 Enrollment/Manifest 为 v2，复用原汇总 | 明确协议、计划参数及材料身份，并消费生成前发布副本；接受维护 v1/v2 两种读取格式 | 采用 |

一份 v2 轮次只对应一个 split。本版只接入已有 development 分区及三个已发布 Profile；formal_evaluation 表示按显式协议消费记录，不表示有未见数据、更不表示 M5 验收。heldout 暂不开放，避免通过填写布尔值或改名认证独立性。将来新增真实留出需要独立数据发布、宿主 Profile、参考和接入测试，不能在本轮创建假样本。

## 契约与边界

- v1 开发轮次行为保持；v1 不接受正式 kind 或 v2 字段。v2 必须携带 Protocol，包含版本化 ID、split、每任务一次、计划设置摘要、实际后端、逐任务复核人，以及 protocol/dataset/implementation/environment 四份实际材料的路径与摘要。
- 计划设置摘要覆盖实际 Plan，唯独排除运行分配的 Campaign ID 和本次能力查询时间。模型能力响应原文摘要、模型/上界、任务顺序、输入/依赖、两级额度、Agent 限制、时限、并发及资源所有权策略均保留。能力响应变化也停止生成，不能只比模型别名。一次正式效果轮不授予恢复或第二次尝试。
- 四份材料是有界的宿主说明/身份清单，不直接放整个源码压缩包。固定字节不能证明说明内容真实、当前二进制与清单一致或人员实际签署；仍由可信宿主在完整冻结时核验。不能把这个局部契约叫作完整环境认证。
- CLI 在凭证/网络前检查并读取材料快照；准备屏障对实际计划再次校验，先发布材料副本，再发布 Enrollment/Manifest/Request，失败不得接纳 Target。参考正文与这些宿主材料均不进入 Agent 可见根。
- v2 汇总必须读取同一批次根目录中的生成前 Enrollment/Manifest 和材料副本，重新 Bind 实际计划，并与请求 Manifest 比较；事后仅更改请求清单无效。旧 v1 仍允许原开发校准路径。
- 复核结论必须来自预定 Reviewer；缺复核仍未知。正式 kind 拒绝 calibration 复核。身份绑定不等于人类真实性认证，不伪造复核或独立性。
- 校准 kind 可走同一 v2 链路，使用受控模型和已见样本验证机制。正式 kind 拒绝 controlled 模型。所有现有失败、未知分母、零放行 null 与成本规则复用原汇总，不增加一套指标。

## 实施与验收计划

1. 增加 v2 契约、确定性设置摘要及兼容性校验，接入 CLI 准备屏障。
2. 离线汇总验证生成前副本、材料与复核人，保持原指标实现。
3. 用开发夹具和本机模拟 API 验证完整链路、零生成拒绝、材料/计划/人员漂移、缺复核未知、串改旧清单拒绝；重算既有真实开发记录检查旧格式兼容。新机制结果不计真实效果。

本轮不读取 .env、不调用真实模型、不重新打包已有工程交付、不暂存或提交 Git。独立准备者、实际留出、未来实际复核安排、完整实现/环境与模型开销授权仍是正式启动依赖。

## 实际实施与设计修正

新增 [Protocol](../../scenarios/rpcx/l2/round/protocol.go)，在原 Enrollment/Manifest/Report 上添加可选契约。schema 1 保留开发类型，schema 2 必须带契约；formal_evaluation 不接受 controlled 模型。开发、校准和正式格式继续共用原计数和参考聚合，不另造一套指标或通用工作流引擎。

CLI 在配置加载时读取四份有界材料副本；准备屏障绑定实际计划与后端，发布材料→Enrollment→Manifest→Request。Summarize 对 v2 读取同一批次的发布副本并重建绑定，材料冲突或人员变化不发布指标；缺参考/复核保持未知。生成前核对当前参考发布，离线则核对归档参考材料，不要求历史工具等于今天的工具。实现/环境说明的真实性及与实际程序的对应仍须宿主审核，本轮没有形成完整冻结器或身份签名系统。

实施中发现并修正：

1. Plan 不包含镜像和容器资源限额。因此增加 `Protocol.Execution`，在准备屏障比较真实 `Executor.Info()`，汇总再比较已发布 execution.json；Docker 要求实际 sha256 镜像 ID。计划摘要覆盖 Plan 内设置，不能称其单独覆盖所有运行环境。
2. 首轮重叠负例在 macOS /var 与 /private/var 路径别名下漏判；统一规范化输出边界后通过。失败保存在 round-first.jsonl，不以修改断言规避。
3. 新 CLI 首轮完整消费失败：M4 已将 Campaign 记录发布为 schema 2，旧 round 只认 schema 1。增加两个明确合法组合：v1 无 writer 协议、v2 使用已知 writer 协议；未知协议或其他组合仍拒绝。这是终态格式兼容，不替代 M4 审计。
4. 默认 Go 缓存权限和 localhost 监听在初始沙箱受限；后续使用 runs 内缓存，并在允许本机监听的工具环境运行模拟服务。只用测试密钥，未读取 .env、调用真实模型或连接 Docker；失败日志保留。

## 验证结果与证据

所有本轮产物位于 `runs/evaluation-contract-01/`，不入 Git。测试和校准中的“正式”仅指受测格式，不是实际正式协议或人员签署。

| 验证 | 实际结果 | 证据 |
| --- | --- | --- |
| 汇总包与完整 CLI 竞态回归 | 2 包通过，159 项测试及子测试 pass、2 项可选测试 skip，无失败/竞态报告；跳过项是既有独立 Campaign 参考验收和 Docker CLI 实验，不计执行 | accepted-race.jsonl |
| v2 CLI → 真实基线/门禁 → 离线汇总 | 本机模拟 API 观察四份材料和三份清单在 POST 前存在，宿主协议/复核人未进入消息；一个原样未迁移候选交付 Rejected，参考未知；1 次模拟准入、25 个服务声明 Token，不计真实消费或成功迁移 | accepted-cli-artifacts 中带 protocol 的新 Campaign/round |
| 准入拒绝 | 配置材料漂移在网络/凭证前失败；计划预算、能力原文字节或实际后端变化在 Target 接纳前停止，POST 为 0；材料发布失败无 Request | accepted-race.jsonl 中 TestBatchProtocolDriftStopsBeforeGeneration / TestBatchProtocolMaterialPublicationFailure |
| 离线关联负例与未知 | 缺发布副本/材料、改材料、改请求或 Enrollment、换复核人/混校准复核均拒绝；缺实际复核仍为未知；合成 Ready/NeedsAttention/失败的 N 保持完整 | 同日志中 TestProtocolRoundRejectsRebindingAndPreservesUnknown / TestProtocolRoundConsumesPublishedSnapshots |
| 原三份真实开发汇总重算 | 原轮次、两份实际人员复核后轮次、助手补充轮次的 valid/execution_complete/counts/metrics/cost/targets 分别一致；原 Request 和报告字节不变 | legacy-recomputation-final.json，final-recomputed-* |
| 静态检查 | round 与 cmd/harness 的 go vet 通过 | vet.log |

原三份真实结果继续为 N=3、S=0、A=0、H=3，错误放行率 null；已知成本仍各自引用同一原 Campaign 的 81 次、1,060,890 Token，不能跨汇总累加。未重跑独立业务参考或完整 L1 矩阵，没有增加真实生成样本。新测试对协议身份和拒绝路径负责，不证明留出独立性、真实人签署或任意 Profile 适用性。

七份相关文档的 203 个本地链接、34 个锚点及代码围栏检查通过，git diff --check 通过。最终日志、两包源码范围摘要、三轮重算比较和原 v0.1.1 归档未变校验记录在 `runs/evaluation-contract-01/verification.json`。源码范围摘要不冒充全 Harness/环境冻结；Git 未暂存、未提交，HEAD 保持 d9b15b9。

## 完成判断与下一步

这一步已打通开发分区的显式协议纳入、生成前发布和离线消费，修正新旧 Campaign 终态兼容性；规则检查、未知处理和旧结果重算有证据。未覆盖的 hosted/heldout Profile 继续拒绝，不增加可跳过认证的插件参数。

下一步取决于真实材料：保留独立准备者/留出依赖，具备后按既定准入标准接入相应 Profile 和独立参考，再固定实际复核角色与完整实现、环境、模型和预算协议。现阶段不追加 Runtime 机制或付费批次，不将此单元计作 M3/M4 整体验收或 M5 正式启动。v0.1.1 旧工程包保持不变，新工作区实现需在后续另一个明确发布中固定身份。
