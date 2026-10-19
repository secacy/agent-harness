# M3.4：Goddd 迁移规范、宿主 Profile 与证据门禁

- 日期：2026-10-19。
- 状态：在本文声明范围验收；Goddd 通过候选校准与确定性 Harness 交付，尚非真实模型效果评测。
- 代码基线：a03629a；前置：[Goddd 离线基线](2026-10-19-goddd-baseline.md)。
- 目标：同一冻结输入上的正确、明确违规、证据不足候选分别得到 ReadyForReview、Rejected、NeedsAttention；新 Profile 可经已有 Runner 完成交付。无真实模型请求。

## 方案取舍

现有 demo-orders 验证绑定其测试和业务探针，直接替换文件名或去掉 pin 会把不适用的证据误用于 Goddd。另写完整 Runtime 又会复制预算、状态和交付流程。因此保留现有 Runner，通过宿主 Profile 选择新的 Goddd 验证策略；复用 quality 聚合、类型分析和测试结构比较核心，业务适配与探针分别维护，不引入规则 DSL。

### 离线依赖如何进入任务

| 方案 | 在本样本上的具体得失 | 选择 |
| --- | --- | --- |
| 放进 Target.Dependencies 当作普通 SDK | 缓存不是 Go 模块，含大于 1 MiB 的上游文件，不符合现有 SDK 快照约束；放宽全部输入会扩大 Agent 可见面 | 不采用 |
| 业务仓库 vendor | 迁移还需要变更 vendored SDK，偏离只读 SDK 边界，原基线也已使用独立缓存 | 不采用 |
| 宿主 Profile 绑定执行材料摘要，工具内嵌已冻结材料并只读装配 | Target 仍只描述业务、SDK、规范；缓存不进入 Agent 文件根。增加宿主材料装配与审计责任，内嵌会增加程序体积 | 采用。沿用既有只读缓存，验证前后核对完整内容，不允许候选选择或改写材料 |

依赖环境包装器组合已有 Executor，增加只读挂载和 GOMODCACHE，保留其并发槽、断网、取消及回收语义。Runner 的基线/Agent 检查和独立门禁各自装配材料；门禁不借用 Agent 可写目录。已发布两个 Profile 的序列化身份保持不变，新字段仅对新 Profile 出现。

### 如何判定业务变化

只靠行为探针无法证明未覆盖分支；直接按正确补丁字节匹配又会把格式、局部变量和允许的 API 适配混成答案。因此使用认证基线生成的有限 AST 适配计划，检查请求链和原业务结构；RPC 的局部期限前缀由类型身份分析验证，实际请求传播、参数映射、空路线和资源释放由可信生产副本观察。参考补丁及 case 预期不进入运行时门禁。

类型分析扩展到带响应指针的 Call，以及“期限设置/释放 → 无初始化的局部响应声明 → 直接 RPC → 原业务映射”这一有边界形态。允许已解析的类型转换，不把任意函数调用当作无副作用。其他控制流、helper 或绑定变化保留 unknown；不把 Goddd 业务改成 demo-orders 的直线封装来适配分析器。

原测试结构比较复用现有核心，新增宿主提供的、绑定固定基线的适配入口；测试改动只允许明确的导入/API/context 参数适配。生产副本剥离候选测试，安装宿主适配后的基线测试和独立编写的行为探针；测试保留、实际执行和行为验证分别取证。

## 交付与验收计划

1. 发布 v0.3.0 Goddd 规范，保留服务的空路线错误约定；恢复每次 RPC 的 30 秒上限并传递 HTTP request ctx，保护 LICENSE/go.sum。
2. 固定三类候选：正确迁移；去掉超时但原业务测试通过；业务等价的路线映射 helper 重构，行为通过但超出结构证明范围。另做 ctx 断链、删除断言和受保护文件变化的机制反例。
3. 候选校准清单与补丁独立冻结，运行时只得到基线、候选、SDK、规范及宿主验证材料。规范条款、证据路径和实际摘要必须能关联。
4. 本地及真实 Docker 校准三类判定；正例包含真实默认到期；原有分析与测试结构回归、新旧 Profile 身份、新 Runner 交付及失败零模型准入分别验证。

