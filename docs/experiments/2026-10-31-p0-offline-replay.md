# P0 离线回放验收（2026-10-31）

本轮验证 [P0 需求](../REQUIREMENTS.md#p0trace-与离线回放)的记录、控制流回放、失败归因与用量核对。未调用外部付费模型；历史源保持只读。结果不增加迁移成功样本，不改变历史判定，也不证明恢复或 P1 对照收益。

## 方法与材料

新增 Run 使用原生 JSONL trace；历史 Run 使用完整消息、工具结果和原预算账本驱动同一个生产 Agent Loop。原始供应商 finish 字段未保存，legacy 模式仅依据成功闭环不变量推导并显式披露；工具时间不补造。逐请求消息和工具定义必须匹配账本的 `input_sha256`，usage 与实际 call 一一对应。

回放随后验证保存的规范、Profile、基线、候选与补丁身份，校验门禁 evidence，并使用原宿主策略和 `quality.Decide` 重新聚合 verdict。回放不会执行模型、工具、候选测试或行为探针。设计、持久化边界及兼容限制集中见 [P0 设计](../worklog/2026-10-31-p0-trace-replay.md)。

本次五项均为需求调整前已有的真实模型 Run；选择包含原三种质量判定和两个实验协议。没有用新生成记录替换历史样本。

| Run / 原轮次 | 原终态 | 原判定及回放判定 | 模型尝试 / 工具 | 已知 token | 门禁 evidence |
| --- | --- | --- | --- | --- | --- |
| `rpcx-agent-3621433355` / fixed GHCLI | Completed | Rejected / Rejected | 21 / 31 | 331,577 | 33 |
| `rpcx-agent-4243529763` / fixed Goddd | Completed | NeedsAttention / NeedsAttention | 40 / 66 | 708,115 | 66 |
| `rpcx-agent-2724386201` / fixed Translation | Completed | NeedsAttention / NeedsAttention | 37 / 61 | 639,944 | 79 |
| `rpcx-agent-1147620506` / fixed Workouts | Completed | ReadyForReview / ReadyForReview | 22 / 38 | 206,331 | 41 |
| `rpcx-agent-323326691` / GHCLI v0.2 | Completed | NeedsAttention / NeedsAttention | 17 / 25 | 140,909 | 34 |
| 合计 | 5/5 一致 | 5/5 一致 | 137 / 221 | **2,026,876** | **253** |

137 次模型尝试均已准入，逐项账本匹配，预留/未知占用为 0。没有冻结价格依据，金额为 null，不能把 token 换成未核实的账单数字。

## 命令与原始证据

四项 fixed Run 位于：

```text
runs/l2-fixed-eval-v0.1.0/campaign/targets/ghcli/runs/rpcx-agent-3621433355
runs/l2-fixed-eval-v0.1.0/campaign/targets/goddd/runs/rpcx-agent-4243529763
runs/l2-fixed-eval-v0.1.0/campaign/targets/translation/runs/rpcx-agent-2724386201
runs/l2-fixed-eval-v0.1.0/campaign/targets/workouts/runs/rpcx-agent-1147620506
```

第五项为 `runs/ghcli-dev-eval-v0.2.0/campaign/targets/ghcli/runs/rpcx-agent-323326691`。账本分别为两轮 `campaign/budget/budget.json`。原材料不随 Git 提供。

```sh
go run ./cmd/harness replay \
  --run runs/l2-fixed-eval-v0.1.0/campaign/targets/ghcli/runs/rpcx-agent-3621433355
```

对其余目录分别执行同一命令。无需密钥或 Docker，自动定位同一归档层级的账本。输出 `mode=legacy-completed`、`ledger_matched=true`、原终态/判定及推导字段说明。

本机原始验收保存于 `runs/p0-historical-replay-2026-10-31/`：

- `summary.json`：五次命令、退出码及原始结果。
- `<run-id>.stdout.json` / `.stderr.log`：逐项 CLI 输出；`<run-id>.final.json` 和 `final-summary.json` 另保留最终计量字段版本的输出。
- `source-before.json` / `source-after.json`：五个 Run 子树（排除缓存目录，包含宿主材料与 evidence）和两份账本，共 **31,896** 个文件的 SHA/size；前后完全一致。
- `failures.txt` / `failures.json`：相同五项的归因表与逐 Run 结果。

这些运行产物被 Git 忽略；本文保留可定位的命令、计数与限制，不以产物目录存在代替核验。

## 失败归因

对上述五目录使用重复的 `failures --run <dir>` 参数，得到：

| 主因 | 计数 | 占全部 Run | 占终态未成功数 |
| --- | --- | --- | --- |
| 门禁证明不足 `gate_incomplete` | 3 | 60% | 75% |
| 门禁拒绝 `gate_rejected` | 1 | 20% | 25% |
| 原流程放行 `success` | 1 | 20% | N/A |

分母是 5 个 Run、4 个终态未成功结果，未完成 0。五个执行过程都 Completed，但四个候选未自动放行；该表不是独立正确性评测，原 GHCLI 误拒等历史问题仍成立。编译、测试、预算和工具等类别在本子集计数为 0，不从缺少案例推断这些故障不会发生。

归因机制另用实际 Runner 的受控构建退出 1、测试退出 1、模型错误和设施错误核对，Replay 与 failures 分类一致。单元测试覆盖预算、门禁、工具、未完成、重复输入、丢失报告及零分母；缺失项不会从预定分母静默删除。

## 新链路与故障检查

| 验收点 | 实际检查 |
| --- | --- |
| 新 Run 统一记录 | 生产 Runner 自动创建 trace；agent、step、模型、工具、预留/结算和门禁埋点；原生回放核对唯一 Run 根、完整子 span、配置和最终材料 |
| 精确控制流 | 正常多工具及未知工具、模型长度/协议错、工具错、预算拒绝、非法末轮、取消/超时，录制后经原 Loop 重放 |
| 用量与调用边界 | 账本和 trace 的唯一 call ID、请求摘要、usage、状态与 revision 核对；未知不作零，拒绝不作已消费调用 |
| 写入失败 | 调用前记录失败不 dispatch；预留已落盘而记录失败保留占用；模型返回后记录失败仍结算已知用量，不重复消费 |
| 进程中断 | 两个自有辅助进程被 SIGKILL：一个确认记录后中断，一个附带残缺尾行；此前确认事件仍可读，开放 Run 不算回放成功 |
| 材料损坏 | 请求、工具 ID/参数/结果、usage、finish、控制边界、候选、策略、账本、预算引用及不完整 trace 的负例均拒绝 |
| 生产集成 | 受控模型经真实文件工具、本地构建与完整门禁交付，再离线回放；不新增模型调用，判定一致 |

相关测试位于 `internal/trace`、`internal/agent`、`internal/budget`、`scenarios/rpcx/runner` 和 `cmd/harness`。相关包竞态回归首次在 Runner 包触及默认 10 分钟总超时，未发生断言失败；其余五个包通过。最终改动的定向竞态回归与真实生产集成分别通过；原超时处及其后尚未完成的 21 项测试（含按条件跳过项）使用独立命令验证通过（152.739 秒），保留最初失败日志，不声称首轮命令全绿。测试日志：`runs/p0-tests-2026-10-31.log`、`runs/p0-final-focused-2026-10-31.log`、`runs/p0-final-runner-focused-2026-10-31.log`、`runs/p0-production-integration-2026-10-31.log`、`runs/p0-vet-2026-10-31.log`，以及 `runs/p0-runner-continuation-2026-10-31.json/.log`（余下测试选择及结果）。日常完整 Runner 回归需显式使用足够的开发测试总时限（命令文档设为 20 分钟），没有放宽候选检查或实验时限。

## 限制

历史失败或在途记录缺少原响应/错误时不支持完整回放；准备材料尚未完整发布或 trace 写入失效的 Run 保留诊断，但不能认证完整回放。完整历史样本的成功不消除这些限制。

回放固定外部结果，不重新验证候选代码行为，也不认证供应商实际收到了请求。摘要链发现损坏，不能抵抗整份材料被一致重写。新格式未提供跨版本迁移、压缩或轨迹轮转；大小上限触发后明确失败。金额、未绑定账本的实际准入和未知用量保持未知。以上不改变 P0 的五个历史 Run、归因统计与账本核对结果。


## 验收结论

P0 的三个验收条件均有实际证据：五个历史 Run 的执行终态和质量判定 5/5 一致；相同样本的失败归因有明确计数与比例；137 个唯一模型调用、2,026,876 已知 token 与原账本一致，预留/未知占用为 0。新采集链路的持久化、取消边界、错误回放与预算失败路径另有机制及生产集成验证。相关改动的竞态测试、`go vet`、`git diff --check` 与文档本地链接检查通过。

本工作单元到此结束。已明确披露的历史缺失字段、不可回放输入及金额未知保持原样；没有替换五个历史样本或修改验收标准，没有进入 P1/P2。
