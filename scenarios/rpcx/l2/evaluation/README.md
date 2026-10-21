# L2 单候选离线参考

支持 Goddd 的冻结输入与 v0.3.0 规范（`Evaluate`），Translation 的冻结输入与 v0.4.0 规范（`EvaluateTranslation`），以及 Workouts 的冻结输入与 v0.5.0 规范（`EvaluateWorkouts`）。三个入口接收相同的 Request：基线/SDK 根、候选及预先固定的全树摘要、规范、身份和一个尚不存在的证据目录。父目录需已存在，输入与输出互不重叠。本地执行必须显式设置 TrustedLocal；真实候选使用受限 Docker 后端。工具不接收原 gate verdict、校准 expected 或 Agent 自述。

新增 GHCLI 固定切片与 v0.6.0 规范入口 `EvaluateGHCLI`，复用相同 Request/Report/Assess，强制 Docker。活跃数据版本 `rpcx-l2-reference-ghcli-data-v2`、报告版本 `rpcx-l2-reference-ghcli-v2`；v1 原数据与更强的命令上下文条件仅作诊断归档。v2 在公开的 HTTP 请求边界检查父 context 与响应消费生命周期，命令路径保留业务顺序、错误和实际 30 秒到期；未公开的上游父 value 传播不参与自动结论。范围修订、冻结顺序及实际执行状态见[接入记录](../../../../docs/worklog/2026-10-21-isolated-application-integration.md#9-第二实现单元单仓库-harness-与独立参考闭环)。

GHCLI 的规范材料按既有契约保存为 `materials/spec.md`；实际装配的 `modules.json`、`modules.bundle/bundle.json` 和五个分片逐件登记到 Artifacts，并复核执行后身份。Report 另绑定 SDK 与宿主材料摘要。轮次消费端检查必需集合及真实有序字节，不能靠删掉某个引用逃过核对；单文件读取上界保持 16 MiB。

自动检查使用 Go 自身的模块声明解析、保护文件字节比较、基线/候选构建、完整原测试事件和新编写的 HTTP→booking→装饰器→RPC 参考测试。参考生产副本剥离候选测试，分别验证映射与错误/空结果、请求 ctx、较短父期限、取消、逐调用 30 秒与释放，并实际等待约 30 秒到期。等价 helper 或 WithDeadline 写法不按生产 AST 模板拒绝；原断言、未覆盖变更和交付依据由另行复核补齐。

Translation 在同一份候选上分别执行 default 与 rpcx_alternate。每配置保留 Go 实际选择的活动/忽略实现和 mock 参与情况、构建、26 项基线测试/子测试、独立行为与实际到期证据。新测试经真实 usecase、两层 tracing 进入 RPC，检查请求/结果映射、Store 次序和用户身份、取消/期限/错误链，以及 History。保护路径另包括原生成 mock。配置由宿主固定，调用方不能省略 alternate；Configurations 中缺项、标签错配或与顶层聚合不符，Assess 拒绝消费。两配置仍只算一个候选。

Workouts 从真实 decorated Handle 进入独立仓储控制，分别观察外层 ctx 与仓储提供的 callback ctx、条件余额调用、余额→trainer→本地提交顺序、参数/空确认、错误链及已成功的远端副作用。默认配置下，两个客户端分别实际等待约 30 秒到期；期限起点对照在两次 RPC 之间加入 500ms，检查是否错误共用一个期限。只读 common 另行构建并执行三项测试，`LocalModules` 保存其摘要与独立结果；SDK/common 的副本也参与执行后完整性检查。缺 common 或任一客户端到期证据时，不能完整通过。

`reference.json` 的 Automatic 是自动检查结果。报告附有数据/规范/基线/候选/生产/工具身份、命令及双日志摘要、后端与容器。`materials/` 保存实际参考和工具材料；`baseline/`、`candidate/`、`production/` 分别保留基线、原候选及注入参考测试的副本。执行前后核对原输入与副本，输入漂移使整份参考 inconclusive。输出目录独占，不覆盖已有记录。

`review-template.json` 绑定 reference.json 的真实字节、Target、Run 和候选，三个项目默认 inconclusive：原测试保留、生产改动覆盖、交付依据。复核者填写身份、RFC3339 时间、理由及文件/证据定位，然后调用：

```go
assessment, err := evaluation.Assess(referenceJSON, reviewJSON)
```

Assess 返回一个新结果，不修改原门禁或参考报告。全部自动检查与复核通过才是 pass；确定违规为 fail；证据或复核不足为 inconclusive。自动 pass 配上未填模板仍为 inconclusive。校准记录必须使用 `kind=development_calibration`，不得作为真实候选的人工独立复核。Assess 检查结构及身份绑定，不能验证填写者身份、定位内容或实际完成了人工审查；消费结果的宿主仍需核对原始材料。

参考数据由 `data/goddd-v2/freeze.json` 、`data/translation-v2/freeze.json` 与 `data/workouts-v1/freeze.json` 分别固定；Translation v1 保留早期校准身份，v2 改为观察内存字段，避免 JSON 标签引发假违规；Goddd 先前用于校准的 v1 也保留，v2 修正 HTTP 发送时刻与实际 RPC 期限之间的测量边界，并加入发送前延迟对照。报告分别保存 ReferenceVersion、数据摘要和实际工具源码摘要。复用 execution、L2 只读依赖装配和 gate 文件操作，不调用现有三场景 Evaluate、类型分析、测试完整性规则、quality.Decide 或其行为探针。源码边界测试检查直接调用范围；共享 Go/SDK/执行设施和同一作者仍存在共同盲区。

生成前可调用 `PublishedIdentity(referenceVersion)` 取得当前发布的元数据：Version 对应报告的 ReferenceVersion（数据版本），另有 SpecSHA256、ReferenceSHA256、ToolSHA256。它认证冻结材料，使用与参考报告相同的摘要算法，不接受候选、不执行命令、不返回探针正文。当前支持 Goddd data-v2、Translation data-v2、Workouts data-v1、GHCLI data-v2；未知发布拒绝。生产工具源码改动会改变 ToolSHA256，已有清单须重新审查固定；历史报告和其归档工具材料保持原身份。

机制与身份/未知处理测试：

```sh
go test -race ./scenarios/rpcx/l2/evaluation
```

真实本地校准仅适用于可信的开发夹具；显式启用后每候选创建新目录：

```sh
HARNESS_L2_REFERENCE_ACCEPTANCE=1 \
HARNESS_L2_REFERENCE_OUTPUT="$PWD/runs/l2-reference-local" \
go test -count=1 -timeout=20m ./scenarios/rpcx/l2/evaluation -run '^TestGodddReferenceAcceptance$'
```

增加 `HARNESS_L2_REFERENCE_BACKEND=docker` 使用固定禁网镜像；`HARNESS_L2_REFERENCE_CASES=correct,missing-timeout,mapping-helper` 选择 Goddd 主要对照。Translation 使用同一组环境变量，将测试名改为 `^TestTranslationReferenceAcceptance$`，主要对照改为 `correct,alternate-missing-timeout,mapping-helper`。Workouts 的测试名为 `^TestWorkoutsReferenceAcceptance$`，主要对照为 `correct,outer-context,mapping-helper`。各场景必须使用不同的输出目录；Workouts 全矩阵使用 `-timeout=30m`。

所有校准仅是开发材料，不调用模型，也不产生真实模型效果指标。删断言对照可以自动通过，所以仍必须保持待复核；实现结果和实际源码检查见[工作记录](../../../../docs/worklog/2026-10-19-l2-development-evaluation.md#10-goddd-单候选参考实现与验收)。Workouts 的实现与验收见同一工作记录第 12 节；[轮次汇总](../round/README.md)在参考之后关联原 Run/门禁和复核，参考包不读取其结果；[首轮真实开发批次](../../../../docs/experiments/2026-10-20-rpcx-l2-real-model-01.md)已按冻结开发协议完成自动参考与汇总，实际人工复核仍待；M5 正式评测的协议冻结与验收尚未完成。
