# 单仓库 Runner：输入、状态与产物契约

- 日期：2026-10-17
- 状态：串行 Runner、任务入口、状态保存与产物交付已通过离线联调
- 对应里程碑：[M1](../MILESTONES.md)
- 验收范围：可控替身模型和本地模拟 API 驱动真实工具与独立验证器；Docker 后端已通过真实容器验收，见 [执行记录](2026-10-17-docker-execution.md)。后续 [实验 03](../experiments/2026-10-18-rpcx-real-model-03.md) 完成受控 rpcx T1/T2 场景的一次真实模型迁移和完整交付，不代表跨仓库稳定性已经验证。

## 目标与职责

`scenarios/rpcx/runner.Run` 串联准备、生成、验证和交付。初期曾考虑以预制补丁为候选来源；实际实现直接接入 `agent.Loop`，公开参考补丁仅用于测试构造已知期望，不成为 Runner 输入。

```text
cmd/harness：解析参数与环境变量，创建 DeepSeek 客户端
    ↓ 注入 agent.Model
runner.Run：准备目录 → Loop.Run → 固定候选 → Evaluate → 导出补丁和报告
                         ↓
                    repotools.Executor
```

Runner 接收 `agent.Model`，不导入 `deepseek`。当前 Runner 负责 rpcx 的相邻 SDK 布局并调用专用验证器，因此放在场景目录；待第二个场景出现后，再根据实际重复内容提取公共执行管理。

## 输入与可见范围

库入口为 `Run(ctx context.Context, config Config) (Record, error)`：

```go
record, err := runner.Run(ctx, runner.Config{
    ScenarioDir: "scenarios/rpcx",
    RunsDir: "runs",
    Model: model,
    ModelName: modelName,
    Limits: agent.Limits{MaxModelCalls: 20, MaxToolCalls: 80},
    Timeout: 5 * time.Minute,
    TrustedLocal: true,
})
```

相对路径以调用进程的工作目录为基准。规范固定为 `specs/v0.1.0.md`，按验证器支持的 SHA-256 检查内容；不支持任意规范或任意源仓库。预算必须为正数，运行输出不能与场景资源重叠，已有祖先的符号链接也参与路径解析。

每次只读取三个已知 fixture 模块及规范。复制时拒绝符号链接、特殊文件和 `.git`，单文件至多 1 MiB，单次读取的目录树至多 10000 个文件、16 MiB。副本文件统一使用 0644；摘要绑定文件路径和内容，当前不描述可执行位或空目录。

模型可见的工具根目录只有：

| 别名 | 内容 | 权限 |
| --- | --- | --- |
| workspace | 本次 demo-orders 副本 | 可读写已有文件，保护 internal/pb |
| rpcx-v1、rpcx-v2 | 本次 SDK 副本 | 只读 |
| spec | 仅包含冻结规范的目录 | 只读 |

规范同时进入初始用户消息。cases、参考补丁、oracle、验证探针、任务元数据和历史运行均不通过工具暴露给模型。模型结束后不再获得验证器探针或门禁结果；首版没有基于最终门禁反馈的再生成循环。

## 状态与判定

```text
Running → Completed / Failed / Cancelled
阶段：Preparing → Generating → Validating → Publishing
```

| 情况 | 运行状态 | 最终质量判定 |
| --- | --- | --- |
| 候选正常提交并完整交付 | Completed | Evaluate 返回的 ReadyForReview、Rejected 或 NeedsAttention |
| 模型错误、输出截断、调用预算耗尽 | Failed | 空；不开始最终验证 |
| 整体超时或验证器异常、基线失败 | Failed | 空；保留部分证据 |
| context 取消 | Cancelled | 空；保留部分证据 |
| 报告或最终状态写入失败 | 返回错误，内存结果为失败 | 空；不得将旧磁盘状态当作完成 |

模型的 `completed` 只表示候选提交。`Completed + Rejected` 是合法组合，不构成 Go error。整体任务配置校验后派生超时 context，传递给复制、Agent 和 Evaluate；退出原因保存在运行记录中，逐次用量和完整对话保存在 Agent 记录中。

