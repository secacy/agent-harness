# GHCLI：新门禁的 Harness 交付与审计验收

- 日期：2026-10-23。
- 状态：已在下述受控范围验收。
- 代码基线：89b6228。
- 前置：[有限资源与完整边界证明](2026-10-22-ghcli-bounded-proof.md#10-资源证据与完整边界的生产接入2026-10-23)。
- 目标：验证新增门禁证据经 Campaign、预算包装、Agent 工具、Runner 发布、审核导航及只读审计后仍然归属正确，且质量未知不会被执行完成覆盖。

## 为什么选择这一步

前一单元已经实际执行独立 Evaluate，但没有用新证据完整交付一个带预算的 Run 并审计整个 Campaign。继续扩展 helper 会增加支持面，却不能回答交付与审计是否消费了新材料；立即重跑真实模型需要新协议、预算和授权，模型轨迹也无法稳定复现边界。本轮选择确定性的集成验收，用已暴露候选重放真实工具动作，直接检验尚未覆盖的发布链路。代价是它不能提供新的自主迁移成功率或未见样本效果。

不新增生产配置接口或另一套运行器。沿用真实 Campaign/Runner、受控模型及 Docker 执行器，两个 Target 分别为原样候选和未知 parent helper；均从公开冻结基线开始，只通过 replace_text 修改。模型再调用 go_check build/test 并提交；完整门禁独立重查。不读取 .env、私有控制或独立参考，不追加真实模型调用或覆盖历史产物。

## 输入与验收契约

使用原保存候选模块摘要 39390941b07031512fd3eac31104acfcb281409e545819d94cf158ecb4c74de9。另一个 Target 仅在新副本把请求 Context 交给恒等 helper，期望保留 NeedsAttention。两个输入及构造差异在执行前落盘。

- 一次 Campaign，Target/命令并发均为 2；共享额度最多 6 次模型准入、600 Token，每 Target 3 次/300 Token，模型声明每次 20 Token。数字仅用于受控账本机制，不是供应商用量。
- 两个 Run 均 Completed，质量分别 ReadyForReview / NeedsAttention；summary、run.json、report.json 及 review.md 不得互相覆盖或混淆。
- 逐请求预算绑定实际 Campaign/Target/Run；6 次已知请求、120 Token，无在途或未知占用；工具失败只接受刻意的越界拒绝。
- candidate.patch 在新的完整基线上重放，模块摘要与交付候选相同；审核链接均在本 Run 内且文件存在，能找到来源/资源/范围证据。
- 原 Campaign 只读审计 pass 且扫描完整；复制审计实际使用的材料，先确认副本 pass，再只篡改副本中的 boundary-proof.json，必须报告该材料的摘要违规。审计只检验记录一致性，不重新判定语义。
- 原 Campaign 的已审计字节、保存候选及冻结历史保持不变；实际 Docker 容器按精确 ID 确认回收。

## 实施与验证

新增显式 opt-in 的 TestGHCLIProofDeliveryCampaign，复用已有脚本模型与真实 CampaignExecutor，没有修改生产代码、迁移规则、Profile 或协议。两个输入、实际预算及资源模板在执行前固定；全程只有已暴露开发材料，不读取私有控制或独立参考。

首次编译检查因未设置两个输入变量而明确 skip，仅作为编译检查。随后带变量和竞态检测实际运行一次，249.07 秒完成测试主体，无竞态报告，没有候选/门禁失败或重跑。两个 Target 各执行 6 条生成前检查、2 条 Agent go_check 和 6 条独立门禁命令，共 28 个真实容器；各自的 3 次受控 Complete 都在账本中结算。

| Target | 执行状态 | 原测试/行为检查 | 来源 / 资源 / 范围 | 最终质量 |
| --- | --- | --- | --- | --- |
| saved | Completed | pass，包含实际到期 | pass / proven / pass | ReadyForReview |
| helper | Completed | pass，包含实际到期 | unknown / unknown / unknown | NeedsAttention |

两份 report.json、Run 终态与 summary 保持同一判定，均绑定实际 Campaign/Target/Run 和各自候选摘要。6 次请求、120 个受控声明 Token 全部结算，无在途或未知占用；这不是供应商用量。原样候选目录摘要保持 39390941b07031512fd3eac31104acfcb281409e545819d94cf158ecb4c74de9。两份 Evaluation 的工具材料仍是 f9e086295e57930d8ac14f81b1a52ff964c89d9af1187dfe169a61f1121323db，与前一单元最终材料相同。

两个候选补丁均在新的完整基线上应用并核对模块摘要；审核文档全部链接存在且不逃出自己的 Run，并确实链接 boundary-proof.json。原 Campaign 审计 pass、ScanComplete=true、InputsUnchanged=true，资源状态为 recorded_removed。该状态是归档记录，不替代实际容器检查。

篡改对照复制原审计所消费的文件并逐件核对摘要，未改副本先得到 pass。随后只给 saved 的 boundary-proof.json 追加一个换行，JSON 仍可读；副本审计为 violated，且唯一违规精确定位该文件。这个对照证明新信封确被摘要核对，不声称审计重做了资源语义证明，也不覆盖同时伪造所有记录的攻击。原 Campaign 的全部已审计文件在对照后保持原摘要。

又用当前源码构建公开 harness 命令：原 Campaign audit 退出 0 / pass，篡改副本退出 1 / violated，两次扫描完整、后者恰有 1 项违规。入口在模型/后端组装前分派，未请求鉴权或运行任何候选。这样确认 API 验收材料也可用公开命令复核。

| 验证 | 实际结果 | 证据（runs/ghcli-harness-delivery-01/ 下） |
| --- | --- | --- |
| 编译与可选入口 | 未设置输入时明确 skip；不计入执行通过 | compile.jsonl |
| 真实 Campaign 集成及竞态检测 | 一项完整测试通过；包含两个 Run、两份补丁重放、审核导航、预算与审计正反例，无竞态报告 | docker-campaign.jsonl、accepted/delivery-result.json |
| 逐任务和批次交付 | 2 Completed；ReadyForReview / NeedsAttention 各 1；不计算新效果率 | accepted/campaign/summary.md、accepted/campaign/summary.json |
| 新证明的审计对照 | 原/未改副本 pass，篡改副本 violated 且定位证明文件，原字节保持 | accepted/audit/、accepted/audit-copy-before/、accepted/audit-copy-tampered/、accepted/proof-tamper.json |
| 公开 CLI 审计 | 原材料退出 0，篡改副本退出 1，均扫描完整 | cli-results.json、cli-audit-original/、cli-audit-tampered/ |
| 容器回收 | 28 份创建/移除证据绑定同一 intent/ID；同一 Docker server 上精确查询，全部容器不存在 | container-ids.json、container-cleanup.json |
| 静态检查 | Runner 包 go vet 通过，当前 harness 构建通过 | vet.log、harness |
| 历史身份 | 冻结数据、原协议、原运行及旧归档的 59,589 份既有文件保持原摘要 | history.json |

完整结论是：新增有限证明已贯通 Harness 的交付与记录审计，NeedsAttention 在执行成功、原测试/行为观察通过和审计通过时仍保留。本轮没有修改生产实现，因此不重复前一单元全部门禁、消费端大回归或正式 L1 矩阵；实际检验集中在原先缺少的新交付链路。

本工作单元到此收敛，不因集成通过自动扩展 helper 支持或启动另一轮付费评测。它不证明自主迁移成功率、未见泛化、人员独立性或全部业务语义；旧实验 1/4、所有归档及剩余里程碑条件不改。运行结果与身份汇总见 summary.json，产物不入 Git。

收尾检查：6 份变更 Markdown 的 261 个本地链接目标、39 个锚点及围栏有效；新增 Go 文件已 gofmt，git diff --check 通过。测试、命令说明、交付状态和本工作记录作为同一工作单元提交。
