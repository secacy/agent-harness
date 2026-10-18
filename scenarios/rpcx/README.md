# rpcx T1/T2 迁移场景

这是公开、可离线复现的开发夹具，使用三个独立 Go module，全部只依赖标准库。SDK 是内存模拟实现，不启动网络服务。

| 目录 | 职责 |
| --- | --- |
| `fixtures/rpcx-v1/` | `example.com/rpcx`，`NewClient` 与无 context 的 `Call` |
| `fixtures/rpcx-v2/` | `example.com/rpcx/v2`，`Dial` 与带 context 的 `Call` |
| `fixtures/demo-orders/` | v1 迁移前基线，运行时只复制、不原地修改 |
| `specs/v0.1.0.md` | 已引用后冻结的 T1/T2 迁移规范 |
| `checks/` | Go 验证工具与嵌入的可信 context 探针 |
| `cases/` | 公开补丁和预期判定；只供场景测试使用 |
| `oracle/` | 独立离线评测的预留位置，当前没有保留评测集 |

## 运行过程

从项目根目录执行 `./scripts/verify-rpcx-t1-t2.sh`。每次创建新的 `runs/rpcx-t1-t2-*/`，预检两个 SDK 与 v1 业务基线，再分别复制整个 fixtures 布局、检查并应用补丁，调用单候选 `Evaluate` 接口，最后比较实际结果与用例预期。

基线业务代码约 360 行，共 10 个测试函数。`Place` 已有 context，`Cancel` 无 context；迁移需要同时适配 inventory、service、handler 和测试。SDK 的相对 replace 在运行副本中保持有效。`go test ./...` 不跨越嵌套模块，所以场景入口显式逐个测试三个模块。

Go 命令设置 `GOPROXY=off`、`GOSUMDB=off`、`GOWORK=off`、`GOTOOLCHAIN=local`、`GOFLAGS=-mod=readonly`，缓存位于 `runs/go-cache/`。入口不接受任意候选仓库，只运行 manifest 列出的公开用例。

## 检查与预期

| 用例 | 构建 | 业务测试 | 关键专项结果 | 判定 |
| --- | --- | --- | --- | --- |
| `correct` | pass | pass | 全部 pass | ReadyForReview |
| `background-fallback` | pass | pass | context_propagation fail | Rejected |
| `missed-callsite` | fail | blocked | 忘记传递 handler 中的 Cancel 参数 | Rejected |
| `deleted-test` | pass | pass（9 个测试） | test_preservation fail | Rejected |
| `protected-path` | pass | pass | protected_paths fail | Rejected |

`manifest.yaml` 使用 YAML 1.2 的 JSON 子集，以标准库解析实现零外部依赖；当前读取器仅接受这种 JSON 语法，拒绝未知字段。每个用例必须声明六项检查和最终判定。实际检查与判定计算完毕后才与预期比较。

- `dependencies`：v2 依赖、版本、本地 replace、v1 导入清除、SDK 内容保持不变。
- `protected_paths`：比较 `internal/pb/` 的文件清单与逐文件 SHA-256，检测新增、删除、改名和修改。
- `build`：构建全部候选包。
- `baseline_tests`：运行候选中的业务测试，保存 JSON 测试事件。
- `test_preservation`：比较原始测试的包路径＋测试名、原有子测试事件，要求实际执行且通过，拒绝删除和跳过；允许新增测试。
- `context_propagation`：注入可信探针后，从 Place/Cancel HTTP handler 到 RPC 后端检查值、deadline、预先取消、执行中取消与订单状态；另做禁止断开 context 的有限语法检查。

`pass` 表示有通过证据；`fail` 表示有明确违反约束的证据；`blocked` 表示前置条件未满足或验证超时；`not_run` 表示未执行。有明确失败则 `Rejected`，否则存在证据缺口则 `NeedsAttention`，六项全部通过才 `ReadyForReview`。

## 产物与证据边界