运行执行错误保留 error 和空 verdict；编译/业务断言失败是候选拒绝，可信探针编译或必要事件不足保留 blocked/unknown，不伪装通过。正例必须真正可放行。所有阶段结束复核输入，报告保留工具、Profile、规范与实际材料身份。既有冻结基线、L1 和选择清单不修改。

## 实现与验证边界

新增 `scenarios/rpcx/goddd`，提供单候选 Evaluate，复用 checks.Evaluation 与 quality.Decide；既有 checks 不反向导入新场景。Runner 按宿主 Profile 选择验证器，Campaign 继续调用同一个 Runner，不新增调度框架。新 Profile 的原始字节摘要为 `c987645fd869fc785081b32ee138102f37f8967ff0b544b742666faf23476544`，旧两个 Profile 的固定摘要测试继续保留。

规范单独发布为 v0.3.0，SDK 和 Goddd 基线仍使用原冻结版本，未原地修改数据。新校准版本 rpcx-goddd-calibration-v1 固定六份手写补丁、各检查预期和规范/基线身份，freeze 摘要为 `618b81a7212dc1e1f3c1fe96cf9109a6da4cdec9dfcc8b88348e9ed92ab22317`。清单及补丁只用于宿主校准测试，不被运行时 Evaluate 读取，也不进入模型消息或工具根。

测试适配新增 CompareAdapted 宿主入口，绑定完整原测试文件摘要及适配材料身份；旧 Compare 的默认策略不改变。Goddd 原有表驱动数据和类型声明保持结构时允许保留，新增、删除或改动则标 unknown；删断言仍按违反处理。实际执行同时要求 run/pass 事件、必需测试/子测试及包终态，缺事件不能按空成功通过。

类型分析允许有类型身份的纯转换及局部响应指针前缀；只证明 RPC 到达时的父 ctx、常量期限与释放安排，不证明整个尾部业务逻辑。后者仍由认证基线的生产范围与行为检查承担。范围比较保留原变量和业务骨架，忽略格式/位置，但没有通用变量重命名或任意等价重构证明；这些限制可能增加 NeedsAttention，当前接受该代价。期限前缀仅能在 routing/proxying.go 的边界方法中剥离，同名 mock 方法不得借用此规则。

宿主离线材料由 Profile 固定归档/清单组合摘要，装配后核对全部 516 个文件；每条命令前后认证，发现漂移终止执行。生成和门禁分别创建材料副本，不共享 Agent 可写缓存。Evidence 保存实际宿主归档/清单、共享测试比较器源文件、Goddd 工具/探针以及类型分析器材料。每项证据绑定规范、基线与候选，直接验证命令保存实际 GOMODCACHE 参数、后端、容器、退出码及日志摘要；类型分析保留 worker 命令与材料，额外模块环境由宿主包装器和装配材料确定。可信探针没有条款归因的异常失败保留 unknown，不把设施异常伪装成业务违反。

## 联调发现与修正

- 手写正确候选最初漏了 mock 接口实现；结构比较发现偏差，补齐后通过。没有通过裁掉 mock 或放宽范围来适配答案。
- Runner 原先使用 `go build -o <目录>`，库模块没有 main package 时会失败。统一改为 `go build -o /dev/null ./...`，同时适用于库和命令模块，不在只读副本/工作区生成二进制。原命令参数断言随之更新；Goddd Docker Runner 实际执行了 Agent 的 build/test，而非只测参数文本。
- 基线测试身份之前按最后一个斜线推断包名，会误解子测试路径；改为使用实际事件的 Package，并要求每项必需测试有一次 run。旧受控事件生产器同步补齐 run，原场景继续回归。
- 首轮 Docker 校准因 OrbStack 未启动失败；启动已有后端并确认固定本地镜像后重跑，没有拉取镜像。失败记录保留在 runs/m3-4-goddd-gate/docker-final.jsonl。
- 六例本地首轮的 LICENSE 补丁缺少“原文件末尾无换行”标记，Git 拒绝重放；修正未发布补丁并重算校准 freeze，再跑全部六例。首轮 runs/m3-4-goddd-gate/local-final.jsonl 保留为失败，不计验收通过。
- 第一轮项目回归包含一项旧构建参数断言失败；更新后另行完整回归。早期编译/测试构造失败日志保留，不与后续通过轮次合并计数。

## 实际验收

