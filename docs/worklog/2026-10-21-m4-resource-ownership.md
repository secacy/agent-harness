# M4.3：Docker 资源归属与精确清理

- 日期：2026-10-21。
- 状态：新声明协议的 Docker 单代恢复，在以下范围验收；未知创建保持阻塞。
- 前置：[单代受控重跑](2026-10-21-m4-controlled-rerun.md)。

## 本步目标与选择

原 Docker.Run 只在内存保存随机名称，进程存活时 defer rm；宿主被杀后没有持久化归属。公共 execution 标签只能找到 Harness 容器，不能证明属于哪次任务；create 响应未知时按名称删除一次也不能排除稍后创建。

| 方案 | 得失 | 取舍 |
| --- | --- | --- |
| 按公共标签扫描后全部删除 | 实现少，会误删其他 Run/批次 | 拒绝 |
| 只保存成功返回的容器 ID | 精确，但无法覆盖创建响应丢失及落盘前死亡 | 不足以作为恢复前置条件 |
| 创建前落盘独占名称、归属和请求摘要，容器带该记录摘要；按 engine/name/ID/标签/镜像/挂载核验 | 有额外控制记录，但能覆盖找到容器时的创建不确定性 | 首版采用 |
| 查不到就算不存在，或等待固定时长 | 无法排除服务端迟到创建，没有可证明的等待上界 | 不采用；发出创建意图后无完成见证且查不到时保持未知，阻止恢复 |
| 后台守护进程/创建代理 | 可进一步管理创建在途性，但新增独立进程、通信和恢复协议 | 暂缓；若未知创建频繁阻碍交付再权衡 |

原 Campaign 计划可选资源协议，冻结 Docker engine ID 和显式 context；旧批次不能补记录冒充原先受跟踪。新注册表与逐命令意图放在根 resources/，不挂入仓库。Runner 知道真实 Run ID 后绑定只属于该 Run 的执行器，覆盖基线、Agent 工具、类型加载和独立门禁。恢复代次沿用同一注册表，但独立检查点不冻结持续增长的运维记录；审计每次重新核对它。

## 状态与停止条件

先保存 intent，再保存 dispatch（表示下一步可能发 create），再执行唯一一次 create。成功响应或精确 inspect 找到资源后保存 created 见证；删除仅使用核验后的完整 ID，再确认不存在后保存 removed。没有 dispatch 可证明没有发起；有 dispatch、无 created、查不到为不确定，绝不因锁释放、重试清理或 daemon 重启而释放该状态。删除/检查失败保持待处理，重复清理不得启动新命令。

清理在原 Campaign 写锁内进行，先静态核对原计划、Run 和资源记录，拒绝活写者、缺协议、错误 engine、归属/摘要/镜像/挂载不匹配及不安全路径。只清理可证明属于该批次和 Run 的容器，不做全局 prune。清理后再用只读检查确认静止，才能进入原恢复检查；资源静止不表示供应商请求已停止或费用可退。

验收先用受控 Docker CLI 注入写入失败、响应丢失、迟到创建、错误归属、daemon 不可用、删除失败、重复清理及并发命令；再运行真实容器强杀宿主/清理/无误删对照。当前 OrbStack 状态为 Stopped，真实验收须以实际连接与执行结果为准，不能把跳过记为通过。无真实模型调用，不改历史实验或付费授权。

## 实现与安全边界

新增 [资源协议与存储](../../internal/execution/resources.go)、[Docker 实时核对](../../internal/execution/docker_resources.go)和[本代命令准入保护](../../internal/execution/resource_guard.go)。ResourcePolicy 固定协议、context、engine ID；ResourceRegistry 绑定原 Campaign/Plan。意图记录独占随机名称、Target/Run 相对身份、镜像、只读挂载和 CLI 参数 JSON 摘要；摘要不是 HTTP 请求字节。容器保存意图与注册表摘要标签，标签只作联合核验，不是扫描后直接删除的许可。

