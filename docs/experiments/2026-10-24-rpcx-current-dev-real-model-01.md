# 当前四任务开发观察 01：安全阻挡与自动交付缺口

- 日期：2026-10-24（Asia/Taipei）。
- 状态：唯一真实启动、四项固定参考、实际助手复核、离线汇总和收尾完成；未重试或修补候选。
- 协议：[l2-current-dev-v0.1.0](l2-current-dev-v0.1.0/README.md)，提交 `65f7e06`；受测生产源码基于 `d4bd69b`，导出内容身份见 implementation.json。
- 范围：development_evaluation / seen-development，四个固定业务切片，各一次原样新候选。非严格未见、独立人员或盲审。
- 材料：[结果入口](../../runs/l2-current-dev-v0.1.0-result/README.md)、[原 Campaign](../../runs/l2-current-dev-v0.1.0/campaign/summary.md)、[最终汇总](../../runs/l2-current-dev-v0.1.0-result/round-final/round.json)。大运行材料不随 Git 提供，没有重新发布交付归档。

## 授权与实际执行

用户针对本版回复“授权按本协议运行一次”。启动器只执行一次，读取 Key 仅作鉴权；一次 Models GET，生成上限 160 次、工具 320 次、保守 Token 1000 万，每 Run 10 分钟、宿主 40 分钟后中断并最多尝试 60 秒收尾。没有第二次启动、自动重试、额度追加、手工改候选或恢复尝试。

实际从 **11:07:45.792657+08:00** 至 **11:18:24.523836+08:00**，宿主墙钟 **638.730 秒**，退出 0，未触发外部强杀。四项均 Completed，Agent 正常提交；执行状态和质量分开。

实际模型 deepseek-flash；C=1,048,576、max_tokens=4,096、Q=1,052,672。model-contract-v1 准入通过，实际 Plan SHA-256 为 `ad64f1afe45c5258e9417067d9508efd80befd395c48a85773f5febb45563f5b`。本次 Models 原文摘要仍为 `b4a502059eb2dafad6380e0a57b2debf31219ae859cba9f0e019d48ae473be2f`，查询时间为当前新值；没有再次观察到无关字段变化，不把这次准入当作容错因果试验。

## 原质量、固定参考与实际复核

助手三项按测试保留 / 变更覆盖 / 交付依据排列。所有判断保存实际 reviewer、RFC3339 时间、理由和源码/规范/证据定位；原门禁不回写。

| Target / 原 Run | 原门禁 | 固定自动参考 | 实际助手三项 | 完整参考/复核 |
| --- | --- | --- | --- | --- |
| Goddd / rpcx-agent-2809463338 | NeedsAttention | pass，8 条命令 | pass / pass / pass | pass，保留原未知 |
| Translation / rpcx-agent-1712004754 | NeedsAttention | pass，双配置 19 条命令 | fail / pass / pass | fail，test 类别：原断言削弱 |
| Workouts / rpcx-agent-4062530435 | Rejected | fail，10 条命令 | pass / fail / pass | fail，migration 类别：直接调用无期限 |
| GHCLI / rpcx-agent-1789045768 | NeedsAttention | pass，v3，11 条命令 | pass / pass / pass | pass，保留原未知 |

原四份候选的构建、基线测试均通过；这些事实不覆盖测试保留或完整语义。Workouts 的固定参考先在 behavior 得到明确失败，后续 expiry 未执行而为 inconclusive；不能写成参考实际到期检查也通过/失败。GHCLI v3 的响应/错误返回、Body 与实际到期检查全部通过。

### Translation：生产期限正确，原测试约束被削弱

两份 tagged adapter 均使用每次调用的 30 秒常量、父 ctx 与 defer cancel，生产映射/错误链及原上层行为经源码核对和固定参考支持。然而 `TestTranslationRoundTrip` 把 `time.Until(deadline) < 29*time.Second` 放宽为 `<= 0`。该测试传入 context.Background，没有更短父期限；原 [29,30] 秒条件变成 (0,30] 秒，1 秒也能被新断言接受。新增测试未恢复原下界。

这是 TEST-001 原断言保留违规，不是确认生产超时已经改成 1 秒。[谓词记录](../../runs/l2-current-dev-v0.1.0-result/translation-assertion-witness.json)只比较保存源码的逻辑，不冒称一次运行时故障实验。固定自动参考 pass 保留，完整复核因 test_preservation fail 而失败。原门禁 test_preservation 为 unknown，NeedsAttention 没有自动放行。

### Workouts：handler helper 未覆盖方法自身的期限

候选把 30 秒放在 command 层两个 helper 中，保留 callback ctx、两次独立派生、零余额跳过及原顺序/错误包装；这些 handler 路径的部分观察通过。但 UsersGrpc.UpdateTrainingBalance 与 TrainerGrpc.CancelTraining 自身直接把传入 ctx 交给 v2，没有恢复每次方法调用的期限。无界父 ctx 直接调用 adapter 时不再有 v1 隐式上限，违反 TIME-001。

