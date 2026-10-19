# rpcx L2 样本准备

当前完成的是样本设计与选择，尚无可执行 L2 夹具。详细取舍、裁剪范围、业务保真要求和准入标准见[工作记录](../../../docs/worklog/2026-10-19-m3-4-sample-selection.md)。

[选择清单 v0.1.0](selection-v0.1.0.json)固定三个开发来源及其 commit：

| 样本 | 来源 | 计划检验的差异 |
| --- | --- | --- |
| l2-goddd-booking | marcusolsson/goddd | 无 ctx 的多层接口、装饰器、请求上下文传播 |
| l2-clean-translation | evrone/go-clean-template v1.18.0 | 已有 ctx、生成 mock、两种必需构建配置 |
| l2-workouts-cancel | ThreeDotsLabs/wild-workouts-go-ddd-example | 回调、条件远程调用、两个客户端、本地 common 模块 |

三个来源均为示例/模板工程。计划保留上游业务切片和原测试，将既有出站边界植入本地 rpcx；30 秒默认超时、translation 的构建标签变体是评测准备添加的条件。它们将提供半真实开发证据，不代表原项目自然依赖 rpcx 或生产迁移效果。

留出名额 l2-heldout-01 为 `awaiting_independent_preparer`：尚无来源、摘要或参考标准，不计入已准备数量和效果分母。独立准备者暂缺；所有已查阅来源记录在 surveyed_sources，不得静默改称严格未见留出。

## 清单边界

- `selected_for_preparation` 表示选定来源，**不表示构建测试已通过**。`prepared_dataset` 与 `runtime_profile_id` 为空是有意保留的未完成状态。
- `reviewed_files` 是实际读取的来源文件及摘要，用来复核选择依据；不是完整源快照，也不是裁剪后的基线摘要。可以从其中固定 commit 的公开 URL 重新获取并校验。
- `planned_build_configurations` 是准备要求，不是当前 Runner 已支持的配置。translation 两配置是同一仓库任务，只计一个样本。
- 这份文件不由 Harness 读取，不是运行清单；它及后续 oracle/预期不得加入 Agent 的可见仓库根。运行时仅接收认证的任务输入、SDK、规范与宿主选择的验证材料。
- 清单提交后保留选择版本；准备证据另存。换来源或改划分时新增选择版本并解释原因，不按迁移成功与否覆盖原选择。

下一步先准备 Goddd 的离线 v1 基线与共用 L2 SDK，记录上游到切片的逐文件映射、原测试保留情况及依赖材料。基线验收和数据冻结后，再制作候选对照、注册新宿主 Profile；旧 T3 数据及两个 demo-orders Profile 保持原身份。
