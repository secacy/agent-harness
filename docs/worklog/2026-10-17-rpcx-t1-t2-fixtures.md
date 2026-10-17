# 离线 rpcx T1/T2 迁移夹具与专项验收

- 日期：2026-10-17
- 状态：已验收
- 对应里程碑：[M0](../MILESTONES.md)
- 当前结论：单场景的预制候选补丁已能离线应用、验证并输出证据；尚未接入 Agent 或批量 Runtime。

## 问题与目标

为后续 Harness 提供一个跨函数、跨文件、需要同步修改测试的迁移任务，以及可重复区分正确和错误候选的验证入口。本阶段只覆盖 T1/T2，不包含隐式 30 秒超时语义。

## 设计与实现

资源集中在 `scenarios/rpcx/`。三个 Go module 全部使用标准库；demo-orders 的 replace 指向相邻本地 SDK。迁移前基线不原地修改，各用例从独立副本开始。业务代码约 360 行、10 个原有测试。

迁移规范 v0.1.0 已被运行引用并冻结。正确补丁沿 HTTP → service → inventory 链路传递 context，并更新测试；四个反例分别引入 Background 兜底、遗漏 Cancel 调用点、删除原有测试、修改生成代码。

运行时检查由根 Go 模块中的场景工具实现，可信探针嵌入工具并注入临时副本。检查计算完成后才读取用例预期进行比较。`manifest.yaml` 采用 YAML 1.2 的 JSON 子集，不引入 YAML 解析依赖。

报告绑定规范、验证材料、夹具和候选摘要，保留命令和日志；检查期间候选变化会使验收失败。`oracle/` 仅保留用途说明，未建立独立离线评测集。

## 实际验证

环境：Go 1.26.5，macOS arm64；禁用 Go 代理、校验数据库、workspace 与 toolchain 自动下载，无第三方依赖。

| 验证内容 | 结果 | 证据 |
| --- | --- | --- |
| SDK v1/v2 与 v1 业务基线 | 通过 | 场景入口的 preflight 日志 |
| 正确迁移补丁 | 六项通过，ReadyForReview | `correct` 报告及六个 context 子用例 |
| Background 反例 | 构建和 10 个业务测试通过，context 检查失败 | `background-fallback/context-probes.log` |
| 遗漏调用点反例 | 构建失败，后续执行检查 blocked | `missed-callsite/build.log` |
| 删除测试反例 | 剩余 9 个业务测试通过，原有测试保留检查失败 | `deleted-test` 报告 |
| 修改生成代码反例 | 构建与业务测试通过，路径保护失败 | `protected-path` 报告 |
| 验证器边界测试 | 通过 | 根模块 `go test ./...` |
| 原始夹具完整性 | 未变化 | 报告 `fixtures_unchanged: true` |

运行产物位于本地 `runs/rpcx-t1-t2-*/`，不提交 Git；完整命令见 [command.md](../command.md)，检查含义和限制见 [场景说明](../../scenarios/rpcx/README.md)。

实现中增加了探针实际执行检查，避免将进程退出 0 直接视为六个探针均通过；同时修正原 `.gitignore` 中本地文档的路径模式，确保协作约定与个人目录不进入提交。

## 限制与下一步

公开反例只能证明本场景的机制有效，不能证明真实仓库迁移成功率。测试保留检查无法证明断言未被削弱。当前副本和摘要检查不提供恶意代码沙箱，也不具备通用进程树回收能力。

下一步围绕单仓库 Runner 定义任务输入、Run 状态、候选产物与验证交接，再接入模型生成补丁。
