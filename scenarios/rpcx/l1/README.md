# L1 v0.1.0：门禁与独立参考交叉验收

本目录是可审查的构造任务集，验证机制和已知边界，不是 M5 留出样本。输入为已冻结的 T3 v0.2.0，另有 19 个完整布局补丁、预期和人工规则依据。第一次正式运行前将 22 份数据材料写入 freeze.json；引用后不原地修改，新增数据另起版本。代码与文档可演进，实际驱动源码另按摘要保存。

- manifest.json：全部八项门禁状态、最终 verdict、关键 Finding 原因码/条款、独立参考构建和行为预期。
- patches/：从原 v1 布局开始的完整补丁，覆盖 demo-orders 及必要的 SDK 反例；每份有固定摘要。
- reviews.md：每项预期的人工依据及参考检查的适用范围。
- oracle/alternate_test.go.txt：额外请求变体，只用于离线参考，不交给运行时门禁。
- freeze.json：上述数据的精确清单和 SHA-256，不锁定任意宿主或防止恶意宿主重签。

旧 T3 的 34 份冻结材料保持原样。L1 使用其 32 项快速参考和两个真实到期检查，另加一个独立请求变体，总计 33 项必需快速叶子测试。没有复制运行时 behavior 探针，也不导入 quality 的判定函数来生成参考答案。

每个 case 都重新应用并重放补丁，比较完整候选布局。门禁与参考使用独立目录；门禁只收到基线、候选、SDK 和规范路径，不收到本目录的 manifest、人工预期或 oracle。参考剥离所有候选测试、安装独立探针和认证 SDK，核验生产字节与门禁候选一致。它会独立构建：构建失败时行为 blocked；非断言错误、缺事件、panic、OOM、取消和日志写入失败都作为执行错误保留。

每个 case 最多并行两条支路，减少两份真实到期检查串行等待的成本；case 之间串行。两条支路不消费对方结论，共享只读候选，目录、日志和测试文本分离。显式本地模式只运行本仓库受控夹具，复用 Go 编译缓存；默认 Docker 仍每命令新容器，不共享可写缓存。任何支路执行出错则取消另一支路并等待回收。

参考快检查通过后才运行真实到期，与 gate verdict 和 expected 无关。最后才比较两套实际结果与 manifest。原测试删除、跳过、保护路径及 SDK 修改可导致门禁拒绝，而行为参考通过：这不构成错误拒绝，人工规则依据另行说明。build tag 未覆盖时默认参考通过，也不说明所有配置正确。

特别保留 unprobed-branch：运行时已知路径通过，但有限范围检查为 unknown；独立新增请求发现 BEHAVIOR-001。门禁 NeedsAttention 与参考 fail 是预期的覆盖差异，不应被抹成通过。

验收同时要求冻结输入不变、全量选择、启用真实到期、预期全匹配且无执行错误。开发模式、快检查或子集永不 accepted，即使 matched=true。inconclusive 不等同通过，也不能因属于负例就算匹配。完整产物包含输入快照、驱动材料、补丁重放、Gate Evaluation、独立参考日志、逐项差异以及所有产物摘要。构造样例计数不代表真实迁移成功率或未见样本错误放行率。

从项目根运行：

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go run ./scenarios/rpcx/l1/cmd/verify
```

默认使用已准备的固定 Docker 镜像。受控本地验收显式添加 `-trusted-local`；开发子集再加 `-development -quick -cases=wrong-duration,shared-expired-context,deleted-test`。`-quick` 只省略参考慢检查，门禁仍执行自己的必需检查。命令输出新建的 runs/rpcx-l1-* 目录及 matched/accepted，异常或不匹配退出非零。

机制测试运行 `go test ./scenarios/rpcx/l1/...`，不运行完整门禁或等待 30 秒；重放全部补丁、验证清单及参考事件协议。正式运行成本和结果记录在 [M2 工作记录](../../../docs/worklog/2026-10-18-m2-evidence-gate.md)。
