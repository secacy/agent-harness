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

这里的 covered 不等于规范全部满足：尚未证明 HTTP → service → inventory 的完整传播、错误与业务状态、所有其他构建配置或任意等价实现。吞错的返回分支仍可能匹配超时边界，必须由行为验证补足。本包不输出质量 verdict；[T3 组合门禁](../checks/README.md)已消费其事实，旧 v0.1.0 检查保持原策略。

## 基线与候选比较

```go
comparison, err := analysis.Compare(baseline.Report, candidate.Report)
```

输入须来自可信 Inspect。Compare 不重新读取源码、SDK 或磁盘材料，不认证任意外部 JSON；它检查记录结构及可比性，并绑定本次消费的两个 Report。调用方负责保存 Comparison，后续离线复核仍须验证原始材料。

分析报告使用 schema_version=2，新增 operation：通过类型信息求得的 RPC 方法字符串常量，如 Inventory.Reserve。方法值、动态表达式或无法确定的操作保留空指针；不会通过变量名猜值。旧 schema 1 报告仍是历史证据，需要当前工具重跑后才能参与比较。

配对先按包路径与 Go 函数完整名分组。两侧各只有一个直接调用、RPC 方法常量相同且候选为 v2，才生成 Match；不依赖相同行号、相同文件或调用总数。ctx 参数适配不改变函数身份，正常增行和同包文件移动可配对。v1→v2 与已有 v2 保留分别记录，候选原有 unknown、violation 及原因不会改写为 pass。

Changes 使用明确原因保留两侧位置：调用新增/删除、同函数多调用歧义、未知所属函数、间接调用、RPC 方法常量未知/变化，以及候选保留 v1。函数改名、移到另一个包或抽取 helper 可能表现为新增加删除；不猜测它们是否语义等价。基线完全没有调用也保留 baseline_calls_empty，需要外层明确适用性。

Gaps 保留双方原有缺口并标记 baseline/candidate。空 Changes 只表示在声明规则下建立了位置对应；接收者或请求参数变化可能仍能配对，不能据此证明业务正确。T3 质量门禁同时使用结构差异、原有分析状态、测试完整性与行为证据。

两侧需使用相同工具材料、Go 版本、平台、架构、cgo 和 tag 集合；tag 的顺序/重复不影响可比性。加载未完成、旧 schema、字段非法或配置不一致返回 error 和空 Comparison，不能伪装成候选违规。结果 schema_version=1，保存两侧输入/工具摘要与 record_sha256；后者是 Go encoding/json 紧凑序列化 Report 的摘要，不是 analysis.json 缩进格式的文件摘要。输出顺序稳定，源记录数组顺序变化仍会改变其绑定摘要。

## 分析范围与执行证据

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
