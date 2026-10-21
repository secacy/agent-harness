# M4：真实 Docker 客户端连接中断与恢复

- 日期：2026-10-21。
- 状态：真实客户端连接中断与恢复链路已在下述范围验收；M4 正式评测冻结仍待。
- 目标：[R4 剩余连接故障验收](2026-10-21-m4-closeout-review.md#7-持久化窗口补充后的状态)。

## 实施前取舍

现有真实 Docker 对照覆盖宿主强杀、遗留容器、精确清理和单代重跑；引擎不可用仅有 CLI 替身证据。本步需要让真实 Docker CLI 连接失败，再恢复同一引擎并走生产清理/恢复接口。

| 注入方式 | 得失 | 选择 |
| --- | --- | --- |
| 停止共享 daemon | 最接近引擎停止，但影响用户其他任务和现有容器 | 不采用 |
| 单独运行 Docker daemon/虚拟机 | 可测试服务重启，但新增运行环境、镜像准备与资源成本 | 当前不采用；以后需要 daemon 重启保证时另设实验 |
| Unix socket 转发代理 | 能精细切断在途连接，但须维护代理并发、转发与清理 | 本窗口发生在旧宿主已死、恢复新连接之前，不需要这份复杂度 |
| 临时 context 与测试自有 socket 别名 | 实际 CLI 和 daemon 不替换；切到不存在的 socket 使新连接失败，恢复别名后仍连同一引擎 | 采用；只证明客户端连接不可用与恢复，不声称 daemon 重启或已建立连接被切断 |

创建临时 DOCKER_CONFIG 和 context，不复制凭证或修改用户全局 context。别名在短路径临时目录中，原 context/engine 身份先只读查询；Plan 固定新 context 与实际 engine ID。切断和恢复只修改测试创建的符号链接，宿主控制目录/资源协议不变，不进入容器挂载。

## 验收链路

复用真实 Docker 宿主 SIGKILL 对照：先完成同伴 Target，再在第二 Target 的实际驻留容器运行时核对 PID/管道并强杀宿主。仍保留公共 execution 标签邻居，所有删除仅使用测试自有完整容器 ID。

1. 切断测试 context 后，以实际 CLI info 失败确认连接不可用；通过原 Unix endpoint 只读查询，证明共享引擎仍健康且 ID 未变。
2. CleanupResources、Check/Prepare 在各自原锁内拒绝不明资源；清理诊断为未停止，旧源树、预算与容器不被改动，不创建恢复事务。
3. 恢复别名，确认 context/engine 与首次 Plan 一致，原遗留容器仍存在。按原记录完整 ID 清理，重复清理不改记录，邻居仍在运行。
4. 再切断一次，证明已有成功清理结果不能作为锁外许可：Prepare 仍拒绝；恢复连接并 Prepare 后，断连时 Execute 同样不得分配新代次或创建模型。
5. 恢复同一连接后执行唯一一次新 Run，实际 Docker 基线/工具/独立门禁完整交付；费用累计、旧树不变；最终只读审计与逐 ID 回收核对。

测试只访问本机 Docker 和固定已有镜像，不拉取镜像、不读取 .env、不调用供应商。不可用时保留未知状态，不把连接错误解释成资源不存在，不解除迟到创建阻塞规则。

## 验证计划与边界

先运行替身/核心竞态回归和编译检查，再以显式环境变量启用真实连接实验。产物保存 `runs/m4-connection-01/`，每轮新目录，失败日志保留。若本机环境不支持 Unix endpoint 或固定镜像不可用，明确停止，不把跳过计为通过。

本步以测试为主，若发现生产缺陷先分析触发和语义，再评审最小修正；不增通用清理命令或多代恢复。Git 保留工作区状态，不暂存、不提交。M3 原人员/留出、正式冻结和 Linux 实机证据边界保持。

## 实际实现与结果

新增包内测试连接夹具，复用既有资源强杀/恢复验收流程。只有测试代码和说明改变，生产 Docker、恢复、预算及审计实现均未修改。三个连接窗口都由真实 Docker CLI 报 `dial unix ... no such file or directory`，不是替身返回错误；每次同时通过原 endpoint 查询同一 engine ID，证明共享引擎仍可用。

| 窗口 | 实际观察与断言 |
| --- | --- |
| 宿主已强杀、清理前 | CleanupResources 的静态审计 pass，但实际连接失败，cleanup.json 保存 Stopped=false；Check/Prepare 均拒绝，整个源树不变。恢复别名后遗留容器仍阻止 Check，不能把断连当不存在 |
| 精确清理完成后 | 再断连，Prepare 仍拒绝，不能复用锁外清理成功结果；恢复后重新实时核对才能提交检查点 |
| 检查点提交后 | 再断连，Execute 拒绝，Result=nil，恢复执行目录未创建；模型工厂和能力回调均未调用。恢复连接后只执行一次模型组装与能力核对，完成唯一新 Run |

最终真实场景由父进程核对 PID 并 SIGKILL 测试宿主；原正确 Target 已完成，另一个遗留容器被按完整 ID 清理。清理前 8 条命令记录中 7 个已正常回收、1 个待清理，新 Run 再新增 7 条，最终 15 条都有 removed 见证。公共标签邻居在清理后仍运行，仅由测试最终收尾移除；旧控制、Run、候选和缓存字节及成员不变。

新 Run 为 Completed/ReadyForReview；全批仍为 4 次受控调用、Known=80、Reserved/Unknown=0。该数字是脚本模型机制用量，不是供应商消费或自动迁移成功率。旧根保持 incomplete，恢复执行 completed，静态资源状态 recorded_removed；审计通过不等于重新认证业务语义。

| 验证 | 实际结果 | 证据（runs/m4-connection-01/） |
| --- | --- | --- |
| 核心竞态回归 | execution/recovery/audit/campaign 四包 258 项测试及子测试通过；12 个可选/辅助入口跳过，无竞态 | core.jsonl |
| 真实 Docker 连接故障链 | 一个真实宿主 SIGKILL 场景、三个断连窗口通过，约 118 秒，无竞态；镜像为既有固定版本，无拉取 | real-first.jsonl、real/*/evidence.json、offline-cleanup/cleanup.json |
| 公共入口和命令回归 | 两包 14 项测试及子测试通过，包括候选交付/历史保留、五 Profile 审计和 CLI 参数/退出码 | regression.jsonl |
| 原生命令 | 本轮归档审计退出 0，API/CLI 报告相同，源树不变 | native.json、native/audit.json |
| 最终容器核对 | 真实引擎 ID 与原计划相同；15 个跟踪容器及自建邻居按完整 ID inspect 均不存在，该复核不执行删除 | containers.json、container-inspect.log |
| 静态与构建 | runner/execution/recovery 的 go vet、原生 harness 构建通过 | vet.log、build.log |
| Linux | runner 测试二进制交叉编译成功，未在 Linux 实机执行 | linux-runner.log、linux-runner.test |

首轮连接链即通过，未发现生产缺陷；没有用重复生成寻找成功。初次普通沙箱读取 daemon 版本被连接权限拒绝，随后在工具批准的本机 Docker 权限范围运行真实测试与最终只读核对；没有停止或启动共享 daemon。临时 context/config/socket 别名在测试结束移除，原全局 context 保持。原生二进制仅审计已停写归档，不接触 Docker。

## 完成判断与后续

R4 的客户端连接不可用→恢复同一引擎→精确清理→实时准入→新 Run 已有实证。它不证明 daemon 崩溃/重启、引擎替换、切断已建立连接或掉电恢复；迟到创建仍保守阻塞，旧协议仍只读。没有修改原实验、判定或指标。

当前可以交付声明范围内的 M4 工程证据，接下来收敛发布身份与可复核归档，并按[正式评测冻结门](2026-10-21-m4-closeout-review.md#4-正式评测的冻结门)整理真实缺项；不再新增运行机制。实际留出、人员安排及最终协议/实现/环境/模型/预算未齐备，M4 总体验收和 M5 启动条件仍未满足。不能由连接实验通过自动推断正式评测已冻结。

本轮最终源码/文档和证据校验保存在 `runs/m4-connection-01/verification.json`，运行产物不入 Git。初始登记 691 份文件，仅六份既有测试/文档改变，另增连接测试与本记录；其余 685 份摘要保持，生产代码、冻结数据/规范、oracle、协议和历史实验未改。六份变更文档的 208 个本地链接、32 个锚点、代码围栏、Go 格式和 git diff --check 通过。临时 context/config/socket 目录已移除，默认 context 仍为 orbstack，最终只读检查确认同一引擎健康、全部自建容器不存在。HEAD 不变、暂存区为空，改动未提交。
