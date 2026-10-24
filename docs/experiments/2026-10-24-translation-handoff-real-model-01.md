# Translation 交接观察 01：提前提交，行为通过，结构证明仍需确认

- 日期：2026-10-24（Asia/Taipei）。
- 状态：唯一真实尝试、轨迹、固定参考、实际助手复核及保全收尾完成。
- 协议：[translation-handoff-dev-v0.1.0](translation-handoff-dev-v0.1.0/README.md)，提交 `c61970d`，受测生产源码基于 `d4e5b2b`。
- 范围：一个已见 Translation 开发任务，一次全新迁移；default/rpcx_alternate 是同一候选的两个必需配置。
- 材料：[原 Campaign](../../runs/translation-handoff-dev-v0.1.0/campaign/summary.md)、[结果入口](../../runs/translation-handoff-dev-v0.1.0-result/README.md)、[离线汇总](../../runs/translation-handoff-dev-v0.1.0-result/round-final/summary.md)、[完整核对](../../runs/translation-handoff-dev-v0.1.0-result/verification.json)。运行材料不随 Git 提供。

## 实际执行与交接

用户对本版明确回复“授权按本协议运行一次”。启动器按冻结 SHA-256 `6775bddf10f6a5c0473d184bda0493e28248a7920a486ec1a84cac5a2b3b7b2e` 执行唯一一次 Models GET 及生成；没有重试、追加额度或手工修补候选。源码、工具与协议成员保持原字节，授权及实际结果另存。

执行从 **21:42:24.959814+08:00** 至 **21:50:09.153826+08:00**，宿主墙钟 **464.184 秒**，退出 0；未触发宿主中断或强杀。原 Run `rpcx-agent-1586805421` 为 **Completed / NeedsAttention**，Agent 在第 **31 次**模型调用有效 stop，随后完成原独立门禁。

本轮启用 final-response-v1，但没有到达末轮或用完工具额度：31 次请求全部仍可用工具，FinalResponseAttempt 未出现。可以报告本策略下的一次提前交付；**真实服务接受末轮空工具请求仍未观察**。没有为触发边界再启动第二次，也不能把提前提交归因为新策略。

实际模型 deepseek-flash，C=1,048,576、O=4,096、Q=1,052,672；新查询保留原文，相关能力核对通过，原文摘要仍为 `b4a502059eb2dafad6380e0a57b2debf31219ae859cba9f0e019d48ae473be2f`。本次实际 Plan 摘要 `7758068cfc66fb6b3e84984fafa7e5205d73f9bfc8abb731324745e2e8429661`，包含显式策略和诊断上限两次。声明相同不证明模型权重或行为不变。

## 已核对轨迹

新分析器核对实际 Plan/Input 策略和全部 **31 次** Complete 输入摘要，重放成功编辑、两份诊断快照及最终候选。模型没有访问 baseline 根，但在首次修改前已从 workspace 读取原测试、适配器及业务链，不能写成未见原测试。

| 调用 | 实际记录 |
| --- | --- |
| 1—13 | 读取规范、两个 SDK、原测试、两配置适配器、usecase/tracing、保护 mock 和依赖；无文件工具错误 |
| 14—19 | 改模块及两个适配器，测试只做必要 v2 导入适配；首次双配置 build 通过 |
| 20—21 | 双配置 test 通过；第一次 migration_check preserved，零 finding |
| 23—24 | 新增上下文、逐次期限和取消后不 Store 的测试；误将 WithValue 接收为两个返回值，双配置编译失败 |
| 25—26 | 改为 WithCancel(WithValue(...)) 后编译通过；新增探针对短/长父 deadline 的预期错误，双配置测试失败 |
| 27—29 | 将新探针修正为父 deadline 与 30 秒上限较早者；双配置 test/build 通过，第二次诊断 preserved |
| 30—31 | 复查模块与测试导入后正常提交；最后一次编辑已被最终本地 build/test 和诊断覆盖 |

首次诊断候选为 `7185bb737ee547b43fcddad0ad7e72af00da48168d2d1fb5801457e71aaef55f`；最终候选及第二次诊断均为 `7d7dd9cad65b36c67609295eed106aa6f5e6c64608015efd048928b3951de9cc`。两次反馈都进入后续有回复的请求；第二次诊断后没有再次编辑。

两次诊断都是 preserved，没有原测试结构违规修复机会。实际新增测试的编译与预期错误由 go_check 反馈后修正；不能称为 migration_check 修复收益。本次第 23 次即扩展测试，仍有 18 次模型机会（含当次），上轮在第 36 次才扩展、剩 5 次；时间顺序与规划不同，不支持新旧单次结果的因果比较。

