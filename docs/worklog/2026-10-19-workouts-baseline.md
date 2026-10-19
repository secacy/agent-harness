# M3.4：Workouts 回调与多模块离线基线

- 日期：2026-10-19。
- 状态：在本文范围验收；代码基线 8be2c81。离线基线完成，迁移 Profile/门禁待接入。
- 前置：[样本选择](2026-10-19-m3-4-sample-selection.md)、[Translation 接入](2026-10-19-translation-evidence-gate.md)。
- 本步交付：第三个开发来源的 U→B 裁剪记录、共用 v1 SDK 植入、trainings/common 两模块及离线复验；不接迁移 Profile/门禁，不运行模型或统计效果。

## 实现前的取舍

| 问题 | 方案及具体得失 | 本轮选择 |
| --- | --- | --- |
| 如何保留取消业务 | 整仓需要 Firestore、gRPC、服务启动和云端组件测试；重写最小 demo 又会丢失原回调/领域规则 | 保留原取消 command、原测试和完整 training 领域及其测试、common 泛型装饰器/logs/metrics 的必要闭包；只替换出站传输。保留 callback 内余额条件、两远程副作用顺序与错误包装，不改成直线逻辑 |
| 服务接口的裁剪 | 保留整个 TrainerService 需连带实现 Schedule/Move；用报错占位方法会制造无意义业务；仅取消接口会缩小切片对外 API | 保留 UserService 和 TrainerService.CancelTraining 原签名，移除非取消的 Schedule/Move 声明及适配器方法；原审查材料保留完整文件。此为明确覆盖损失，不声称整个上游服务 API 兼容 |
| common 模块 | 合并进 trainings 最省装配，却消除已选多模块差异；把 common 当不执行的静态文件又缺少依赖自身证据 | 保留原模块路径与本地 replace；common 为只读本地模块，独立构建/测试，trainings 还实际经过其泛型装饰器。基线工具只增加宿主固定的本地模块列表，不引入可配置工作流 |
| v1 的 ctx 语义 | 把 ctx 透传进自造 SDK 会提前完成迁移；在出站层加取消预检查会改变原接口行为 | 两个适配器继续接受 ctx，但调用共用 rpcx-v1 的无 ctx Call，隐式 30 秒期限由 SDK 提供。文档明确父 ctx 在 v1 传输中不传播，这是待迁移差异；业务测试验证取消规则/顺序和默认期限，不要求迁移后继续丢失父 ctx |

固定上游 8ecfcdf05b1462c4757bd2dcac9086c78e9f7791，逐文件核对 Git blob 与已选摘要。新数据发布 rpcx-l2-workouts-v1，不修改旧数据/规范/选择清单。准备时可下载公开依赖，正式复验使用认证材料、空构建缓存、禁网 Docker 与只读输入。

## 验收计划

- 原取消四子例与领域测试保持字节，新增测试单列，不以删断言使基线变绿。
- 两个真实 RPC 后端检查参数、余额规则、无余额分支、余额失败阻止教练调用、教练失败不提交仓储；不制造跨远端事务回滚保证。
- 原仓储回调提交语义由新增内存仓储探针实际观察：失败不提交本地副本，但已发生远端副作用不回滚。
- common 实际被 trainings 使用；每模块构建/完整测试事件单独保存。缺本地模块、原测试事件、模块结果或命令失败均不能 accepted。
- 来源原件＋准备补丁可重放当前基线；全部依赖/许可证/校验和、输入与工具身份可审查。
- 本地与固定 Docker 正式复验通过、前后材料不变、记录容器回收；原 Goddd 默认入口与 Translation 矩阵机制继续回归。

## 实际数据与保真范围

发布 [rpcx-l2-workouts-v1](../../scenarios/rpcx/l2/data/rpcx-l2-workouts-v1/README.md)，freeze SHA-256 为 `5efa3908ad6f80a8673a4893ef28bb6213a4a8d9a281bce4498726aa69282cb2`。74 份材料由清单保护，加 freeze.json 共 75 份。trainings 有 19 份文件（13 原样、5 修改、1 新增），common 有 11 份（8 原样、2 修改、1 新增）；另复制共用 SDK 八份原字节。30 份上游审查件逐一核对固定树的 Git blob 身份，选择阶段查阅的八份文件也核对原 SHA-256。原选择清单、既有数据、规范和历史实验不回写。

