# rpcx 的有限边界分析

`Inspect` 在显式提供的执行后端内加载目标模块并查询 Go 类型，返回生产包、SDK 调用位置、直接超时边界及覆盖缺口。可信分析程序与 `go list -deps -export -json` 在同一命令环境内运行，编译器导出数据随命令销毁，宿主只消费 JSON 事实。选择依据见 [ADR-004](../../../docs/adr/004-analysis-execution-boundary.md)。

```go
backend, err := execution.NewDocker(ctx, execution.DockerConfig{})
if err != nil {
    return err
}
result, err := analysis.Inspect(ctx, analysis.Request{
    ModuleDir:   "/work/candidate/demo-orders",
    SDKDirs:     []string{"/work/candidate/rpcx-v1", "/work/candidate/rpcx-v2"},
    EvidenceDir: "/work/evidence/analysis", // 尚未存在或为空，且位于输入外
    Backend:     backend,
})
```

调用方负责提供冻结输入、可信 SDK、context 时限和执行后端。Local 只用于可信夹具。分析过程只进行编译及类型查询，不运行候选 init 或测试；Docker 仍采用现有网络、权限、资源及回收限制。

支持的直接形态是：函数开始处从 context 参数调用标准库 WithTimeout，常量为 30 秒，随后注册对应 defer cancel，将派生变量传给真实 v2 Call；结尾直接返回 RPC 错误，或使用夹具中的 if err != nil 返回分支。绑定依据 Go 对象，支持导入别名、类型别名、跨文件字段、常量表达式，以及短声明复用原 ctx 参数。

`Call.status` 的含义：

- `covered`：整个函数匹配上述有限形态，直接 RPC 边界的派生、常量与 defer 关系有结构依据。
- `violation`：已匹配形态中的常量明确不是 30 秒，记录实际数值及 timeout_duration_mismatch。
- `unknown`：helper、间接调用、接口分派、未证明的父 context、资源释放或控制流等，并记录对应 Gap。
- `legacy`：v1 SDK 位置清单，用于之后与迁移候选比较，不表示已经证明 v1 的超时行为。

这里的 covered 不等于规范全部满足：尚未证明 HTTP → service → inventory 的完整传播、错误与业务状态、所有其他构建配置或任意等价实现。吞错的返回分支仍可能匹配超时边界，必须由行为验证补足。完整基线/候选位置比较与运行时质量聚合将在后续接入；当前不输出质量 verdict，也未接入旧 Evaluate。

生产文件必须被活跃包或 Gap 解释。未激活文件列为 inactive_build_file；完全未进入包清单的目录仍逐文件登记 unlisted_build_file。reflect/unsafe/plugin 导入保留 dynamic_import 缺口。接口同名 Call 可能需要人工消歧；其他已确认非 SDK 的具体方法不会列为 RPC。

`Report.status=complete` 表示分析完成，可以同时存在 unknown 或 violation。`load_failed` 保存加载退出码与诊断，没有包/调用结论；加载失败原因须结合独立构建与后端结果判断，不直接推导候选违规。启动、取消、OOM、协议非法、配置/摘要错误或输入变化返回 error，结果不提供可用 Report。

证据目录保存：

- `rpcx-analysis-tool/`：本次可信程序源码，材料摘要绑定两份嵌入文件。
- `inputs.json`：全部输入和工具文件摘要；输入总摘要覆盖业务模块及显式 SDK 根。
- `execution.json`：后端、镜像 ID、命令、容器 ID、退出码。
- `stdout.json`、`stderr.log`：实际输出，包括失败的部分材料。
- `analysis.json`：经协议与绑定检查的结果；宿主复查发现输入变化时撤回该文件。

每个根独立只读挂载，保持同级 replace 布局；目录名须不同且输入根彼此不重叠。每文件最多 1 MiB，合计最多 10000 文件、16 MiB。文件摘要和前后复查用于绑定证据，隔离能力由执行后端提供。

默认测试验证可信本地加载、冻结候选、支持/未知形态、配置缺口、故障和协议。真实 Docker 对照单独启用，命令见[开发说明](../../../docs/command.md#m23-隔离类型加载与边界分析)。
