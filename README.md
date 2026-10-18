# Multi-Repo Coding Agent Harness

面向公共 SDK 不兼容升级的多仓库 Coding Agent 运行与验证框架，使用 Go 实现。项目目标与范围见 [项目说明](docs/PROJECT.md)。

当前已实现离线 `rpcx` T1/T2 迁移场景：本地 SDK v1/v2、10 个业务测试的迁移前仓库、版本化规范、五种候选补丁及公开专项验收。单仓库 Runner 已串联有界 Agent 循环、仓库工具、独立验证及补丁/报告导出，并提供 DeepSeek 命令入口。Docker 后端已接入 Agent 检查与最终验证；命令入口默认使用容器执行。实验 03 完成一次真实模型自主迁移、测试反馈修复、独立门禁和产物交付，M1 最小闭环已在受控场景验收；批量 Runtime、重复运行稳定性和独立离线评测尚未验收。

新增的 [T3 默认超时场景](scenarios/rpcx/t3/README.md)已完成 M2.1 冻结验收：四个手工候选均通过原业务测试，独立参考检查发现漏超时与错误期限，真实 30 秒到期得到验证。M2 的质量门禁增强与 Agent T3 接入仍在后续阶段，见[验收记录](docs/experiments/2026-10-18-rpcx-t3-fixture-01.md)。

M2 已增加绑定证据的质量聚合，以及在执行后端内加载类型的 [rpcx 有限边界分析](scenarios/rpcx/analysis/README.md)。后者已通过本地与 Docker 对照，能提供 SDK 位置、直接超时关系和覆盖缺口；完整 T3 门禁仍在实现中，进度见[里程碑](docs/MILESTONES.md)。

冻结 T3 场景的[测试结构检查](scenarios/rpcx/testintegrity/README.md)已实现：允许可信基线的最小 ctx/API 适配，区分明确的断言删除与尚未证明的重写。[可信行为检查](scenarios/rpcx/behavior/README.md)在同一候选生产副本运行 40 项业务及 T3 检查，包括两个真实 30 秒到期检查。真实 Docker 正反例已通过；这些证据的完整门禁组合与新规范 Runner 接入仍待完成。

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

[实验 02](docs/experiments/2026-10-18-rpcx-real-model-02.md) 在相同预算下加入预算通知与收尾提示，仍未提交；模型新增测试后检查失败，原样候选事后诊断为 Rejected。

[实验 03](docs/experiments/2026-10-18-rpcx-real-model-03.md) 将模型调用上限调整为 40，实际使用 31 次模型、54 次工具调用完成交付；原 Run 判定 ReadyForReview，20 个业务测试及可信 context 探针通过。该单次结果不代表稳定成功率或最优预算。
