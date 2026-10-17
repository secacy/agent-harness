# Multi-Repo Coding Agent Harness

面向公共 SDK 不兼容升级的多仓库 Coding Agent 运行与验证框架，使用 Go 实现。项目目标与范围见 [项目说明](docs/PROJECT.md)。

当前已实现离线 `rpcx` T1/T2 迁移场景：本地 SDK v1/v2、10 个业务测试的迁移前仓库、版本化规范、五种候选补丁及公开专项验收。单候选验证接口与有界 Agent 循环已实现；循环使用模型和工具替身验证。真实模型与仓库工具、批量 Runtime 和独立离线评测尚未接入。

需要本地 Go 1.23 或更新版本、Git 和 POSIX shell；已在 Go 1.26.5 / macOS arm64 验证。

```sh
./scripts/verify-rpcx-t1-t2.sh
```

命令关闭 Go 依赖下载及 toolchain 自动下载，在 `runs/` 创建独立副本、日志和 `report.json`。五种用例均符合预期且基线未变时退出 0；反例被正确拒绝也属于场景验收通过。

- [场景结构与检查边界](scenarios/rpcx/README.md)
- [里程碑](docs/MILESTONES.md)
- [开发与运行命令](docs/command.md)
- [本阶段实现与验证记录](docs/worklog/2026-10-17-rpcx-t1-t2-fixtures.md)
- [Agent 循环实现与示例](docs/worklog/2026-10-17-minimal-agent-loop.md)
