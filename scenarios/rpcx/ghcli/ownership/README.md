# GHCLI Body 资源证明原型

本目录是开发研究，不被 Harness、worker 或质量聚合导入，不输出 ReadyForReview。它使用 AST/go/types 检验一种有限模式是否足以解释响应资源转移，结果为 proven、violated 或 unknown；这些结果不能直接当作运行时判定。

输入是已经指定的 WithTimeout 派生、RPC 调用及完整类型信息。父 context 来源、30 秒期限、真实 SDK 调用身份和构建覆盖需由其他检查认证。本原型只支持同一函数内的错误退出、空响应/空 Body 退出、成功包装返回，以及一个未导出的 io.ReadCloser 包装器的 Close。未知 helper、其他资源参数、额外方法、重绑定或未消费的类型/字段用途保留 unknown。

proven 时给出四条资源去向，并生成只含 RPC 调用及返回的比较视图：移除本次已绑定的 context 实参，将已解释的退出结构还原为原响应/原错误返回。请求、操作名等其余实参保持；原 AST 不变。这个视图不包含函数前缀、dispatchHTTP、导入或全部声明，不能证明整文件业务保真。

构造测试与本机动态见证使用自己编写的边界，不读取私有控制答案或独立参考；SDK 的类型与动态行为来自已发布本地夹具的真实 client.go。读取已保存候选的可选测试只解析/type-check outbound.go，核对其调用/返回视图与公开基线，既不执行该候选，也不加载整个业务包。

普通类型/结构对照：

```sh
go test -race ./scenarios/rpcx/ghcli/ownership
```

可选动态见证（输出目录必须不存在，需要本机 Go，不用 Docker 或模型）：

```sh
HARNESS_GHCLI_OWNERSHIP_OUTPUT="$PWD/runs/ghcli-ownership-native-01" \
  go test ./scenarios/rpcx/ghcli/ownership -run '^TestRuntimeStudy$' -count=1
```

该测试将八组构造输入复制到新模块，以本地 replace、GOPROXY=off 执行原生 Go 测试。正确例、等价 helper 与返回 error 别名的例子的四项动态测试均通过；五种错误变体必须出现指定的动态断言失败，才算反例见证成立。编译失败、超时、无测试事件不能冒充发现行为错误。父测试通过并不表示各个错误变体的内部测试通过。

完整结果、取舍与后续接入边界见[工作记录](../../../../docs/worklog/2026-10-22-ghcli-bounded-proof.md#9-body-资源与剩余比较的原型2026-10-23)。原型独立存放也使它不进入当前门禁的嵌入材料；后续接入时将有限检查迁入 worker，避免长期维护两份实现。
