# M3.4：Workouts 回调、多模块迁移判定与 Runner 接入

- 日期：2026-10-19。
- 状态：本工作单元验收完成；代码基线 1777e16。
- 前置：[Workouts 基线](2026-10-19-workouts-baseline.md)。
- 本步范围：发布新规范与宿主 Profile，校准单候选门禁，确定性 Runner 交付；真实模型、独立 oracle 与异构批次效果评测另行推进。

## 实现前的取舍

| 问题 | 可行方案及代价 | 选择及判别证据 |
| --- | --- | --- |
| 回调 ctx 来源 | 只分析适配器参数会漏掉上层传错 ctx；完整跨包/闭包数据流证明引入大量分析复杂度；有限结构约束配合实际探针覆盖当前切片但不证明任意重构 | 保留原 command/领域结构，可信仓储用不同于 Handle 的 ctx 调用真实 callback，核对值、较短期限和取消。替换外层 ctx 的反例必须拒绝；等价 helper 超范围保持 NeedsAttention |
| 两次调用的超时 | Handle 共用 30 秒便于编写，却改变 v1 每次调用重新计时的行为；每个出站方法设置期限可保留原语义 | 每次 RPC 从其实际参数 ctx 派生 30 秒上限并释放，保留较短父期限；分别观察两个客户端与返回后的释放，实际等待到期。零余额分支只调用 trainer |
| 失败后的提交 | 只断言返回 error 无法发现错误提交；添加补偿会改变既有副作用语义 | 原回调原测试与可信探针共同检查顺序、错误链、失败不提交本地副本，以及余额成功后 trainer 失败时远端余额仍存在。测试仓储不是 Firestore 或跨远端事务证明 |
| common 如何验收 | 只读挂载只限制修改，不证明依赖自身测试执行；把它当第二 Target 又错误增加样本数 | 一个 Target，common 作为认证只读依赖，生成前及独立门禁均单独构建并核对其完整测试事件；报告区分模块，缺证据不能进入生成/放行 |
| 复用门禁 | 立即统一场景引擎会把业务差异塞入配置；复制文件/缓存工具增加分叉 | 复用 gate/L2/analysis/testintegrity，Workouts 保留显式业务判定与探针；只扩展 Runner 的宿主前置模块测试清单，旧 Profile 字节身份保持 |

遵循 ADR-005/007，基线数据和既有规范不变，新规范 v0.5.0；不另建动态规则 DSL。校准补丁/expected 不进入 Agent 或门禁。

## 验收计划

- 正确、漏一个客户端期限、Background、捕获外层 ctx、改业务分支/提交、删断言、保护路径、等价 helper；逐候选检查判定与条款，语义反例须有实际探针归因。
- 两个客户端分别检验父值/取消/较短期限、每调用 30 秒、正常/错误返回释放与真实到期；保留原 26 项及新增 11 项业务测试。
- common 构建失败/缺测试事件阻断模型；篡改输入/依赖、缺日志不产生虚假判定；导出补丁可重放。
- 本地完整矩阵、Docker 三类主对照及确定性 Runner，相关机制回归；所有测试跳过项与未执行部分单列。

## 实际接入

发布独立 v0.5.0，规范 SHA-256 为 `789d30540799249d0f5d96b3ba921b7e00d4cbc1e457491a2a79eba89829a526`。冻结的 rpcx-l2-workouts-v1 不改；九份校准补丁与 manifest 另以 freeze.json 认证，其摘要为 `e61e9ed77aaa37a12b5127ed6b98b61fe4ac43429c1e887d02cf1065a4b04539`。这些预期不进入 Agent 可见材料或 Evaluate 参数。

新的 `rpcx-workouts-t3-v1` Profile 固定 trainings 基线、common 与两个 SDK、v0.5.0、业务 37 项及 common 3 项测试身份，Profile JSON 摘要 `0f792983fa48a9b0ff65ddb182ca0ce56d1c580de2937eef13c9498e8bdb8318`。生成前独立执行 common 的 build/test，随后执行 trainings 的 build/test；输入绑定、容器/日志和两模块测试身份进入 preflight 报告。common 失败、缺事件、取消均零模型调用。旧 Profile 的身份与路径不改。

单候选门禁沿用已有 checks.Evaluation/quality.Decide 契约，包含九项必需检查：dependencies、local_modules、protected_paths、build、baseline_tests、test_preservation、request_scope、migration_completeness、semantic_behavior。common 在独立门禁再次构建与核对完整测试事件。trainings 的原测试适配仅变更已认证的 rpcx 导入、NewClient/Dial 和 Call 的 ctx 参数；原取消 command、领域与断言不从候选复制到可信测试。有限结构只容忍两个适配器方法开头的标准期限/释放前缀；其余生产差异留作 unknown，明确行为违规由可信探针拒绝。