命令退出码：完整交付为 0（包含 Rejected），参数/配置错误为 2，执行失败或超时为 1，取消为 130。CLI 不根据质量判定自动合并或发布代码。

## 产物与发布顺序

```text
runs/rpcx-agent-<随机 ID>/
  run.json                   # 状态、阶段、时间、错误和已发布判定
  inputs/
    task.json                # 配置、规范摘要、基线摘要及三个模块的文件摘要
    spec/spec.md             # 固定规范；模型仅能读取这个子目录
  baseline/                  # 可信基线及相邻 SDK
  workspace/                 # Agent 修改的业务仓库及相邻 SDK
  artifacts/
    agent.json               # 对话、工具调用、退出原因、调用次数与逐次用量
    candidate.patch          # 实际候选与基线的差异；失败时也尽量导出
    report.json              # 输入摘要、候选/补丁摘要、验证证据与异常
  evidence/                  # Evaluate 的执行副本、命令日志及专项验证证据
  cache/                     # 本地后端缓存；Docker 的可写缓存随命令容器销毁
```

`run.json` 在工作开始前创建，每次阶段变化使用同目录临时文件加 rename 更新。初始输入错误可能在创建 Run 前直接返回。进程崩溃可能遗留 Running；当前没有崩溃恢复、逐轮对话落盘或断点续跑。

模型正常停止后固定候选摘要，独立验证器只在 evidence 副本运行。验证前后校验原始资源、基线副本、SDK 和规范；最终导出再次核对候选与受测摘要。检测到变化时清空最终质量判定，避免将过期证据视为有效交付。

补丁由独立临时 Git 索引生成，不在候选创建 `.git`，也不产生提交。支持新增、删除、修改、二进制和无末尾换行。报告记录补丁摘要；空补丁是合法文件。先保存 Agent 记录、补丁和报告，最后才写 Completed；`run.json` 是判断整次交付是否完成的依据。文件替换原子性不等同于断电持久性。

父 context 已取消时，收尾使用独立的 10 秒 context，尝试导出部分修改和保存终态；Git 命令响应该 context，普通本地文件写入仍依赖文件系统返回。收尾失败同样返回错误。新运行始终创建新目录，重试不复用旧目录。

## 执行环境边界

命令入口默认创建 Docker 后端，将同一实例传给 Agent 检查和 Evaluate。库调用通过 `Config.Backend` 显式注入后端；原 `TrustedLocal: true` 和 CLI 的 `--trusted-local` 保留为可信宿主执行选项，不能与容器后端同时使用。

本地后端现由共享执行层负责环境白名单、输出硬上限和普通进程组回收；该后端仍不限制宿主文件访问，也不约束主动脱离进程组的后代。Docker 后端只读挂载本次模块、禁网并限制资源，退出时删除容器；详细边界见 [执行记录](2026-10-17-docker-execution.md)。

本阶段未调用真实 DeepSeek API、未读取本地密钥文件，也未据此统计自主迁移成功率。

## 验收证据

| 验收点 | 测试 |
| --- | --- |
| 实际工具改动、路径保护反馈、构建通过、六项独立检查通过 | TestRunDeliversVerifiedCandidateAndKeepsHistory |
| 同一入口交付 Completed + Rejected，重复运行不改写历史和原始夹具 | 同上 |
| 补丁应用到干净基线后的摘要等于受测候选 | 同上 |
| API 错误、预算耗尽、取消和超时仍保留部分补丁与 Agent 记录 | TestRunKeepsPartialCandidateOnGenerationFailure |
| 无显式本地执行配置、输出重叠/别名、规范变化或符号链接输入被拒绝 | TestRunRejectsUnsafeInputsBeforeModelCall |
| 报告写入失败和终态写入失败不会宣称完成 | TestRunDoesNotPublishSuccessWhenEvidenceCannotBeWritten、TestRunReturnsErrorWhenTerminalStateCannotBeWritten |
| 基线/SDK/规范副本被改动时不执行最终验收 | TestRunRejectsChangedBaselineBeforeValidation |
| 基线构建失败保存命令证据；验证中取消保存部分证据 | TestRunPreservesFailedBaselineEvidence、TestRunCancellationDuringValidationPersistsEvidence |
| 新增/删除/二进制/无末尾换行/空文件可由补丁还原 | TestExportPatchRoundTripsAllFileChanges |
| 命令入口 → 模拟 HTTP API → DeepSeek 适配器 → Loop → Evaluate → 报告；密钥不入 JSON | TestCommandRunsAdapterLoopAndIndependentValidation |
| 候选进程不继承模型凭证或宿主 Git 配置 | TestLocalPreservesStreamsExitAndControlledEnvironment |