原取消 Handle 文件完全保留：`UpdateTraining → callback(ctx, tr) → tr.Cancel → 条件余额更新 → 教练取消 → 返回 tr`。四种取消规则的原表数据、断言和 mock 也不改；完整 training 领域及四份原测试因包闭包保留。原测试/子测试共 26 项。领域附带的 reschedule 测试仍执行，但 Schedule/Move 的远程接口和服务路径不在切片内，不能据此声称这些功能已迁移或已验证。

两个出站类型保留 UsersGrpc/TrainerGrpc 存量名称，构造参数改为本地后端地址并返回连接错误，内部使用相同 rpcx-l2-sdk-v1。用户 ID、int64 余额和训练时刻含义保留；Go time.Time 代表原 Protobuf timestamp 的同一时刻，响应仅为空确认。网络、序列化、连接行为不是保真承诺。v1 丢失父 ctx 并隐式设置 30 秒，这是显式植入的迁移任务条件，不冒称原上游行为。

新增 11 项 RPC/取消测试与 common 的 3 项装饰器测试，连同 SDK 5＋10 项，实际执行计数为 `26＋11＋3＋5＋10＝55`。它们仍只对应一个开发来源、一个业务任务；common 独立构建测试不把样本数加一。新增测试实际经过原泛型装饰器、原 Handle 和两个 RPC 适配器：检查四种余额规则、零余额分支、请求参数、余额失败阻止教练调用、教练失败不提交本地仓储、成功才提交、原错误链以及每次默认期限和返回释放。已成功的远端余额副作用保留，不虚构补偿或事务回滚。测试仓储只代表所声明的回调提交契约，不证明 Firestore 实现。

来源映射先将原 internal/trainings、internal/common 分别映射至同级模块根，再重放 adaptation.patch；JSON 记录每个文件的源/目标与原因。原件、完整模块依赖版本及许可证均保存；原英文注释按字节保留，新增注释中文。Logs 的 HTTP/formatter 启动器和云端 service/component_test.go 作为审查件保留、明确排除；取消闭包没有使用它们。common/errors 为原领域实际需要的闭包，保留原件；不重写领域错误或第三方库来省依赖。

## 离线依赖与工具变化

用 common/trainings 各自 `go list -deps -test -json` 的实际版本联合闭包确定分发内容。6 个完整源码模块为 uuid、pkg/errors、logrus、testify、yaml、x/sys；它们的实际版本保持固定上游选择。裁剪后 tidy 初次将 x/sys 降至 logrus 最低要求 v0.13.0，准备时显式保留上游已选 v0.47.0，并重新整理两模块校验和；没有把这个准备中间态发布为基线。

其余 3 个仅在裁剪模块图出现的版本（包括 common 图中未执行的依赖测试版本）只保留元信息与许可，标 source_included=false；按 path＋version 区分，不因另一个版本被使用就冒称整个模块源码均分发。9 个模块记录、801 份归档成员，展开 11,361,216 字节、压缩 1,874,651 字节，沿用单文件 8 MiB、总展开 32 MiB、10000 文件的现有限制。两个模块在专用准备缓存中的 go mod verify 通过。

baseline 的静态 dataset 增加 LocalModules；Workouts 固定为 rpcx-v1 → rpcx-v2 → common → trainings，各模块都执行 version/build/test。认证的 tests.json 必须恰好覆盖完整模块集；只读挂载包含 common，业务通过相对 replace 使用它。只增加既定准备流程的模块集合，不新增规则 DSL、动态命令或 Runtime 插件。Goddd 默认三模块、Translation 两配置入口及发布 pin 保留；新版本仍由宿主固定 freeze 摘要，不能修改后重算同名清单。

## 验证结果

