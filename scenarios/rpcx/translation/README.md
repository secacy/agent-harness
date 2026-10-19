# Translation 双配置迁移验证

在[冻结的翻译切片](../l2/data/rpcx-l2-translation-v1/README.md)上，按 [v0.4.0 规范](specs/v0.4.0.md)验证 rpcx v1 → v2。default 与 rpcx_alternate 是同一个 Target、Run 和候选的两个必需配置，只计一个开发样本。标签变体和 RPC/30 秒默认语义为评测准备植入，不能解释为上游原项目的自然迁移。

## 输入、检查与边界

`Evaluate(ctx, checks.EvaluationRequest)` 接收认证基线、候选、规范、空证据目录和显式执行后端，不读取 cases 或预期结果。宿主固定两种配置，不能通过任务参数省略 alternate。两配置分别执行 go list、构建、26 项基线测试/子测试、类型分析和可信行为检查。九项必需检查共同判定；任一配置明确违规得到 Rejected，未证明范围得到 NeedsAttention，全部通过才可 ReadyForReview。输入、依赖、命令或证据设施错误返回 error 与空 verdict。

生产范围检查只允许认证基线的有限 API/期限前缀适配。类型分析保留两份报告中的非活动文件缺口；只有已知互斥文件能由另一必需配置对应，额外文件和其他缺口不能抹去。聚合保留所有配置的发现及证据，后面的 pass 不会覆盖前面的 blocked/unknown/fail。

可信副本删除候选测试，安装宿主适配的全部基线测试和[独立行为探针](probes/boundary_test.go.txt)。探针实际经过 usecase、tracing 和 WebAPI，观察父期限/取消/值、每次 30 秒上限与释放、请求/结果映射、错误链、RPC→Store 顺序；通过组在两种配置分别等待真实约 30 秒到期。这里的独立指不信任候选测试，仍不是 M5 的独立参考标准。

LICENSE、go.sum、原生成 mock 按字节保护；go list 另确认 mock 实际参与测试。完整测试结构包含不同接收者类型上的同名 mock 方法。允许必要导入/API 适配，不能删断言或改表数据。基线的 26 项中，15 项来自上游，11 项为新增边界检查；上游 mock 测试通过不代表真实传输已验证。

## Runner 与材料

宿主 `ProfileTranslation` / `rpcx-translation-t3-v1` 固定规范、数据、测试、保护路径和配置矩阵。通过 `runner.TargetFromTranslation(id, fixturesDir, specPath)` 构造显式 Target，再交给已有 Runner/Campaign Go API。两配置的启动检查全部通过后才调用模型。一次 Agent `go_check` 顺序运行两配置，共用工具期限；模型不能自行指定 tags。最终门禁在独立副本重新检查。

离线模块材料由宿主认证并只读装配，继续经过共享执行器与命令槽。Agent 文件根仅含 workspace、spec、rpcx-v1、rpcx-v2；校准预期、探针和最终证据不属于这些根。旧单仓库 CLI 仍只支持原 demo-orders 参数入口，没有新增 Translation 付费命令。

校准的 `evaluation.json`（Runner 中为 report.json 的 validation）引用 `configurations/<id>.json`、实际活动源文件、配置化命令/日志、类型分析及比较、测试结构和探针。每份证据绑定规范、基线与候选；工具身份覆盖本门禁、共用事件/文件实现、测试比较器及离线材料装配源码。Runner 另交付补丁、结构化报告和 review.md。

## 开发校准

[manifest](cases/manifest.json)、七份手写补丁和[冻结清单](cases/freeze.json)属于开发对照，不进入 Agent/运行时门禁，不用于计算模型效果或错误放行率。

| 候选 | 预期 | 关键依据 |
| --- | --- | --- |
| correct | ReadyForReview | 两配置的所有必需证据通过 |
| alternate-missing-timeout | Rejected | default 通过；alternate 的新增基线检查与独立探针发现缺少期限（TIME-001） |
| default-only-api | Rejected | default 通过；alternate 仍引用 v1，构建失败（BUILD-001） |
| mapping-helper | NeedsAttention | 两配置行为通过，等价 helper 超出有限生产结构证明（SCOPE-001） |
| background-fallback | Rejected | 两配置全部基线测试通过，独立探针发现请求值/父取消丢失（CTX-001） |
| protected-mock | Rejected | 测试仍通过，但生成 mock 被修改（PATH-001） |
| deleted-assertion | Rejected | 测试仍通过，但原断言被删除（TEST-001） |

常规测试不运行真实到期：

```sh
go test -race ./scenarios/rpcx/translation ./scenarios/rpcx/testintegrity ./scenarios/rpcx/analysis/worker
```

完整校准使用已准备的固定 Docker 镜像、禁网执行，不调用模型：

```sh
HARNESS_TRANSLATION_ACCEPTANCE=1 HARNESS_TRANSLATION_BACKEND=docker \
  HARNESS_TRANSLATION_OUTPUT="$PWD/runs/translation-calibration" \
  go test -v -count=1 -timeout=25m -run '^TestTranslationAcceptance$' ./scenarios/rpcx/translation
```

`HARNESS_TRANSLATION_CASES=correct,alternate-missing-timeout,mapping-helper` 可选主要对照；省略 BACKEND 使用可信本地模式，仅适用于这些开发夹具。Runner 确定性编辑、双配置 go_check、独立门禁、补丁重放及审核交付对照：

```sh
HARNESS_TRANSLATION_ACCEPTANCE=1 HARNESS_TRANSLATION_BACKEND=docker \
  HARNESS_TRANSLATION_OUTPUT="$PWD/runs/translation-runner" \
  go test -v -count=1 -timeout=15m -run '^TestRunTranslationDelivery$' ./scenarios/rpcx/runner
```

实施选择、失败记录及实际验收范围见[工作记录](../../../docs/worklog/2026-10-19-translation-evidence-gate.md)。