上述为可控模型的机制验收。Docker 执行隔离已完成基础验收，下一步开展真实模型迁移实验；批量调度、持久化重试和独立离线评测仍待实现。

## 第一项任务：单候选验证接口

已在 `scenarios/rpcx/checks/evaluate.go` 定义 `EvaluationRequest`、`Evaluation` 和接受 `context.Context` 的 `Evaluate` 函数。请求包含基线、候选、规范、证据目录，以及可选的 Go 缓存目录。结果包含规范版本与摘要、基线及候选摘要、检查、命令证据与质量判定。

补丁应用、候选工作目录创建和 expected 比较仍在用例驱动代码里；验证器只创建验证副本与证据目录。SDK 布局仍遵循本地场景约定，接口不依赖项目根目录、不读取 manifest、不创建 Run、不保存最终报告。

### 实现取舍

- **基线证据由验证器自己取得**：在验证副本重新运行可信基线测试，不把原有测试集合当作候选可提交的字段。当前公开用例存在少量重复基线执行，优先保持独立 API 的可信边界；未来确有成本问题时再设计可校验的基线证据复用。
- **输入与验证目录分开**：原始基线和候选不注入探针；命令只在证据目录内运行。执行前后核对输入、验证副本和 SDK 摘要，验证期间变化不发布有效判定。
- **规范绑定**：内置冻结 v0.1.0 的 SHA-256，任何其他内容均拒绝执行，不仅比较文件名或版本字样。
- **退出分类**：候选构建或测试失败为 `Rejected`、空 Go error；基线失败、命令启动异常、取消、命令 deadline、输入变化和写入失败为非空 error、空最终判定。
- **日志与结构化输出分开**：日志保留 stdout 和 stderr，JSON 测试事件只解析 stdout，避免测试代码编译诊断污染事件解析而被误判为验证器异常。
- **有限取消支持**：命令超时派生自父 context，并记录取消或 deadline 原因；限制等待输出管道的时间。尚未实现完整进程树清理，不扩大该测试结论。

### 验证结果

| 验证内容 | 结果 | 证据 |
| --- | --- | --- |
| 无 manifest 的独立目录直接调用接口 | 通过 | `TestEvaluatePreparedCandidateWithoutManifest` |
| 输入不修改、命令在验证副本运行 | 通过 | 同上，核对输入摘要与命令工作目录 |
| 候选普通编译失败与测试编译失败 | 通过 | 返回 Rejected 和空 Go error，保留编译诊断 |
| 不支持的规范、输出覆盖输入、路径别名、旧证据目录 | 通过 | `TestEvaluateRejectsUnsupportedSpecAndUnsafeEvidence` |
| 基线失败、命令不存在、预先取消 | 通过 | `TestEvaluateExecutionErrorsHaveNoVerdict` |
| 已运行子进程响应父取消和 deadline | 通过 | `TestCommandHonorsParentCancellationAndDeadline`；不代表完整进程树验收 |
| 五种公开用例逐项回归 | 通过，六项检查与 manifest 全部一致，原始夹具未变 | `./scripts/verify-rpcx-t1-t2.sh`；本地报告 `runs/rpcx-t1-t2-1079602869/report.json` |

新增测试时修正了 macOS `/var` 与 `/private/var` 别名的路径比较，以及测试辅助子进程的参数分隔。编译失败用例放入真实已有包，避免把通配包枚举行为当作编译验收依据。

该接口现已接入本页描述的 Runner。五种预制补丁仍由原公开用例驱动程序回归，Runner 的生成阶段只使用 Agent。
