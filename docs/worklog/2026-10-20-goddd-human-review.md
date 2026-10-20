# Goddd 实际人工复核材料准备

- 日期：2026-10-20
- 状态：Goddd 实际人工复核已完成并接入新离线汇总；其余两仓复核仍待
- 对应里程碑：[M3 收尾 R1](2026-10-20-m3-closeout-review.md#r1实际人工复核阻塞-a6)
- 准备基线：8fad98a；对象是原实验的 goddd / rpcx-agent-2744869177，不是后续新工具校准
- 当前结论：secacy 确认测试保留 pass、变更覆盖 pass、交付依据 fail；完整参考为交付类 fail，原 NeedsAttention 保持。A6 仍缺其余两仓复核，M3 不提前验收

## 目标与做法

R1 的下一项工作是实际检查测试保留、生产变更覆盖、交付依据。原材料分散在 Run 和独立参考目录，原审核文档还有两处源码断链，仅给出未填 JSON 模板不便于开始审查。

本步选择复制已有材料、增加旁路导览，保持原文件字节；直接改原 review.md 会改变历史交付，重新验证又会混入后续规则版本，均不符合检查原候选的目标。这是既定复核流程的材料整理，不引入新门禁或复核系统，也不新增 ADR。

本地入口：[Goddd 人工复核包](../../runs/goddd-human-review-01/packet/README.md)。runs 不入 Git，对外提供此记录时须另带 packet 目录；仅克隆源码不能取得这次原实验材料。包内使用相对导航、来源摘要与交付校验清单，可以独立阅读；不含模块缓存和完整账本，不是完整运行复现归档。原报告的绝对路径与原断链保持历史原样。

## 实际交付

| 材料 | 具体内容 |
| --- | --- |
| README.md | 三个复核项目的具体问题、规范条款、基线/候选文件与行号、证据覆盖和局限；不预填通过结论 |
| original-run / reference | 原基线、候选、SDK、规范、Profile、补丁、审核报告、原门禁材料、独立参考探针与日志的字节副本 |
| changes.json | 25 个模块文件的完整差异范围：11 个修改、14 个未变、无新增/删除；8 个非测试文件（含 go.mod）和 3 个测试文件修改 |
| test-events.md | 22 个原测试及子测试在基线、候选、独立参考候选中的 pass 日志行号；另列新增 7 个身份，不以事件相同推断断言保留 |
| original-link-defects.json | 原 review.md 第 108/110 行误指 demo-orders；提供实际 goddd 的两处位置，记录原交付缺陷仍存在 |
| manifest.json / checksums.json | 来源到副本的字节摘要，以及本包交付文件摘要；不作为人工真实性认证 |
| review-notes.md / 原 review-template.json | 空白工作表及原未填模板；真实人员审查后另存记录，填写身份、时间、理由与定位 |

原测试结构报告的 25 个声明包含 helper，24 个 preserved、1 个 unknown。唯一 unknown 是新增 `_ context.Context` 的 stub helper；原范围检查另未证明 mock/mock.go 和 routing/proxying.go。导览仍覆盖所有生产差异，不只查这三个位置。独立 HTTP 参考不经过 mock 回调，特别保留其人工源码检查要求。

原 22 个测试身份和全部候选测试通过、独立参考自动 pass 均是历史观测；不能代替原断言和 helper 的实际审查，也不能忽略原审核源码断链。实际复核允许 pass、fail 或保留 inconclusive；有交付缺陷不自动等于业务迁移错误。

## 验证与修正

| 检查 | 结果 / 证据 |
| --- | --- |
| 原输入、候选和历史材料不变 | 6296 份登记来源摘要保持；包含上轮收尾材料及本次新增引用范围，非新增测试数量 |
| 副本身份与绑定 | 893 个原文件副本，共 7,537,226 字节；候选/基线全树、规范、补丁、reference 原文与模板摘要一致 |
| 补丁还原 | 在新基线副本应用原补丁，全部 25 个文件的路径与字节等于原候选；只还原文件，不执行候选 |
| 原证据 | 31 份门禁直接引用的文件及绑定、16 份独立参考日志摘要匹配 |
| 原测试执行记录 | 基线 22 个身份、候选 29 个身份均有 pass 事件；22 个原身份均在独立参考候选结果中 pass |
| 断链定位 | 原文两个不存在的目标及正确候选位置已逐一核对；不改旧文档 |

初次人工计数误写为 12 个修改文件，实际差异与补丁清单均为 11，已修正。首次补丁还原在仓库内的深层忽略目录调用 git apply，命令退出成功但没有应用路径；完整文件摘要比较发现不一致。改为从仓库根指定新副本目录，再核对全树一致，不能只凭命令退出码认定还原成功。过程问题保留在 runs/goddd-human-review-01/preparation-issues.json。

准备脚本与检查结果在该目录的 prepare.py、verification.json 和 validate.py。仅运行文件核对、补丁还原和文档检查，未运行 Go/Docker 测试、候选、模型或新参考；没有读取 .env 或创建已填写的 candidate_review。最终材料导航、校验清单与原文件不变检查结果见 navigation-verification.json。

最终检查六份新增/更新导航文档的 147 个本地目标与 14 个锚点通过；校验清单覆盖 899 份包内交付文件（不递归计算清单自身），原文两处断链作为已知缺陷单列。Git 差异检查通过，生产代码和冻结输入均未修改。

## 实际复核接入（2026-10-20）

以上记录最初的材料准备过程；随后用户提交笔记并明确“已完成复核”。原笔记位于 runs/goddd-human-review-01/ 下名为 `review-notes.md ` 的文件（文件名末尾有空格），保持原文件不动；在新 completed-review-01 目录按字节复制为 review-notes-original.md，转录为 [review.json](../../runs/goddd-human-review-01/completed-review-01/review.json)。助手只转录用户已确认的判断，不将既有 assistant-review-verification.json 当作人员复核。

| 项目 | secacy 的结论 | 理由与边界 |
| --- | --- | --- |
| test_preservation | pass | 五个原测试文件仅有允许的 API/ctx 适配或追加测试，原断言与 helper 保留；22 个原身份历史执行 pass。新增测试的未使用字段及宽时间窗口不作为独立行为证明 |
| change_coverage | pass | 限固定候选、v0.3.0 和 default 配置；请求 ctx、逐调用 30 秒期限、映射及错误/空结果语义有依据，mock 回调单独静态审查；不覆盖真实网络、忽略取消的后端、压力或完整部署 |
| delivery_evidence | fail | 原 review.md 第 108/110 行发现项源码链接错误，妨碍 SCOPE-001 的直接追溯；第 37/39 行另有备用正确链接，结构化证据未丢失；后续渲染修复不能消除原缺陷 |

笔记只给出日期，原接口要求 RFC3339 时间。经询问，用户明确授权“按本条回复的接收时间登记”；接收回复后立即读取时钟，登记为 **2026-10-20T17:17:36+08:00**。这是记录时间，不推断实际活动审查起止时间；耗时仍为未知。provenance.json 保存原笔记摘要、用户确认、时间依据及助手转录说明。原模板、材料包和原实验文件保持不变。

使用既有 summarize CLI、新 Request 和全新同级输出目录，已发布[新汇总](../../runs/goddd-human-review-01/completed-round-01/summary.md)。三个原自动参考仍 pass；完整参考为 Goddd fail（delivery）、Translation/Workouts 各 inconclusive（review_missing）。Goddd 的三个实际人工项目均有结论，作为单仓复核工作已闭环；交付问题如实进入结果，不要求为了完成评测而把原缺陷改判 pass。

原 Run 及候选质量不改，三项仍 Completed / NeedsAttention。N=3、A=0、H=3，自动成功 0/3、错误放行率 null、成本 81 次准入和 1,060,890 Token 均保持。这里的 fail 是完整独立参考对交付缺陷的结论，不是业务迁移错误或错误放行。round 的 delivery_verified=true 只表示结构化交付身份核对成功，不代表原 Markdown 源码导航通过人工审查。

首次将汇总输出设为 review.json 所在目录的子目录，被既有输入/输出隔离检查拒绝，未创建汇总；保留 first-attempt.json 和原请求，改为同级 completed-round-01 后成功，没有放松隔离代码或覆盖旧产物。本次只消费离线材料，未重跑候选、参考、模型或 Docker。

接入校验见 completed-review-01/verification.json：6297 份登记来源（含原用户笔记）和 899 份包内交付文件保持摘要；新汇总的 211 份来源及归档副本匹配，三项决定与原笔记一致，原指标和成本一致。五份更新文档的 103 个本地链接与 19 个锚点、代码围栏及 Git 差异检查通过；没有生产代码变更。

## 下一步（复核后）

继续整理并完成 Translation、Workouts 的实际复核，保留每项判断、理由、定位及独立新汇总。独立留出继续待独立准备；Goddd 原断链已有后续渲染修复，但原交付 fail 不抹去。M3 收尾状态保持，未进入 M4。
