# 场景共用验证机制

`gate` 提供文件身份、路径/模块检查、测试事件观察和命令取证，业务验收规则仍由具体场景维护。

[Recorder](command.go)由单次验证独占，`Commands`、`Evidence` 指向该报告的切片，`Binding` 固定规范、基线和候选身份。Save/SaveJSON 成功落盘后才追加证据；Run 保存执行结果、两份原始日志和命令 JSON，并在执行前后核对显式挂载输入。

场景提供受信任的输出路径、命令、挂载、缓存及带 Environment 的执行器。Run 保留业务非零退出供上层归因；取消/超时、设施异常、资源超限、写入失败和输入漂移返回错误。已返回的诊断仍尝试保存。该类型不支持并发写同一报告，也不决定质量判定。

`checks.Command` 是公共 Command 的别名，保持既有 JSON 字段。公共源码进入新验证器材料摘要；旧报告按其归档身份读取。实现取舍与本轮验证见[研发对齐记录](../../../docs/worklog/2026-10-24-rd-alignment-review.md#7-命令取证公共实现)。

机制测试：

```sh
GOCACHE="$PWD/runs/go-cache" go test -race ./scenarios/rpcx/gate
```
