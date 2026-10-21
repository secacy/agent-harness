# Workouts 受托助手复核与补充汇总

- 日期：2026-10-20
- 状态：受托源码/证据评审及开发校准汇总完成；不是实际人员签署
- 基线：d9b15b9；保留已有 Translation 等文档的未提交修改，本轮不暂存、不提交 Git
- 对应：[M3 收尾 R1](2026-10-20-m3-closeout-review.md#r1实际人工复核阻塞-a6)
- 委托：用户要求进入下一步，并明确“其他如果需要复核则由你完成”
- 当前结论：Workouts 助手判断为测试保留 pass、变更覆盖 pass、交付依据 fail；三仓均有复核结论（两份用户确认、一份助手评审），原协议实际人工口径仍为 2/3，独立留出未准备，M3 不提前总体验收

## 目标与取舍

本步直接完成 Workouts 剩余的源码和证据检查，不再只给用户一张空白表。对象固定为 workouts / rpcx-agent-2398064069、v0.5.0、default；原候选、门禁和独立参考不重跑、不改判。

助手受托评审与原协议的实际人员复核不是同一身份。已有 Assess 接受 candidate_review/development_calibration，而真实开发轮次的 attachReference 明确拒绝后者。比较三种处理方式：

| 方案 | 当前影响 | 选择 |
| --- | --- | --- |
| 将助手记录标 candidate_review 并接原正式轮次 | 接口可能只校验非空姓名就接受，但会把原约定的人员审查缺口藏起来；用户委托也不等于 secacy 签署 | 不采用 |
| 扩展 Review/Manifest 与指标协议，正式支持助手角色 | 长期可行，但会改变运行记录与评测口径，需要新协议版本，当前只为一个收尾记录引入明显复杂度 | 暂不实现 |
| 复用 development_calibration，单独生成带身份边界的补充汇总 | 完成用户委托，保留所有结果，可直接复验；不能将其当成原正式评测补齐人工的证明 | 本次采用，接受同时保留原正式汇总与补充汇总的成本 |

新 Manifest 仅用于事后开发校准，不修改生成前的清单。原真实实验纳入范围、成本、两份用户记录及原正式汇总保持不变；不新增 ADR 或运行时功能。

## 实际评审

详细源码、行号、条款和证据入口见 [review.md](../../runs/workouts-assistant-review-01/review.md)，结构化记录为 inputs/review.json，reviewer 明确为 Codex assistant，kind 为 development_calibration。登记时间采用时钟实际读数 2026-10-20T17:51:28+08:00；活动耗时未知，不借用用户身份。

| 项目 | 助手结论 | 已完成的审查 |
| --- | --- | --- |
| test_preservation | pass | trainings 共 19 文件、6 份测试；唯一变动测试仅 rpcx 导入，精确撤销后与原全文一致，其余测试和 helper 字节未变；原 37 个业务身份及 common 3 个身份历史 pass，无 skip |
| change_coverage | pass | 另三份非测试修改为 go.mod 和 users/trainer 适配器；两个 RPC 独立派生 30 秒并 defer cancel，参数/空确认/返回保持；未变的 Handle 使用仓储 callback ctx，保留条件余额、顺序、错误链与远端副作用边界 |
| delivery_evidence | fail | 原 review.md 第 111 行发现项链接误指 demo-orders；第 33 行有备用正确入口，结构化证据存在。这是交付导航缺陷，不是业务迁移错误 |

已直接阅读两个适配器、真实 command/测试、取消/余额规则、common logging/metrics、SDK 及独立参考探针。参考区分 outer/callback，保留 16 个行为子例及父测试 pass；users/trainer 分别约 30.032231271 秒、30.031608384 秒到期，子例及父测试均 pass。500ms 间隔对照检查逐调用期限起点；失败路径检查不提交本地状态、保留已发生的远端副作用。历史日志只是证据，不算本轮执行。

common 和两个 SDK 的完整文件集合、LICENSE/go.sum 及未改业务文件保持摘要。补丁只改四个文件；没有新增业务分支、重试或补偿。结论限固定取消切片与协作取消 SDK，不外推真实网络、不响应 ctx 的后端、压力、完整部署或跨远端事务；同一开发助手及共享设施仍有共同盲区。

## 汇总与验收边界

[补充汇总](../../runs/workouts-assistant-review-01/supplemental-round/summary.md)使用新 ID l2-dev-eval-v0_1_0-assistant-supplement-01、kind=development_calibration；沿用 Goddd/Translation 的原实际记录引用，仅增加 Workouts 助手记录。三项完整参考均为 delivery 类 fail，执行/原质量仍为三项 Completed / NeedsAttention；自动成功 0/3、错误放行率 null、原 81 次准入与 1,060,890 Token 均保持。显示的成本来自原批次，不是本次助手评审的成本。

原正式[Translation 后汇总](../../runs/translation-human-review-01/completed-round-01/summary.md)仍是 2 fail / 1 inconclusive，不回写为 3 fail；原实际人工口径仍未满。受托 Workouts 评审已经交付，无需用户重复填写来完成本次委托；严格人员复核缺口保留为评测限制，而非用助手记录消除。独立留出仍待独立准备，不由助手创建一个已见样本冒充。

prepare.py、evidence-index.json、sources-before.json 与 verification.json 位于 runs/workouts-assistant-review-01/，为一次性评审材料，不纳入运行时工具。原材料已绑定摘要，源码克隆不携带 runs，复核时须另提供原归档及这些新记录。本步未读取 .env、发起供应商请求、运行候选/参考或 Docker，也未更改原规则及协议。

最终核对通过：6614 份登记来源摘要保持，41 份门禁直接证据和 24 份参考日志匹配，补丁还原的全部 19 个文件等于原候选；补充汇总的 213 份来源与归档副本一致。七份评审/项目文档的 147 个本地链接、22 个锚点、代码围栏和 Git 差异检查通过。原正式汇总仍 2 fail / 1 inconclusive，补充汇总为 3 fail；两者身份分开，原指标/成本相同，HEAD 与暂存区保持不变。

## 下一步

受托的剩余源码/证据复核已完成，当前不再排一项“等用户填写 Workouts”。后续以三仓交付缺陷、原自动成功率及评审身份差异形成阶段交付结论，保留原协议人工与独立留出的未满足项；需要改变正式验收范围时另行记录，不能据补充校准自动宣布 M3 完成或切换 M4。