| 验证 | 实际结果 | 证据 |
| --- | --- | --- |
| 六类本地候选 | 6/6 预期判定及逐检查预期通过，全部原业务测试均通过 | runs/m3-4-goddd-gate/local-accepted.jsonl |
| 三类真实 Docker 候选 | 正确 ReadyForReview、漏超时 Rejected、映射 helper NeedsAttention | runs/m3-4-goddd-gate/docker-accepted.jsonl |
| 真实期限 | 正确、等价 helper 的可信生产副本实际约 30 秒到期；缺期限反例由快速实际观测拒绝，不伪造等待 | 各校准 evidence/commands、logs、analysis 及 evaluation.json |
| 本地 Runner | 新 Profile、生成前基线、编辑、独立门禁、补丁重放和审核链接通过；旧 Profile 身份不变 | runs/m3-4-goddd-gate/runner-local-accepted.jsonl |
| Docker Runner | 脚本化模型使用真实编辑工具与 go_check build/test，独立门禁交付 ReadyForReview，导出补丁重放一致 | runs/m3-4-goddd-gate/runner-docker.jsonl |
| 核心竞态检测 | 4 个包、140 项测试及子测试通过，无竞态报告 | runs/m3-4-goddd-gate/core-race-final.jsonl |
| Profile/基线竞态对照 | 新 Profile、旧材料 pin、子测试包归属、基线失败零模型调用通过，5 项测试及子测试 | runs/m3-4-goddd-gate/profile-race.jsonl |
| 最终项目回归 | 21 个有测试的包、701 项测试及子测试通过，退出 0；25 项条件测试跳过，不计已执行 | runs/m3-4-goddd-gate/project-tests-final.jsonl |
| 最后增补的 Goddd 竞态对照 | 26 项测试及子测试通过，包含期限屏蔽边界、模块等价引号与未知语法处理；与前轮重叠，不合并计数 | runs/m3-4-goddd-gate/goddd-race-verified.jsonl |
| 改动包静态检查 | go vet 通过 | runs/m3-4-goddd-gate/vet-verified.log |

Docker 三类校准开始后补充了更严格的事件一致性检查、探针格式与 HTTP 非法 JSON 归因，并冻结逐检查预期；后续六例本地与 Docker Runner 覆盖这些实际行为。最后将期限屏蔽规则限定到唯一 RPC 文件、补齐 Target 路径规范化，并对有限模块语法无法确定的情况保留 unknown、支持等价引号路径；正例结构及错误范围负例由最终机制测试覆盖，没有改变已运行候选的预期或重写历史报告。最后新增的模块语法与前缀路径检查由 runs/m3-4-goddd-gate/goddd-race-verified.jsonl 覆盖。37 个记录到精确 ID 的 Docker 容器均已不存在，Harness 标签下无残留；Agent go_check 的两次容器 ID 未进入工具文本，另以成功回收返回及标签清单核对。结果见 runs/m3-4-goddd-gate/docker-cleanup-verified.json；首次脚本因错误文本大小写未识别“no such object”的记录保留。逐检查矩阵见 runs/m3-4-goddd-gate/matrix.json。最终项目回归退出 0；最后增补分支另经 Goddd 竞态对照和静态检查验证。未重跑完整 L1 独立正式矩阵或旧场景全量 Docker，相关条件跳过项不计实际执行。

## 本步交付与下一步

Goddd 已能够通过宿主 Profile 经现有 Harness 完成“基线检查 → 工具循环 → 独立判定 → 候选补丁与审核材料”闭环；模型是确定性替身，不是一次真实自主迁移。手写校准集证明新场景的有限支持和拒绝/保留缺口能力，不能代替独立 oracle，也不能计算真实错误放行率或宣称 M3 整体完成。

没有读取 .env、调用真实模型、修改旧冻结数据/历史实验或开放付费 batch 命令。其余两个开发切片、独立参考评测、供应商真实预算上界和留出准备仍待完成。下一项工作按已有选择准备 go-clean-template 的离线切片与两种必需构建配置，再单独接入其验证范围；Goddd 的真实模型运行仍须按冻结实验协议和新的实际授权推进。

交付检查：126 个本地文档链接目标与围栏配对、22 份变更 Go 文件格式、git diff --check 及旧冻结数据/历史归档边界检查通过。原始运行材料和本轮源码/日志摘要保存于 runs/m3-4-goddd-gate/summary.json，运行产物不入 Git。