新 batch Docker 路径在首次计划中组装协议。Campaign 接纳任务前发布注册表；Runner 在首份 run.json 后填入真实 Run ID/路径，绑定覆盖请求归属的执行器。基线、Agent 工具、类型加载和独立门禁经相同后端记录。原无协议单 Run/历史批次不回填、不升级。控制目录不进入源码挂载，镜像与既有资源约束不变。

每份记录先文件同步、同目录 rename、目录同步；已发布内容不覆盖，规范 JSON 拒绝重复键、未知/隐式缺字段及重新排版。临时文件和无 intent 的初始化残片不授予创建权限。dispatch 后无创建见证而查不到时保持未知；成功响应丢失但找到并核对容器时，可先补 created 再删除。既有 created 还核对完整 ID；原名称重用、容器改名、错标签/镜像/挂载均阻塞，不误删。daemon 不可用或身份变更同样阻塞。

清理先检查逐条意图，再用注册表标签做只读库存核对；未登记容器只导致阻塞，不被删除。任何命令回收未知均返回 ErrResourcesUnknown，Campaign/恢复共享的 ResourceGuard 在槽位释放前关闭本代后续命令准入，避免不断积累遗留容器；已在途命令各自退出/回收。护栏不会自动复位，也不解除未知费用。

[CleanupResources](../../internal/recovery/resources.go)取得原锁、认证计划、完整只读审计后精确清理，再实时只读复核和发布后审计。活写者拒绝清理；错误可能发生在已清理部分资源之后，保留 cleanup.json 诊断。重复调用的最终静止状态和已发布记录不变，逐次动作可由 removed 变为 absent。成功返回已释放锁，不能当作恢复许可；Check/Prepare/Execute 仍用 DockerResourcesStopped 在自己的锁内重新检查。没有清理 CLI、全局 prune 或 assume-clean。

审计升至 campaign-audit-v5，独立核对资源记录与原 Plan/Run 的关系，resource_state 只表述 recorded_pending/recorded_removed，违规/缺口时为 unknown。它不连接 Docker，不证明当前资源停止。resources 是随新 Run 增长的运维子树，不进入迁移检查点 pin；原业务/控制与质量证据 pin 不放松，每次审计仍读取并复核资源来源。该长期选择见 [ADR-010](../adr/010-docker-resource-ownership.md)。

## 验收结果

| 检查 | 结果与范围 | 证据（runs/m4-resources-01/） |
| --- | --- | --- |
| 核心竞态回归 | execution/campaign/recovery/audit 四包 258 项测试及子测试通过，12 个辅助/可选 Docker 入口跳过，无竞态 | core-complete.jsonl |
| 受控 CLI 故障 | 实际经过替身 CLI 进程：正常与重复回收、创建响应丢失后精确回收、迟到创建不被反复缺失放行、错标签/镜像/挂载/ID、改名、引擎更换/不可用、删除失败、未登记容器、不可持久化意图零 create、请求不能覆盖归属、未知后不再发新命令 | 上述 execution 测试；resources-first 为较早子集 |
| 静态审计反例 | pending/removed 记录与原计划/Run 正确绑定；错归属/计划/见证、null 见证、额外成员、重新排版、路径链接、旧协议均不得 pass | 上述 audit 测试；audit-resources-first.jsonl 为早轮 |
| 真实 Docker 中断、清理和恢复 | 最终单案例通过、无竞态：活写者清理返回 Busy；按管道/PID 核对自身宿主后 SIGKILL，遗留容器阻止恢复；精确清理后只读检查与 Prepare/Execute 通过，新 Run 在 Docker 实际构建/测试/门禁并交付 ReadyForReview | docker-final.jsonl；docker-final/*/evidence.json、cleanup-1/、cleanup-2/、execute/ |
| 真实并发命令 | 四个同时执行的 Docker 命令正常退出/回收，无竞态；记录独立，实时复核无资源残留 | docker-concurrent-final.jsonl、concurrent-final/*/evidence.json |
| 真实无误删与旧成果保留 | 带公共 execution 标签的测试邻居在清理后仍运行；重复清理不改记录，旧控制/Run/候选/缓存全树字节与成员保持，仅 resources/recoveries 按协议新增 | 同上固定断言 |
| 首次 Runner/CLI 回归 | 两包 20 项测试及子测试通过，含实际串并行故障隔离、只读命令退出码与离线配置 | runner-cli.jsonl |
| 五 Profile 归档回归 | 五 Profile、损坏绑定、未完成/不支持格式及报告漂移共 24 项测试及子测试通过 | profiles.jsonl |
| 原生命令 | 旧真实/混合批次、旧检查点、旧单代重跑和新 Docker 恢复五份材料，退出均 0，consistency=pass，来源全树不变；新 Docker 的 CLI/API 报告一致 | native.json、native-* |
| 编译与静态 | 28 个有测试包编译、7 个无测试包跳过；gofmt、go vet、原生构建通过；最后的并发测试由实际测试编译覆盖 | source-compile.jsonl、vet-complete.log、build-complete.log |
| Linux | execution/audit/recovery/campaign 四个测试二进制交叉编译通过，未在 Linux 上执行 | linux-*.log、linux-*.test |