固定参考 `TestL2ReferenceCancellation/repeat` 实际观察 **RPC has no deadline**，behavior=fail。原门禁另有两项约 35 秒的实际到期失败记录；二者分别保留。这里 Rejected 有行为依据，不能仅因 helper 不在静态支持范围就称误拒；相同报告中的静态 unknown 也保留。

### Goddd 与 GHCLI：复核支持，但不回填自动成功

Goddd 的 HTTP→booking/装饰器→RoutingService 链正确传递请求 ctx，构造 ctx 不再存储为请求来源，局部 request、30 秒常量及早退形式超出部分有限证明；mock 仅必要签名适配，无新增空成功分支。固定参考及实际源码复核支持当前改动。

GHCLI 仅修改模块元数据与 outbound.go。有效 HTTP request 分支使用原 request.Context，逐调用派生 30 秒；dispatchHTTP/cancelBody 原样保留响应、错误及 body 所有权。生产调用者在 http.NewRequest 成功后调用，未以 Background 替代有效请求 parent；未声称 nil request 获得规范以外的新保证。当前有限证明仍不支持该 parent/命名常量形式，故保留 NeedsAttention。

## 指标与费用

N=4、R=4、Q=4、A=0、H=3、S=0。有效自动成功 **0/4**，需人工确认 **3/4**；A=0，错误放行率无定义，不能报告 0%。实际复核支持的两项 NeedsAttention 不算自动成功。执行失败数为 0；质量 Rejected 与两项完整参考/复核失败分开记录，test 与 migration 各一项，没有确认本轮交付依据误拒。

| Target | 生成准入 / 工具 | 已知输入 / 输出 Token | 合计 Token | Run 秒数 |
| --- | --- | --- | --- | --- |
| Goddd | 38 / 77 | 835,174 / 12,635 | 847,809 | 272.191 |
| Translation | 24 / 45 | 328,699 / 6,485 | 335,184 | 411.526 |
| Workouts | 29 / 55 | 337,403 / 5,519 | 342,922 | 218.714 |
| GHCLI | 16 / 24 | 113,871 / 2,490 | 116,361 | 225.758 |
| 全批 | **107 / 201** | **1,615,147 / 27,129** | **1,642,276** | **638.730（宿主墙钟）** |

107 笔均 settled，未知/在途占用及预算拒绝均为 0；缓存输入不重复累加。Run 并发，不把各自秒数相加当批次墙钟；固定参考、助手复核及审计时间在批次外，不计入生成墙钟。实际金额、人类活动耗时与接入工时未知。没有额外参考模型调用。

## 复核方法与保全

实际助手 codex:/root 参与过开发、旧候选复核、协议准备及协调。先逐项阅读原候选/规范并写 initial 记录，再执行固定独立参考和最终复核；读取候选时已知原质量元数据，不称盲审。生成对话独立，但共享 SDK、工具链与设施，存在共同盲区，助手不冒用用户或独立人类身份。

原 Campaign 审计 consistency=pass、execution_state=completed、violations/gaps=0，资源记录为 recorded_removed。四份补丁在全新基线上无模糊/偏移重放，得到相同完整候选摘要；711 个审核链接和 62 个复核来源定位通过。131 个记录到的精确容器 ID 在固定 context/engine 上均不存在，本次 registry 原文字节摘要标签下也无遗留容器。

参考与复核/汇总完成后，原批次 19,289 份非缓存文件逐件不变；809 份当前源码/导出材料、两份固定二进制和 12 份协议成员保持一致。原始费用与四候选留在原目录，所有后续材料放在新结果目录。最终汇总 valid=true、execution_complete=true、inputs_unchanged=true。证据见[验收记录](../../runs/l2-current-dev-v0.1.0-result/verification.json)。未修改生产 Go、冻结规则/数据、旧实验或旧归档，未把常规测试当作新增效果样本。

## 结论与下一步边界

这次观察没有证明自动交付提高：当前实现四项都完成了可审查交付，但没有自动放行；两项具体错误被阻挡，另外两项需要源码复核才能支持。它同时暴露生成端的断言保留/期限放置问题和门禁有限形式覆盖问题，不能只用成功率或“全部安全”概括。

本次协议到此结束。下一步先对这两类缺口做取舍：优先考虑生成约束/可反馈的规范违规能否减少不必要改动，而非立即扩展所有 helper/常量的证明形式；任何新修复循环或效果比较需另行设计、固定预算和候选历史边界。当前不直接修改提示、门禁或再调用一次来追求通过。

旧四任务 1/4、后续 GHCLI 0/1 与本轮 0/4 分开保留，代码/参考/模型与采样条件不同，不作严格因果比较。原 M3—M5 的严格留出/人员总体验收、生产泛化和实际审核收益缺口仍存在。
