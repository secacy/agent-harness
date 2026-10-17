# 里程碑

## M0：离线 rpcx T1/T2 场景夹具

- 状态：已验收（2026-10-17）
- 范围：三个本地 Go module、10 个业务测试、版本化迁移规范、五种候选补丁、公开专项验证与报告。
- 验收：SDK 与迁移前业务基线测试通过；正确补丁六项检查通过；四个反例分别被预期检查拒绝；原始夹具保持不变。
- 证据：[实现记录](worklog/2026-10-17-rpcx-t1-t2-fixtures.md)、[场景说明](../scenarios/rpcx/README.md)、`./scripts/verify-rpcx-t1-t2.sh`。
- 证据缺口：尚无 Agent 自主迁移、隐式超时迁移、可靠批量执行或独立离线评测能力；当前验证不提供恶意代码执行隔离。

## M1：单仓库 Agent 最小闭环

- 状态：部分实现；单候选验证接口、有界 Agent 循环与仓库工具已实现，完整 M1 尚未验收。
- 目标：给定迁移前仓库和规范，由模型通过工具自主生成候选，再调用独立验证接口交付补丁与证据；不向模型提供参考补丁。
- 当前设计与任务：[最小 Agent 循环](worklog/2026-10-17-minimal-agent-loop.md)。已有执行与状态设计保留在 [Runner 契约](worklog/2026-10-17-single-repo-runner.md)，按实际闭环需要逐步实现。
- 实现顺序：单候选验证接口（已完成）→ 有界 Agent 循环（机制测试通过）→ 仓库工具（可信夹具联调通过）→ DeepSeek 适配与执行环境边界 → Agent 生成候选后独立验收。
- 首项任务：已实现 `Evaluate(ctx, EvaluationRequest) (Evaluation, error)`；独立于 cases manifest、补丁应用和预期比较，命令在验证副本运行，支持父 context 和规范内容绑定。验收细节见工作记录。
- 当前成果：`internal/agent.Loop.Run` 串行组织模型与工具交互，保存完整消息和调用 ID，处理错误反馈、预算及取消；使用替身完成机制测试。
- 当前成果：`internal/repotools.Executor` 提供读取、搜索、精确替换与可选 Go 检查；预设模型已连通真实夹具的构建失败、修复和测试反馈。见 [仓库工具记录](worklog/2026-10-17-repository-tools.md)。
- DeepSeek 接入进度：已实现消息协议结构与 `encodeMessages`，覆盖多工具调用、结果关联、错误文本反馈和输入只读；工具定义转换、响应转换与 HTTP 客户端尚未实现。
- 证据缺口：DeepSeek 适配、隔离执行后端、Runner 状态、产物发布与完整进程树回收尚未实现；本地检查仅回收普通进程组，不是进程沙箱；预设模型测试不证明自主迁移能力。
