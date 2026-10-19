# M3.4：三个异构 Target 的确定性批次验收

- 日期：2026-10-19。
- 状态：已完成本工作单元的本地与 Docker 批次验收；不等于 M3 整体验收。
- 对应：[M3 多仓库批量闭环](../MILESTONES.md#m3多仓库批量闭环)、[批次设计](2026-10-19-m3-batch-runtime.md)、[L2 样本选择](2026-10-19-m3-4-sample-selection.md)。
- 代码基线：f12b6d9。

## 问题与取舍

Goddd、Translation、Workouts 先前各自通过单仓库 Runner，但同一 Campaign 从未同时接收三个不同 Profile。复制 demo-orders 的调度测试不能证明异构输入的工作区、规范、验证和预算归属正确。

本次沿用已有 `campaign.Run` 与可信的 `runner.CampaignExecutor`，新增显式验收测试，不另建批次命令或通用插件。正常批次用三个已冻结开发样本各自的正确补丁构造独立脚本模型；测试期参考补丁只用于生成编辑工具调用，不进入 Agent 可见根或门禁输入。这个选择能稳定比较并发前后的候选及证据，代价是不能据此计算真实模型自动成功率。真实付费配置仍缺供应商上界认证和批次入口，不能用任意填写的上界放开付费并发。

先为三仓分别运行单 Target Campaign 作参照，再以 `Concurrency=2`、`ExecutionConcurrency=2` 运行混合批次。第二个混合批次仅在 Translation 的第一条基线命令注入非零退出；Goddd 和 Workouts 不改输入、模型脚本或预算。比较排除时间戳、耗时和 Run ID，核对候选/补丁摘要、规范/基线摘要、验证器、材料摘要及各检查状态。最后用禁网 Docker 后端运行三仓正常批次，检查实际容器回收。

## 可复核结果

| 对照 | 实际结果 | 运行证据 |
| --- | --- | --- |
| 三次单独运行 | Goddd、Translation、Workouts 各 1 个 Completed / ReadyForReview；各 2 次受控模型调用、40 个模拟 Token | `runs/m3-4-heterogeneous/solo-*/campaign/summary.json` |
| 本地混合批次 | 3 个 Completed / ReadyForReview；6 次受控调用、120 个模拟 Token，无预留或未知占用；三个候选与单独运行的关键证据一致 | `runs/m3-4-heterogeneous/local.jsonl`、`heterogeneous-local-3561661123/campaign/summary.json` |
| Translation 基线故障 | 2 个 Completed / ReadyForReview、1 个 Failed / `baseline_failed`；故障 Target 零模型调用且无质量 verdict；其余两仓关键证据与单独运行一致 | `runs/m3-4-heterogeneous/local.jsonl`、`heterogeneous-fault-2288992171/campaign/summary.json` |
| Docker 混合批次 | 3 个 Completed / ReadyForReview；分别绑定 `rpcx-goddd-t3-v1`、`rpcx-translation-t3-v1`、`rpcx-workouts-t3-v1`，预算 6 次受控调用、120 个模拟 Token | `runs/m3-4-heterogeneous/docker.jsonl`、`heterogeneous-docker-222271727/campaign/summary.json` |
| Docker 回收 | 三份 Run 报告中的 46 个不同容器 ID 逐一 inspect 均不存在；Harness 标签容器清单为空 | `runs/m3-4-heterogeneous/docker-container-audit.json` |
| 最终本地竞态复验 | 单独、混合与故障对照全部通过；无测试失败或竞态报告 | `runs/m3-4-heterogeneous/local-race-final.jsonl` |

测试还检查每个 Target 的输入、Profile、Run、预算绑定及补丁重放；Translation 的两种必需配置和 Workouts 的 common 三项前置测试没有被混合批次省略。这里的 Token 来自脚本模型声明，只证明账本和归属机制，不是供应商用量或费用。故障为受控后端返回基线非零，不冒充实际业务红基线或强杀 Harness。

首轮本地与 Docker 批次之后，仅收紧了测试脚本对各自规范版本的提示检查；最终本地竞态复验覆盖该变化，生产执行与门禁代码未改。`go vet ./scenarios/rpcx/runner`、`git diff --check` 及本次改动文档的 119 个本地链接目标通过。所有运行材料保存在忽略的 `runs/`，不进入 Git。

## 边界与下一步

本轮三个样本仍是开发切片，正确补丁为预编写参考；尚未得到独立参考复核下的真实生成候选。因此不报告有效自动成功率、错误放行率或人工审核成本。独立留出名额仍待独立准备者，不计入样本数。下一工作单元应先固定独立于运行时门禁的参考标准与指标分母，再核实真实模型预算上界、开放付费批次入口并执行开发集实验；M4 的进程中断恢复另行处理。
