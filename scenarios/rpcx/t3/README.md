# rpcx T3 默认超时场景

这是 T3 默认超时迁移的数据发布 `rpcx-t3-data-v1`，迁移规范仍为 v0.2.0。它验证“原业务测试通过，不代表超时迁移正确”，供证据门禁及独立验收使用。Runner 已支持本规范；本目录的场景验收报告只表示数据验收结果。

## 输入与契约

三个本地模块只依赖标准库，replace 指向相邻 SDK；全部构建禁网。新 v1 的 Call 隐式派生 30 秒超时，v2 的 Call 接收 ctx 但不补期限。demo-orders 与旧业务基线字节一致，原 10 个测试未增加超时断言；旧 T1/T2 场景保持自己的 SDK、规范和补丁。

规范为 [v0.2.0](specs/v0.2.0.md)：每次 Reserve/Release 补回 30 秒上限，保留父请求更短期限、取消和值，返回后释放派生 context，并正确传播错误与订单状态。内存后端协作响应取消，SDK 不强制终止不合作函数。

| 补丁 | 原业务测试 | 独立快速参考检查的预期 |
| --- | --- | --- |
| correct-timeout | 10 个通过 | 32 个叶子测试通过 |
| missing-timeout | 10 个通过 | TIME-001、TIME-003；两个封装都漏超时和派生资源释放 |
| release-missing-timeout | 10 个通过 | TIME-001、TIME-003；只有 Release 漏迁移 |
| wrong-duration | 10 个通过 | TIME-001、TIME-002；3 秒不是默认 30 秒，也提前收紧了 5 秒父期限 |

manifest 在实际检查完成后用于比较预期。文件名不决定参考检查结论；任何执行错误、观测不足、必需测试未运行都不能当作预期反例通过。

## 运行

从项目根目录运行，需要本地 Git、Go 和已准备固定镜像的 Docker：

```sh
./scripts/verify-rpcx-t3.sh
```

默认核对 freeze.json 的 schema、数据版本及逐文件摘要，并执行完整验收。冻结清单覆盖 fixtures/specs/cases/oracle 下全部 27 份数据；正式引用后不原地变更，数据变化需要新发布。根 README 可维护；[验收工具](../acceptance/README.md)在目录外独立演进，实际源码随报告保存。

这 27 份数据与原 T3 冻结清单中的对应项逐字节一致。旧混合清单、34 份完整材料及原工具保存于 [_archive](../_archive/README.md)；历史报告继续使用原条件。版本边界见 [ADR-005](../../../docs/adr/005-versioned-evaluation-data.md)。

快速开发检查可显式使用可信宿主：

```sh
./scripts/verify-rpcx-t3.sh --development --quick --trusted-local
```

开发模式永不报告 accepted=true；quick 跳过实际到期，不能独自验收 M2.1。省略 trusted-local 则仍使用 Docker。正式模式和完整开发检查都包含新 v1 的真实到期，以及快速检查通过候选的 Place/Cancel 真实到期；候选的两项慢检查并发运行。慢观察 40 秒，Go 测试上限 90 秒，命令上限 3 分钟；每条 Docker 命令冷编译，总体验收需要数分钟。

## 证据与边界

每次生成 runs/rpcx-t3-*，保存 report.json、分离的 stdout/stderr 日志、输入副本、候选、重放副本和独立参考副本。实际数据归档位于 inputs/_snapshot，工具材料保存为 materials/*.txt。schema_version=2 报告分别记录数据版本/输入摘要和工具摘要，并保存代码版本、镜像和资源配置、命令时长及退出码、原测试身份与事件、失败条款、补丁重放和输入不变检查。

参考副本保留候选生产文件，移除候选测试，安装 [oracle](oracle/README.md)；生产文件摘要必须与候选一致。候选测试、TestMain 或测试 helper 不参与参考检查。参考标准不是沙箱，任意生产代码伪造测试输出等对抗性行为不在本阶段证明范围内。

快速检查观测到的默认 deadline 必须位于调用前到后端到达期间所允许的 30 秒区间。观测窗口超过 250ms 记为 inconclusive，不能放宽成“有期限就通过”；这仍无法区分区间内微小偏差。慢检查补充真实到期，但只覆盖当前列明路径。

参考结果为 pass/fail/inconclusive，报告的 accepted 仅表示本场景验收完成，不代表 ReadyForReview。手工候选不能推算模型成功率或业务错误放行率；本目录是 L1 开发资源，不是未见留出数据。
