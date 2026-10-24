# 当前实现四任务开发观察：协议与启动准备

- 日期：2026-10-24（Asia/Taipei）。
- 状态：本地准备验收后，已获本版授权完成唯一真实执行、参考、实际助手复核与收尾，见第 7 节；下文准备阶段表述保留原时点。
- 实现基线：`d4bd69b`。
- 前置：[历史候选回顾](../experiments/2026-10-24-gate-value-review.md)。
- 交付：[l2-current-dev-v0.1.0 协议](../experiments/l2-current-dev-v0.1.0/README.md)，含固定配置、纳入、数据、当前实现/环境与启动器。

## 1. 为什么现在做这个

已有回顾解释了门禁的价值和误拒/未知成本，但使用的仍是旧候选、旧报告。公共取证与 GHCLI 门禁补强已经通过脚本化交付；它们不能说明当前自主生成会产出什么。本轮准备一次当前实现的新候选开发观察，记录真实正确性、未知和成本，随后停止并报告。

不以四项全部 ReadyForReview 作为完成条件。Rejected、NeedsAttention、无候选和执行失败都是观察结果；不能看到失败后修补再跑，也不能用当前旧候选的校准结果替代。

## 2. 样本、预算与方法取舍

| 选择 | 能回答什么 / 当前代价 | 本次决定 |
| --- | --- | --- |
| 继续单 GHCLI | 消费上限低，可检验新生成，但仍围绕近期单场景 | 不作为本轮主体 |
| 四个已有 Profile 各一次 | 同一当前实现跨四种业务/构建布局；输入已验收，接入变量较少 | 采用，全部明确 seen-development |
| 新仓库/严格留出 | 更能检验推广性，但要准备来源、可信规则/参考和暴露边界 | 保留独立工作，不临时塞进这次观察 |
| 再重放旧候选 | 能检验现有规则兼容，不能观察新生成 | 已有证据足够，不重复代替真实观察 |

采用与旧四任务相同的业务基线、规范、Profile、顺序与单任务限制：40 次准入/80 次工具/250 万 Token/10 分钟；两个任务及两个命令槽。全批 160 次准入/320 次工具/1000 万 Token，为四项上限之和，避免额外引入紧共享额度竞争。仍受每请求保守预留 Q 和实际已知/未知费用约束，不保证能调用到次数上限。宿主 40 分钟后停止，最多再尝试 60 秒收尾。

这些是沿用的开发参数，不声称最佳容量或硬金额上限。更紧总预算能限制消费，却会把迁移结果与共享池挤压混在一起；本轮优先观察单次任务表现。增加单任务额度可能提高完成机会，也会破坏旧限制的可比性；本轮不放宽。

GHCLI 的 method 标签从旧隔离准备后已校准统一记为已见开发：其基线、旧候选和答案已进入后续开发与复核，不再声称未见。旧分层结果保持原名，本轮 N=4 全部已见，Translation 两配置仍是一个 Target。

## 3. 当前与历史的差异

- 生产门禁包含后续分类、有限来源/Body/完整边界证明与响应返回值观察，公共命令取证已接入。
- 独立参考固定当前发布身份，GHCLI 改用 data-v3；数据/工具摘要都重新登记。旧 v2 报告不重写、不在新协议里冒充 v3。
- 能力采用已验收 model-contract-v1，固定相关模型能力/请求设置，同时保存当前实际 Models 原文身份；不因无关展示字段变化自动停止，但关键契约变化仍零生成停止。
- 本次助手参与开发、旧复核与协调，所有输入已经暴露，关系写入 reviewers。用户此前已委托后续复核由助手完成；没有新独立人类或盲审安排。

因此新旧结果不能作严格因果比较。模型别名、底层权重/默认采样和执行噪声未固定，多个实现与参考变化同时存在；即使成功数增加也不归因某个修复。目的仅是得到当前四个已支持输入的一次新观察，原四任务 1/4、后续 GHCLI 0/1 独立保留。

## 4. 实际准备

