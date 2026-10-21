# M4：持久化窗口强杀与恢复验收

- 日期：2026-10-21。
- 状态：下述组件与单机受控恢复窗口已验收；M4 总体验收仍未通过。
- 依据：[收尾核对 R1—R3](2026-10-21-m4-closeout-review.md#2-对照原验收条件)。
- 目标：补齐基线之后、候选固定但验证前的新 Run 对照，以及预算预留后未调用、响应后未结算的真实强杀证据。

## 实施前取舍

Runner 已有包内观察函数，预算 Ledger 已有包内持久化替身。直接用磁盘轮询抢时机不能证明命中了结算之前；导出通用暂停配置又会扩大宿主 API 和生产流程的维护负担。因此 Runner 只增加 candidate_fixed 观察点；预算在本包测试中复用 persist：真实快照写入并同步后暂停预留，在底层模型返回后、结算快照写入前暂停结算。没有全局 hook、暂停 CLI 或新的公共接口。

代价是预算两个窗口在组件层验收：真实 Model.Complete→落盘→SIGKILL→RecoverSnapshot→Activate→新 Run 预算绑定，而非整条 rpcx Campaign。Runner 窗口另走真实恢复 Prepare/Execute 和独立门禁。两类证据分列，不把组件验收冒称完整业务链或真实供应商消费。若发现层间语义不一致，先停验收，不能用新增配置掩盖问题。

## 预期断言

| 窗口 | 强杀前可观察条件 | 强杀后与恢复行为 |
| --- | --- | --- |
| baseline_passed | 真实基线通过，第二 Target 未调用模型 | 保留已完成同伴，原锁内准备后从固定基线新建 Run；费用累计、原树不变 |
| candidate_fixed | 模型已停止、候选摘要已计算且输入复核完成，尚未进入独立验证 | 通过管道核对候选摘要；磁盘 PhaseValidating 但无终态/验证报告，不能误报完成；新 Run 完整重跑 |
| reserved_before_model | reserve 的实际快照已同步，底层模型调用计数为 0 | 原 reserved 转为整笔 unknown，不因已知测试模型未调用而退款；新 Run 继承账户/批次占用，旧调用不能重新绑定或结算 |
| response_before_settlement | 底层受控模型返回完整 usage，persist 收到 settled 候选但尚未写入，磁盘仍 reserved | 内存回复随强杀丢失，恢复仅依据磁盘保留整笔 unknown，不能拿测试证人回复补结算或退款 |

预算组件另设额度恰好耗尽的对照，断言新模型不调用；检查相同恢复转换幂等、重复激活拒绝、旧快照原文不变。子进程通过专用 FD 发确认后阻塞，父进程匹配自己启动的 PID、复读磁盘后 SIGKILL 并等待回收，不用 sleep 猜测窗口。

candidate_fixed 的摘要通过受控测试管道传递；原生产流程此时只落 PhaseValidating，候选 pin 尚未写入 run.json。不为测试修改该发布契约，审计不得把内存证人当已发布完成依据。

## 验证与交付

计划执行预算组件竞态测试、Runner 新窗口真实强杀/重跑、受影响核心回归及静态检查；运行产物放在 `runs/m4-windows-01/`。不读取 .env、不发 API、不接触 Docker；引擎不可用后的恢复另作下一单元。

本步不新增多代恢复，不改冻结规范/数据或旧实验；Git 保持未暂存、未提交。验收前不将里程碑缺口标为关闭。

## 实际实现与范围

生产代码仅在 Runner 完成候选摘要计算和输入复核后调用一次包内观察函数，公共入口仍传 nil；候选和终态的落盘顺序不变。预算生产代码没有修改。测试分别保留真实磁盘快照、PID/管道证人、SIGKILL 退出证明、恢复账目与最终材料。

Runner 新窗口强杀前复读同伴终态、当前 Run 和基线、逐目标账目、候选树；确认没有 evidence、validation cache 或最终报告。Prepare 后还要求原运行 audit 为 incomplete、当前 Target 为 Interrupted 且没有可采纳完成结果；Execute 后断言新 Run、ReadyForReview、两次受控调用、累计费用和整个旧树不变。已完成结果再次进入只读返回，不创建模型。

预算两窗口各有正常额度与恰好耗尽对照，共四次真实 SIGKILL。磁盘原始值都是 Calls=1、Reserved=100；预留后未调用的模型次数为 0，响应后未落盘为 1 且测试证人有完整 usage=20。二者恢复后都为 Calls=1、Unknown=100，证人不用于退款。余额充足时新 Run 一次调用后为 Calls=2、Known=20、Unknown=100；耗尽时新模型零调用。相同结算幂等，尝试拿旧调用补结算触发 ErrIntegrity 并封闭后续调用，累计占用不减少。

这补上了 R3 所列精确窗口的预算组件证据；它使用 RecoverSnapshot/Activate 而非 recovery.Prepare/Execute，不声称新增了这两个点的完整 Campaign 实验。Runner 的基线后、候选前验收及既有模型在途/报告后对照继续走完整恢复宿主。

## 验收结果

| 验证 | 实际结果 | 证据（runs/m4-windows-01/） |
| --- | --- | --- |
| 预算首轮 | 全包竞态回归通过；四个真实强杀组件场景均保留未知占用 | budget-first.jsonl、budget/*/evidence.json |
| 四个核心包最终竞态回归 | budget/campaign/recovery/audit 合计 289 项测试及子测试通过，5 个辅助/可选入口跳过；没有竞态报告 | core-final.jsonl；预算四份最终 evidence 位于 budget-final/ |
| 两个新增 Runner 窗口首轮 | 基线后、候选固定窗口均完成强杀和新 Run 交付；父测试加两子测试共 3 项通过 | rpcx-first.jsonl、rpcx/*/evidence.json |
| 最终完整 Runner 矩阵 | 六次真实宿主 SIGKILL，父测试加六子测试共 7 项通过，约 246.5 秒，无竞态；包含新增的未完成候选不采纳断言 | rpcx-final.jsonl、rpcx-final/*/evidence.json |
| 既有公共入口和审计 CLI | 两包 14 项测试及子测试通过，包括 nil 观察函数下的候选交付与历史保留、五 Profile 审计和 CLI 参数/退出码 | regression.jsonl |
| 原生命令 | 新基线后、候选固定两个归档均退出 0，API/原生报告一致；原根 incomplete、新代次 completed，全树字节及成员不变 | native.json、native-*/audit.json |
| 静态与构建 | budget/runner 的 go vet、原生 harness 构建通过 | vet.log、build.log |
| Linux | budget 和 runner 测试二进制交叉编译通过，未在 Linux 运行 | linux-*.log、linux-*.test |

Runner 基线后场景旧费用 Known=20，新 Run 两次调用后总 Known=60；候选固定场景从 40 增至 80。新 Run 均 Completed/ReadyForReview，旧控制/Run/缓存全树不变。这些 Token 和迁移是脚本化受控模型的机制数据，不计真实模型效果；原实验 A=0 与全部判定保持。

首轮预算和两个 Runner 案例均通过；随后补充“冲突后零准入”和“Prepare 不采纳未完成 Run”断言，分别由最终核心与完整 Runner 矩阵覆盖。各轮证据用新目录保留，不相加扩大检查点或样本数量。本轮没有生产逻辑缺陷修复；新增观察点不改变原序列化、预算或质量判定规则。

## 完成判断与下一步

R1/R2 所列基线后、模型在途、候选验证前、报告/终态窗口在声明的单机受控范围有识别/恢复证据。R3 的两个精确持久化窗口已补齐预算组件强杀、保守转换和新账本准入；完整 Campaign 与预算组件的证据边界继续分列。尚未证明真实供应商网络、掉电或任意后代进程可靠性。

下一项先设计真实引擎连接故障注入：仅影响被测客户端或使用独占测试引擎，验证不可用时阻塞、恢复后按归属清理和准入；不停止用户共享 daemon。正式评测冻结、M3 原人员口径与实际留出缺口保持，不能宣布 M4/M5 通过。

最终源码/文档/证据摘要及验证范围见 `runs/m4-windows-01/verification.json`。初始登记的 689 份仓库文件仅有列明的 10 份已有代码/说明改动，另增本记录与预算组件测试；其余 679 份摘要保持，冻结数据、协议、oracle 和历史实验未改。Git 保持原 HEAD，未暂存、未提交。

八份变更文档的 220 个本地链接、39 个锚点及代码围栏、Go 格式、git diff --check 均通过。没有读取 .env、请求真实 API、连接 Docker 或覆盖历史运行产物；Linux 结果仅为交叉编译，不能计为执行验收。
