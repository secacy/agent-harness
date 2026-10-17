# rpcx T1/T2 迁移场景

这是公开、可离线复现的开发夹具，使用三个独立 Go module，全部只依赖标准库。SDK 是内存模拟实现，不启动网络服务。

| 目录 | 职责 |
| --- | --- |
| `fixtures/rpcx-v1/` | `example.com/rpcx`，`NewClient` 与无 context 的 `Call` |
| `fixtures/rpcx-v2/` | `example.com/rpcx/v2`，`Dial` 与带 context 的 `Call` |
| `fixtures/demo-orders/` | v1 迁移前基线，运行时只复制、不原地修改 |
| `specs/v0.1.0.md` | 已引用后冻结的 T1/T2 迁移规范 |
| `checks/` | Go 验证工具与嵌入的可信 context 探针 |
| `cases/` | 公开补丁和预期判定；只供场景测试使用 |
| `oracle/` | 独立离线评测的预留位置，当前没有保留评测集 |

## 运行过程

从项目根目录执行 `./scripts/verify-rpcx-t1-t2.sh`。每次创建新的 `runs/rpcx-t1-t2-*/`，预检两个 SDK 与 v1 业务基线，再分别复制整个 fixtures 布局、检查并应用补丁、构建、运行业务测试和专项探针。

基线业务代码约 360 行，共 10 个测试函数。`Place` 已有 context，`Cancel` 无 context；迁移需要同时适配 inventory、service、handler 和测试。SDK 的相对 replace 在运行副本中保持有效。`go test ./...` 不跨越嵌套模块，所以场景入口显式逐个测试三个模块。

Go 命令设置 `GOPROXY=off`、`GOSUMDB=off`、`GOWORK=off`、`GOTOOLCHAIN=local`、`GOFLAGS=-mod=readonly`，缓存位于 `runs/go-cache/`。入口不接受任意候选仓库，只运行 manifest 列出的公开用例。

## 检查与预期

| 用例 | 构建 | 业务测试 | 关键专项结果 | 判定 |
| --- | --- | --- | --- | --- |
| `correct` | pass | pass | 全部 pass | ReadyForReview |
| `background-fallback` | pass | pass | context_propagation fail | Rejected |
| `missed-callsite` | fail | blocked | 忘记传递 handler 中的 Cancel 参数 | Rejected |
| `deleted-test` | pass | pass（9 个测试） | test_preservation fail | Rejected |
| `protected-path` | pass | pass | protected_paths fail | Rejected |

`manifest.yaml` 使用 YAML 1.2 的 JSON 子集，以标准库解析实现零外部依赖；当前读取器仅接受这种 JSON 语法，拒绝未知字段。每个用例必须声明六项检查和最终判定。实际检查与判定计算完毕后才与预期比较。

- `dependencies`：v2 依赖、版本、本地 replace、v1 导入清除、SDK 内容保持不变。
- `protected_paths`：比较 `internal/pb/` 的文件清单与逐文件 SHA-256，检测新增、删除、改名和修改。
- `build`：构建全部候选包。
- `baseline_tests`：运行候选中的业务测试，保存 JSON 测试事件。
- `test_preservation`：比较原始测试的包路径＋测试名、原有子测试事件，要求实际执行且通过，拒绝删除和跳过；允许新增测试。
- `context_propagation`：注入可信探针后，从 Place/Cancel HTTP handler 到 RPC 后端检查值、deadline、预先取消、执行中取消与订单状态；另做禁止断开 context 的有限语法检查。

`pass` 表示有通过证据；`fail` 表示有明确违反约束的证据；`blocked` 表示前置条件未满足或验证超时；`not_run` 表示未执行。有明确失败则 `Rejected`，否则存在证据缺口则 `NeedsAttention`，六项全部通过才 `ReadyForReview`。

## 产物与证据边界

运行目录保存规范及 manifest 副本、各用例补丁、候选工作目录、构建输出、命令日志与 JSON 报告。报告记录 Go 版本、命令参数、退出码、耗时，以及规范、manifest、验证材料、夹具、补丁和候选文件树的 SHA-256。探针运行完毕后移除注入文件，并检查候选内容是否仍与受测摘要对应，最后确认原始夹具未变化。

这些检查只证明本夹具的已列行为。保留测试名和通过状态无法证明所有断言未削弱；context 语法检查不做通用数据流分析。公开补丁与专项探针不能作为独立效果评测集。

工作副本、只读约定与摘要检查不提供恶意代码沙箱。当前只执行可信的公开夹具；未来接入 Agent 时仍需实现文件访问限制、可信检查隔离和进程资源管理。超时目前使用 Go 测试超时与命令上下文，尚不提供完整进程树清理保证。