1. 从已提交实现导出 809 份 cmd/internal/scenarios/scripts/模块材料，不含 docs、runs、凭证或 Git；源码身份为 `163bb32ded02902622fe313d742ce05f7ab43c5260c75a411abb7ee4e256c0cf`。从快照禁用依赖网络构建 harness/summarize，随后在新输出位置再次构建并核对两份二进制一致。
2. 通过已有 TargetFrom/ValidateTarget 与 PublishedIdentity 核对四任务实际快照、规范和当前参考。只读检查 Go、现存 Docker 镜像及 engine，无拉镜像、容器或仓库命令。
3. 原样复制 2026-10-23 的 Models/解析材料作为历史期望，不读取 .env 或再次查询。最初准备辅助代码仍引用 2026-10-20 的旧路径，在固定协议前改为本版历史材料，重新生成期望 Plan；未改变任何旧文件。
4. 固定 dataset/reviewers/implementation/environment、schema 2 development Enrollment、batch 与离线 expected-plan；ReadProtocolMaterials/Bind 核对四份实际材料并演练拒绝条件。
5. 复用已验收启动器，仅改版本身份、Target 数与 12→40 分钟宿主窗口。保持独占启动记录、干净 Git、材料/源码/二进制/环境检查、只解析 Key、不执行 shell 文本、无重试和自有进程组停止规则；不新增生产执行框架。

启动器和配置绑定本机路径/引擎；要搬机器执行需另版，不编辑原冻结文件绕过。freeze 不包含自身，协议文档也不进入实现源码列表，避免身份递归。freeze 后不得改本版成员而不重新发布；这里的 worklog 和入口说明不属于冻结成员。

## 5. 已验证的边界

| 检查 | 实际结果 | 证据（runs/l2-current-dev-v0.1.0-preparation/） |
| --- | --- | --- |
| 当前输入及环境 | 四项输入/规范/活跃参考、只读镜像与引擎核对通过 | metadata.json；协议 environment.json |
| 源码及二进制 | 809 份当前文件与快照一致，离线构建及另位置重建相同 | source-identity.json、build/rebuild 日志、verification.json |
| 协议绑定与漂移对照 | 四 Target / 四材料 / model-contract-v1；无关原文变化接受且 Plan 身份保留；能力/额度/尝试/材料/未知策略漂移拒绝 | expected-binding.json |
| 固定二进制 batch --check | 通过，未查询能力 | check.log |
| 完整启动器 --check | valid=true、targets=4、model_queried=false、credentials_read=false、batch_created=false | launch-preflight.json |
| 启动器复用/局部契约 | 与旧已验收实现差异仅限上述常量；9 项假凭证/优化模式对照通过，未读取真实密钥 | verify.py、verification.json |
| 文件冻结 | 12 份冻结成员、当前/导出 809 份源和两二进制一致 | freeze.json；verification.json |

本版 freeze SHA-256：`d1935862e4cebd6b6e571bf473e7985609abc2aefdd82e3cf0bd39e97bf540e1`。harness 二进制为 `ca000a1e49ca4103bb5d6ff10564f657c8d7ead9e90b281b59206ee99aa8a263`，summarize 为 `068e1ffee4dfcf3e62f18cb7a9411f316460f6f62f7de354af415b71059abc5f`。

本轮没有重跑全项目 Go/真实业务测试或生成实验；本地 Go 辅助程序只认证输入/发布身份及绑定，准备结果不作为新的业务效果。没有创建本版实际批次或启动收据，没有读取 .env、发起 GET/POST 或创建容器。

## 6. 执行授权及完成条件

此前一次性许可已经分别用于原实验。本版尚需明确许可：仅向既定 DeepSeek 两端点传输任务代码、规范、SDK/允许依赖及工具交互；一次 Models GET，最多 160 次生成、320 次工具、1000 万保守 Token，每项 10 分钟、全批宿主 40 分钟加最多 60 秒收尾，不重试/追加。Key 只用于鉴权，不进入模型消息或交付材料。

授权是实际材料提交之后、读取凭证/外发前的最后一步。无授权时停在本地准备完成；能力/环境漂移或启动前错误留下失败记录，不自动重新申请另一轮或放宽本版。

