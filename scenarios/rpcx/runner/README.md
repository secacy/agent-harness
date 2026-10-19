# 单仓库生成、验证与审核交付

Runner 接收 `agent.Model` 与显式执行后端，准备工作副本，驱动工具循环，固定提交候选，再调用独立 `checks.Evaluate`。模型只能看到业务工作副本、只读 SDK 和规范；cases、L1 期望、oracle、门禁材料及最终证据不属于工具根目录。文件工具与执行后端共同限定访问范围，摘要校验不替代执行隔离。

## 显式 Target 与宿主 Profile

新的 Go API 可使用 `Config.Target`，与 ScenarioDir/SpecVersion 互斥。例如先调用 `TargetFromScenario("orders", scenarioDir, "v0.2.0")` 构造已知场景描述，再将其地址放入 Config.Target。该函数只构造描述，不读取或执行仓库；Run 负责认证实际输入。旧命令行入口继续使用 ScenarioDir/SpecVersion，内部转换为相同任务模型。

ProfileT1T2 / ProfileT3 分别对应 rpcx-demo-orders-t1t2-v1 / rpcx-demo-orders-t3-v1，固定布局、SDK/基线/规范摘要、默认构建配置、十个必需测试和验证实现。原始仓库或 SDK 可以位于不同物理目录，工作副本按受认证的布局组装；这不是任意业务仓库支持。额外构建配置或未知 Profile 被拒绝，新仓库需单独接入可信规则。已使用 Profile 的内容调整发布新 ID，旧材料身份由固定摘要测试保护。

显式 Target 先创建独立 Run，再读取和认证实际输入；文件消失、摘要漂移留下 Failed / Preparing 记录，failure_code=input_invalid。旧场景入口保留非法或变更输入在创建 Run 前拒绝的兼容行为；新的 T1/T2 Profile 也认证完整输入快照，不再让随意修改的基线借用旧探针。Task ID 与预算 Target ID 必须一致。

## 生成前基线检查

准备副本后进入 CheckingBaseline 阶段，在配置的执行后端运行 go build 和 go test -json；十个必需测试及对应包必须真实通过。空成功、跳过、缺事件、命令失败、取消或超时均阻止模型调用，不产生候选质量判定。必需检查的非零退出记录 baseline_failed，执行设施错误记录 baseline_error，原始原因和退出状态保留。

inputs/profile.json 保存宿主实际配置，Input.ProfileSHA256 绑定其完整落盘字节；preflight/baseline.json 绑定配置、规范、基线及完整布局，命令记录后端/容器与 stdout/stderr 的路径和摘要。结构化报告的 baseline_check 同样保留这些事实。完整独立门禁仍在提交后重新认证并验证自己的输入，启动检查不会豁免任何质量要求。

Run 的整体 Timeout 现在包含新增基线命令；每条基线命令上限 30 秒，模型预算准入发生在该阶段之后。当前仍只提供 Go API 的显式 Target 接入，没有 batch 命令、调度或恢复功能。

## 选择规范

`Config.SpecVersion` 为空默认 v0.1.0，原命令行为保持不变。T3 必须同时设置 `ScenarioDir` 为 `scenarios/rpcx/t3`、`SpecVersion` 为 `v0.2.0`。版本仅接受这两个值，规范须匹配宿主支持的确切 SHA-256；T3 基线及两版 SDK 在调用模型前核对可信 pin，验证阶段仍会重新核对。版本与目录不匹配不能回退到旧规范。

迁移规范随任务进入提示和只读 spec 根目录。停止生成后，规范、基线及候选摘要必须与 Evaluation 绑定一致；验证后候选变化使证据失效，返回错误和空判定。模型预算耗尽、取消或未提交时保存部分修改，不自动补做独立验收。

## 可选共享预算

宿主可以设置 `Config.Budget`，提供 internal/budget 的账本及 Target ID。Runner 创建实际 Run ID 后绑定预算模型，在 Agent Complete 前预留；生成前的配置绑定失败不会调用模型。预算拒绝记录为 limit_reached，Run 失败并保留部分候选，不发布质量判定。

inputs/task.json 与 artifacts/report.json 的可选 budget 字段保存 Campaign/Target/Run、共享账本路径和模型配置摘要；review.md 提供相同定位。Loop 的调用尝试数与账本实际准入数分别读取，未知用量不能算零。模型必须提供宿主核实的 Profile；目前这是 Go API 接口，CLI 尚未新增批次预算参数，详见[预算模块](../../../internal/budget/README.md)。

## 读取交付

每次运行创建新的 `rpcx-agent-*` 目录，不覆盖既有 Run：

- `run.json`：阶段与执行终态。只有 `Completed` 表示全部产物发布完成；`Rejected` 可以是完整交付的质量结论。
- `preflight/`：生成前基线报告及命令日志，不属于候选质量门禁证据。
- `inputs/`、`baseline/`：任务、规范和基线副本；`workspace/`：实际候选及只读 SDK 副本。
- `artifacts/candidate.patch`：相对 `baseline/demo-orders` 的候选补丁，可用 Git 重放核对候选摘要。
- `artifacts/report.json`：规范、后端、输入、补丁及 Evaluation 的结构化事实。
- `artifacts/review.md`：确定性生成的审核入口，包含文件变化、检查对应条款、发现和源码位置、证据链接及摘要、缺口、直接命令和内层执行材料入口。
- `artifacts/agent.json`：完整对话、工具反馈、预算通知和已知/未知用量。review 中 Agent 总结标为未经独立确认的自述，不用于质量判定。
- `evidence/`：独立检查的实际副本、材料、配置、命令和日志；Evidence 路径均相对此目录。

先读 review 的判定和缺口，再按条款查看变化、具体发现与证据；代码位置的行号指向基线或实际候选，不假装所有命令都有源码行号。所有证据绑定规范、基线和候选；支持范围、配置、探针材料与内层命令在相应 JSON 中保留。ReadyForReview 是进入常规人工审核的条件，不是自动合并或所有业务语义已证明。

报告和审核材料先落盘，最后发布 run.json。任一产物或终态写入失败，Run 返回错误并清空质量判定，尽力重写诊断材料；若无法重写审核材料则尝试移除旧文件。强杀可能留下未完成发布，必须以 run.json 为准；跨进程恢复和离线审计属于 M4。

## 离线联调

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test -p 1 -count=1 ./scenarios/rpcx/runner ./cmd/harness
```

确定性模型通过真实编辑工具提交三种 T3 候选，调用真实门禁；验证正确超时、漏超时、未知 helper 的三种判定及原 M1 行为、摘要、证据、补丁重放、审核链接和发布失败。可信行为通过的候选各包含实际 30 秒到期，故不是纯替身单测。命令测试使用本机模拟 HTTP 服务，不访问真实模型。可设置 `HARNESS_RUNNER_ARTIFACTS` 保留 T3 测试运行目录；`HARNESS_DOCKER_TEST=1` 启用相同三例的真实 Docker 对照，需已有固定镜像。
