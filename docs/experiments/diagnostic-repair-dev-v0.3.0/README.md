# 诊断反馈定点修复协议 v0.3.0：离线流程校准

本版依据用户确认的[阶段调整设计](../../worklog/2026-10-25-diagnostic-repair-comparison.md#10-生成与验证分阶段的离线调整)，只校准实验驱动，不启动新的真实模型对照。[v0.2.0](../diagnostic-repair-dev-v0.2.0/README.md) 及其[真实失败记录](../2026-10-25-diagnostic-repair-real-model-01.md)保持原样。

## 唯一实验条件变更

[protocol.json](protocol.json) 新增 execution_order=generate-then-validate，版本 ID 更新，其余配置沿用 v0.2.0。[seed.json](seed.json)、[prompt.txt](prompt.txt)、[复核模板](review-template.md)与前版同字节；基线、规范、SDK 和生产门禁不变。

流程为：两组准备 → A 生成并保存 → B 生成并保存 → A 独立验证 → B 独立验证。两组对话、目录、缓存与预算仍独立，A 不收到诊断，B 只收到一次宿主起始诊断。不得向 B 暴露 A 的候选、对话或验证结果。生成阶段的 go_check 仍会执行，不等到两组生成结束才反馈；延后的是最终独立验证。

每组上限仍为 12 次生成、24 次工具、150 万 Token、3 分钟准备、5 分钟生成、6 分钟独立验证；宿主 28 分钟，最多尝试 60 秒收尾。受控模型只声明机制用量，不产生供应商消费。不延长时间，也不借用另一组额度。

## 阶段结果与失败边界

- Generated：已保存候选、原始对话和 artifacts/generation.json；独立验证尚未开始，checks_passed=unknown。
- Validating：正在验证已保存的候选，开始前重新核对候选、依赖及可信输入身份。
- Finished：验证流程结束，仍按原六项判据逐项解释，不自动代表完整修复成功。
- Error：该阶段执行失败；原生成快照、候选及费用保留，检查结果为 unknown。Prepared 但从未生成的另一组最终为 NotStarted。

生成记录只发布一次，独立验证更新 attempt.json，不改写 artifacts/generation.json。A 验证错误后整轮仍 Aborted，B 保持 Generated，验证未执行；不自动重试或继续穿越设施故障。输入漂移、共享账本错误、执行设施错误或材料发布失败仍停止后续工作，因此生成阶段出现故障时也可能没有 B 候选。本版只消除“A 最终验证先于 B 生成”的依赖。

原完整验证仍调用 translation.Evaluate，双配置、类型分析和真实到期检查均保留。超时或缺证据不补造质量 verdict，局部修复与原迁移成功率分开。delivery_evidence 默认 unknown，只有追加实际复核后才能解释完整修复结果。

## 离线入口与检查范围

本版 TestDiagnosticRepairExperiment 只支持 prepare、offline，明确拒绝 real；不读取 .env 或凭证、不查询 Models、不调用生成 API。是否开展新真实对照需另作收益决策，不以本次校准通过自动解锁。

[freeze.json](freeze.json)记录驱动构建/嵌入材料和本版协议输入的摘要。普通机制测试使用进程内模型与执行替身，注入真实 context 期限、验证错误、候选漂移及发布故障；这些检查不冒称运行了 Docker 或业务门禁。

从项目根可执行针对性机制检查：

```sh
go test ./scenarios/rpcx/runner -run '^(TestRepair|TestDiagnosticRepairExperiment)' -count=1
```

普通入口跳过，不进行 Docker 校准。现有 offline 模式可用受控模型执行真实 Docker 和完整生产门禁，但本单元不默认重复两轮完整门禁。其显式操作为：

```sh
mkdir -p runs/diagnostic-repair-v0.3-build
go test -c -o runs/diagnostic-repair-v0.3-build/repair.test ./scenarios/rpcx/runner
cd scenarios/rpcx/runner
HARNESS_REPAIR_MODE=prepare \
HARNESS_REPAIR_LAUNCH=../../../runs/diagnostic-repair-v0.3-build/launch.json \
../../../runs/diagnostic-repair-v0.3-build/repair.test \
  -test.run='^TestDiagnosticRepairExperiment$' -test.v
```

prepare 只检查冻结文件和二进制身份，生成独占回执。若另行决定运行完整 Docker 校准，可设置 HARNESS_REPAIR_MODE=offline、HARNESS_REPAIR_OUTPUT 为不存在且父目录已存在的新目录，使用同一精确测试名及 30 分钟测试超时。校准不证明模型自主修复或诊断增益；本版无真实模型执行命令。
