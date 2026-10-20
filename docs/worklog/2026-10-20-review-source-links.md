# 审核材料：按实际模块解析源码位置

- 日期：2026-10-20
- 状态：已验收（呈现回归及三份实际报告的只读消费）
- 基线：496d257
- 对应里程碑：[M3.4](../MILESTONES.md#m3多仓库批量闭环)
- 前置证据：[唯一真实 L2 批次](../experiments/2026-10-20-rpcx-l2-real-model-01.md#已确认的交付缺陷)

## 问题与实施选择

上轮 450 个审核链接中，6 个 Finding 源码位置仍指向 workspace/demo-orders，三个实际模块却是 goddd、translation、trainings。变更表链接已使用 Report.Input.RepositoryName；renderValidation 的位置分支单独硬编码了旧模块名。该缺陷降低审核材料的可用性，不改变原门禁判定或候选行为。

这是既定布局下的直接修复：renderReview 已解析实际模块名并保留旧 demo-orders 回退，将这个结果显式传给 renderValidation，统一变更表与源码位置。无需新增配置或 Profile 查找，也不在第二个位置复制模块回退规则。位置仍由 baseline/candidate 分别映射到 baseline/workspace；路径转义与行号呈现沿用现有实现。不新增 ADR。

本单元不修改门禁、候选、真实实验原报告、复核结论或冻结协议，不读取 .env 或调用模型。新源码与旧协议的代码身份自然不同；未来实验应另行发布匹配版本，不改旧协议中的源码摘要。

## 验收计划

1. 用真实目录结构构造 NeedsAttention 的 Finding，覆盖 demo-orders 兼容入口和三个 L2 Profile；分别指向仅存在于基线的删除文件、候选文件，并覆盖中文/空格/括号等转义。实际检查每个 Markdown 链接能否解析到文件，不只比较字符串。
2. 先在旧实现运行新增回归，确认三个 L2 case 会因模块名错误失败，再实施修复。
3. 通过可选的已发布 Campaign 入口，只读载入上轮三个实际 Report、原基线/候选与 Agent 记录，在内存重新渲染并按原 Run 布局核对链接。保留原 review.md 的字节，不发布替代报告或伪造新 Run。
4. 执行相关呈现/发布回归与 go vet；核对旧 Campaign、冻结数据/协议未改，再更新进度并提交一个完整修复单元。

本单元不替代人类源码/交付复核，也不证明 NeedsAttention 的支持边界已解决。

## 实际实现与验证

仅调整 renderValidation 的模块参数：复用 renderReview 已解析的 RepositoryName，源码位置链接按相同模块布局拼接。旧空模块名仍由上层统一回退到 demo-orders，未新增规则、配置或判定。baseline 与 candidate 的位置映射、转义和行号均保留。

新增文件布局回归：legacy-fallback、显式 demo-orders 与三个 L2 Profile；每例同时覆盖已删除基线文件和带中文、空格、方括号、圆括号的候选路径。先在未修复源码运行，Goddd/Translation/Workouts 三项因 baseline/demo-orders/removed.go 不存在而失败，两个旧入口通过；失败日志保留。这证明回归确实捕获跨模块分支，而非仅重复拼接字符串。

新增可选 TestReviewPublishedCampaignLinks，指定已发布 Campaign 后读取原 Report、基线/工作副本与 Agent 记录，只在内存渲染并按原 Run 目录验证链接；不执行候选命令或模型，也不保存替代审核文件。它要求三个 Profile 均实际出现并含源码位置，避免空 findings 的正例绕开缺陷。

| 验证 | 实际结果 | 证据 |
| --- | --- | --- |
| 修复前文件布局回归 | 两个 demo-orders 入口通过，三个 L2 子例明确失败，退出 1 | runs/review-source-links-before.jsonl |
| 修复后呈现/终态发布竞态回归 | 9 项测试及子测试通过，无跳过/失败/竞态；覆盖模型自述转义、证据缺口与终态发布失败撤回判定 | runs/review-source-links-race.jsonl |
| 实际报告只读消费 | Goddd 2、Translation 3、Workouts 1 个源码位置及其余报告链接均解析到现有文件；原 review.md 字节未变 | 同一竞态日志中的三条逐 Target 记录 |
| 静态检查 | rpcx/runner 的 go vet 通过 | runs/review-source-links-vet.log |
| 历史材料检查 | 上轮 5907 份 Campaign 文件及协议包 6 份材料均未改；原三项 NeedsAttention 保留 | runs/review-source-links-summary.json |

未重跑整项目、真实生成、Docker、独立参考或质量判定；改动仅影响报告链接呈现，针对性回归覆盖本次变更。运行日志不入 Git，未读取 .env。

最终 116 个本地文档链接目标、Markdown 围栏、Go 格式与 git diff --check 通过；冻结数据、规范、补丁及协议包无改动。失败/通过测试日志摘要和材料检查保存在 runs/review-source-links-summary.json。

## 结论与下一步

新生成报告已按实际模块定位源码；上轮原报告中的 6 个断链作为历史证据保留，不覆盖文件或改变旧 Run 的摘要、判定与复核状态。只读重渲染验证不是一次新迁移效果评测，不能将它计入原实验成功率。

下一步对真实批次全部 NeedsAttention 的支持边界做具体权衡，先区分合法表达未覆盖、真实行为变更与证据缺失，再决定有限扩展或继续人工确认；不直接放宽规则，也不为获取放行而追加本轮请求。M3 仍在推进，人类复核和独立留出仍未补齐。
