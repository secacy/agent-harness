# 里程碑

## M0：离线 rpcx T1/T2 场景夹具

- 状态：已验收（2026-10-17）
- 范围：三个本地 Go module、10 个业务测试、版本化迁移规范、五种候选补丁、公开专项验证与报告。
- 验收：SDK 与迁移前业务基线测试通过；正确补丁六项检查通过；四个反例分别被预期检查拒绝；原始夹具保持不变。
- 证据：[实现记录](worklog/2026-10-17-rpcx-t1-t2-fixtures.md)、[场景说明](../scenarios/rpcx/README.md)、`./scripts/verify-rpcx-t1-t2.sh`。
- 证据缺口：尚无 Agent 自主迁移、隐式超时迁移、可靠批量执行或独立离线评测能力；当前验证不提供恶意代码执行隔离。

## M1：单仓库 Agent 最小闭环

- 状态：部分实现；单候选验证接口已抽出，完整 M1 尚未验收。
- 目标：给定迁移前仓库和规范，由模型通过工具自主生成候选，再调用独立验证接口交付补丁与证据；不向模型提供参考补丁。
- 当前设计与任务：[最小 Agent 循环](worklog/2026-10-17-minimal-agent-loop.md)。已有执行与状态设计保留在 [Runner 契约](worklog/2026-10-17-single-repo-runner.md)，按实际闭环需要逐步实现。
- 实现顺序：单候选验证接口（已完成）→ 有界 Agent 循环 → 仓库工具和 DeepSeek 适配 → Agent 生成候选后独立验收。
- 首项任务：已实现 `Evaluate(ctx, EvaluationRequest) (Evaluation, error)`；独立于 cases manifest、补丁应用和预期比较，命令在验证副本运行，支持父 context 和规范内容绑定。验收细节见工作记录。
- 证据缺口：Runner 状态、产物发布、完整进程树回收及 Agent 循环尚未实现；单候选验证接口不作为通用 Runtime 已完成的证明。