若执行，按固定版本原样保留所有结果，对全部可认证候选做固定独立参考和实际助手复核；新目录保存审计、补丁重放、导航、费用、资源回收和分层汇总。缺项保留未知。此后先报告再选择研发，不因一次失败自动继续规则扩展或消费额度，也不靠本轮关闭原严格留出/人员验收条件。

文档收尾：五份变更 Markdown 的 226 个本地目标、26 个锚点及围栏检查通过，`git diff --check` 通过。协议、固定清单/摘要、启动器和对应说明作为一个完整准备单元提交；大源码快照、二进制及准备日志留在 runs，不入 Git。


## 7. 授权后的唯一实际执行

用户对本版回复“授权按本协议运行一次”。以提交 `65f7e06`、冻结二进制及原协议启动，11:07:45.792657+08:00 至 11:18:24.523836+08:00，墙钟 638.730 秒，退出 0。一次 Models GET 后准入成功，四项均 Completed；原质量 NeedsAttention 3 / Rejected 1 / ReadyForReview 0。107 次生成准入、201 次工具调用、1,642,276 已知 Token，无未知/在途占用、拒绝、自动重试或候选修补。

四个固定参考各执行一次，Goddd/Translation/GHCLI 自动 pass，Workouts 因直接 RPC 无 deadline 而 fail。实际助手先读各原补丁并记 initial 源码观察，再消费参考和完成三项复核：Goddd、GHCLI 完整 pass；Translation 因原断言削弱而 test fail；Workouts 为 migration fail。未把自动参考 pass 或助手支持改成原门禁放行；最终有效自动成功 0/4、需确认 3/4、错误放行率 A=0 无定义。完整理由、限制及成本见[实际报告](../experiments/2026-10-24-rpcx-current-dev-real-model-01.md)。

| 收尾 | 实际结果 | 证据（runs/l2-current-dev-v0.1.0-result/） |
| --- | --- | --- |
| 只读审计 | pass / completed / 扫描完整，违规与缺口 0，资源 recorded_removed | audit/audit.json |
| 补丁重放/审核导航 | 四份补丁无模糊/偏移重放后等于原候选；711 个本地链接存在 | replay-navigation.json |
| 固定参考及实际复核 | 参考 48 条命令，三 pass/一 fail；实际完整两 pass/两 fail；参考未执行项保留 inconclusive | reference-results.json；references/、reviews/ |
| 最终离线汇总 | valid/execution_complete/inputs_unchanged=true，四项全部纳入 | round-final/round.json |
| 来源保全 | 原批次 19,289 份非缓存文件、809 份实现原文/副本、两二进制、12 份协议成员保持 | verification.json |
| 复核定位 | 62 个实际复核源码/证据路径存在 | verification.json |
| 资源回收 | 固定 engine/context 的 131 个精确 ID 均不存在，原 registry 摘要标签下为空 | resources.json；registry-live.json |

参考 driver 只调用固定 Evaluate API，使用协议声明后端和 15 分钟父时限，不传入原 verdict/expected；生成阶段结束后才运行。Workouts behavior 得到明确失败后 expiry 未执行，并未将该未知伪装成执行通过。Translation 的断言问题用保存源码的谓词差异说明，没有另造生产超时失败或改候选。没有重复生成、规则变更、补跑失败参考或伪造人类活动计时。

本单元只新增实验与来源说明，不修改生产代码或冻结协议。原四任务 1/4、后续单任务 0/1 与本轮 0/4 分开保留；没有依据把差异归因某个修复。协议已使用完毕，下一项研发先权衡生成约束/可反馈违规与证明范围的收益，不自动继续消费或修补本轮。

实验收尾文档检查：六份变更 Markdown 的 228 个本地目标、28 个锚点和围栏通过，`git diff --check` 通过。原始材料与实现/协议保全再次复核通过，摘要保存在 result/verification.json；无新生产变更，不重复运行 Go/L1 回归。报告与对应状态更新作为完整实验单元提交。
