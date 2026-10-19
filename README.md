# Multi-Repo Coding Agent Harness

面向公共 SDK 不兼容升级的多仓库 Coding Agent 运行与验证框架，使用 Go 实现。项目目标与范围见 [项目说明](docs/PROJECT.md)。

当前已实现离线 `rpcx` T1/T2 迁移场景：本地 SDK v1/v2、10 个业务测试的迁移前仓库、版本化规范、五种候选补丁及公开专项验收。单仓库 Runner 已串联有界 Agent 循环、仓库工具、独立验证及补丁/报告导出，并提供 DeepSeek 命令入口。Docker 后端已接入 Agent 检查与最终验证；命令入口默认使用容器执行。实验 03 完成一次真实模型自主迁移、测试反馈修复、独立门禁和产物交付，M1 最小闭环已在受控场景验收；批量 Runtime、重复运行稳定性和独立离线评测尚未验收。

[T3 默认超时场景](scenarios/rpcx/t3/README.md)已完成冻结验收，证明候选可以通过原有测试却违反超时契约。运行时[组合质量门禁](scenarios/rpcx/checks/README.md)结合测试结构、有限类型分析与 40 项可信行为检查；[冻结 L1 独立交叉验收](scenarios/rpcx/l1/README.md)的 19 例均符合预期，保留参考检查发现的覆盖差异，M2.3 已在声明范围内验收。

[单仓库 Runner](scenarios/rpcx/runner/README.md)现在可以显式选择 T3 规范，并从实际结构化结果生成 `artifacts/review.md`，将条款、修改、证据和缺口关联，单列 Agent 自述。M2.4 的离线与 Docker 联调结果见[工作记录](docs/worklog/2026-10-18-m2-evidence-gate.md#21-m24新规范-runner-与确定性审核交付)。[T3 真实实验 04](docs/experiments/2026-10-19-rpcx-real-model-04.md)以 28 次模型、55 次工具正常提交，原判定 NeedsAttention；独立参考通过，不计自动成功。后续已有限支持新增测试导入，公共 helper 仍保留未知，原实验结果不变。[M2 收尾评审](docs/worklog/2026-10-18-m2-evidence-gate.md#25-m2-收尾有限范围验收与-m3-移交)确认判定与审核交付在声明范围内验收；真实自动迁移效果与人工审核成本仍待后续验证。

[M3 具体设计](docs/worklog/2026-10-19-m3-batch-runtime.md)已记录多仓库任务、预算预留及 L2 数据划分方案，M3.1 已实现[预算核心](internal/budget/README.md)与单 Run 的可选 Go API 接入，在受控模型下验证调用前预留和未知费用保留；M3.2 已接入[明确的 Target 与宿主 Profile](scenarios/rpcx/runner/README.md#显式-target-与宿主-profile)，在模型调用前实际构建并核对十个基线测试；当前另接入 Goddd 与 Translation 的独立宿主配置。M3.3 新增 [Campaign Go API](internal/campaign/README.md)，提供 Target/命令双重并发限制、共享预算、故障隔离及结果汇总，并接入现有 Runner；目前以确定性模型验证机制，未开放付费批次命令。M3.4 已完成 [L2 样本选择和 Goddd 离线基线](scenarios/rpcx/l2/README.md)，[Goddd 迁移门禁与 Runner](scenarios/rpcx/goddd/README.md)已接入，正确/违规/证据不足候选分别校准三类判定；[Translation 双配置离线基线](scenarios/rpcx/l2/data/rpcx-l2-translation-v1/README.md)与[双配置迁移门禁及 Runner](scenarios/rpcx/translation/README.md)已接入，[Workouts 回调与多模块基线](scenarios/rpcx/l2/data/rpcx-l2-workouts-v1/README.md)也已准备，待其迁移门禁及独立效果评测，整体进度见[里程碑](docs/MILESTONES.md)。

Harness 需要本地 Go 1.23 或更新版本、Git 和 POSIX shell；Translation / Workouts 夹具分别保留 Go 1.26.0 / 1.25.0 要求。已在 Go 1.26.5 / macOS arm64 验证。

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
- [单仓库 Runner、审核材料与交付](scenarios/rpcx/runner/README.md)

容器执行的镜像准备、限制和集成测试见 [Docker 执行记录](docs/worklog/2026-10-17-docker-execution.md) 与 [运行命令](docs/command.md)。

[首次真实模型实验](docs/experiments/2026-10-17-rpcx-real-model-01.md) 已执行：模型在预算内未提交，原 Run 保留失败；补丁通过了事后独立诊断，尚不计作自动迁移成功。

[实验 02](docs/experiments/2026-10-18-rpcx-real-model-02.md) 在相同预算下加入预算通知与收尾提示，仍未提交；模型新增测试后检查失败，原样候选事后诊断为 Rejected。

[实验 03](docs/experiments/2026-10-18-rpcx-real-model-03.md) 将模型调用上限调整为 40，实际使用 31 次模型、54 次工具调用完成交付；原 Run 判定 ReadyForReview，20 个业务测试及可信 context 探针通过。该单次结果不代表稳定成功率或最优预算。
