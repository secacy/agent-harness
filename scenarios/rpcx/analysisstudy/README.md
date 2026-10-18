# M2.3 分析选型实验

这里保存可复跑的开发原型，全部实现位于 `_test.go`，没有运行时 API，不被 `checks` 或 `runner` 调用。它回答方法身份、常量识别以及验证方式互补的问题，不输出候选质量判定。

从仓库根目录运行：

```sh
env HARNESS_ANALYSIS_STUDY=1 GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test -json -count=1 ./scenarios/rpcx/analysisstudy
```

默认根模块测试会跳过实验。显式开启后，使用 `execution.Local` 在临时目录运行本仓库定义的可信样例，复制冻结 T3 v2 SDK，不读取模型凭证、不访问网络、不修改原夹具。不得用这个入口加载不可信候选。

- `TestIdentificationStudy`：同一份活跃文件的 AST 分别交给有限词法原型与 `go/types` 查询。覆盖 12 个源码样例、显式激活 tag 的对照，以及依赖缺失和取消。记录位置、常量、未知项、配置外文件、材料摘要和加载耗时。
- `TestComplementaryStudy`：运行正确、helper、吞错、未执行分支四个候选；再执行遗漏分支作控制。证明身份与常量清单不能代替行为验证，当前探针通过也不能代替覆盖分析。
- `TestTrustedCopyStudy`：比较混合候选测试与独立副本；包含正常、TestMain、测试 init、未激活测试和生产 init。检查实际测试事件、退出码及复制的生产文件摘要。

`STUDY` 行是结构化观察，`PROBE_FAILURE` 保留预期反例的实际测试输出。测试通过表示观察符合实验预期，包含候选检查失败；不能解释为所有候选都正确。样例不是留出集，计数不用于计算模型成功率或项目错误放行率。

原型限制：只加载固定模块名的单个根包；只使用当前宿主平台和关闭 cgo 的配置；不分析完整调用图、context 数据流或测试语义等价性。helper 的方法与常量可识别，不代表已证明 helper 的传播关系。函数值和接口分派保留未知；未激活文件单独列出，不能被活跃配置的成功覆盖。AST 原型支持的是明确列出的语法子集，其缺口不证明所有 AST 方案都不可行。

类型加载使用 `go list -deps -export -json` 和编译器导出数据，绑定本次离线构建结果；首个加载可能编译依赖。同一批样例共用隔离缓存，耗时不是冷缓存基准或 AST 与类型方案的公平性能对比。生产门禁还需验证 Docker 下的文件/导出数据传递、工具链和目标配置一致性。

设计依据与观察见 [M2 工作记录](../../../docs/worklog/2026-10-18-m2-evidence-gate.md#14-m23分析选型的最小对照)及[实验记录](../../../docs/experiments/2026-10-18-m2-analysis-study-01.md)。