类型分析需要恰好对应两次出站 RPC，调用点各自具有合法 ctx 派生；实际探针进一步区分 Handle 与 callback ctx。可信仓储用不同的 value 和较短期限调用原 callback，使错误捕获外层 ctx 的补丁在原测试和类型检查均可通过时仍被 CTX-001 拒绝。两个客户端分别验证每调用 30 秒、较短父期限、取消传播、释放和真实约 30 秒到期；业务分支、错误链及本地提交另由同一路径检查。trainer 失败后先前成功的余额远端效果仍计入观察，不声称有补偿或回滚。

## 已执行验证与实际边界

| 验证 | 结果 | 证据 |
| --- | --- | --- |
| 本地九组候选 | 1 ReadyForReview、7 Rejected、1 NeedsAttention；每组九项检查状态与最终 manifest 逐项复核，所有补丁摘要一致。trainer 缺期限、Background、外层 ctx、零余额误调用、吞错误均有探针条款归因 | `runs/m3-4-workouts-gate/local-first.jsonl`、`local-matrix.jsonl`，各 `evaluation.json`；最终 manifest 离线核对 |
| 本地确定性 Runner | Completed/ReadyForReview；common 前置三测试、trainings 37 测试、受控模型编辑、go_check、独立门禁、报告身份及补丁重放通过 | `runs/m3-4-workouts-gate/runner-local.jsonl`、其记录的 Run 目录 |
| Docker 三类候选 | correct/outer-context/mapping-helper 分别 ReadyForReview/Rejected/NeedsAttention；9/8/9 条实际 Docker 命令均有容器 ID。该轮编译早于补充保存类型加载 execution/inputs 文件及最终清单完整性断言，不能称作最后代码字节的 Docker 复跑 | `runs/m3-4-workouts-gate/docker.jsonl`、各 `evaluation.json` |
| Docker 确定性 Runner | 最终代码下 Completed/ReadyForReview；前置 common/trainings 共 4 条命令、独立门禁 9 条命令；候选摘要、Profile 身份、补丁重放与审核链接均由显式测试断言 | `runs/m3-4-workouts-gate/runner-docker.jsonl`、`runner-docker/rpcx-agent-1338227795/artifacts/report.json` |
| 容器回收 | 四份 Docker 报告中 39 个不同容器 ID 逐一 inspect 均不存在，Harness 标签容器清单为空 | `runs/m3-4-workouts-gate/docker-container-audit.json` |
| 生成前故障注入 | common 构建非零、测试缺事件、执行取消均未触达模型；门禁的 common/输入/依赖材料异常没有候选质量 verdict | `runner-mechanisms-first.jsonl`、`mechanisms-first.jsonl` |
| 结构及材料 | 九补丁重放、正确/未知/删断言结构对照、规范/基线/补丁清单摘要及当前预期全项核对通过 | `mechanisms-first.jsonl`、`freeze-final.jsonl`、本轮离线核对 |

`runs/` 均不入 Git。脚本化模型与手写候选仅证明开发机制，不能换算真实模型成功率或独立错误放行率。现有 Workouts 切片不覆盖 Firestore、Schedule/Move、跨远端回滚和任意等价重构。独立留出仍按已确认方案等待独立准备者。

Docker Runner 首次启动时自动审批返回“账户用量已达上限”，命令当时未执行。用户随后明确授权，本轮重新发起的审批通过，最终代码下的 Docker Runner 测试通过。该脚本化模型对照不代表真实模型迁移效果。没有读取 .env 或调用真实模型。

补充收尾：Workouts/Runner/L2 三个有测试包的相关竞态回归共 143 项测试/子测试通过、8 项显式验收被跳过、无失败或竞态报告，见 `runs/m3-4-workouts-gate/core-race.jsonl`。Workouts Runner 的完整交付另由上表显式启用，不能把跳过项加为通过。旧 Goddd/Translation 两包另有 47 项测试/子测试通过、2 项显式验收跳过、无竞态，见 `old-scenes-race.jsonl`；不同轮次不合成一个项目总数。`go vet` 对改动包通过，最终规范/补丁清单、旧四个 Profile 摘要、129 个本地文档链接和代码围栏、Go 格式、git diff --check 已核对。

Git 写入首次受到 `.git/index.lock` 沙箱限制；用户现已明确授权，提交前重新核对差异并通过正常提权审批写入。运行证据保留在忽略的 `runs/m3-4-workouts-gate/`，不进入 Git。
