# 日常开发与运行

以下命令从项目根目录执行。根模块要求 Go 1.23+；固定场景此前使用 Go 1.26.5 验证。需要本机 Git，Docker 集成需要预先准备镜像。当前阶段见[开发入口](README.md)。

## 固定场景检查

```sh
./scripts/verify-rpcx-t1-t2.sh
```

无需模型或 Docker，运行五个固定候选并核对预期。报告写入新建的 `runs/` 子目录；详细检查含义见[rpcx 场景](../scenarios/rpcx/README.md)。

## 核心机制测试

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly go test ./internal/agent ./internal/budget ./internal/recovery ./internal/execution ./cmd/harness
```

默认不访问真实模型或开启真实 Docker 集成；部分测试启动本机模拟 HTTP 服务或自有辅助进程。修改其他模块时测试对应包，场景专项验收见各模块 README 和[阶段命令参考](archive/command.md)。

## 单任务入口

```sh
go run ./cmd/harness --help
```

当前支持单任务、`batch`、`audit`、`replay`、`failures`。单任务生成读取终端中的 `DEEPSEEK_API_KEY`、`DEEPSEEK_MODEL`，不会自动加载 `.env`；默认使用 Docker。可用参数以 `--help` 为准。

## 受预算约束的批次入口

先检查[配置示例](../examples/rpcx-l2-batch.json)：

```sh
go run ./cmd/harness batch --config examples/rpcx-l2-batch.json --check
```

该命令不读密钥、不联网、不创建运行目录；它检查配置结构，不认证实际输入内容或执行环境。

真实生成前，填写实际模型、预算、时限和全新输出目录；相对路径基于配置文件目录。示例不是冻结实验协议，纳入清单摘要须与当前实现匹配。先核对配置，再选择操作：

```sh
mkdir -p runs
go build -o runs/harness ./cmd/harness
```

仅查询模型能力，读取密钥并联网，但不生成代码：

```sh
runs/harness batch --config runs/my-batch.json --probe-model
```

实际生成，会消耗模型额度并发送仓库内容与工具交互；使用另一份配置和新的输出目录：

```sh
runs/harness batch --config runs/my-run.json
```

`runs/my-batch.json` 和 `runs/my-run.json` 需自行按示例准备，不随仓库提供。两级预算控制调用次数和 token；不是金额账单上限。批次审核入口为输出目录下的 `campaign/summary.md`。字段、能力准入与返回码详见[批次参考](archive/command.md#受预算约束的批次入口)。

## 只读审计已有 Campaign

停止原写者后，选择全新、位于源目录之外的输出目录：

```sh
runs/harness audit --campaign /absolute/path/to/campaign --output /absolute/path/to/new-audit
```

使用前按上文编译二进制。审计不需要模型或 Docker，只核对已记录材料；通过不代表候选正确，也不授予恢复权限。接口与结果见[audit](../internal/audit/types.go)。

## 按需查阅

- 模型与仓库工具：[Agent](../internal/agent)、[预算](../internal/budget/README.md)、[执行](../internal/execution/README.md)。
- 已有恢复 Go API：[recovery](../internal/recovery/README.md)；当前是受控整代重跑。
- 旧交付包复验：[历史演示](archive/DEMO.md)，归档和 `runs/` 不随 Git 提供。
- 场景校准、故障测试及完整参数：[阶段命令参考](archive/command.md)，使用前核对对应代码和材料。

step resume 尚无可用命令；原恢复 API 是整代重跑。


## 离线回放与失败归因

新 Runner Run 自动写入 `trace.jsonl`。回放只读现存材料，使用记录的模型/工具结果驱动生产循环，验证输入与预算并重新聚合质量判定；不调用模型、不执行仓库命令，不需要 Docker 或密钥。

```sh
go run ./cmd/harness replay --run <run-directory>
```

预算 Run 默认向祖先目录查找 `budget/budget.json`；只复制了 Run 时，用 `--ledger <archived-budget.json>` 显式提供原账本。不会跟随记录中旧机器的绝对账本路径。JSON 输出区分本地模型尝试、实际准入、拒绝、已知 token、预留和未知占用；无价格依据时 `cost=null`。未绑定预算的 Run 不能声称已核账本。

完整历史 Completed 对话可进入 `legacy-completed` 模式，逐请求摘要与原账本认证工具定义；缺失的 finish reason 明确标为成功控制流推导，工具时间保持未知。旧失败/截断记录、损坏或不完整 trace 会拒绝完整回放，不把原 verdict 直接当作结果。材料与支持边界见 [P0 设计](worklog/2026-10-31-p0-trace-replay.md)。

```sh
go run ./cmd/harness failures --run <run-a> --run <run-b>
go run ./cmd/harness failures --run <run-a> --run <run-b> --json
```

表格显示主因、计数、占全部 Run 和终态未成功数的比例；未完成单列，零分母 N/A。JSON 另列每 Run 和次要失败检查。重复目录或缺失终态报告会报错，不删减分母。归因描述原记录中的运行/质量结果，不代表独立真值评测。

源码离线回归：

```sh
go test -race -timeout 20m ./internal/trace ./internal/agent ./internal/budget ./internal/repotools ./scenarios/rpcx/runner ./cmd/harness
```

包括自有辅助进程强杀后的记录读取，默认不访问模型或开启 Docker 集成。五个历史 Run 的路径、复验结果与数据限制见 [P0 离线验收](experiments/2026-10-31-p0-offline-replay.md)；原 `runs/` 材料不随 Git 提供。

## P1 实验准备与汇总

生成四仓、三组、三次重复的草案、实际初始提示/工具清单和待填人工复核模板。输出目录必须不存在；此入口不读取模型凭证、不查询供应商或启动 Docker。

```sh
go run ./cmd/harness ablation prepare --output runs/p1-preparation
```

`protocol.draft.json` 故意留空待测的模型能力来源和 Docker 引擎身份，不能作为已冻结或获准付费的协议。`generation-definitions.json` 可检查 A 的最小任务信息及 B/C 相同的完整生成配置；动态预算通知按同一 `final-response-v1` 策略生成。`task-reviews.template.json` 默认四项 pending，复核依据见[人工复核材料](worklog/2026-10-31-p1-truth-review.md)。

查询供应商同源 `/models` 元数据和本机 Docker 身份，不发送模型生成请求。该命令需要环境中的 `DEEPSEEK_API_KEY`，不在输出中保存密钥；模型元数据、声明上界及原始响应保存到新目录。

```sh
go run ./cmd/harness ablation probe --draft runs/p1-preparation/protocol.draft.json --price docs/experiments/p1-baseline-v0.1.0/price-basis.json --output runs/p1-probe
```

正式冻结和启动使用同一个已构建的二进制，以便核对实际执行字节。运行相关源码需先提交；无关文档和人工复核文件的编辑可以继续。冻结还包含 Git HEAD、源码完整清单、模型原始响应、参考身份及 12 份实际初始提示/工具。

```sh
go build -trimpath -o runs/p1-harness ./cmd/harness
runs/p1-harness ablation freeze --probe runs/p1-probe --output runs/p1-freeze
```

冻结目录中的 `task-reviews.template.json` 与 `authorization.template.json` 默认 pending，不能执行。实际人完成逐仓复核且模型/费用上限获明确授权后，另存完整记录并按冻结清单启动一次预定批次：

```sh
runs/p1-harness ablation run --freeze runs/p1-freeze --reviews <actual-task-reviews.json> --authorization <actual-spending-authorization.json> --output <new-experiment-root>
runs/p1-harness ablation reference --freeze runs/p1-freeze --experiment <experiment-root>
```

启动核对冻结字节、真实 Docker engine/image/resources、当前模型声明及全部预算授权，之后串行执行预定项；不自动重试。参考命令不读取模型凭证，在新的独占参考目录评测所有固定候选，包括未放行和未正常提交者。实际人工复核写入每候选参考目录的 `review.json` 后可重新只读汇总；缺复核保持未知。字段校验只能认证记录结构与绑定，不能证明填写者身份或实际完成了复核。

已有实验可只读核对 trace、预算、固定候选与独立参考，再输出 JSON、CSV、Markdown：

```sh
go run ./cmd/harness ablation report --experiment <p1-root> --output <new-report-directory>
```

汇总保留全部预定尝试、零分母及未知项，不修改原收据或人工复核。生产 `replay` 继续只处理普通 Run；实验 Go API 的 `runner.ReplayExperiment` 认证专用实验收据。运行、参考评测与聚合的 Go API 分别为 `ablation.Run`、`ablation.EvaluateAll`、`ablation.Collect`。准备、模型元数据查询、冻结及容器校准均不等于正式实验已启动或 P1 已验收。

离线机制验证使用受控模型和执行替身，不代表三组迁移效果：

```sh
go test -race ./scenarios/rpcx/l2/ablation
go test -race ./scenarios/rpcx/runner -run '^TestExperiment|^TestDescribeExperiment|^TestReplay' -count=1
go test -race ./cmd/harness -run '^TestAblation' -count=1
```
