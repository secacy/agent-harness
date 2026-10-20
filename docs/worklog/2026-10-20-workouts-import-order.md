# Workouts：导入顺序归一与门禁回归

- 日期：2026-10-20
- 状态：已实现并验收，限同一导入块内的顺序归一；真实候选仅作开发校准
- 代码基线：f3588e6
- 对应里程碑：[M3.4](../MILESTONES.md#m3多仓库批量闭环)
- 前置：[六项未知检查的根因与支持范围评审](2026-10-20-needs-attention-review.md)

## 范围与实现选择

本单元落实前次评审确定的最小范围：Workouts 的同一导入块内，路径及别名集合相同、顺序或空行分组不同，应获得相同结构指纹。其他声明和语句仍逐项比较；原期限、上下文、测试与业务证据仍是独立必需检查。Goddd/Translation 的证明扩展不在本单元。

现有 AST 指纹同时遍历导入声明及 File.Imports。新增局部 sortImports：按路径、再按别名稳定排序每个 import 块，然后按声明顺序重建共享节点的文件级导入索引。只修改解析后的比较副本，不改候选文件。

不直接使用 go/ast.SortImports：它会去掉重复导入，本单元要求保留重复项及完整声明集合。也不将所有导入块合并，避免扩大已确定的支持范围。没有新配置、通用 AST 等价引擎或跨场景抽象；沿用既有 directives 拒绝。users 的既有 time 适配处理保持原样，排序不增加可忽略导入类别。

Profile、规范和冻结校准数据不变。具体生产比较器字节进入新的 MaterialsSHA256；Profile ID 不代替源码身份。原实验协议、Run 和质量结论不回写。

## 验证安排

1. 快速结构测试使用已认证基线和正确迁移，覆盖换序/分组正例及新增、删除、重复、路径、别名、点/空白导入、导入块边界、RPC 方法/参数/错误处理、额外声明和语义指令反例。另核对同路径不同别名排序。
2. 显式运行九项冻结 Workouts 校准，逐项比较原 manifest 的全部检查、判定和条款；冻结材料不修改。
3. 对首轮真实 Workouts 候选，仅复制到独立开发校准目录，用新比较器完整 Evaluate，并构造错误时长、提前 cancel 对照。禁网 Docker 执行业务命令，保存工具、候选、规范及日志身份；不调用模型，不填人类复核，不更改原实验成功率。
4. 运行相关包回归、静态检查及材料边界检查，核对原 Campaign 文件摘要与容器回收。所有产物放在忽略的 runs/workouts-import-order-01/。

## 实际结果

快速测试及竞态检测均通过：每轮 35 项测试及子测试，完整候选验收的显式开关项在这两轮跳过，另由完整矩阵执行，不算作快速测试通过项。新增范围测试包含两个排序/分组正例和十八个反例，另有别名身份对照。

九项冻结校准在可信本地实际执行完毕，全部检查状态、最终判定及条款归因与原 manifest 一致：1 ReadyForReview、7 Rejected、1 NeedsAttention。日志为 matrix.jsonl，逐项 evaluation.json 位于 matrix/；没有更新预期来迎合新实现。

项目回归首轮的 cmd/harness 与 internal/deepseek 因沙箱禁止 localhost 监听中断，整条命令退出 1；原失败保留在 project-tests.jsonl。随后在允许本机监听的环境仅复核这两个包，localhost-recheck.jsonl 中两包、97 项测试及子测试通过；只使用模拟 API，不请求真实模型。其余包在首轮通过或无测试；最终逐包核对为 25 个有测试的包通过、7 个包无测试，无未解决失败，不把重复执行的测试数相加。既有 T3 三类交付及 Workouts/Profile 机制包含在项目回归中；显式跳过的其他 Docker/正式验收不算通过。

新验证器的原样副本已获得 ReadyForReview，候选摘要仍为 `61204cac51555eaf729d58c67f4194874e303471d7ece396cdc4dff2774d1af8`。工具材料从原 `c433278b0e2835547b3940ee576914d59f1c018ee7dc0c10bac16626f3547834` 变为 `c9367ca7c7f40e01af64548ef82eb8e07b51791866935c600e722a074fa8cf3a`；新报告单独存放于 development-calibration/original_candidate_new_verifier/。这是已见开发样本的校准，不计为新模型迁移或原实验自动成功。

三组 Docker 校准全部完成，每组均实际执行基线、候选、原测试、类型加载和可信业务检查；只有需要实际到期证据的原样正例执行最终到期步骤，反例在快速行为检查确认失败后停止。

| 开发校准 | request_scope | migration_completeness | semantic_behavior | 最终判定 |
| --- | --- | --- | --- | --- |
| 原样候选＋新验证器 | pass | pass | pass | ReadyForReview |
| 同一候选仅将 trainer 期限改为 5 秒 | pass | fail | fail | Rejected |
| 同一候选仅增加提前 cancel | unknown | unknown | fail | Rejected |

三个输入均保留原真实候选的导入顺序，驱动未改写其导入。结构归一不承担期限或释放证明，错误时长仍由独立类型/行为证据阻断，提前取消也未因前缀处理得到放行。原三项 NeedsAttention、人类待复核状态及正式指标保持不变。

收尾审计确认：原 Campaign 的 5907 份文件摘要未变；本轮 31 个 Docker 容器均已不存在，计数包括九/八/八条门禁命令和各两次类型加载。规范、Profile、冻结数据及原协议均未改；只有 Workouts 的两个 Go 文件发生源码/测试变化。go vet、Go 格式及 git diff --check 通过。摘要、逐包记录、容器 ID 和驱动源码/二进制身份保存在 verification.json；所有运行材料留在忽略目录，不进入 Git。

本单元按既定边界验收，不表示 M3 整体完成。下一步分别设计 Goddd 的 ctx 适配模板与未使用形参名证明；Translation 的控制流/绑定证明以及失败提示策略继续保留原边界。本轮未调用真实模型或读取 .env。

六份文档的本地链接目标与代码围栏完整性检查通过，记录见 document-checks.json。

后续 Goddd 的具体设计与原型证据见 [mock context 与未使用形参适配](2026-10-20-goddd-context-adaptation.md)。
