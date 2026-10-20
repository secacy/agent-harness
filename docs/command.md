# 开发与运行命令

以下命令从项目根目录运行，已在 Go 1.26.5 / macOS arm64 验证。最低声明版本为 Go 1.23，需要已安装的本地 toolchain、Git 和 POSIX shell。

## 完整场景验收

```sh
./scripts/verify-rpcx-t1-t2.sh
```

离线运行两个 SDK、v1 业务基线和五种候选补丁。输出本次 `report.json` 路径；预期反例被拒绝且其余预期均满足时返回 0。每次运行使用新目录，历史产物保留在 Git 忽略的 `runs/`。

## T3 超时场景验收

先按下文准备固定 Docker 工具链镜像，再运行：

```sh
./scripts/verify-rpcx-t3.sh
```

默认禁网 Docker 执行，核对 freeze.json 的冻结材料，再检查两个 SDK、业务基线、四个候选和真实 30 秒到期。四个候选均需通过原有 10 个业务测试；参考检查只有正确补丁通过，其余候选按条款失败。原 SDK 与正确候选的实际到期必须通过，报告才可出现 `accepted: true`。每次创建 `runs/rpcx-t3-*/`；冷编译及两组实际到期使完整验收需要数分钟，不访问模型 API。

开发期间可以执行快速联调：

```sh
./scripts/verify-rpcx-t3.sh --development --quick --trusted-local
```

该命令显式使用可信宿主，跳过慢检查，允许未冻结材料。匹配预期可返回 0，但 `accepted` 始终为 false；`--quick` 必须与 `--development` 一起使用。省略 `--trusted-local` 可在 Docker 中开发联调。正式模式下材料变化会失败，不自动更新 freeze.json。

