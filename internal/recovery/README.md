# 停止后的恢复检查

## 只读前置检查

`Check(ctx, Request)` 只检查恢复前置条件，不创建新 Run、不启动模型、不重开账本、不转换预留，也不执行资源清理。返回成功后写锁已释放，结果只是检查快照；实际恢复仍须在持锁期间重新验证。

请求必须提供原 Campaign 目录、全新独立审计输出目录、预期计划 SHA256 和可信 `RunInspector`。rpcx 使用 `runner.InspectRun`。同时必须注入可信宿主的 `VerifyResourcesStopped` 只读检查；没有该能力返回 `ErrResourcesUnknown`，不把成功取得文件锁当作资源停止证明。当前没有通用 Docker 静止检查或 `assume-clean` 选项。

实现只接受 Campaign v2 / `flock-v1`，拒绝旧格式、锁缺失/不安全、计划身份冲突、未支持的恢复事务、审计违规/缺口、来源漂移、缺账本及 HaltReason。旧 Campaign v1 仍可通过 `harness audit` 只读审计，但不能在这里补锁升级。

当前不选择 Target，也不判定新尝试名额、下一请求是否有足够余额或供应商能力是否仍有效；这些还需由后续实际恢复准入实现。检查通过不能等同于允许付费重跑。

持锁后先由宿主确认资源静止，再通过现有审计核对状态、证据和逐请求账目；最后复核头部、账本来源摘要及锁身份。审计报告的 `writer_state` 仍为 `not_checked`，因为 audit 本身没有取得写锁；不要把单独的审计报告误称为恢复证明。

本包位于组装层，依赖 audit/campaign/budget/writer，避免 campaign 反向导入 audit。将来恢复调度仍要复用 Campaign 的执行契约；这里的检查成功结果不会作为可长期复用的许可。

