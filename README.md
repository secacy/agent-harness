# Multi-Repo Coding Agent Harness

面向公共 SDK 不兼容升级的多仓库 Coding Agent 运行与验证框架，使用 Go 实现。项目目标与范围见 [项目说明](docs/PROJECT.md)。

当前已实现离线 `rpcx` T1/T2 迁移场景：本地 SDK v1/v2、10 个业务测试的迁移前仓库、版本化规范、五种候选补丁及公开专项验收。单仓库 Runner 已串联有界 Agent 循环、仓库工具、独立验证及补丁/报告导出，并提供 DeepSeek 命令入口；可控模型和本地模拟 API 已通过端到端联调。Docker 后端已接入 Agent 检查与最终验证，并通过真实容器验收；命令入口默认使用容器执行。真实模型迁移、批量 Runtime 和独立离线评测尚未验收。

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
- [预算感知与及时提交的设计及验证](docs/worklog/2026-10-18-budget-awareness.md)
- [仓库工具实现与边界](docs/worklog/2026-10-17-repository-tools.md)
- [DeepSeek 适配器与验收](docs/worklog/2026-10-17-deepseek-adapter.md)
- [单仓库 Runner、状态与交付](docs/worklog/2026-10-17-single-repo-runner.md)

容器执行的镜像准备、限制和集成测试见 [Docker 执行记录](docs/worklog/2026-10-17-docker-execution.md) 与 [运行命令](docs/command.md)。

[首次真实模型实验](docs/experiments/2026-10-17-rpcx-real-model-01.md) 已执行：模型在预算内未提交，原 Run 保留失败；补丁通过了事后独立诊断，尚不计作自动迁移成功。

[实验 02](docs/experiments/2026-10-18-rpcx-real-model-02.md) 在相同预算下加入预算通知与收尾提示，仍未提交；模型新增测试后检查失败，原样候选事后诊断为 Rejected。M1 继续保留未验收状态。