最终真实案例在清理前有 8 条命令记录：7 个已正常回收，1 个宿主被杀后遗留。清理按该记录完整 ID 删除 1 个；新 Run 再增加 7 个命令，均正常回收。两次模型尝试合计 4 次受控生成、Known=80、Reserved/Unknown=0；这是固定参考编辑的机制用量，不是供应商消耗或自主迁移成功率。再次创建的 Run 使用原冻结输入，未续接旧候选。

最终逐 ID 复核确认两轮各 15 个跟踪容器及测试邻居均已不存在；邻居只由测试自己的最终收尾删除，清理阶段的无误删断言独立保留。初始 OrbStack 已停止，直接启动受工作区权限限制；经授权的本机启动后，真实 Docker 29.8.2 与预装固定镜像可用，全程未拉取镜像。首轮 Docker 链路通过；随后补上本代 ResourceGuard、活写者拒绝断言、延长测试邻居驻留期，再用全新 docker-final 目录验证，原轮不回写、不相加为独立样本。首轮最终 ID 检查脚本误按大小写匹配 Docker 的缺失诊断，拒绝了实际的 `no such object`；修正脚本后核对通过，不改生产逻辑。保留 container-verification.log，最终结果见 container-verification.json。最终评审还修正了指针 JSON 解码可能把 null 见证视为缺失的问题：所有资源记录强制为对象，新增三个见证文件及审计反例均通过；最终核心回归覆盖，真实 Docker 正例和协议流程不变。并发评审将正常收尾改为仅读本命令记录，整表留给持锁清理；见证按 removed→created→dispatch 读取，避免读到先缺前驱后见后继的不可能前缀。补充四命令真实并发对照及最终核心回归通过。

## 停止边界与后续

本单元完成 Docker 资源归属→锁内审计/精确清理→实时检查→单代重跑的闭环；没有真实模型调用、付费恢复入口、连续多代恢复、Local 任意后代清理或掉电保证。引擎故障/更换和迟到创建的故障注入是受控 CLI 证据，没有停止用户的整个 daemon 冒充隔离故障；无完成见证且始终查不到的创建仍可能长期阻塞，这是当前明确的可用性代价。

暂不继续增加执行机制。下一项应先按 M4 验收表核对已交付能力、残余缺口和最低可交付，再推进既有评测协议/样本口径的冻结；独立留出仍待准备，M3 原复核边界不变。整个 M4 与 M5 不因本单元通过自动完成。旧规范、夹具、补丁、oracle 和历史实验不改，运行产物不入 Git；改动保持未暂存、未提交。

最终交付检查：11 份变更文档的 219 个本地链接、41 个锚点、代码围栏、Go 格式和 git diff --check 通过；本轮开始时的文件中仅 23 份代码/说明有列明变更，冻结数据与实验文档摘要保持。原生命令、逐容器 ID 核对、核心/并发/真实恢复及最终源码摘要见 runs/m4-resources-01/verification.json。HEAD 保持 d9b15b9，暂存区为空；本地引擎仅为测试启动，未停止全局 daemon 或清理用户其他资源。
