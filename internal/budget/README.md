# 预算不变量

模型调用通过 `NewModel` 绑定 Target/Run 与宿主核实的 Profile，真实调用前预留、返回后结算。账本位于 Agent 可见目录之外，同一模型不能重复包装。

## 调用与持久化

- 批次和 Target 同时受调用数及 token 限额约束。每次预留 `ContextTokens + MaxOutputTokens`，保持“已知消耗 + 在途预留 + 未知占用”不超过额度。
- 合法 usage 按实际结算；缺失 usage 保留整额未知占用。非法用量、冲突结算或持久化失败关闭新增准入，未知不能当零成本。
- 先持久化账本快照，再发布内存状态。结算独立于已取消的 Run context，在途调用仍需收尾。
- Loop 的调用尝试数与账本的准入数不同；实际消费与保守占用从账本读取。token 上界依赖宿主与供应商遵守 Profile，不是金额账单封顶。

## 恢复边界

`RecoverSnapshot` 只转换已校验的数据：保留历史身份和累计占用，将在途预留转为未知；它不写账或授权模型调用。

`Activate` 在原 Campaign 锁内独占创建新代账本，继承原限额和全部占用。不能重开旧目录、清零预算或重复绑定旧 Run；新绑定前须已持久化恢复收据。`CanAdmit` 只是余额检查，实际调用仍须原子预留。

API 约束见 [Model](model.go)、[Ledger](ledger.go)、[恢复转换](recovery.go)和[激活](activate.go)；跨包恢复顺序见 [recovery](../recovery/README.md)。设计依据见 [ADR-006](../../docs/adr/006-campaign-budget-reservations.md)。
