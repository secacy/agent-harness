# 只读审计核心

`Inspect(ctx, Request, RunInspector)` 检查已有 Campaign 的磁盘记录，写入全新的独立目录。调用方先停止原写者，传入绝对路径；输入与输出不能互相包含。实现不初始化模型或执行后端。

M4.1a 已支持 Campaign v1、预算快照 v1 与 Run v2 的归属/状态信封。plan、Target、summary 沿用 Campaign 的版本契约，Run 的业务交付材料通过可信宿主函数扩展。没有场景函数且存在 Run 时，结果包含 `run_inspector_missing`，整体为 `inconclusive`。

当前 `campaign-audit-v5` 另支持 Campaign v2：仍按单 Run 契约核对，增加写锁协议声明和原始空常规锁文件的来源检查；还会检查 recoveries 中的恢复准备检查点、其 before 原文/来源 pin、确定性 after 状态和 head 摘要。它不取得文件锁，`writer_state` 仍是 `not_checked`；[恢复检查](../recovery/README.md)另行取得锁并要求资源静止能力。工具版本升级不改写旧报告或原质量判定。

`recovery_pending` 表示目录或检查点尚未提交；`recovery_prepared` 表示 head 完整绑定；是否执行另外检查。原 Campaign 的 execution_state 保留，不把检查点中的 Interrupted/Adopted 当成根目录已被恢复。检查点预算 v2 允许历史多 Run 身份，当前根账本仍只支持 v1。新增检查只证明数据关系，不增加业务语义结论。正常临时发布文件按未提交残片处理；额外文件、冲突检查点、来源漂移、篡改 after/head 均不得得到已准备结论。

读取、判断和发布按以下顺序执行：

1. 从固定相对布局读取记录，登记文件摘要、缺失路径和相关目录成员。
2. 核对身份、摘要引用、发布前置条件及终态；从每条调用重新计算两级预算。
3. 复核来源，将依赖漂移或未完成复核的发现降为证据缺口。
4. 写入 `audit.json`、`sources.json`、`audit.md`，最后发布带这三份文件摘要的 `published.json`。

`Report.Consistency` 区分 `pass`、`violated` 和 `inconclusive`；`ExecutionState` 另行描述发布状态。合法未完成记录可以一致性通过，预算中的 reserved/unknown 仍保持占用。`InputsUnchanged` 的 null 表示无法完成来源复核，`WriterState` 固定为 `not_checked`。

来源中的违规和缺口通过 Report 返回；配置、取消或报告发布失败返回 error。调用方需要同时检查这两个结果。报告完整性由最后的发布标记及文件摘要确认，不能只看 `audit.json` 是否存在。源码目录不写锁、缓存或修复标记。

View 只读取根目录内的常规文件与目录，拒绝内部符号链接、路径越界和特殊文件；单文件 16 MiB、最多登记 100000 项来源、首次保留文件内容总计 512 MiB，JSON 嵌套不超过 128 层。来源复核需要再次读取，不能把内容规模上限理解成总 I/O 字节数。未引用缓存不递归读取。宿主函数必须经同一个 View 读取，并给发现项列全依据；该接口用于可信宿主代码，不是 Go 代码沙箱。

rpcx 的五种 Profile 已由 [Runner 场景函数](../../scenarios/rpcx/runner/audit.go)接入，CLI 用法见[命令说明](../../docs/command.md#只读审计已有-campaign)。已完成五个受控 Unix SIGKILL 窗口及正常完成对照，具体范围见工作记录第 12 节；恢复与重试仍待。摘要链只证明已读取材料自洽，不认证历史真实性、不重判迁移质量、不授予恢复或释放预算的许可。设计、取舍和本单元验收见[工作记录](../../docs/worklog/2026-10-20-m4-readonly-audit.md)。

运行构造快照及边界对照：

```sh
go test -race ./internal/audit ./internal/budget ./internal/campaign
```

`TestAuditArchive` 默认跳过。指定 `HARNESS_AUDIT_CAMPAIGN` 可对已有归档做额外只读检查；`HARNESS_AUDIT_OUTPUT` 可指定全新报告目录。这是开发验收入口，仍只检查核心范围，不代表已有业务审计命令。

## 恢复执行代次

`recoveries/execution` 由首次计划的 RecoveryPolicy 授权，当前只支持单代串行受控重跑。审计独立核对 activation 收据、started 标记、继承账本、场景 Run 及 result；新调用必须属于收据且落在原绝对生成期限内。旧根 `execution_state` 保留历史状态，另用 `recovery_execution_state` 表示本代 incomplete/completed/terminal_with_failures；一致性无法确认时为 unknown。CLI 与 audit.md 同时显示两者。

原检查点与旧 Run 不变；缺少结果是可解释的中断，不能据此自动再执行。有完整结果还必须匹配实际新 Run 和账本，才可只读重入。缺口、违规和漂移依旧阻止恢复准入。[实现与验收](../../docs/worklog/2026-10-21-m4-controlled-rerun.md)。

## 资源记录与实时资源的区别

有资源协议时审计 registry、intent、dispatch、created、removed 的原文字节、计划/Run 归属及状态前提，来源漂移仍降级。resource_state 为 recorded_pending / recorded_removed；一致性不足为 unknown。该字段只表示记录，audit 不连接 Docker，不能据此认定当前静止。当前状态必须由恢复锁内的 DockerResourcesStopped 实时核对。控制记录暂存残片没有执行权限；未知创建不得凭缺失记录放行。

resources 运维子树随新 Run 增长，不纳入迁移检查点的不可变 pin；它由每次审计独立验证。原根/Run/验证材料的 pin 规则不变。协议和边界见 [ADR-010](../../docs/adr/010-docker-resource-ownership.md)。
