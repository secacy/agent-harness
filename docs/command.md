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
