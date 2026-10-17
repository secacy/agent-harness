# rpcx 真实模型迁移实验 03：模型调用预算对照

- 日期：2026-10-18
- 结果：原 Run 为 Completed，独立门禁为 ReadyForReview；补丁功能审核与还原检查通过，留有一项格式提示。
- 代码提交：`10b8fe0d066368f42024ff97b5bba5cd2174daeb`
- 运行代码与实验 02 相同，复用相同摘要的二进制；预算对照设计与实验结果一并提交。
- 原 Run：`rpcx-agent-991311719`
- 实验目录：`runs/rpcx-real-03-6bjv9ge2/`（Git 忽略）

## 目标与固定条件

依据[预算对照设计](../worklog/2026-10-18-budget-awareness.md#下一步设计模型调用预算对照)，从原始基线运行一次，检验较宽模型调用预算下能否正常提交并交付质量合格的候选。

相对[实验 02](2026-10-18-rpcx-real-model-02.md)，唯一主动调整的配置是模型调用上限从 20 变为 40。工具调用上限仍为 80，单次最多输出 4096 Token，整体超时 10 分钟。沿用 deepseek-flash、官方 API、thinking disabled、非流式请求，原始 demo-orders 基线、v0.1.0 规范、SDK、现有任务提示和工具、独立 Evaluate。模型不接收实验 02 的候选与诊断。

使用相同 Docker 29.8.2 / Linux arm64，固定镜像 `golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd`，资源为 2 CPU、2048 MiB 内存、128 进程，禁网、只读源码与 SDK、临时缓存。启动前核对夹具、规范和二进制摘要。

本次只运行一次，不自动重试或继续增加预算。原 Run 的正常提交、Completed 状态、ReadyForReview 判定，以及补丁人工审核和还原一致性共同构成交付证据。正常结束或事后诊断通过均不能单独替代这些条件。

## 解释边界

剩余预算通知会随着配置变化，临近上限的收尾提示从第 38 次开始；本次是新轨迹，不能假设它复现实验 02 的前 20 轮。真实服务模型快照未固定，未显式指定 temperature/seed，单次结果只能提供该条件下的证据，不能证明因果关系、最优预算或总体成功率。

若 20 次以内完成，不能声称实际使用额外修复机会；若 21–40 次内完成，可以记录较宽预算下完成的事实，仍需结合成本与重复性判断。候选被拒绝时分析质量，耗尽预算时分析轨迹，基础设施错误按实际原因分类。

## 执行与结果

启动时间为台北时间（UTC+8）2026-10-18 00:35:50，原始时间以 UTC 保存在 `experiment.json`。启动前确认生产代码与实验 02 一致，复用二进制的 SHA-256 为 `fc6731f02cd56a20992418fc4ad1714555b7a0c64cf7b8aa05f686379a2139d3`。实际命令和固定镜像、资源参数保存在同一文件，以下为等价调用：

```sh
./runs/harness-experiment-03 \
  --model deepseek-flash \
  --endpoint https://api.deepseek.com/chat/completions \
  --runs runs/rpcx-real-03-6bjv9ge2/tasks \
  --max-model-calls 40 --max-tool-calls 80 \
  --max-tokens 4096 --timeout 10m
```

密钥由启动器作为数据读取 `.env` 中的指定变量，只传入客户端进程；没有执行文件内容或将密钥写入报告。Harness 入口仍不自动加载 `.env`。本次实际运行一次，未人工修改候选、续跑失败任务或追加模型请求。

| 项目 | 实测 |
| --- | --- |
| 进程退出码 | 0 |
| Run 状态 / 最终阶段 | Completed / Publishing |
| Agent 退出原因 | completed，第 31 次调用正常结束 |
| 模型 / 工具调用 | 31 / 54 |
| 工具错误结果 | 1 次：第 20 轮测试失败，模型后续修复 |
| 外层耗时 | 126.809 秒，含预检、Agent 检查、原 Run 独立验证与收尾 |
| 原 Run 独立验证 | 六项检查均通过，ReadyForReview |

工具分布：list_files 11、read_file 15、search 4、replace_text 19、go_check 5。31 次请求均返回 usage：输入 333481、输出 9216、合计 342697 Token，缓存命中 322176、未命中 11305，未知用量调用为 0。

累计输入包含历史重复发送，不是单次上下文长度；总 Token 约为实验 02 的 2.34 倍，不能直接换算为货币成本倍数。本次未估算实际费用。

## 实际轨迹与修复

| 模型轮次 | 实际行为 |
| --- | --- |
| 1–9 | 读取 SDK、业务代码与测试、规范，查找依赖与调用点 |
| 10–15 | 适配 go.mod、inventory、service、handler 及原有测试，同时增加部分 context 测试 |
| 16–19 | 增加 HTTP context 测试，发现并修正一次文本替换造成的函数声明缺失 |
| 20 | 同轮运行 build 和 test：构建通过，新增 HTTP 测试因错误的 deadline 假设失败 |
| 21–26 | 根据错误修改测试，读取受影响代码修正不完整替换，增加真正携带 deadline 的专项测试；最后一次编辑在第 26 轮 |
| 27 | 所有业务测试通过 |
| 28–29 | 搜索旧导入、Background 使用位置并复查最终代码 |
| 30 | 再次 build 和 test，均通过 |
| 31 | 正常提交候选，随后由原 Run 调用独立 Evaluate 并发布产物 |

第 20 轮失败是模型新增的 `TestPlaceRequestContextPropagation` 检查了一个没有 deadline 的 context。模型保留值和取消传播断言，并使用 WithTimeout 增加独立 deadline 测试；没有删除原始测试断言。第 27 和 30 轮测试均覆盖最后一次修改后的文件。

31 条预算通知逐次核对正确。提交前通知剩余 10 次模型调用，因此没有触发剩余 3 次时的软提醒。本次支持“在 40 次上限内可以使用额外轮次处理失败反馈并提交”这一事实，不能证明临界收尾提示促成了成功，也不能推断在 20 次上限下必然复现相同轨迹。

## 独立验证与补丁审核

以下检查在原 Run 中自动完成，无需事后重新诊断：

| 检查 | 结果 |
| --- | --- |
| dependencies | v2 依赖、替换及导入通过 |
| protected_paths | internal/pb 文件集合与摘要未变 |
| build | 候选所有包构建通过 |
| baseline_tests | 候选 20 个业务测试全部通过 |
| test_preservation | 原有 10 个测试均保留且执行通过 |
| context_propagation | 两条请求链的 metadata、deadline、cancellation 六个可信探针用例通过 |

所有独立命令都记录同一固定 Docker 后端与各自容器 ID。原 Run 的 `run.json`、报告和验证记录中的候选摘要一致。

补丁修改 7 个文件：go.mod、inventory/client.go、orders/service.go、server/handler.go，以及三个测试文件。人工差异审核确认：

- NewClient 改为 Dial，依赖与导入升级到 rpcx/v2；Reserve/Release 接收 context 并传到 RPC。
- Place 沿用已有 context，Cancel 新增 context 参数，HTTP Cancel 使用 r.Context()；请求链路没有 Background/TODO 兜底。
- 原有参数检查、错误包装、锁和状态更新顺序保持；RPC 失败时不提交订单状态变化。
- 原有测试断言保留或增加检查，新增 10 个测试：库存层 2 个、服务层 3 个、HTTP 层 5 个。
- Deadline 专项测试实际使用 WithTimeout 并比较期限；预取消测试检查错误或 HTTP 状态及订单状态，正确考虑 SDK 在进入注册 handler 前检查取消。

审核仍有两项范围说明：部分新增测试只在回调内断言，没有各自设置“回调已执行”标志，不能把这些单项测试视为完整覆盖证明；应结合原有调用断言、其他状态断言及可信探针理解其证据。`cmd/server/handler_test.go` 有一处函数声明与局部类型处于同一行，`gofmt -l` 将其列出，属于格式提示，不影响当前规范验收；作为候选审核事项保留，未人工修改原产物。

将导出补丁应用到干净基线后，文件树摘要与原候选相同：

- 候选与补丁还原摘要：`a6bdde87e23bdad4c4fa58ff4fa00d89522e06f4be48b2c27b304050fcbca23e`
- 补丁摘要：`96c6a7496d6e55a7f3c3d93838b41665f1180d6eedea373da383d0018e0c6b36`

原始夹具、规范、实验 01/02 关键证据均保持不变；实验结束后没有遗留 Harness 容器，实验 JSON、日志和补丁未检出本次 API Key。

## 比较与结论

| 指标 | 实验 02 | 实验 03 |
| --- | --- | --- |
| 模型调用上限 | 20 | 40 |
| 实际模型 / 工具调用 | 20 / 40 | 31 / 54 |
| 原 Run | Failed，无质量判定 | Completed，ReadyForReview |
| 失败测试后的处理 | 最后一轮失败，没有后续额度 | 第 20 轮失败，后续修复并再次验证 |
| 候选业务测试 | 事后诊断 11 通过、1 失败 | 原 Run 20 个全部通过 |
| 最终提交 | 无 | 第 31 轮正常提交 |
| 总 Token | 146531 | 342697 |
| 外层耗时 | 52.156 秒 | 126.809 秒 |

本次达到了预定单仓库交付条件：真实模型自主修改与处理测试反馈、预算内正常提交、独立门禁通过、补丁功能审核与还原一致。M1 最小闭环在受控 rpcx T1/T2 场景下完成一次真实运行验收，格式提示供候选维护者审核。

这不是总体成功率、最优预算或跨仓库可靠性的证明；没有真实业务上线证据，也未覆盖隐式超时迁移、崩溃恢复或批量调度。40 保留为本次实验配置，不据单次成功修改默认值，也不将前两次失败改计为成功。后续可进入批量 Runtime 的设计，并在更多独立任务中记录质量和成本。

## 证据位置

均相对于 `runs/rpcx-real-03-6bjv9ge2/`，启动器除外：

- `experiment.json`、`console.log`：固定条件、实际命令、二进制摘要、退出和耗时。
- `tasks/rpcx-agent-991311719/run.json`、同目录的 `inputs/task.json`：终态与输入来源。
- 同一任务目录的 `artifacts/agent.json`、`artifacts/report.json`、`artifacts/candidate.patch`：完整对话、原 Run 的质量报告与实际补丁。
- 同一任务目录的 `evidence/`：基线、构建、业务测试和可信探针日志。
- `analysis.json`、`patch-replay/`：预算核对、用量、审核提示及补丁还原。
- 启动器为根目录下的 `runs/launch-rpcx-experiment-03.py`，作为数据解析必要环境变量，不执行 `.env`。
