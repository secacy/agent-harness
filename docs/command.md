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

报告包含输入文件摘要、执行后端与命令日志、原测试事件、参考检查 pass/fail/inconclusive、失败条款、补丁重放及生产代码绑定。预期反例的失败算匹配；未执行、执行错误或观测不充分不能冒充预期反例失败。这是 M2.1 离线场景证据，没有运行时质量判定；现有 `cmd/harness` 尚未接入 T3。检查边界见 [T3 场景说明](../scenarios/rpcx/t3/README.md)。

## 验证工具自身测试

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test ./...
```

覆盖受保护路径新增/删除/改名/修改、测试缺失/跳过/未执行、专项探针未执行、context 别名、符号链接拒绝及判定边界。另在无 manifest 的临时目录中直接调用 `Evaluate`，验证输入不被修改、普通编译与测试编译失败的判定、规范绑定、证据目录约束、基线失败、命令启动失败，以及父 context 取消和 deadline 传播。

该命令仅测试根模块；接口集成测试会运行其临时副本中的业务测试，但不替代两个 SDK 和五种公开补丁的完整场景验收。完整验证使用上面的场景入口。

## 证据绑定与质量聚合测试

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test ./internal/quality ./scenarios/rpcx/checks
```

覆盖宿主必需集合、缺项、未知/跳过、不适用的许可与理由、fail 优先，以及证据缺失、篡改、错误候选绑定和路径约束。集成测试从真实 Evaluate 材料重算判定，再删除必需项或篡改日志；同时核对旧五用例的原状态、判定、失败条款与证据。

新 Evaluation 与公开场景报告使用 schema_version=2，新增 rule_ids、findings、evidence 和 gaps。单候选证据路径相对 EvidenceDir；公开场景每个结果的 evidence_directory 相对本次运行目录。Runner 报告内的 validation 保存该新结构，证据路径相对该 Run 的 evidence/。

每个证据绑定规范、基线与候选摘要，并校验实际文件 SHA-256。quality-policy.json 保存宿主策略副本，context-probe.go.txt 保存可信探针；materials_sha256 为紧凑 JSON 编码策略后紧接探针原始字节的 SHA-256，仅标识策略与探针材料，不是整个验证器源码摘要。verifier_version 是宿主维护的版本标识；公开报告的 checks_sha256 只覆盖场景 checks/，不能解释为整个 Harness 的源码摘要，复现仍需对应 Git 版本和工具链。

证据缺口得到 NeedsAttention；充分的明确失败得到 Rejected 并保留缺口；错绑定、材料篡改或记录非法返回执行错误与空判定。新结构不证明原检查未覆盖的语义，不把 v0.1.0 升级为 T3；现阶段仍拒绝将 v0.2.0 交给旧 Evaluate。技术依据见 [ADR-002](adr/002-evidence-bound-quality.md)。

## Agent 循环测试

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test ./internal/agent
```

使用模型和工具替身，检查消息关联、工具顺序、错误反馈、调用预算、取消和退出分类，同时执行 `ExampleLoop_Run` 示例。不访问模型服务，也不实际修改业务仓库。根模块 `go test ./...` 同样包含这些测试。

## 仓库工具测试

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test ./internal/repotools
```

检查文件访问边界、保护路径、替换约束、输出限制和 Go 命令生命周期。集成测试使用预设模型和真实工具，在临时 `demo-orders` 副本制造并修复编译错误，最后运行真实业务测试；不访问模型服务，不修改原始夹具。macOS/Linux 执行命令与进程组测试，其他平台仅执行文件工具测试。首次运行需编译隔离缓存中的标准库。

## DeepSeek 适配器测试

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test ./internal/deepseek ./internal/agent
```

验证消息与工具映射、请求设置、响应错误、输出截断、取消/超时和逐次 Token 用量记录，并运行本地模拟 API 与真实文件工具的循环联调。需要本机临时端口监听权限，不需要 API Key，不访问真实模型服务。根模块 `go test ./...` 同样包含这些测试。

## 单仓库 Runner 离线验收

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test ./scenarios/rpcx/runner ./cmd/harness
```

可控模型通过真实工具修改临时夹具，再运行独立验证、导出补丁并验证还原一致性。覆盖正确/被拒绝的候选、失败/预算/取消/超时、输入变化、报告与状态写入失败，以及多次运行的历史保留。命令入口测试使用本机模拟 HTTP 服务，需要临时端口监听权限；不需要真实 API Key，不访问真实 DeepSeek 服务。

## 单仓库任务入口

```sh
go run ./cmd/harness --help
```

入口接收 `--scenario`（默认 `scenarios/rpcx`）、`--runs`（默认 `runs`）、`--model`、`--timeout`、`--max-model-calls`、`--max-tool-calls`、`--max-tokens` 及测试用完整 `--endpoint`。模型名也可由 `DEEPSEEK_MODEL` 提供；密钥只从 `DEEPSEEK_API_KEY` 读取，不提供密钥参数，也不会自动加载 `.env`。

命令默认使用 Docker。先启动本机 Docker 引擎并准备固定工具链镜像；任务执行使用本地镜像 ID，缺失镜像时报错，不自动下载：

```sh
docker pull golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd
```

真实模型调用形式如下，会使用当前终端的模型凭证。[前两次实验](experiments/2026-10-18-rpcx-real-model-02.md) 均因预算耗尽未提交；[实验 03](experiments/2026-10-18-rpcx-real-model-03.md) 在 40 次模型调用上限下正常提交、门禁通过并完成交付。下面显式使用实验 03 的预算，默认模型调用上限仍为 20：

```sh
# 预先配置 DEEPSEEK_API_KEY 和 DEEPSEEK_MODEL。
go run ./cmd/harness --model "$DEEPSEEK_MODEL" --max-model-calls 40 --max-tool-calls 80 --max-tokens 4096 --timeout 10m
```

`--image` 可指定预先准备的 Linux Go 镜像，构造后端时记录并固定其镜像 ID；要求包含 `/usr/bin/env` 和 Go，且不能声明额外匿名卷。`--cpus` 默认 2，`--memory-mib` 默认 2048，`--pids` 默认 128。默认禁网、只读根文件系统及只读源码/SDK，只向容器开放 1 GiB 临时空间，每条命令独立销毁。`--trusted-local` 显式切换到原可信宿主后端，用于受控夹具调试；不会在 Docker 失败时自动降级。

命令打印 Run 和报告路径。直接运行编译出的程序时，完整交付返回 0（含 `Rejected`），参数/配置错误返回 2，执行失败/超时返回 1，取消返回 130；`go run` 自身可能将程序非零退出码转换为 1，脚本需要精确退出码时先 `go build -o runs/harness ./cmd/harness` 再运行该二进制。审核材料及状态语义见 [Runner 记录](worklog/2026-10-17-single-repo-runner.md)。

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

```sh
env HARNESS_DOCKER_TEST=1 GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go test -json -count=1 ./scenarios/rpcx/testintegrity -run '^TestDockerShowsTheLimitsOfNameOnlyPreservation$'
```

需要已准备的固定镜像和本机 Docker。运行正确补丁、宿主适配、删除断言、等价 helper 重写四组对照：每组原十个测试均实际通过且名称相同，结构检查分别 preserved/preserved/violated/unknown。输出 BUSINESS_TEST_EVENTS 与 TEST_STRATEGY，核对容器回收。这是可信开发样例的机制对照，未代表任意测试语义等价或完整 T3 门禁，API 及限制见 [testintegrity](../scenarios/rpcx/testintegrity/README.md)。