| 验证 | 实际结果与限制 | 证据 |
| --- | --- | --- |
| 来源与准备重放 | 上游 Git blob/已选文件摘要一致；从审查原件按映射重放准备补丁，两个业务模块的文件树完全一致 | runs/m3-4-workouts-preparation/upstream-files.json；下方机制中的 TestWorkoutsPreparationPatchReplays |
| 机制与竞态回归 | baseline、Goddd、Translation 三个有测试的包，97 项测试/子测试通过，无竞态报告；l2/CLI 编译通过；原双配置机制仍覆盖全部 14 条命令/67 项执行预期 | runs/m3-4-workouts-preparation/mechanisms-final.jsonl |
| 多模块故障注入 | common 构建非零、缺事件、取消、工作副本缺 go.mod，以及业务缺事件都不能 accepted；前置 common 失败不启动业务命令。替身不冒称真实红测 | 同上 TestWorkoutsRequiresLocalModule |
| 正式可信本地 | 12 条命令、55 项测试/子测试，accepted=true、inputs_unchanged=true；认证归档另行装配、构建缓存为空，不用准备机模块缓存 | runs/rpcx-l2-baseline-1239354218/report.json |
| 正式 Docker | 相同工具/冻结身份，固定镜像、禁网、只读模块与依赖、各命令新容器和空缓存；12 条命令、55 项执行通过 | runs/rpcx-l2-baseline-665645787/report.json |
| 容器回收 | 报告中 12 个精确 ID 均不存在，Harness 标签下无残留；只读查询，没有删除其他资源 | runs/m3-4-workouts-preparation/container-cleanup.json |
| 旧 Goddd 默认入口 | 实际复跑 9 条命令、37 项测试/子测试，accepted=true、inputs_unchanged=true | runs/rpcx-l2-baseline-2006354170/report.json |
| 静态与依赖检查 | l2/CLI 的 go vet 及准备缓存的两模块 go mod verify 通过 | runs/m3-4-workouts-preparation/vet.log、common-module-verification.log、trainings-module-verification.log |

本地与 Docker 报告 ToolSHA256 均为 `57dfcaf26f25b4a99588242b60ae5f382e60f5f977bba079599d9c307776dd29`，保存实际工具文件、命令及日志。这不是整个 Harness 二进制身份。首轮机制日志 mechanisms-first.jsonl 保留：新测试直接比较 /var 与规范化后的 /private/var，六个子例在执行注入前失败；测试输出目录改用相同路径规范化，未放松输入/挂载认证，最终竞态回归通过。来源准备脚本的未完成中间输出不作为发布证据。

没有运行完整项目 Runtime 回归、旧场景全量 Docker、L1 正式矩阵或真实模型；改动限于新数据及准备工具的静态模块集合，原默认真实基线和双配置机制分别复验，不把条件跳过项或重复执行相加扩大覆盖。

## 交付判断与下一步

第三个开发来源已具备可审查来源、原回调/条件副作用与原测试、多本地模块和可复跑的 v1 基线。完成的是数据准备与环境/保真验收，没有注册 Workouts 迁移 Profile、规范或候选门禁，也没有独立 oracle 和模型效果数据。

下一步设计 Workouts 迁移规范与宿主 Profile，明确如何证明回调 ctx 来源、两个客户端逐调用的期限/释放，以及零余额分支和部分远端成功后的错误/提交语义；以正确、明确违规、超出证明范围三类候选校准后再接 Runner。随后才汇入三个异构 Target 的 Campaign 与独立参考评测。M3 整体仍未完成，留出名额仍待独立准备。

未读取 .env、调用模型服务或新增付费 batch CLI。源码、来源、归档、报告与日志身份和本轮核查结果保存在 runs/m3-4-workouts-preparation/；运行产物不入 Git。按一项完整工作提交，不按文件拆分。

交付核查：141 个本地文档链接目标及围栏配对、33 份变更 Go 文件格式、来源补丁重放、发布 pin 与全部 74 份材料、两份正式报告的当前工具/日志摘要及 git diff --check 通过。已有选择清单、数据、规范、Profile 与历史归档未改动。结果及源码/材料身份见 runs/m3-4-workouts-preparation/summary.json。