运行目录保存规范及 manifest 副本、各用例补丁、候选工作目录、构建输出、命令日志与 JSON 报告。报告记录 Go 版本、命令参数、退出码、耗时，以及规范、manifest、验证材料、夹具、补丁和候选文件树的 SHA-256。每个用例的验证副本、构建输出与验证日志位于 `<case>/evaluation/`；报告中命令记录指向确切日志。验证器核对输入与验证副本摘要，最后由用例驱动程序确认原始夹具未变化。

这些检查只证明本夹具的已列行为。保留测试名和通过状态无法证明所有断言未削弱；context 语法检查不做通用数据流分析。公开补丁与专项探针不能作为独立效果评测集。

公开脚本仍用于可信本地夹具。工作副本、只读约定和摘要检查本身不提供恶意代码沙箱；M1 的 Agent Runner 已接入文件工具限制与共享 Docker 执行后端，检查默认在受限容器运行。直接使用可信本地后端仍受宿主进程组能力限制，不能把摘要校验视为执行隔离。

## 单候选验证接口

`checks.Evaluate(ctx, req)` 接受已准备的候选目录，可以在没有 `cases/`、manifest 或 `oracle/` 的目录中独立调用。它不应用补丁、不比较 expected、不创建 Run；调用方负责最终报告保存。

```go
evaluation, err := checks.Evaluate(ctx, checks.EvaluationRequest{
    BaselineDir:  "/work/baseline/demo-orders",
    CandidateDir: "/work/candidate/demo-orders",
    SpecPath:     "/work/specs/v0.1.0.md",
    EvidenceDir:  "/work/evidence",
    CacheDir:     "/work/go-cache", // 可选，默认 EvidenceDir/go-cache
})
```

基线和候选各自需要相邻的 `rpcx-v1/`、`rpcx-v2/`。这是当前场景的输入布局约定；验证器没有依赖项目根目录的位置。相对路径以调用进程工作目录为准，输出路径不能与输入重叠（包括符号链接别名），证据目录必须为空或尚不存在，避免覆盖历史证据。

接口只接受冻结 v0.1.0 规范的确切 SHA-256，结果保存规范版本与摘要。它重新运行可信基线测试以取得原有测试身份和事件，而不接受候选或用例提供的预期集合。基线和候选命令均在证据目录下的验证副本中执行，SDK 使用基线侧的可信副本；候选侧 SDK 如有修改，依赖检查仍会拒绝它。

返回的 `Evaluation` 包含基线/候选摘要、六项检查、命令证据与质量判定。候选构建或测试失败返回 `Rejected` 和空 Go error；基线失败、命令无法启动、规范不支持、输入变化、取消或命令 deadline 耗尽返回非空 error，保留部分证据并清空最终判定。父 context 传递到复制与命令执行；这不等于 Runner 的持久化状态或完整进程树回收已实现。


## M2.2 结构化证据

新生成的 Evaluation 与公开报告使用 schema_version=2；原六项检查和五个候选的结论保持不变。Check 保留 status/detail，并增加条款、适用性、Findings 和证据 ID；Findings 可记录原文件位置，删除位置指向基线。命令级结果不伪造代码行号。

Evidence 索引为每个材料保存相对路径、SHA-256，以及规范/基线/候选三项绑定。新增材料包括输入文件清单、测试身份和事件、宿主策略、可信探针及有限 context 语法结果；命令日志仍保留。input-snapshots.json 可复核保护路径与 SDK 差异，test-inventory.json 可复核哪些原测试实际执行。

聚合由 internal/quality 的普通函数执行，所需集合由宿主确定。缺项或解释/引用不足不会放行；完整的已知失败优先拒绝并保留其他缺口。材料摘要或绑定不符、重复 ID、路径越界或记录非法是执行错误，最终判定为空。独立接口不读取 manifest 的 expected。

公开报告各结果的 evidence_directory 相对运行根目录；Evidence.path 相对该目录。Runner 嵌入的 validation 使用 Run/evidence 作为根。字段升级不改变历史报告，也不补全旧版测试断言与调用覆盖缺口，T3 仍待后续运行时检查接入。

后续 [analysis](analysis/README.md) 提供独立的类型加载与有限 RPC 边界事实，已通过本地和 Docker 验证，尚未接入本页的旧 Evaluate。它的 covered 是结构形态的结论，不能代替上述六项检查或完整 T3 判定。
