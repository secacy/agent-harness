# rpcx L2 样本准备

已完成样本选择和 [Goddd 迁移前离线基线](data/rpcx-l2-goddd-v1/README.md)，另已完成 [Translation 双配置离线基线](data/rpcx-l2-translation-v1/README.md)，Workouts 待准备。Goddd 的可信本地与断网 Docker 复验均通过，已新增 [Goddd 迁移规范、宿主 Profile 与证据门禁](../goddd/README.md)。来源取舍见[样本选择记录](../../../docs/worklog/2026-10-19-m3-4-sample-selection.md)，实际准备与验证见[基线工作记录](../../../docs/worklog/2026-10-19-goddd-baseline.md)。

[选择清单 v0.1.0](selection-v0.1.0.json)固定三个开发来源及其 commit：

| 样本 | 来源 | 计划检验的差异 |
| --- | --- | --- |
| l2-goddd-booking | marcusolsson/goddd | 无 ctx 的多层接口、装饰器、请求上下文传播 |
| l2-clean-translation | evrone/go-clean-template v1.18.0 | 已有 ctx、生成 mock、两种必需构建配置 |
| l2-workouts-cancel | ThreeDotsLabs/wild-workouts-go-ddd-example | 回调、条件远程调用、两个客户端、本地 common 模块 |

三个来源均为示例/模板工程。计划保留上游业务切片和原测试，将既有出站边界植入本地 rpcx；30 秒默认超时、translation 的构建标签变体是评测准备添加的条件。它们将提供半真实开发证据，不代表原项目自然依赖 rpcx 或生产迁移效果。

留出名额 l2-heldout-01 为 `awaiting_independent_preparer`：尚无来源、摘要或参考标准，不计入已准备数量和效果分母。独立准备者暂缺；所有已查阅来源记录在 surveyed_sources，不得静默改称严格未见留出。

## 清单边界

- `selected_for_preparation` 表示当时选定来源，**不表示构建测试已通过**。旧选择清单的 `prepared_dataset` 与 `runtime_profile_id` 保留为空；后续准备结果另存，不回写历史选择。
- `reviewed_files` 是实际读取的来源文件及摘要，用来复核选择依据；不是完整源快照，也不是裁剪后的基线摘要。可以从其中固定 commit 的公开 URL 重新获取并校验。
- `planned_build_configurations` 是准备要求，不是当前 Runner 已支持的配置。translation 两配置是同一仓库任务，只计一个样本。
- 这份文件不由 Harness 读取，不是运行清单；它及后续 oracle/预期不得加入 Agent 的可见仓库根。运行时仅接收认证的任务输入、SDK、规范与宿主选择的验证材料。
- 清单提交后保留选择版本；准备证据另存。换来源或改划分时新增选择版本并解释原因，不按迁移成功与否覆盖原选择。

Goddd 数据版本为 rpcx-l2-goddd-v1，包含共用 SDK 契约 rpcx-l2-sdk-v1、15 个原业务测试、离线依赖及来源映射。复验入口为 `go run ./scenarios/rpcx/l2/cmd/verify-baseline`，默认使用已有固定 Docker 镜像；显式 `-trusted-local` 运行可信本地夹具。工具核对 37 项测试及子测试、包终态和执行前后材料；不调用模型。

Goddd 已接入宿主 Profile 与确定性 Runner，正确/违规/未知候选的校准不计作真实模型迁移效果；实现与证据见 [Goddd 工作记录](../../../docs/worklog/2026-10-19-goddd-evidence-gate.md)。Translation 已保留原翻译测试、生成 mock 与 tracing，两个互斥 RPC 实现均通过离线基线复验；它还没有迁移 Profile 或候选质量门禁，见 [工作记录](../../../docs/worklog/2026-10-19-translation-baseline.md)。后续先接入双配置的迁移验证，再准备 Workouts；独立参考评测与真实批次实验仍待完成。


Translation 使用 `go run ./scenarios/rpcx/l2/cmd/verify-baseline -dataset rpcx-l2-translation-v1`，默认 Docker，可信本地加 `-trusted-local`。每配置执行 26 项测试及子测试（15 项来自原测试、11 项为新增边界检查），SDK 共 15 项；67 是含配置身份的执行计数，两个配置仍只计一个样本。go list 另核对活动/忽略的 RPC 源文件和原 mock 是否参与构建。夹具需要 Go 1.26.0 或更新版本，工具不会自动下载新工具链。

版本选择由宿主静态注册，并认证 freeze.json 的发布摘要；不能原地重算清单冒充同一数据版本。Goddd 默认入口与旧发布材料保持不变，复验工具的配置矩阵能力不等于 Runner 已支持 Translation。
