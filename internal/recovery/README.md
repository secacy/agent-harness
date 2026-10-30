# 恢复协议

本包组装停止后 Campaign 的恢复流程，当前支持单代受控整 Run 重跑，不提供 step 续跑。各操作重新取得原写锁并核对来源与资源；锁外检查结果不能作为后续操作的许可。

## 恢复检查

`Check` 只检查并写独立审计报告，返回前释放锁。调用方提供可信 RunInspector 和实时资源静止检查器；缺失、未知、账本 halt 或来源漂移均阻塞。旧格式不能通过补锁获得恢复权限。

## 恢复准备检查点

`Prepare` 在同一锁内检查并写不可变 checkpoint，再发布绑定摘要的 `head.json`。相同 ID 重入核对，冲突不覆盖；原控制记录和 Run 保持不变。

检查点保留累计预算与尝试历史，在途预留转未知；它不激活模型调用。`Pending` 与 `Prepared` 按提交标记区分，调用方同时检查返回值和 error，文件存在不等于确认提交。

## 激活与受控重跑

`Execute` 从有效检查点选择未完成 Target；新增尝试必须由首次计划的次数与绝对期限允许，完整终态不得因质量不佳而重跑。输入和模型配置由宿主重新核实。

发布顺序为“activation 收据 → 继承账本/Run 绑定 → started → 新 Run → result”。恢复不清零预算、不更换原输入与 Profile；旧 Run 不改写。

- `Targets` 必须非 nil；空列表只采纳/汇总原终态，不发起模型调用。
- 同一选择已有完整有效结果时只读重入；已分配但缺结果返回 `ErrInterrupted`，不自动重复执行。不同选择返回冲突。
- 失败时可能已有结果或部分发布，须同时查看 error、返回结果和审计。具体请求与错误语义见 [Execute](execute.go)。

## Docker 资源核对与清理

`CleanupResources` 在原锁内静态审计、精确清理、实时确认静止并发布后审计。失败可能发生在部分清理之后，不能假定没有副作用。

`DockerResourcesStopped` 是供 Check/Prepare/Execute 在锁内使用的实时只读检查器。清理成功返回后锁已释放；后续恢复仍须重新取得锁并检查。

设计依据：[写者排他](../../docs/adr/008-campaign-writer-ownership.md)、[不可变检查点](../../docs/adr/009-immutable-recovery-checkpoints.md)、[资源归属](../../docs/adr/010-docker-resource-ownership.md)。本协议不能直接作为未来 step 恢复的格式要求。
