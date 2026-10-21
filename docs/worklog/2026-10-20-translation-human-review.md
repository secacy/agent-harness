# Translation 人工复核材料与结果

- 日期：2026-10-20
- 状态：材料准备与实际人工复核均已完成，已接入新离线汇总
- 准备基线：d9b15b9；对应 [M3 收尾 R1](2026-10-20-m3-closeout-review.md#r1实际人工复核阻塞-a6)
- 对象：原 translation / rpcx-agent-1269740581，规范 v0.4.0，同一候选的 default 与 rpcx_alternate 两种必需配置
- 当前结论：secacy 确认测试保留 pass、变更覆盖 pass、交付依据 fail；与 Goddd 一同进入新汇总，Workouts 仍待复核。原 NeedsAttention 和 M3 收尾状态保持，本轮不暂存、不提交 Git

## 目标与实现

Goddd 的实际复核已接入新离线汇总；接下来为 Translation 提供测试保留、生产变更覆盖、交付依据三项的可操作材料。其双配置和较早的 request/response 准备语句需要分别说明，不能直接复用 Goddd 的上下文链路清单。

沿用已确定的旁路复制方案：原字节、报告和判定不变，新导览提供正确导航和证据定位。复制 preflight 以保留原审核入口的基线检查；大型宿主依赖缓存/压缩包不复制，保留元数据并列出排除项。该包供源码与历史证据审查，不是完整运行复现归档，不重新运行模型、候选、参考或 Docker，也不实现暂缓的 Translation 分析规则。

入口：[Translation 复核包](../../runs/translation-human-review-01/packet/README.md)。准备时创建了包外[复核笔记](../../runs/translation-human-review-01/review-notes.md)，现已由用户填写并确认；包内模板和原 JSON 模板保持未填。runs 不入 Git，源码克隆不携带本地原实验副本，对外复核需另行提供材料包。

## 材料内容与待判断位置

| 材料 | 内容与边界 |
| --- | --- |
| 原基线/候选、SDK、规范、补丁、Profile、preflight、门禁与独立参考 | 按字节复制；保留原绝对路径与原三处断链，不改历史文件 |
| changes.json | 20 个模块文件中 5 个修改、15 个未变，无新增/删除；4 个非测试文件与 1 个测试文件变更 |
| test-events.md | 两配置分别列出 26 个原身份在原基线、原候选与参考候选中的 pass 行号；不能以同名且通过证明断言保留 |
| configuration-index.json | 从原 go list 提取并核对双配置活动/忽略实现、共同类型/tracing 与生成 mock；原门禁和独立参考的选择相同 |
| 原 test-integrity / scope / alternate comparison | 40 个声明中 39 preserved，TestTranslationRoundTrip 为 unknown；三个 webapi 生产文件需结构复核；alternate 期限形态为 unsupported_timeout_shape |
| README / review-notes | 按规范条款列明具体问题、行号和覆盖限制；三项人工结论不预填 |
| manifest / checksums | 逐副本来源/摘要、排除材料及包内校验清单，不认证实际人员审查 |

测试重点是 v2 导入及 t.Fatal 文案由 v1 default 改为 per-call 30s，复核者应检查原断言条件、表数据、helper 是否保持，不能笼统忽略所有字符串差异。生成 mock 属受保护文件，两配置均实际出现在测试源文件选择中。

生产重点包括两个实现的调用、共享 30 秒常量、当前 ctx 和派生资源释放；alternate 第 33—37 行先准备本地 request/response 再创建期限，应单独对照 TIME-001 说明，不能将原分析器 unknown 当作迁移错误，也不能用自动参考 pass 抹掉结构复核要求。未改 usecase/两层 tracing 也提供导航，便于检查 Store 次序、错误链、身份及 History。

原 review.md 第 120/122/124 行的发现项错误指向 demo-orders；第 33/34/36 行变更表有正确备用链接。记录实际影响，不将其说成源码全部丢失，也不因本包补充导航改写原交付。留给实际复核者判断三项状态、理由与位置。

## 验证

准备结果位于 runs/translation-human-review-01/verification.json：

- 6459 份登记来源摘要保持，覆盖原实验材料及 Goddd 已完成的复核/汇总；不是新增测试计数。
- 463 个原文件副本，共 2,142,326 字节；候选/基线全树、规范、Profile、补丁、参考原文和模板绑定一致。
- 在新基线副本应用原补丁后，全部 20 个候选文件路径与字节一致。只还原文件，没有执行候选。
- 79 份门禁直接证据及绑定、38 份参考日志摘要匹配；两配置的原 26 个测试身份均有 pass，实际源文件选择与生成 mock 参与记录匹配。
- 独立参考原日志各有 12 个行为子例与父测试 pass，无 skip；实际期限观测约 30.0344 秒、30.0316 秒。本轮仅读取，不算重新执行验证。

初次材料提取脚本只读取 go list 的 TestGoFiles，而原 usecase 是外部测试包，测试文件位于 XTestGoFiles，因字段不存在停止。修改为同时读取两字段后，逐配置匹配原参考所记录的完整测试选择；首轮副本保留，未放松 mock 必须参与的断言。

prepare.py、validate.py 和 navigation-verification.json 保存一次性材料准备及检查；不纳入运行时工具。检查包内导航、模板未填、交付清单和原材料不变；没有新增实际复核、修改正式指标或消费模板生成新参考结论。

最终六份导航文档的 75 个本地目标、两个锚点及代码围栏检查通过；包内校验清单覆盖 470 个文件（不含清单自身）。确认 HEAD 仍为准备基线、Git 暂存区为空，本轮文档修改保持未提交；生成材料只在忽略的 runs 中保存。

## 材料准备时的下一步

复核者直接填写包外笔记，三项分别给出 pass / fail / inconclusive、理由及两配置的文件/证据位置。实际确认后再另存人员记录，以新请求、新目录接入离线汇总；Workouts 仍待后续复核，独立留出仍待准备，M3 不提前验收。

## 后续：受托助手代码评审

用户随后委托助手实际审查。本次已对照原规范、全部五个变化文件、其他测试文件摘要、SDK、usecase 与两层 tracing、双配置源文件选择、独立参考探针及历史命令日志；结果另存为[assistant-review.md](../../runs/translation-human-review-01/assistant-review.md)。这是 development_calibration 性质的开发侧源码评审，不是实际人员签署，未输入正式汇总。

| 项目 | 助手判断 | 依据与限制 |
| --- | --- | --- |
| test_preservation | pass，限固定候选的两配置 | 唯一变化测试精确撤销 SDK 导入与一条 t.Fatal 文案后，与基线全文一致；三个其他测试文件字节一致，原生成 mock 和每配置 26 个身份的执行证据齐全 |
| change_coverage | pass，限 v0.4.0 与声明配置 | 四个非测试变化均有迁移依据；alternate 的两条准备语句保留自基线，随后逐调用派生 30 秒期限，没有新增业务调用或 I/O；两配置分别观察了 ctx、字段、Store 次序、错误链及实际到期，不外推任意部署 |
| delivery_evidence | fail，发现项导航缺陷 | 原文第 120/122/124 行目标不存在；第 33/34/36 行存在正确备用链接，因此是发现项跳转失效，不是全部证据丢失或业务迁移违规 |

历史期限观测分别为 30.034428459 秒与 30.031588808 秒，行为各 12 个子例及父测试 pass、无 skip。本次仅消费历史材料与作源码核对，未重跑候选或模型，也未扩展暂缓的分析器。原 unknown 与 NeedsAttention 保持，用户笔记、原模板、Goddd 实际复核及原指标均不改变。实际人工复核仍待。

本次定位、原材料/笔记不变及精确测试差异检查见 runs/translation-human-review-01/assistant-review-verification.json。

## 实际人工复核与接入（2026-10-20）

随后用户填写 secacy、记录时间及三项结论。笔记标题当时仍有“辅助草稿，未签署”，因此先向用户核实，得到明确答复“确认，是我的实际复核结论”后才转录；不把助手开发评审或验证文件当作人员签署。新目录 completed-review-01/ 按字节保存转录时的 review-notes-original.md、[review.json](../../runs/translation-human-review-01/completed-review-01/review.json)、来源与时间依据 provenance.json。中断后用户仅移除了笔记标题中的草稿/未签署字样；另存 review-notes-current.md 与 notes-title-update.json，确认正文、决定和绑定不变，不覆盖已转录的快照或旧汇总。

| 项目 | secacy 的实际结论 | 范围与理由 |
| --- | --- | --- |
| test_preservation | pass | 限固定候选的两配置；唯一测试变化是 v2 导入和诊断文案，原断言/阈值不变，其他三个测试文件及生成 mock 保留；每配置 26 个原身份历史 pass，无 skip |
| change_coverage | pass | 限 v0.4.0、default 和 rpcx_alternate；四个非测试变化有迁移依据，两个实现均逐调用派生期限并释放，alternate 局部准备沿用基线；字段、错误链和 Store 次序保持，不外推真实网络、压力或完整部署 |
| delivery_evidence | fail | 原 review.md 第 120/122/124 行三个发现项源码链接失效；第 33/34/36 行有备用入口，结构化身份和双配置证据仍可用；这是交付导航缺陷，不是源码丢失或业务迁移违规 |

用户确认将笔记中的 `2026-10-20 17:43` 按 Asia/Taipei 登记为 **2026-10-20T17:43:00+08:00**；秒位补零只作格式规范化，不表示计量到秒的活动审核时间，活动耗时仍未知。JSON 保留原 Target、Run、候选及 reference 原文摘要，理由、证据定位及局限来自已确认笔记；原模板、助手记录、原批次及 Goddd 人员记录均不修改。

新 Request 沿用 Goddd 已完成的复核引用，再加入 Translation 的实际记录，输出到同级的全新 completed-round-01/。[新汇总](../../runs/translation-human-review-01/completed-round-01/summary.md)已由既有 summarize CLI 成功生成：Valid、InputsUnchanged、ExecutionComplete 均为 true；Goddd / Translation 完整参考均为 fail，类别均为 delivery；Workouts 仍 inconclusive / review_missing。单仓复核完成允许明确 fail，不要求为了结束评测将原缺陷改判 pass。

三个自动参考仍 pass，原三项仍 Completed / NeedsAttention；N=3、A=0、H=3、自动成功 0/3、错误放行率 null、81 次准入及 1,060,890 Token 均保持。round 的 delivery_verified=true 表示结构化交付关联成功，不代表 Markdown 导航通过人工复核。没有重跑候选、参考或模型，也未扩展暂缓的分析器。

接入核对脚本与结果保存为 completed-review-01/verify.py、verification.json；核对原笔记、材料包、来源副本、两份已完成复核及不变的指标/成本。此处新增人员记录是用户确认结论的转录；接口只认证结构及绑定，不认证真实人员或独立盲审。

最终接入核对通过：6459 份历史来源及 470 份包内文件摘要保持，笔记两版仅标题不同且均已归档；新汇总的 212 份来源和副本匹配，Goddd 的人员记录引用保持，两个必需配置齐全，原指标和成本一致。五份更新文档的 109 个本地链接、21 个锚点、代码围栏和 Git 差异检查通过；HEAD 与暂存区未变，无生产代码变更。首次恢复核对因用户更新笔记标题而停止，确认仅标题变化后分别校验历史快照与当前版本，没有重写旧摘要清单来隐藏变化。

## 当前下一步

继续准备 Workouts 的实际人工复核材料，完成剩余三项判断及新汇总。独立留出仍待独立准备；M3 继续收尾，不提前宣布通过或进入 M4。本轮文档修改保留未暂存、未提交。
