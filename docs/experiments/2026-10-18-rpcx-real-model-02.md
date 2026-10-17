# rpcx 真实模型迁移实验 02

- 日期：2026-10-18
- 结果：调用预算耗尽，原 Run 为 Failed；事后独立诊断为 Rejected。
- 代码提交：`9955dc88eeaa44087ce6a0778fc39ec13236c2ab`
- 原 Run：`rpcx-agent-3876253587`
- 实验目录：`runs/rpcx-real-02-_1expfbb/`（Git 忽略）

## 目标与判据

在[实验 01](2026-10-17-rpcx-real-model-01.md) 的相同调用预算和场景条件下，检验加入[预算感知与收尾提示](../worklog/2026-10-18-budget-awareness.md) 后，模型能否正常提交候选，由原 Run 的独立门禁验收并完整交付。

预先约定只执行一次，不改变提示或增加预算后重跑覆盖结果。完成状态与质量判定分别记录：正常提交并交付不代表迁移正确；只有预算内正常提交、Run 为 Completed、门禁为 ReadyForReview，且实际补丁审核和还原检查无异常，才作为本次 rpcx T1/T2 单仓库闭环跑通的证据。候选拒绝、预算耗尽或运行失败都保留原始结果。

## 固定条件与变化

固定 deepseek-flash、官方 chat completions API、非流式且 thinking disabled、每次最多 4096 输出 Token、20 次模型调用、80 次工具调用、整体 10 分钟。使用原始 demo-orders 基线、v0.1.0 规范与相同本地 Go 镜像，Docker 资源为 2 CPU、2048 MiB 内存和 128 进程，禁网、只读源码挂载和临时缓存。

镜像为 `golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd`；规范摘要为 `330e439ab69a9c84dbb88343ef5ccc430eeec357bb3f54e01dcad83ce860e251`。启动前核对夹具文件摘要与实验 01 一致。

本次变化仅为预算通知与任务收尾提示的组合，未显式设置 temperature/seed，真实服务模型快照仍未固定。比较只能观察两次运行的差异，不能证明改进因果关系或总体成功率提高。

密钥由实验启动器作为数据读取本地 `.env`，只传入模型客户端进程，不执行文件中的命令，不写入报告或模型输入。源码中的 Harness 入口仍不自动加载 `.env`。原始执行与分析材料保存在 Git 忽略的独立 `runs/` 目录中。

## 实际执行

启动前已构建实验二进制，核对夹具文件摘要及规范与实验 01 一致，并确认本地 Docker 29.8.2 / Linux arm64、固定镜像和无遗留 Harness 容器。预检记录位于 `runs/rpcx-real-02-preflight.json`。自动审批曾在命令执行前要求明确外部传输授权；获得用户明确授权后启动一次实验，前次拦截没有发起模型调用，不计为模型失败样本。

运行起始为台北时间（UTC+8）2026-10-18 00:24:56，原始时间以 UTC 保存在 `experiment.json`。提前构建二进制后执行以下等价命令；实际绝对路径、镜像和资源参数同样保存在该文件：

```sh
./runs/harness-experiment-02 \
  --model deepseek-flash \
  --endpoint https://api.deepseek.com/chat/completions \
  --runs runs/rpcx-real-02-_1expfbb/tasks \
  --max-model-calls 20 --max-tool-calls 80 \
  --max-tokens 4096 --timeout 10m
```

## 原始结果

| 项目 | 实测 |
| --- | --- |
| 进程退出码 | 1 |
| Run 状态 / 阶段 | Failed / Generating |
| Agent 退出原因 | limit_reached（模型调用预算耗尽） |
| 模型 / 工具调用 | 20 / 40 |
| 工具错误结果 | 1 次：最后一轮 go_check test 返回测试失败 |
| 外层耗时 | 52.156 秒，含预检和收尾，不含提前构建二进制与事后诊断 |
| 原 Run 质量判定 / 独立验证 | 空 / 未执行，report.json 的 validation 为空 |

20 次调用均返回 usage：输入 142385、输出 4146、合计 146531 Token，缓存命中 132992、未命中 9393，未知用量调用为 0。累计输入包含每轮历史重传，不等于单次上下文大小；未推算货币费用。

工具次数：list_files 7、read_file 12、search 2、replace_text 16、go_check 3。没有 API 或容器基础设施错误；最后一次测试失败属于可反馈给模型的工具结果，但没有后续额度供模型处理。

## 轨迹与预算通知

| 模型轮次 | 实际行为 |
| --- | --- |
| 1–10 | 读取规范、SDK、业务代码及测试，搜索引用和函数；workspace 根目录被列举三次 |
| 11–15 | 迁移依赖、库存封装、服务、HTTP handler 及原有测试 |
| 16 | go build 通过 |
| 17 | go test 通过，此时原有 10 个测试已经完成适配 |
| 18–19 | 继续新增两个 HTTP context 测试，未提交 |
| 20 | go test 失败，随后预算耗尽，无正常提交 |

完整记录中有 20 条宿主预算通知，逐次核对剩余模型与工具次数均正确。第 18 次调用前通知剩余 3 次并提示收尾；第 19 次提示剩余 2 次；第 20 次明确“最后一次模型调用，执行工具后没有后续轮次”。模型仍在第 18 次回复表示要增加 context 专项测试，并在最后一次请求执行测试。

