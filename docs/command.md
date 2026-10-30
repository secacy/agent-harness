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

当前支持单任务、`batch`、`audit`。单任务生成读取终端中的 `DEEPSEEK_API_KEY`、`DEEPSEEK_MODEL`，不会自动加载 `.env`；默认使用 Docker。可用参数以 `--help` 为准。

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

使用前按上文编译二进制。审计不需要模型或 Docker，只核对已记录材料；通过不代表候选正确，也不授予恢复权限。接口与结果见[audit](../internal/audit/README.md)。

## 按需查阅

- 模型与仓库工具：[Agent](../internal/agent)、[预算](../internal/budget/README.md)、[执行](../internal/execution/README.md)。
- 已有恢复 Go API：[recovery](../internal/recovery/README.md)；当前是受控整代重跑。
- 旧交付包复验：[历史演示](archive/DEMO.md)，归档和 `runs/` 不随 Git 提供。
- 场景校准、故障测试及完整参数：[阶段命令参考](archive/command.md)，使用前核对对应代码和材料。

新需求中的 trace replay、统一失败归因统计和 step resume 尚无可用命令。
