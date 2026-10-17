# 里程碑

## M0：离线 rpcx T1/T2 场景夹具

- 状态：已验收（2026-10-17）
- 范围：三个本地 Go module、10 个业务测试、版本化迁移规范、五种候选补丁、公开专项验证与报告。
- 验收：SDK 与迁移前业务基线测试通过；正确补丁六项检查通过；四个反例分别被预期检查拒绝；原始夹具保持不变。
- 证据：[实现记录](worklog/2026-10-17-rpcx-t1-t2-fixtures.md)、[场景说明](../scenarios/rpcx/README.md)、`./scripts/verify-rpcx-t1-t2.sh`。
- 证据缺口：尚无 Agent 自主迁移、隐式超时迁移、可靠批量执行或独立离线评测能力；当前验证不提供恶意代码执行隔离。

## M1：单仓库 Agent 最小闭环

- 状态：部分实现；单仓库 Runner 已通过离线端到端联调，Docker 执行后端已通过真实容器验收，真实模型迁移尚未验收。
- 目标：给定迁移前仓库和规范，由模型通过工具自主生成候选，再调用独立验证接口交付补丁与证据；不向模型提供参考补丁。
- 当前设计与任务：[最小 Agent 循环](worklog/2026-10-17-minimal-agent-loop.md)。已有执行与状态设计保留在 [Runner 契约](worklog/2026-10-17-single-repo-runner.md)，按实际闭环需要逐步实现。
- 实现顺序：单候选验证接口 → 有界 Agent 循环 → 仓库工具 → DeepSeek 适配 → 串行 Runner（离线联调通过）→ Docker 执行后端（已验收） → 真实模型生成候选后独立验收。
- 首项任务：已实现 `Evaluate(ctx, EvaluationRequest) (Evaluation, error)`；独立于 cases manifest、补丁应用和预期比较，命令在验证副本运行，支持父 context 和规范内容绑定。验收细节见工作记录。
- 当前成果：`internal/agent.Loop.Run` 串行组织模型与工具交互，保存完整消息和调用 ID，处理错误反馈、预算及取消；使用替身完成机制测试。
- 当前成果：`internal/repotools.Executor` 提供读取、搜索、精确替换与可选 Go 检查；预设模型已连通真实夹具的构建失败、修复和测试反馈。见 [仓库工具记录](worklog/2026-10-17-repository-tools.md)。
- DeepSeek 接入进度：请求/响应转换、HTTP 客户端、取消与错误处理、逐次用量记录已实现；本地模拟 API 已与循环及真实文件工具联调。见 [适配器记录](worklog/2026-10-17-deepseek-adapter.md)。
- Runner 进度：`scenarios/rpcx/runner.Run` 已串联目录准备、Agent 生成、独立 Evaluate 和补丁/报告交付；`cmd/harness` 负责组装模型。已验证状态与判定分离、失败证据、取消/超时、落盘错误、历史保留和补丁还原一致性。见 [Runner 记录](worklog/2026-10-17-single-repo-runner.md)。
- 执行后端：`internal/execution` 为 Agent 检查和 Evaluate 提供共享接口；Docker 固定本地镜像 ID、只读挂载、禁网、限制资源，取消/超时后显式删除容器。真实容器已验证边界、OOM/输出超限、回收以及正确/被拒绝候选的交付。见 [Docker 执行记录](worklog/2026-10-17-docker-execution.md)。
- 证据缺口：真实模型迁移、崩溃恢复、容器引擎中断后的遗留清理与独立离线评测尚未验收；本地后端仍只用于可信仓库，普通进程组回收不约束主动脱离的后代。容器边界测试不证明自主迁移能力。