## 原门禁、固定参考和实际复核

原运行完成 18 条独立验证命令。构建配置、构建、原测试执行、测试结构、依赖、保护路径、类型迁移与行为检查均 pass；唯一 unknown 是 request_scope：两份生产适配器被标记 unmodeled_production_change，因此保持 NeedsAttention。

源码检查确认两配置都使用新 Dial/Call(callCtx)，保留原 ctx.Err 预检查、映射、tracing、错误链和 Store 次序，每次从父 ctx 派生 30 秒并 defer cancel。有限结构比较没有建立本次完整结构对应：新增常量、注释及独立 callCtx 形式仍有超出归一化范围的差异。没有通过逐项消融认定某一个差异单独决定结果，也没有为本候选调整规则。

在启动固定参考前读取完整差异、全部变更生产/测试文件及未改的业务链，保存实际[源码初审](../../runs/translation-handoff-dev-v0.1.0-result/source-review-initial-translation.json)。随后固定 Translation data-v2 在独立副本执行 **19 条 Docker 命令**：

| 检查 | default / rpcx_alternate |
| --- | --- |
| 活动实现、标签及生成 mock | pass / pass |
| 依赖、保护路径与生产构建 | pass / pass |
| 原 26 个测试/子测试 | 全部通过，无遗漏或跳过 |
| 完整候选测试 | 各 36 项通过，包含新增 10 项测试/子测试，无失败或跳过 |
| 请求、映射、错误/取消/期限及业务次序 | pass / pass |
| 实际约 30 秒到期 | pass / pass |

固定自动参考为 **pass**，输入保持不变。实际助手复核的 test_preservation 与 change_coverage 为 pass；delivery_evidence 为 **fail**：模型最终自述将原 webapi 测试写成“三项顶层、五项子测试”，原文件实际为四项顶层、七项子测试。结构化门禁与实际事件保留正确数量，209 个审核链接可用；这是交付自述的计数错误，不能解释为删除原测试或生产行为失败。

完整参考/复核合成结论因该交付项为 **fail**；原 NeedsAttention 及业务参考 pass 分别保留，不改写。复核者 codex:/root 参与开发、旧样本检查、准备和协调，知道提前提交与原判定，不称独立人类或盲审。新结果没有晋升为 ReadyForReview。

## 指标、费用与保全

固定 N=1、R=1、Q=1、A=0、H=1、S=0。有效提交 1/1、原门禁完成 1/1；有效自动成功 **0/1**，需人工确认 **1/1**，错误放行率因 A=0 无定义。业务参考 pass 不能绕过原放行条件；离线汇总的 reference fail 是自动参考与实际复核的合成状态，不能写成业务自动参考失败。

| 项目 | 实际记录 |
| --- | --- |
| 模型 / 工具尝试 | 31 / 54，migration_check 两次包含在内 |
| 已知输入 / 输出 Token | 521,351 / 6,997 |
| 已知总 Token | 528,348 |
| 未知 / 在途占用 / 预算拒绝 | 0 / 0 / 0，31 次均结算 |
| Run / 宿主墙钟 | 463.535 / 464.184 秒 |
| 固定参考墙钟 / 命令耗时合计 | 181.262 秒 / 180.886 秒，在生成批次外 |
| 精确策略增量成本、金额及人类活动耗时 | 未测量或未知 |

原批次审计 pass，execution_state=completed，resource_state=recorded_removed，零 violation/gap。补丁在新基线无模糊/偏移重放到同一完整候选，209 个审核链接有效；59 个记录到精确 ID 的生成/验证/参考容器均已不存在，本 Campaign 精确 registry 标签无遗留。

原批次 3,085 份非缓存文件、820 份受测源码及导出、两份二进制和 16 份协议成员核对不变。离线汇总 valid=true、execution_complete=true、inputs_unchanged=true。原始材料与候选没有手工修改，所有参考、复核和分析另存；详见完整核对。未改产品 Go 或将受控校准计入真实分母。

## 本次发现与下一步

获得了一次完整可审查交付：Agent 修正自己的新增测试、复验最终候选并提前提交，Harness 完成独立判定和材料保全。末轮关闭工具本身没有在真实请求中触发，策略效果仍未知；不能从旧 Failed 到本次 Completed 推导成功率提高。

当前剩余问题更明确：可被源码与固定参考支持的生产形式仍落入结构证明 unknown；交付自述还存在计数错误。下一步宜先评审 request_scope 的有限支持边界及可复用证据，判断是否值得增加归一化/证明能力，再设计实现。无需立即重复生成或提高预算。本报告不启动下一项变更，原 M3—M5 留出、人员口径和总体验收缺口保持。
