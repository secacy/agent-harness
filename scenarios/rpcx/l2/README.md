# rpcx L2 样本准备

已完成样本选择和 [Goddd 迁移前离线基线](data/rpcx-l2-goddd-v1/README.md)，另已完成 [Translation 双配置离线基线](data/rpcx-l2-translation-v1/README.md)和 [Workouts 回调与多模块离线基线](data/rpcx-l2-workouts-v1/README.md)。Goddd 的可信本地与断网 Docker 复验均通过，已接入 [Goddd](../goddd/README.md) 与 [Translation 双配置门禁](../translation/README.md)及宿主 Profile。来源取舍见[样本选择记录](../../../docs/worklog/2026-10-19-m3-4-sample-selection.md)，实际准备与验证见[基线工作记录](../../../docs/worklog/2026-10-19-goddd-baseline.md)。

[选择清单 v0.1.0](selection-v0.1.0.json)固定三个开发来源及其 commit：

| 样本 | 来源 | 计划检验的差异 |
| --- | --- | --- |
| l2-goddd-booking | marcusolsson/goddd | 无 ctx 的多层接口、装饰器、请求上下文传播 |
| l2-clean-translation | evrone/go-clean-template v1.18.0 | 已有 ctx、生成 mock、两种必需构建配置 |
| l2-workouts-cancel | ThreeDotsLabs/wild-workouts-go-ddd-example | 回调、条件远程调用、两个客户端、本地 common 模块 |

三个来源均为示例/模板工程。已保留上游业务切片和原测试，将既有出站边界植入本地 rpcx；30 秒默认超时、translation 的构建标签变体是评测准备添加的条件。它们提供半真实开发证据，不代表原项目自然依赖 rpcx 或生产迁移效果。

原独立人员口径的留出名额 l2-heldout-01 为 `awaiting_independent_preparer`：尚无来源、摘要或参考标准，不计入已准备数量和效果分母。该人员安排尚未登记；所有已查阅来源记录在 surveyed_sources，不得静默改称严格未见留出。

