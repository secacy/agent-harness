# 开发与运行命令

以下命令从项目根目录运行，已在 Go 1.26.5 / macOS arm64 验证。最低声明版本为 Go 1.23，需要已安装的本地 toolchain、Git 和 POSIX shell。

## 完整场景验收

```sh
./scripts/verify-rpcx-t1-t2.sh
```

离线运行两个 SDK、v1 业务基线和五种候选补丁。输出本次 `report.json` 路径；预期反例被拒绝且其余预期均满足时返回 0。每次运行使用新目录，历史产物保留在 Git 忽略的 `runs/`。

## 验证工具自身测试

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test ./...
```

覆盖受保护路径新增/删除/改名/修改、测试缺失/跳过/未执行、专项探针未执行、context 别名、符号链接拒绝及判定边界。另在无 manifest 的临时目录中直接调用 `Evaluate`，验证输入不被修改、普通编译与测试编译失败的判定、规范绑定、证据目录约束、基线失败、命令启动失败，以及父 context 取消和 deadline 传播。

该命令仅测试根模块；接口集成测试会运行其临时副本中的业务测试，但不替代两个 SDK 和五种公开补丁的完整场景验收。完整验证使用上面的场景入口。

## Agent 循环测试

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test ./internal/agent
```

使用模型和工具替身，检查消息关联、工具顺序、错误反馈、调用预算、取消和退出分类，同时执行 `ExampleLoop_Run` 示例。不访问模型服务，也不实际修改业务仓库。根模块 `go test ./...` 同样包含这些测试。

## 仓库工具测试

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test ./internal/repotools
```

检查文件访问边界、保护路径、替换约束、输出限制和 Go 命令生命周期。集成测试使用预设模型和真实工具，在临时 `demo-orders` 副本制造并修复编译错误，最后运行真实业务测试；不访问模型服务，不修改原始夹具。macOS/Linux 执行命令与进程组测试，其他平台仅执行文件工具测试。首次运行需编译隔离缓存中的标准库。

## DeepSeek 适配器测试

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test ./internal/deepseek ./internal/agent
```

验证消息与工具映射、请求设置、响应错误、输出截断、取消/超时和逐次 Token 用量记录，并运行本地模拟 API 与真实文件工具的循环联调。需要本机临时端口监听权限，不需要 API Key，不访问真实模型服务。根模块 `go test ./...` 同样包含这些测试。

## 单仓库 Runner 离线验收

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test ./scenarios/rpcx/runner ./cmd/harness
```

可控模型通过真实工具修改临时夹具，再运行独立验证、导出补丁并验证还原一致性。覆盖正确/被拒绝的候选、失败/预算/取消/超时、输入变化、报告与状态写入失败，以及多次运行的历史保留。命令入口测试使用本机模拟 HTTP 服务，需要临时端口监听权限；不需要真实 API Key，不访问真实 DeepSeek 服务。

## 单仓库任务入口

```sh
go run ./cmd/harness --help
```

入口接收 `--scenario`（默认 `scenarios/rpcx`）、`--runs`（默认 `runs`）、`--model`、`--timeout`、`--max-model-calls`、`--max-tool-calls`、`--max-tokens` 及测试用完整 `--endpoint`。模型名也可由 `DEEPSEEK_MODEL` 提供；密钥只从 `DEEPSEEK_API_KEY` 读取，不提供密钥参数，也不会自动加载 `.env`。

命令默认使用 Docker。先启动本机 Docker 引擎并准备固定工具链镜像；任务执行使用本地镜像 ID，缺失镜像时报错，不自动下载：

```sh
docker pull golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd
```

真实模型调用形式如下，会使用当前终端的模型凭证。[首次真实试跑](experiments/2026-10-17-rpcx-real-model-01.md) 已完成记录，因预算耗尽未提交；真实模型端到端自动完成尚未验收：

```sh
# 预先配置 DEEPSEEK_API_KEY 和 DEEPSEEK_MODEL。
go run ./cmd/harness --model "$DEEPSEEK_MODEL" --timeout 5m
```

`--image` 可指定预先准备的 Linux Go 镜像，构造后端时记录并固定其镜像 ID；要求包含 `/usr/bin/env` 和 Go，且不能声明额外匿名卷。`--cpus` 默认 2，`--memory-mib` 默认 2048，`--pids` 默认 128。默认禁网、只读根文件系统及只读源码/SDK，只向容器开放 1 GiB 临时空间，每条命令独立销毁。`--trusted-local` 显式切换到原可信宿主后端，用于受控夹具调试；不会在 Docker 失败时自动降级。

命令打印 Run 和报告路径。直接运行编译出的程序时，完整交付返回 0（含 `Rejected`），参数/配置错误返回 2，执行失败/超时返回 1，取消返回 130；`go run` 自身可能将程序非零退出码转换为 1，脚本需要精确退出码时先 `go build -o runs/harness ./cmd/harness` 再运行该二进制。审核材料及状态语义见 [Runner 记录](worklog/2026-10-17-single-repo-runner.md)。

## Docker 执行后端验收

```sh
env HARNESS_DOCKER_TEST=1 GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test ./internal/execution ./scenarios/rpcx/runner ./cmd/harness
```

需要可访问的本机 Linux 容器引擎、上述已拉取镜像和本地临时端口权限。不需要模型密钥，不访问真实模型服务。覆盖源码/根文件系统只读、未挂载宿主文件和密钥不可见、网络受限、资源限制、OOM、输出超限、取消/超时回收，以及容器内候选迁移、独立验证和补丁还原。全新命令容器不复用 Go 缓存，因此明显慢于本地测试。

不设置 `HARNESS_DOCKER_TEST=1` 的常规 `go test ./...` 会明确跳过真实 Docker 集成，仍执行本地后端、容器参数校验、回收失败替身、Runner 和模拟 API 测试；不能把该结果记作容器验收。