当前入口使用独立数据发布 rpcx-t3-data-v1，验收驱动位于 scenarios/rpcx/acceptance；数据清单与工具源码分别绑定。旧 T3 完整快照及原清单保存在 scenarios/rpcx/_archive/rpcx-t3-v0.2.0，活动 27 份数据与旧项字节一致。模块路径调整造成的旧混合清单问题及处理依据见 [工作记录第 24 节](worklog/2026-10-18-m2-evidence-gate.md#24-冻结数据与验收工具的版本边界) 和 [ADR-005](adr/005-versioned-evaluation-data.md)。

报告包含输入文件摘要、执行后端与命令日志、原测试事件、参考检查 pass/fail/inconclusive、失败条款、补丁重放及生产代码绑定。预期反例的失败算匹配；未执行、执行错误或观测不充分不能冒充预期反例失败。这是 M2.1 离线场景证据，没有运行时质量判定；`cmd/harness` 的 T3 接入另见下文单仓库任务入口。检查边界见 [T3 场景说明](../scenarios/rpcx/t3/README.md)。

## 验证工具自身测试

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test -p 1 ./cmd/... ./internal/... ./scenarios/...
```

覆盖受保护路径新增/删除/改名/修改、测试缺失/跳过/未执行、专项探针未执行、context 别名、符号链接拒绝及判定边界。另在无 manifest 的临时目录中直接调用 `Evaluate`，验证输入不被修改、普通编译与测试编译失败的判定、规范绑定、证据目录约束、基线失败、命令启动失败，以及父 context 取消和 deadline 传播。

该命令仅测试根模块；接口集成测试会运行其临时副本中的业务测试，但不替代两个 SDK 和五种公开补丁的完整场景验收。完整验证使用上面的场景入口。

显式列出代码目录是为了排除 `runs/` 留存的实验源码；Git 忽略规则不会阻止 `go test ./...` 扫描这些文件。`-p 1` 限制包级并发，避免进程超时测试受编译争抢影响。

## 证据绑定与质量聚合测试

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test ./internal/quality ./scenarios/rpcx/checks
```

覆盖宿主必需集合、缺项、未知/跳过、不适用的许可与理由、fail 优先，以及证据缺失、篡改、错误候选绑定和路径约束。集成测试从真实 Evaluate 材料重算判定，再删除必需项或篡改日志；同时核对旧五用例的原状态、判定、失败条款与证据。

新 Evaluation 与公开场景报告使用 schema_version=2，新增 rule_ids、findings、evidence 和 gaps。单候选证据路径相对 EvidenceDir；公开场景每个结果的 evidence_directory 相对本次运行目录。Runner 报告内的 validation 保存该新结构，证据路径相对该 Run 的 evidence/。

每个证据绑定规范、基线与候选摘要，并校验实际文件 SHA-256。quality-policy.json 保存宿主策略副本，context-probe.go.txt 保存可信探针；materials_sha256 为紧凑 JSON 编码策略后紧接探针原始字节的 SHA-256，仅标识策略与探针材料，不是整个验证器源码摘要。verifier_version 是宿主维护的版本标识；公开报告的 checks_sha256 只覆盖场景 checks/，不能解释为整个 Harness 的源码摘要，复现仍需对应 Git 版本和工具链。

证据缺口得到 NeedsAttention；充分的明确失败得到 Rejected 并保留缺口；错绑定、材料篡改或记录非法返回执行错误与空判定。新结构不证明原检查未覆盖的语义，不把 v0.1.0 升级为 T3；v0.2.0 由独立的 T3 八项策略处理，不进入旧六项策略。技术依据见 [ADR-002](adr/002-evidence-bound-quality.md)。

## Agent 循环测试

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test ./internal/agent
```

使用模型和工具替身，检查消息关联、工具顺序、错误反馈、调用预算、取消和退出分类，同时执行 `ExampleLoop_Run` 示例。不访问模型服务，也不实际修改业务仓库。根模块 `go test ./...` 同样包含这些测试。

## M3.1 模型预算与单 Run 接入

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" GOMODCACHE="$PWD/runs/module-cache" go test -race -p 1 ./internal/budget ./internal/agent ./internal/deepseek
```

检查两级调用/Token 额度、并发争抢、FIFO 等待/取消、未知/非法用量、重复结算、存储故障、HTTP 前持久化和重定向拒绝。使用进程内模型与 localhost 模拟服务，需要本机临时端口权限，不需要 API Key。

Runner 接入另由 `go test ./scenarios/rpcx/runner -run '^TestRunBudgetBindingAndDenial$'` 验证完整交付与零准入拒绝；使用相同离线环境变量。Go API 之外现提供下文的受预算约束批次入口；没有随源码发布已经核实的真实模型上界常量。预算记录格式及宿主示例见[模块说明](../internal/budget/README.md)。

## 仓库工具测试

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test ./internal/repotools
```

检查文件访问边界、保护路径、替换约束、输出限制和 Go 命令生命周期。集成测试使用预设模型和真实工具，在临时 `demo-orders` 副本制造并修复编译错误，最后运行真实业务测试；不访问模型服务，不修改原始夹具。macOS/Linux 执行命令与进程组测试，其他平台仅执行文件工具测试。首次运行需编译隔离缓存中的标准库。

## 受预算约束的批次入口

先检查[三个 L2 开发 Target 的配置模板](../examples/rpcx-l2-batch.json)：

```sh
go run ./cmd/harness batch --config examples/rpcx-l2-batch.json --check
```

`--check` 不读密钥、不访问网络、不创建运行目录。它检查 JSON 字段、Target 固定摘要与宿主 Profile 的匹配、路径边界和参数；不认证磁盘输入内容、模型是否可用、Token 上界或 Docker 环境。输入内容和实际基线在 Run 中检查。

准备真实实验时，可先用已填写模型名、指定新输出目录的配置查询能力：

```sh
go build -o runs/harness ./cmd/harness
runs/harness batch --config runs/l2-capability.json --probe-model
```

`--probe-model` 读取终端的 `DEEPSEEK_API_KEY`，只查询同源 Models API 并保存 `config.json/models.json/capabilities.json`；不执行仓库命令，不初始化 Docker，不创建 Campaign、Run 或预算账本，不调用生成接口。它允许正数但尚不足以预留 Q 的 Token 配置，以便先查容量再审查预算；实际生成仍要求两级额度足够。与 `--check` 互斥，旧输出目录仍拒绝复用。程序不自动加载 `.env`，探测材料不能替代正式实验协议；准备工作见[实验准备记录](worklog/2026-10-20-l2-experiment-preparation.md)。

模板不是已冻结的实验协议。使用前将 `model.name` 的 `REPLACE_WITH_MODEL_ID` 替换为实际模型标识，审查预算、时间限制、并发及新输出目录；可复制到同样位于项目根下一层的 `runs/` 再修改。相对路径一律基于配置文件所在目录。字段含义：

| 字段 | 含义 |
| --- | --- |
| `schema_version`、`id`、`directory` | 当前格式为 1；批次身份；尚不存在的外层输出目录，父目录须已存在 |
| `concurrency`、`execution_concurrency` | 活跃 Target 数和实际执行命令数的独立上限 |
| `budget.max_calls/max_tokens` | 全批生成请求准入次数、累计输入输出 Token 限额；不是金额限额 |
| `model.name/endpoint/max_tokens` | 固定模型、完整 chat URL（可省略为官方默认）和显式输出限制；不能配置 C |
| `execution` | 默认 Docker，资源默认 2 CPU / 2048 MiB / 128 PIDs；仅可信夹具可显式设 `trusted_local: true` |
| `targets[].input` | 既有 `task.Target`：版本、规范、依赖及仓库摘要；不包含 oracle/expected |
| `targets[].budget` | 单 Target 生成次数与 Token 限额，同时受全批额度约束 |
| `targets[].max_model_calls/max_tool_calls/timeout` | Agent 尝试/工具次数和整个 Run 时限（例如 `10m`） |
| `enrollment.path/sha256`（可选） | 静态评测纳入清单的文件及原文字节摘要；相对路径基于本配置目录，任务与当前参考身份不符时在 API 访问前拒绝 |
| `model.expected_context_tokens`（可选） | 正数表示实际生成前必须匹配的服务上下文容量；0/省略不比较，负数拒绝。只比较 Discover，不覆盖真实上界 |

设置 expected_context_tokens 后，实际查询容量不符时先保存 config.json、models.json 和 capabilities.json，退出 2；不初始化执行后端/Campaign，不发送生成请求。该诊断目录保留且不能覆盖。`--check` 不联网确认容量，`--probe-model` 仍可保存变化后的声明用于诊断，不按该期望值阻止探测。

[首轮 L2 开发协议 v0.1.0](experiments/l2-dev-eval-v0.1.0/README.md)已固定真实模型名、三仓身份、每任务 250 万/全批 750 万 Token 和执行/评测口径；区别于通用示例中的 600 万草案。已冻结不代表已授权或已运行，实际启动须核对材料并取得本次生成授权。

三仓模板现引用[静态纳入示例](../examples/rpcx-l2-enrollment.json)，固定 Target 顺序及基线/规范/参考数据/工具身份；仍不是完整实验协议。参考工具生产源码变化会使摘要过期，应先审查并发布匹配的清单，再开始新实验。普通批次可省略 enrollment，继续按原方式运行，但没有生成前的评测纳入记录。

带 enrollment 的实际批次先固定静态文件快照；Campaign 保存真实 plan.json 后，在接纳任何 Target 前发布 `enrollment.json`、`round-manifest.json`、`round-request.json`。计划身份由实际原文字节计算，缺项、换序、身份漂移或发布失败均阻止任务开始。`--probe-model` 只检查静态清单，不生成这些动态文件。

批次结束后，可先用自动生成的请求离线汇总：

```sh
go run ./scenarios/rpcx/l2/cmd/summarize -config /absolute/batch/round-request.json
```

请求中的 references 初始为空，缺独立参考/复核保留未知；不会把未复核候选计为成功。完成参考和复核后，在新请求副本中填写对应报告/复核路径与 SHA-256，并选择新的 evidence_dir 再汇总，保留原记录。原门禁、候选和运行状态不被改写。完整接口见[轮次说明](../scenarios/rpcx/l2/round/README.md)。

实际运行形式如下，**会读取终端中已设置的 `DEEPSEEK_API_KEY` 并发送仓库代码与工具交互**；程序不自动加载 `.env`。先准备固定 Docker 镜像，并使用自己审核过的配置：

```sh
go build -o runs/harness ./cmd/harness
runs/harness batch --config runs/l2-development.json
```

每批先鉴权 GET 同源 `/models`（保留 `/v1` 等部署前缀），不跟随重定向。仅接受唯一匹配且明确支持文本、具有正整数 `context_window` 和 `max_output_tokens` 的模型；缺字段不猜默认值。请求的输出上限必须在服务允许范围内。两级 Token 额度都至少能预留一次 `Q=C+O` 才继续；多个请求仍由账本原子准入，余额不足可以等待或拒绝，不保证所有 Target 能完成。元数据查询不计入生成请求预算。

输出为 `directory/{config.json,models.json,capabilities.json,execution.json,campaign/}`。`models.json` 保留本次响应原始字节，能力记录及 Campaign 账本绑定其 SHA-256；能力材料完整落盘后才启动 Campaign。每个 Target 的基线、Agent、独立验证和交付仍由原 Runner 完成，审核入口为 `campaign/summary.md`。已有输出目录拒绝复用；没有自动重试或恢复。

直接运行二进制时：所有 Target 完成交付返回 0（允许质量 Rejected/NeedsAttention）；局部执行失败、未启动任务或设施错误返回 1；配置非法返回 2；取消返回 130。必须同时查看执行计数与质量计数，退出 0 不代表全部迁移正确。服务遵守声明上界是预算承诺的前提；本机模拟服务验收不等于已认证真实服务或已完成 L2 效果评测。设计和实际证据见[工作记录](worklog/2026-10-20-budgeted-batch-cli.md)。

机制验收（本机模拟 HTTP，需要 localhost 监听权限，不访问真实模型）：

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test -race ./internal/deepseek ./cmd/harness -run 'TestDiscover|TestModelsEndpoint|TestBatch'
```

## DeepSeek 适配器测试

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test ./internal/deepseek ./internal/agent
```

验证消息与工具映射、请求设置、响应错误、输出截断、取消/超时和逐次 Token 用量记录，并运行本地模拟 API 与真实文件工具的循环联调。需要本机临时端口监听权限，不需要 API Key，不访问真实模型服务。根模块 `go test ./...` 同样包含这些测试。

## 单仓库 Runner 离线验收

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test -p 1 -count=1 ./scenarios/rpcx/runner ./cmd/harness
```

可控模型通过真实工具修改临时夹具，再运行独立验证、导出补丁并验证还原一致性。覆盖正确/被拒绝的候选、失败/预算/取消/超时、输入变化、报告与状态写入失败，以及多次运行的历史保留。命令入口测试使用本机模拟 HTTP 服务，需要临时端口监听权限；不需要真实 API Key，不访问真实 DeepSeek 服务。

针对审核材料的源码位置，可只读载入一个已有三仓 L2 Campaign，重新在内存渲染并检查链接：

```sh
HARNESS_REVIEW_CAMPAIGN="$PWD/runs/l2-dev-eval-v0.1.0/campaign" go test -race ./scenarios/rpcx/runner -run '^TestReviewPublishedCampaignLinks$'
```

该入口不创建 Run、不调用模型或门禁、不覆盖旧 review.md；要求三种已发布 L2 Profile 均有实际源码位置。未设置环境变量时显式跳过。无需运行产物的布局回归是 `TestReviewSourceLocationsUseRepositoryLayout`，同时覆盖旧 demo-orders 入口、三个 L2 模块、删除位置与路径转义。修复依据见[工作记录](worklog/2026-10-20-review-source-links.md)。

## M3.2 Target 与生成前基线检查

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" GOMODCACHE="$PWD/runs/module-cache" go test -race -p 1 ./internal/task ./scenarios/rpcx/runner -run 'TestTargetStructure|TestExplicitTarget|TestUnsupportedTarget|TestBaseline|TestSupportedProfiles'
```

检查 Target/Profile 适用范围、实际输入与配置材料绑定、基线构建/必需测试事件、取消、输入漂移、失败日志及零模型准入；正常路径实际运行可信本地 Go 基线。故障注入使用可控执行器，不冒充真实业务红测。

验证新增基线阶段的 Docker 路径：使用相同离线环境变量，加 `HARNESS_DOCKER_TEST=1` 执行 `go test ./scenarios/rpcx/runner -run '^TestExplicitTargetDockerBaselineBeforeModel$'`。该对照只进行构建与十个原测试，随后使用立即报错的进程内模型替身，不调用真实服务。需已准备固定镜像，沿用下文 Docker 准备步骤。

## M3.4 Goddd 离线基线复验

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go run ./scenarios/rpcx/l2/cmd/verify-baseline
```

默认使用已有固定 Docker 镜像，断网并只读挂载冻结输入；为每条命令创建新容器和空构建缓存。重新装配独立的固定模块材料，构建并测试 Goddd 和共用 SDK v1/v2，核对 37 项测试及子测试、必需包终态、来源映射及输入前后摘要。报告写入新的 `runs/rpcx-l2-baseline-*/report.json`；不调用模型，accepted 仅表示该迁移前基线可复跑。

可信本地复验显式添加 `-trusted-local`。机制测试为 `go test -race ./scenarios/rpcx/l2/...`，包含冻结/来源异常在执行前拒绝、归档边界、缺失/跳过事件、失败证据与输入变化，不执行完整 Docker 基线。数据范围和限制见 [L2 说明](../scenarios/rpcx/l2/README.md)。

## M3.4 Translation 双配置基线复验

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go run ./scenarios/rpcx/l2/cmd/verify-baseline -dataset rpcx-l2-translation-v1
```

默认固定 Docker、断网和只读输入；加 `-trusted-local` 只运行可信本地夹具。分别验证 default/rpcx_alternate 活动源文件、生成 mock、构建和 26 项测试及子测试，连同 SDK 共 67 项配置化执行。需要 Go 1.26.0 或更新工具链，不自动下载。accepted 只证明迁移前基线，尚无该样本的迁移门禁或模型效果结论，见 [Translation 工作记录](worklog/2026-10-19-translation-baseline.md)。

## Goddd 候选与 Runner 校准

```sh
HARNESS_GODDD_ACCEPTANCE=1 HARNESS_GODDD_OUTPUT="$PWD/runs/goddd-calibration" go test -v -count=1 -timeout=15m -run '^TestGodddAcceptance$' ./scenarios/rpcx/goddd
```

上述命令只执行可信本地夹具。增加 `HARNESS_GODDD_BACKEND=docker` 使用已准备的固定镜像，`HARNESS_GODDD_CASES=correct,missing-timeout,mapping-helper` 可选择三类主要候选。改为 `-run '^TestRunGodddDelivery$' ./scenarios/rpcx/runner` 执行确定性 Runner 联调；不调用模型服务。机制单测、证据范围及冻结清单见 [Goddd 说明](../scenarios/rpcx/goddd/README.md)。

固定 context 变体的完整门禁与直接 mock 传播校准：

```sh
env HARNESS_GODDD_ACCEPTANCE=1 HARNESS_GODDD_BACKEND=docker HARNESS_GODDD_OUTPUT="$PWD/runs/goddd-context-variants" GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test -json -count=1 -timeout=15m ./scenarios/rpcx/goddd -run '^TestGodddContextVariantsAcceptance$'
```

三个候选预期分别为 ReadyForReview、Rejected、NeedsAttention；另执行收到同一 ctx、值/取消、原请求及返回结果的直接回调探针。属于开发校准，不调用模型或重算历史实验指标；原始字节、固定变体和选择记录见新证据目录的 adaptations.json。

Goddd 命名期限与构造器参数读取的独立范围证明校准：

```sh
env HARNESS_GODDD_ACCEPTANCE=1 HARNESS_GODDD_BACKEND=docker HARNESS_GODDD_OUTPUT="$PWD/runs/goddd-routing-scope" GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test -json -count=1 -timeout=15m ./scenarios/rpcx/goddd -run '^TestGodddRoutingScopeAcceptance$'
```

三个候选分别检验有限形式、5 秒错误期限和额外常量；正例另执行 nil/已取消/有值构造器 ctx 的直接探针，确认请求值、取消、30 秒上限及释放。routing-scope.json 记录原始节点与完整剩余结构，不更改候选或历史报告。

## 单仓库任务入口

```sh
go run ./cmd/harness --help
```

入口接收 `--scenario`（默认 `scenarios/rpcx`）、`--spec-version`（默认 `v0.1.0`）、`--runs`（默认 `runs`）、`--model`、`--timeout`、`--max-model-calls`、`--max-tool-calls`、`--max-tokens` 及测试用完整 `--endpoint`。模型名也可由 `DEEPSEEK_MODEL` 提供；密钥只从 `DEEPSEEK_API_KEY` 读取，不提供密钥参数，也不会自动加载 `.env`。

命令默认使用 Docker。先启动本机 Docker 引擎并准备固定工具链镜像；任务执行使用本地镜像 ID，缺失镜像时报错，不自动下载：

```sh
docker pull golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd
```

真实模型调用形式如下，会使用当前终端的模型凭证。[前两次实验](experiments/2026-10-18-rpcx-real-model-02.md) 均因预算耗尽未提交；[实验 03](experiments/2026-10-18-rpcx-real-model-03.md) 在 40 次模型调用上限下正常提交、门禁通过并完成交付。下面显式使用实验 03 的预算，默认模型调用上限仍为 20：

```sh
# 预先配置 DEEPSEEK_API_KEY 和 DEEPSEEK_MODEL。
go run ./cmd/harness --model "$DEEPSEEK_MODEL" --max-model-calls 40 --max-tool-calls 80 --max-tokens 4096 --timeout 10m
```

T3 任务须显式选择新场景和规范版本；以下命令会调用真实模型，离线验收请使用上一节测试命令：

```sh
go run ./cmd/harness --scenario scenarios/rpcx/t3 --spec-version v0.2.0 --model "$DEEPSEEK_MODEL" --max-model-calls 40 --max-tool-calls 80 --max-tokens 4096 --timeout 10m
```

此处预算沿用[实验 04](experiments/2026-10-19-rpcx-real-model-04.md)：该唯一一次 T3 真实运行为 Completed / NeedsAttention，不计自动交付成功；不能由示例推断稳定效果。版本/摘要不符或 T3 可信基线、SDK 混装在模型调用前失败，不自动使用旧规则。任务完成后先读 `artifacts/review.md` 的条款、证据与缺口，完整字段见 `artifacts/report.json`。Agent 自述单独展示，不构成验证证据。

`--image` 可指定预先准备的 Linux Go 镜像，构造后端时记录并固定其镜像 ID；要求包含 `/usr/bin/env` 和 Go，且不能声明额外匿名卷。`--cpus` 默认 2，`--memory-mib` 默认 2048，`--pids` 默认 128。默认禁网、只读根文件系统及只读源码/SDK，只向容器开放 1 GiB 临时空间，每条命令独立销毁。`--trusted-local` 显式切换到原可信宿主后端，用于受控夹具调试；不会在 Docker 失败时自动降级。

命令打印 Run、报告及审核材料路径。直接运行编译出的程序时，完整交付返回 0（含 `Rejected`），参数/配置错误返回 2，执行失败/超时返回 1，取消返回 130；`go run` 自身可能将程序非零退出码转换为 1，脚本需要精确退出码时先 `go build -o runs/harness ./cmd/harness` 再运行该二进制。审核材料及状态语义见 [Runner 说明](../scenarios/rpcx/runner/README.md)。

## Docker 执行后端验收

```sh
env HARNESS_DOCKER_TEST=1 GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test ./internal/execution ./scenarios/rpcx/runner ./cmd/harness
```

需要可访问的本机 Linux 容器引擎、上述已拉取镜像和本地临时端口权限。不需要模型密钥，不访问真实模型服务。覆盖源码/根文件系统只读、未挂载宿主文件和密钥不可见、网络受限、资源限制、OOM、输出超限、取消/超时回收，以及容器内候选迁移、独立验证和补丁还原。全新命令容器不复用 Go 缓存，因此明显慢于本地测试。

不设置 `HARNESS_DOCKER_TEST=1` 的常规 `go test ./...` 会明确跳过真实 Docker 集成，仍执行本地后端、容器参数校验、回收失败替身、Runner 和模拟 API 测试；不能把该结果记作容器验收。

## M2.3 分析选型实验

```sh
env HARNESS_ANALYSIS_STUDY=1 GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test -json -count=1 ./scenarios/rpcx/analysisstudy
```

在临时目录运行本仓库定义的可信样例，比较有限 AST 与类型查询、行为检查的互补性、混合测试与独立生产副本。输出 `STUDY` 结构化记录及预期反例的 `PROBE_FAILURE` 日志；可重定向至 runs 下的新文件留档。不调用模型、不使用 Docker、不修改冻结夹具。默认根模块测试跳过此实验；该包不接受外部候选，不输出运行时质量判定。范围和结果见[实验记录](experiments/2026-10-18-m2-analysis-study-01.md)。

## M2.3 隔离类型加载与边界分析

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test ./scenarios/rpcx/analysis/...
```

运行可信临时副本，验证 v1 基线、四个冻结候选、别名/常量/跨文件、未知形态、配置缺口、协议及输入变化；还比较实际调用增删、同数量替换、方法常量变化和文件移动。输出 ANALYSIS、ANALYSIS_MATERIAL 及 COMPARISON 记录。本地测试也会编译和执行可信加载程序，不执行候选测试或 init。不启用 Docker 时跳过真实容器对照。

```sh
env HARNESS_DOCKER_TEST=1 GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test -json -count=1 ./scenarios/rpcx/analysis -run '^TestInspectDockerConfigurationAndCoverage$'
```

需要已准备的固定镜像和本机 Docker。先分析 v1 基线，再运行三个对照：正确边界、错误超时和显式 tag 下的额外调用。默认配置下的两份候选可与基线配对，显式 tag 与默认基线不兼容而拒绝比较；同时检查实际配置、材料绑定及容器回收，不访问模型服务。分析和比较结果是有限结构事实，完整质量判定仍待接入。API 与材料说明见 [analysis](../scenarios/rpcx/analysis/README.md)。

只运行位置比较机制测试：

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test ./scenarios/rpcx/analysis -run '^TestCompare(CorrespondenceAndChanges|RejectsIncompatibleOrMalformedReports|OrderingDoesNotGuessCorrespondence)$'
```

该子集只消费构造的分析记录，不启动 Go 加载子进程或 Docker；实际源码与容器联调仍用上面两条命令。

## M2.3 测试结构完整性

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test -json -count=1 ./scenarios/rpcx/testintegrity
```

对冻结原测试作宿主最小适配，再比较候选完整结构。覆盖四个冻结候选、合法修改、断言删除、helper 重写、测试执行入口/构建范围变化及输入边界；输出 TEST_INTEGRITY 报告。默认跳过 Docker，对临时副本应用补丁，不执行候选测试，不调用模型，也不修改原夹具。

新增导入边界的完整本地对照（仅限可信开发夹具）：

```sh
env HARNESS_T3_TEST=1 GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test -json -count=1 ./scenarios/rpcx/checks -run 'TestT3LocalQualityMatrix/added-'
```

三组分别验证：合法 errors/time 导入为 ReadyForReview；同时删除断言为 Rejected；同时提取未知生产 helper 为 NeedsAttention。通过与需关注组仍执行真实超时检查，不以结构比较代替行为证据。

```sh
env HARNESS_DOCKER_TEST=1 GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test -json -count=1 ./scenarios/rpcx/testintegrity -run '^TestDockerShowsTheLimitsOfNameOnlyPreservation$'
```

需要已准备的固定镜像和本机 Docker。运行正确补丁、宿主适配、删除断言、等价 helper 重写四组对照：每组原十个测试均实际通过且名称相同，结构检查分别 preserved/preserved/violated/unknown。输出 BUSINESS_TEST_EVENTS 与 TEST_STRATEGY，核对容器回收。这是可信开发样例的机制对照，未代表任意测试语义等价或完整 T3 门禁，API 及限制见 [testintegrity](../scenarios/rpcx/testintegrity/README.md)。

## M2.3 可信运行时行为

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test -json -count=1 ./scenarios/rpcx/behavior
```

默认只运行装配、摘要、事件完整性和执行故障机制测试，不启动 Docker 或等待 30 秒。接口及产物说明见 [behavior](../scenarios/rpcx/behavior/README.md)。

```sh
env HARNESS_DOCKER_TEST=1 GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test -json -count=1 ./scenarios/rpcx/behavior -run '^TestDocker(FrozenBehavior|BehaviorCounterexamples|DeadlineWithoutTimer)$'
```

在固定镜像运行四个冻结候选、丢失父 context/未释放/吞错/生产入口退出，以及“表面期限正确但不安装计时器”的反例，共 9 组、11 条容器命令。正确候选与最后一组反例会执行真实到期检查，整组约三分钟；不访问网络或模型。输出 TRUSTED_BEHAVIOR、原始事件及执行元数据；最后一组还将完整证据保留在 runs/rpcx-behavior-deadline-only-*，报告该路径。其余组使用测试临时目录。

可用 `HARNESS_BEHAVIOR_TEST=1` 运行 `-run '^TestLocalFrozenBehavior$'`，仅适用于仓库中可信的四个手工夹具。该入口不代表任意候选代码可以安全地在宿主执行；本轮正式联调使用 Docker。

## M2.3 T3 组合质量门禁

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test -json -count=1 ./scenarios/rpcx/checks -run '^TestT3(ProductionScope|RejectsUnsupportedInputsBeforeExecution|ExecutionFailureHasNoVerdict|BaselineFailureHasNoQualityVerdict|CandidateEventsDoNotInferSuccessFromExitCode|PinsMatchFrozenScene)$'
```

只运行 T3 门禁的范围、输入、测试事件与执行失败机制检查；完整检查包的默认测试还包括旧 v0.1.0 回归。单候选 API 与八项必需检查见 [checks](../scenarios/rpcx/checks/README.md)。

```sh
env HARNESS_T3_TEST=1 GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test -json -count=1 -timeout=15m ./scenarios/rpcx/checks -run '^TestT3LocalQualityMatrix$'
```

在可信本地夹具运行 17 组开发对照，覆盖三类质量判定、违规与未知并存、候选测试入口干扰、SDK/保护路径修改及遗漏调用点；行为通过的候选包含真实 30 秒检查。仅供本仓库受控样例使用，复用本地编译缓存，不执行外部候选，不调用模型。全部证据和 evaluation.json 保存在 runs/rpcx-t3-quality-*，日志输出 T3_QUALITY_ARTIFACTS。

```sh
env HARNESS_DOCKER_TEST=1 GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test -json -count=1 -timeout=10m ./scenarios/rpcx/checks -run '^TestT3DockerQualityMatrix$'
```

固定镜像下运行正确候选、删除断言、错误期限加未覆盖构建配置三组组合门禁。正确候选执行真实到期检查；其他组保留违规和阻断依据。独立参考交叉验收使用下方 L1 入口；这些开发对照不能替代正式 L1 验收或 L2 留出评测。

## M2.3 L1 独立交叉验收

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test -json -count=1 ./scenarios/rpcx/l1/...
```

重放 19 个完整补丁，检查清单、冻结输入保护及参考事件协议；不启动完整门禁或等待真实到期。

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go run ./scenarios/rpcx/l1/cmd/verify -trusted-local
```

显式执行本仓库受控夹具的全量冻结验收，包含门禁和独立参考的真实到期检查，耗时数分钟。不加 `-trusted-local` 则使用已准备的固定 Docker 镜像，冷编译成本更高；不隐式拉取镜像或回退到本地。两种后端都不访问模型服务。

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go run ./scenarios/rpcx/l1/cmd/verify -trusted-local -development -quick -cases=wrong-duration,shared-expired-context,deleted-test
```

开发子集仅核对选中样例，`-quick` 只省略参考慢检查，不改变运行时门禁。完整数据、冻结规则及原始产物见 [L1 说明](../scenarios/rpcx/l1/README.md)。命令输出新建的 runs/rpcx-l1-* 路径及 matched/accepted；开发、子集或省略慢检查永不 accepted。每项有 gate.json、参考日志及补丁重放，最终 report.json 记录条款/原因差异、参考适用范围和产物摘要。

## M3.4 Translation 双配置迁移校准

```sh
env HARNESS_TRANSLATION_ACCEPTANCE=1 HARNESS_TRANSLATION_BACKEND=docker HARNESS_TRANSLATION_CASES=correct,alternate-missing-timeout,mapping-helper HARNESS_TRANSLATION_OUTPUT="$PWD/runs/translation-calibration" GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test -json -count=1 -timeout=25m ./scenarios/rpcx/translation -run '^TestTranslationAcceptance$'
```

固定镜像、禁网执行，在同一候选的 default/rpcx_alternate 下分别构建、测试、类型分析和行为验证。省略 CASES 运行全部七组校准；正例与等价 helper 组各包含两配置真实约 30 秒到期。省略 BACKEND 只适用于可信本地开发夹具。报告与逐配置证据保留在 OUTPUT 下的新目录；不访问模型、不读取 .env，也不代表独立参考评测。

```sh
env HARNESS_TRANSLATION_ACCEPTANCE=1 HARNESS_TRANSLATION_BACKEND=docker HARNESS_TRANSLATION_OUTPUT="$PWD/runs/translation-runner" GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test -json -count=1 -timeout=15m ./scenarios/rpcx/runner -run '^TestRunTranslationDelivery$'
```

使用确定性模型重放编辑，验证双配置基线先于模型、go_check 反馈、独立质量判定、补丁重放和审核链接。Translation 需本地/容器 Go 1.26.0 或更新；离线依赖已随固定数据发布，仍需提前准备固定工具链镜像。规范与校准边界见 [Translation 说明](../scenarios/rpcx/translation/README.md)。

## M3.4 Workouts 多模块离线基线

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go run ./scenarios/rpcx/l2/cmd/verify-baseline -dataset rpcx-l2-workouts-v1
```

默认使用已准备的固定 Docker 镜像，依次复验 rpcx-v1、rpcx-v2、common、trainings，均只读挂载，使用认证离线依赖和各命令空构建缓存。可信本地夹具可追加 `-trusted-local`。需 Go 1.25.0 或更新版本；不会自动下载工具链或运行云端组件。12 条命令、55 项测试/子测试，两个本地业务模块仍只计一个样本。报告的 accepted 只表示冻结基线通过，不是迁移效果。

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test -race -json -count=1 ./scenarios/rpcx/l2/...
```

包含发布身份/来源和准备补丁重放、common 构建失败/缺测试事件/取消/输入漂移、旧默认入口与 Translation 配置矩阵机制。真实业务及隔离复验使用上面的基线入口，不使用替身结果冒充业务运行。范围及裁剪损失见 [Workouts 说明](../scenarios/rpcx/l2/data/rpcx-l2-workouts-v1/README.md)和[工作记录](worklog/2026-10-19-workouts-baseline.md)。

## L2 轮次离线汇总

```sh
go run ./scenarios/rpcx/l2/cmd/summarize -config /absolute/path/request.json
```

读取已发布 Campaign/Run、独立参考与可选复核，不运行模型或候选命令。Request 固定纳入清单、原 Campaign、全新输出目录和带摘要的参考/复核路径；配置字段与示例见[轮次说明](../scenarios/rpcx/l2/round/README.md)。所有路径为绝对路径，已有输出拒绝覆盖。缺材料保留未知；身份/摘要冲突返回非零且不发布指标。退出 0 表示汇总有效，不表示全部迁移成功或复核完成。开发校准使用独立 kind，不计真实模型效果。

```sh
go test -race ./scenarios/rpcx/l2/round
```

覆盖零分母、未知区间、缺文件、跨 Run/候选/规范、重复引用、原失败/未启动任务、费用只计一次及输入/归档漂移。可选的已有三仓受控 Campaign 校准会对既有候选运行参考，需显式设置环境变量，见轮次说明；不读取 .env 或调用模型 API。