后续独立准备按 [M5 交接流程](../../../docs/experiments/m5-evaluation-draft.md#独立准备与接入交接)先提供允许的元数据，再约定完整材料保管及宿主接入；当前没有可运行的 heldout Profile。若开发调优侧为新增 Profile 而打开样本/答案，须记录暴露并调整其用途，不能仅凭换一个来源名称保留未见声明。

用户随后提出使用全新 Agent，现另行开展 [Agent 隔离准备试点](../../../docs/worklog/2026-10-21-agent-isolated-preparation.md)。准备者不继承主调优对话，仅回报允许元数据；完整新材料不进入现有开发夹具或主 Agent 上下文。试点单列 fresh_agent_preparation，不改原选择清单或冒充独立人员保管，也不自动计入可运行 L2 数量。

[首阶段回执](agent-preparation-v0.1.0.json)记录一个库级协议候选的 prepared_baseline：准备者禁网构建及所选 54 项测试/子测试通过，另保留一个未执行的外部服务测试套件。主流程只核对公开元数据，没有读取完整材料或重跑。应用业务代表性、独立参考与宿主 Profile 仍待接入，不能把基线状态当作 L2 准入或迁移成功。

[首轮开发评测工作](../../../docs/worklog/2026-10-19-l2-development-evaluation.md)采用三个 Target 各一次尝试，独立行为参考结合源码/交付复核；参考未知与确认错误分列。[三场景单候选参考](evaluation/README.md)及[真实批次](../../../docs/experiments/2026-10-20-rpcx-l2-real-model-01.md)已完成：三项原判定均为 NeedsAttention，自动参考均 pass，S/N=0/3、A=0，错误放行率无定义。Goddd、Translation 的实际人员复核与 Workouts 的助手补充已分列，见[阶段收尾](../../../docs/worklog/2026-10-20-m3-closeout-review.md)。[轮次汇总](round/README.md)现支持 v2 协议绑定及一次尝试的身份关联、未知区间和成本分列；独立留出与完整正式评测仍待准备。

## 清单边界

- `selected_for_preparation` 表示当时选定来源，**不表示构建测试已通过**。旧选择清单的 `prepared_dataset` 与 `runtime_profile_id` 保留为空；后续准备结果另存，不回写历史选择。
- `reviewed_files` 是实际读取的来源文件及摘要，用来复核选择依据；不是完整源快照，也不是裁剪后的基线摘要。可以从其中固定 commit 的公开 URL 重新获取并校验。
- `planned_build_configurations` 是准备要求，不是当前 Runner 已支持的配置。translation 两配置是同一仓库任务，只计一个样本。
- 这份文件不由 Harness 读取，不是运行清单；它及后续 oracle/预期不得加入 Agent 的可见仓库根。运行时仅接收认证的任务输入、SDK、规范与宿主选择的验证材料。
- 清单提交后保留选择版本；准备证据另存。换来源或改划分时新增选择版本并解释原因，不按迁移成功与否覆盖原选择。

Goddd 数据版本为 rpcx-l2-goddd-v1，包含共用 SDK 契约 rpcx-l2-sdk-v1、15 个原业务测试、离线依赖及来源映射。复验入口为 `go run ./scenarios/rpcx/l2/cmd/verify-baseline`，默认使用已有固定 Docker 镜像；显式 `-trusted-local` 运行可信本地夹具。工具核对 37 项测试及子测试、包终态和执行前后材料；不调用模型。

Goddd 已接入宿主 Profile 与确定性 Runner，正确/违规/未知候选的校准不计作真实模型迁移效果；实现与证据见 [Goddd 工作记录](../../../docs/worklog/2026-10-19-goddd-evidence-gate.md)。Translation 已保留原翻译测试、生成 mock 与 tracing，两个互斥 RPC 实现均通过离线基线复验；现已接入[双配置迁移 Profile、门禁和 Runner](../../../docs/worklog/2026-10-19-translation-evidence-gate.md)。[Workouts 基线](../../../docs/worklog/2026-10-19-workouts-baseline.md)也已通过两种后端复验，其[迁移 Profile/门禁和确定性 Docker Runner](../workouts/README.md)已接入；真实开发批次与独立自动参考完成，整体正式评测状态见上文。


Translation 使用 `go run ./scenarios/rpcx/l2/cmd/verify-baseline -dataset rpcx-l2-translation-v1`，默认 Docker，可信本地加 `-trusted-local`。每配置执行 26 项测试及子测试（15 项来自原测试、11 项为新增边界检查），SDK 共 15 项；67 是含配置身份的执行计数，两个配置仍只计一个样本。go list 另核对活动/忽略的 RPC 源文件和原 mock 是否参与构建。夹具需要 Go 1.26.0 或更新版本，工具不会自动下载新工具链。

版本选择由宿主静态注册，并认证 freeze.json 的发布摘要；不能原地重算清单冒充同一数据版本。Goddd 默认入口与旧发布材料保持不变，Translation Runner 通过另行发布的宿主 Profile 固定两种必需配置，配置执行不由选择清单推导。

Workouts 使用 `go run ./scenarios/rpcx/l2/cmd/verify-baseline -dataset rpcx-l2-workouts-v1`，默认 Docker，可信本地加 `-trusted-local`。保留 Go 1.25.0 与 trainings/common 本地模块关系；四个模块分别构建/测试，12 条命令、55 项测试/子测试。26 项来自原取消 command 和完整 training 领域；11 项取消/RPC 测试、3 项 common 装饰器测试及 SDK 的 15 项另列。两模块仍只计一个样本。原回调与原断言不改，Schedule/Move、云端组件和真实 Firestore 提交语义明确不在范围；基线复验不是模型迁移效果，其真实开发实验与后续开发校准另行保留。