`go test -race ./internal/recovery` 使用受控模型、控制层任务与精确的无命令屏障；其中会 SIGKILL 本测试启动的 Campaign 子进程，再两次检查，断言源材料未变。它没有运行实际业务迁移或重试。指定 `HARNESS_WRITER_ARTIFACTS` 可以保留新输出目录；细节见 [M4.2a 工作记录](../../docs/worklog/2026-10-20-m4-recovery.md#10-m42a写者排他与只读恢复检查)。

## 恢复准备检查点

`Prepare(ctx, PrepareRequest)` 在同一写锁内执行完整检查，提交一份不可变的恢复状态；它会写 `recoveries/`，与只读 Check 不同。请求额外提供稳定恢复 ID，资源静止证明和 RunInspector 仍然必需。原 Campaign v2、budget v1 与 Run 保持原样，不开启新模型请求。

```text
recoveries/
  checkpoint-<id>.json    # before 原文字节（base64）、来源 pin、after 预算/历史
  head.json               # 单一提交点，绑定检查点摘要
```

`CommitState` 区分 Unknown、Pending、Prepared；初始未写入时可以为空。错误可以发生在文件已经发布之后，因此必须同时检查 error；有文件不等于已确认提交。相同 ID 重入幂等，已存在且不同内容永不覆盖；其他 ID、篡改或未知版本拒绝。发布后重新执行只读审计，报告保存在本次独立输出下的 committed 子目录。

After.Budget 是经过逐调用校验的 budget v2 数据：保留 RunIDs、累计 Calls/Known 及两级上限，reserved 转 unknown 并记录恢复 ID，关闭当前 RunID。它不是可继续生成的 Ledger，原 budget/budget.json 中的预留也不改写。后续激活必须完整采用检查点并累计全部占用，不能重建空账本。

After.Targets 单列 Recorded、Adopted、Interrupted、Unstarted；无首记录的 Run 残片为 Abandoned。已完整发布但未索引的 Run 可以采纳，仅有 Completed 报告的 Running Run 不采纳。来源是单 Run Campaign，当前不创建第二次尝试；多 Run 预算格式及历史结构仍需 M4.2c 激活才能形成端到端重跑能力。

运行 `HARNESS_RECOVERY_ARTIFACTS="$PWD/runs/recovery-checkpoints" go test -race ./internal/recovery` 可保留三次恢复准备 SIGKILL 与重入材料；测试只杀自己启动并确认 PID 的辅助进程。完整 rpcx 中断验收和原生命令用法见 [M4.2b 工作记录](../../docs/worklog/2026-10-20-m4-recovery.md#11-m42b恢复控制检查点)与[命令说明](../../docs/command.md#恢复检查点验收)。没有公开恢复生成命令。

## 激活与受控重跑

`Execute(ctx, ExecuteRequest)` 在原写锁内重新审计已提交检查点并校验资源静止，显式选择未完成 Target。首次 Plan 可选 `RecoveryPolicy{MaxAttempts, GenerationDeadline}`，次数包括首次，截止时间为绝对时刻；缺省不允许第二次尝试，历史计划不可后补。Completed 的任意质量结果不得重跑。原输入位置由 Runner 再认证；缺失时失败，不拿旧候选续接。

有新尝试时必须提供 Backend、Execute、NewModel 和可信 VerifyModel；后者重新核实原 Profile，不改变上界和材料身份，不代替真实付费授权。Targets 必须非 nil；空列表只汇总/采纳原终态，不创建模型或账本，也不要求重跑授权。

```text
recoveries/execution/
  activation.json          # 不可变选择、检查点 pin、Run ID/ordinal/前驱
  budget/budget.json       # 继承全部历史的预算 v2，仅本代修改
  started.json             # 收据与账本就绪后发布，之后才执行
  targets/<id>/runs/<run>/ # 新 Run，独占创建
  result.json              # 最后发布核验后的新旧结果和累计费用
```

本代串行执行，复用 Campaign.Execute、CheckAttempt 和共享执行槽位。新 Run 在调用宿主前绑定预算，rpcx 按预分配 ID 独占建目录。模型准入和执行 context 都受原期限约束。失败或取消仍保留记录；局部错误进入结果，完整交付的质量判定不重算。

同一选择的完整结果可只读重入，不调用模型工厂或验证回调；有分配而无结果返回 ErrInterrupted，默认不重开账本、不重复执行或补跑遗漏 Target。即便 activation 尚未发布的目录残片也阻塞，需后续明确处置。其他选择返回冲突。Execute 返回 error 时仍可能已有完整 result，必须同时查看 Result 和审计报告；不能把文件存在当成功。

这是 Go API，不提供付费恢复 CLI，不实现连续多代恢复或通用 Docker 清理。[具体取舍与验证](../../docs/worklog/2026-10-21-m4-controlled-rerun.md)。

## Docker 资源核对与清理

`CleanupResources(ctx, CleanupRequest{Request, Docker})` 仅接受首次计划已声明资源协议的 Campaign v2。它持原写锁，核对预期计划、完整静态审计和 Run 归属后，调用实时精确清理，再只读确认静止并发布后审计。活写者、旧协议、错误 engine、篡改或未知创建一律拒绝；不改旧 Run、候选或账目。首次审计在 OutputDir，实时结果在 cleanup.json，后审计在 after；错误时可能已清理一部分资源，必须保留诊断后重新核对，不能视为无副作用。

`DockerResourcesStopped(d)` 返回可供 Check/Prepare/Execute 在锁内使用的实时只读检查器。清理成功返回时锁已释放，不是后续操作的长期许可；恢复必须用该检查器重新验证。资源日志是持续增长的运维记录，检查点不冻结该子树，原业务/控制来源 pin 不放松。没有清理或付费恢复 CLI，没有全局 prune、assume-clean 或 Local 任意后代清理保证。

真实验收已验证 Docker 宿主 SIGKILL→活容器阻止恢复→静态审计/精确清理→重复清理不改记录→实时检查→Prepare→新 Run 实际 Docker 交付。使用固定迁移编辑模型；这不是一次真实供应商实验，原单次实验不追加尝试。[取舍及证据](../../docs/worklog/2026-10-21-m4-resource-ownership.md)。
