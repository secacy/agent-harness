# T3 独立参考标准

本目录从 v0.2.0 规范编写行为参考测试，不导入 scenarios/rpcx/checks 的规则函数或复用原 context 探针。它由离线 acceptance 驱动使用，不加入 Agent 文件工具的可见根目录，也不由运行时 Evaluate 读取。

- deadline_test.go.txt：32 个快速叶子测试，覆盖 Reserve/Release 的默认/较短/较长期限、值、取消、返回后释放、连续调用，以及 HTTP 和 service 错误/订单状态。每个目标后端有明确计数，Cancel 的准备下单不计入目标 Release。
- expiry_test.go.txt：协作后端等待真实 ctx.Done，分别验证 Place/Cancel 的约 30 秒默认到期、错误身份和失败状态。每项有停止信号与有界回收；不能回收则让检查进程失败。
- v1_expiry_test.go.txt：独立验证新 v1 SDK 的隐式 30 秒实际到期。

测试模板按原文安装在独立副本，所有材料摘要保存于输入清单。生产代码摘要与候选绑定，候选 `_test.go` 不进入参考副本。测试通过必须具备明确的必需事件；单有退出码 0 不足以证明执行。

失败记录使用 REFERENCE_FINDING JSON 标记条款和原因；测量不足使用 skip 事件与 REFERENCE_INCONCLUSIVE，报告将其标为 inconclusive。未到截止时间就放弃等待、缺少事件、JSON 无效或 Docker 出错均不能解释为预期语义失败。日志还保存观测区间和实际耗时。

当前样例数据使用 reference-sku/http-sku/service-sku 等，区别于原业务测试的 book/2；到期测试另用 slow-sku/9。错误与状态通过 API 行为判断，不比较补丁文本。

独立实现并不消除共同盲区。本标准只覆盖列明配置和路径，尚无任意程序等价性、恶意代码防伪或其他构建配置保证。未来运行时 T3 探针必须独立设计并以反例核对盲区；不得把复用本目录的同一组测试重新命名为独立评测。L1 开发材料可审查，不作为 M5 留出样本。
