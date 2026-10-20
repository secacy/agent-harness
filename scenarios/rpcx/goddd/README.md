# Goddd 迁移验证

在[冻结 Goddd 基线](../l2/data/rpcx-l2-goddd-v1/README.md)上验证 rpcx v1 → v2，使用 [v0.3.0 规范](specs/v0.3.0.md)。它是第一个接入 Runner 的半真实开发切片，保留 booking 接口、两层装饰器、HTTP 请求链和原业务映射，不代表上游项目自然依赖 rpcx。

## 输入与判定

`Evaluate(ctx, checks.EvaluationRequest)` 只接收基线、候选、规范、证据目录及宿主执行后端。基线/SDK 必须匹配 rpcx-l2-goddd-v1，候选与基线目录不可重叠；证据目录必须为空。规范、基线、候选及命令副本在执行前后核对，输入变化或设施错误返回 error 和空 verdict。

八项证据共同判定：依赖、保护路径、构建、22 项原测试及子测试的执行、测试结构、生产改动范围、类型化 RPC 对应与期限前缀、可信业务/请求/期限行为。确定违规得到 Rejected，必需证据不足得到 NeedsAttention，所有必需检查通过才是 ReadyForReview。有限 AST 适配支持当前认证调用链，等价 helper 重构仍可能需要人工确认。

mock 的完整文件允许既有回调形式，或 FetchRoutesFn 字段与调用同时增加 ctx 的固定形式。测试 helper 仅对已认证、未使用新增参数的位置支持 `ctx` / `_` 两种写法；其他参数改名和逻辑改动不作一般等价处理。`adaptations.json`、`adaptations/` 及两份测试比较报告保存变体来源和选择，`adapted-tests/` 与可信执行采用同一集合。具体范围见[适配设计与验收](../../../docs/worklog/2026-10-20-goddd-context-adaptation.md)。

可信生产副本剥离候选测试，安装由认证基线适配的原测试和[独立编写的行为探针](probes/boundary_test.go.txt)，检查参数与响应映射、远端错误/未知货物、父期限/取消/值传播、每次 30 秒上限及释放；正例还实际等待默认期限到期。这里的“独立”指不信任 Agent 与候选测试，**不是 M5 的独立离线参考标准**。

宿主 Profile 为 `rpcx-goddd-t3-v1`，通过 `runner.TargetFromGoddd(id, fixturesDir, specPath)` 构造显式 Target，再传入已有 `runner.Config.Target`。旧单仓库 CLI 的 ScenarioDir/SpecVersion 入口仍只接受原两版 demo-orders；本步没有新增 Goddd 付费命令。

宿主绑定离线依赖归档及清单身份，在生成前和独立验证时分别装配 GOMODCACHE，通过 Executor 包装器只读挂载并前后认证。缓存不是 Agent 文件根，不包含在 Target 的普通 SDK 输入中；Agent 仍只看到 workspace、spec、rpcx-v1、rpcx-v2。Docker 的命令槽、断网和回收约束继续生效；可信本地模式不提供容器隔离。

## 冻结校准集

[manifest](cases/manifest.json) 与六份手写候选补丁由[独立校准清单](cases/freeze.json)固定；不进入运行时门禁或 Agent 输入。这是开发对照，不能据此计算真实模型错误放行率。

| 候选 | 预期结果 | 关键证据 |
| --- | --- | --- |
| correct | ReadyForReview | 原测试、范围、类型化前缀及行为均通过 |
| missing-timeout | Rejected | 原测试通过，实际 RPC 缺少期限（TIME-001） |
| mapping-helper | NeedsAttention | 行为通过，等价重构超出生产结构证明范围（SCOPE-001） |
| background-fallback | Rejected | 父请求值/取消未保留（CTX-003） |
| deleted-assertion | Rejected | 测试仍通过，原断言被删除（TEST-001） |
| protected-license | Rejected | LICENSE 被改动（PATH-001） |

常规机制测试不等待真实 30 秒到期：

```sh
go test -race ./scenarios/rpcx/goddd ./scenarios/rpcx/testintegrity ./scenarios/rpcx/analysis/worker
```

完整本地校准显式启用，仅用于可信夹具；每次生成新目录，不调用模型：

```sh
HARNESS_GODDD_ACCEPTANCE=1 HARNESS_GODDD_OUTPUT="$PWD/runs/goddd-calibration" \
  go test -v -count=1 -timeout=15m -run '^TestGodddAcceptance$' ./scenarios/rpcx/goddd
```

增加 `HARNESS_GODDD_BACKEND=docker` 使用已准备的固定镜像；`HARNESS_GODDD_CASES=correct,missing-timeout,mapping-helper` 选择三类主要对照。Runner 的确定性编辑、go_check 构建/测试、独立验证、补丁重放及审核材料联调用同样环境变量运行：

```sh
HARNESS_GODDD_ACCEPTANCE=1 HARNESS_GODDD_BACKEND=docker \
  HARNESS_GODDD_OUTPUT="$PWD/runs/goddd-runner" \
  go test -v -count=1 -timeout=15m -run '^TestRunGodddDelivery$' ./scenarios/rpcx/runner
```

固定 context 变体的完整正例、错误时长、未知 mock 改动，以及直接回调传播探针使用同一开关：

```sh
HARNESS_GODDD_ACCEPTANCE=1 HARNESS_GODDD_BACKEND=docker \
  HARNESS_GODDD_OUTPUT="$PWD/runs/goddd-context-variants" \
  go test -v -count=1 -timeout=15m -run '^TestGodddContextVariantsAcceptance$' ./scenarios/rpcx/goddd
```

校准产物 `evaluation.json` 是逐检查、条款、证据与命令索引；Runner 另交付 `artifacts/candidate.patch`、`report.json` 和 `review.md`。实施取舍、失败轮次与实际验收见[工作记录](../../../docs/worklog/2026-10-19-goddd-evidence-gate.md)。