预算信息已提供，但本次没有观察到模型及时结束。不能将失败解释为通知缺失，也不能从两次运行断言模型完全不使用预算信息。

## 补丁审核与事后诊断

补丁修改 7 个文件：go.mod、inventory/client.go、orders/service.go、server/handler.go 及三个受影响测试文件。实际差异检查确认：

- 依赖、导入与构造函数适配到 rpcx/v2、v2.0.0 与 Dial。
- Place 与 Cancel 两条请求链传递已有 context，Cancel 从 handler 的 r.Context() 传到 service、inventory 和 RPC。
- 原有参数检查、错误包装、锁、订单状态更新顺序及测试断言保留；原有 10 个测试名完整，新增 2 个测试。
- internal/pb、原始夹具及规范保持不变。

新增测试有具体问题：

1. `TestCancelContextPropagation` 的注册 RPC handler 要求所有调用携带自定义 context 值，但创建测试订单时调用 `Place(context.Background(), ...)`。初始化 Reserve 就返回 `request values not propagated`，测试在调用 Cancel 之前失败。
2. `TestPlaceContextPropagation` 先取消请求再调用 Place。rpcx/v2 在进入注册 handler 前检查 ctx.Err()，所以 handler 内的值和 deadline 断言没有执行；测试通过只能支持取消后没有创建订单，不能证明其声称的 context 值与 deadline 传播。另外它使用 WithCancel，本身也没有增加 deadline。
3. Cancel 测试的 `called` 标志没有区分创建订单的 Reserve 与取消订单的 Release；即使修正初始化问题，也需要重新检查该断言是否真正覆盖取消路径。

这些是模型生成测试的问题，不能通过删除测试或把第 17 轮旧快照当作最终候选来掩盖。本次没有修改保留候选，也没有继续调用模型。

实验结束后对原样候选另行调用相同 Evaluate，仍使用 Docker，证据保存在 `diagnostic/`。该诊断不修改原 Run 或原报告：

| 独立检查 | 结果 |
| --- | --- |
| dependencies、protected_paths、build | 通过 |
| test_preservation | 原有 10 个测试均执行通过 |
| context_propagation | 通过，可信探针覆盖两条链的 metadata、deadline 和 cancellation |
| baseline_tests（候选全量业务测试） | 失败，12 个测试中 11 个通过、1 个失败 |
| 事后质量判定 | Rejected |

原 Run 保持 Failed、无最终质量判定；事后 Rejected 是补充诊断，不能写成原 Run 已完成门禁，也不能计为自动迁移成功。

补丁应用到干净基线后与保留候选摘要一致：

- 候选与补丁还原摘要：`6a47157be779e384202996941ca93ab138f12085edef207c3b6d628b68aac40b`
- 补丁摘要：`c58bbc41f3443ecb3f345ce82023dc55c06b3033a323420c931afca797f5cdac`

诊断后再次核对原 Run 产物和实验 01 关键证据未变；未发现遗留 Harness 容器，实验 JSON、日志与补丁未检出 API Key。

## 与实验 01 的比较及结论

| 指标 | 实验 01 | 实验 02 |
| --- | --- | --- |
| 原 Run | Failed / limit_reached | Failed / limit_reached |
| 模型 / 工具调用 | 20 / 47 | 20 / 40 |
| 预算通知 | 无 | 20 条，次数核对正确 |
| 构建、原有测试通过轮次 | 16 / 17 | 16 / 17 |
| 末尾行为 | 新增测试后未再次检查、未提交 | 新增测试后检查失败、未提交 |
| 最终候选事后诊断 | ReadyForReview | Rejected |
| 总 Token | 153807 | 146531 |
| 外层耗时 | 59.137 秒 | 52.156 秒 |

本次预算通知及最后一轮提示按设计进入对话，仍未得到预算内正常提交。不能据调用、Token 或耗时下降宣称迁移效率提高；最终候选质量也没有达到交付要求。M1 继续保持部分实现，真实模型闭环未验收。

后续需要重新权衡预算安排：增加预算可给失败反馈留下修复机会，但增加成本；强制预留总结轮可以获得提交，却可能交付被拒绝的候选；更早提示收尾或减少重复探索可能节省轮次，但均需独立检验。先明确关注预算内完成还是固定成本下的质量，再选择一个条件复测，不直接叠加多个改动，也不自动重试到成功。

## 证据位置

除预检和启动器外，路径均相对于 `runs/rpcx-real-02-_1expfbb/`：

- `experiment.json`、`console.log`：条件、实际命令、退出与耗时。
- `tasks/rpcx-agent-3876253587/run.json`、`artifacts/report.json`、`artifacts/agent.json`、`artifacts/candidate.patch`（后三者位于同一任务目录）：原始状态、报告、20 轮对话、用量及补丁。
- `diagnostic/report.json`、`diagnostic/evidence/`：事后独立判定与命令日志。
- `analysis.json`、`patch-replay/`：预算逐次核对、统计、审核结论、摘要与补丁还原。
- `runs/rpcx-real-02-preflight.json`、`runs/launch-rpcx-experiment-02.py`：启动前预检与仅解析必要环境变量的实验启动器。
