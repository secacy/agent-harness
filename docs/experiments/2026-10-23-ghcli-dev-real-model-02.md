# GHCLI 开发评测 02：未知范围中的响应返回值变化

- 日期：2026-10-23（Asia/Taipei）。
- 状态：唯一真实启动、固定独立参考、实际助手复核、离线汇总和收尾完成；未重试、未修补候选。
- 协议：[ghcli-dev-eval-v0.2.0](ghcli-dev-eval-v0.2.0/README.md)，提交 dcb48ff；生产实现 75554dc。冻结 README 的未授权状态记录冻结时事实，本报告记录后续实际事件。
- 类型：schema 2 / development_evaluation / development，N=1，已见且参与门禁开发的有限应用切片；不是未见留出或独立人类盲审。
- 材料：[本次入口](../../runs/ghcli-dev-eval-v0.2.0-result/README.md)、[原 Campaign](../../runs/ghcli-dev-eval-v0.2.0/campaign/summary.md)、[最终汇总](../../runs/ghcli-dev-eval-v0.2.0-result/round-final/round.json)。产物不随 Git 分发。

## 授权与实际执行

用户针对本版回复“授权按 v0.2.0 协议运行一次”。许可覆盖读取 .env 的 DEEPSEEK_API_KEY、一次 Models GET、向既定 DeepSeek Chat Completions 发送本任务代码/规范/SDK/允许依赖/工具交互，最多 40 次生成、80 次工具及 250 万保守 Token，Run 10 分钟、宿主 12 分钟中断和最多 60 秒收尾，不重试或追加额度。

启动于 12:06:23.534120+08:00，结束于 12:10:33.061631+08:00，宿主墙钟 249.525 秒，退出 0。唯一 Run 为 rpcx-agent-323326691，执行状态 Completed、Agent 正常提交、原质量 NeedsAttention；Run 耗时 248.508 秒。启动器未触发超时/强杀；基线通过后才调用模型，未自动重试或手工修改候选。

实际模型 deepseek-flash，C=1,048,576、请求 O=4,096、Q=1,052,672。当前查询原文 SHA-256 为 b4a502059eb2dafad6380e0a57b2debf31219ae859cba9f0e019d48ae473be2f，与本版历史期望相同，查询时间为本次新值。显式 model-contract-v1 准入成功，实际 Plan 全文摘要为 a31ebe33a6cbba2b748cb113da04a28bdabfb5ca4a3ebadb6d7b7816c6491bcb，原文及 Profile 进入实际计划和账本。线上本轮没有再次观察到附加字段变化，不能把这次准入当作元数据容错的因果实验；容错边界已有[模拟服务证据](../worklog/2026-10-23-capability-contract.md)。

## 原结果、参考及实际复核

| 层次 | 结果与含义 |
| --- | --- |
| 原执行 | Completed，原样发布补丁和审核材料 |
| 原门禁 | NeedsAttention；构建、原测试、保护路径、依赖、迁移完整性、有限语义观察均 pass，request_scope=unknown |
| 固定独立自动参考 | pass；6 项检查通过，11 条 Docker 命令，含实际到期；原输入未变化 |
| 实际助手复核 | test_preservation=pass / change_coverage=fail / delivery_evidence=pass |
| 完整参考＋复核 | fail，类别 migration；原门禁不回写 |

候选摘要 b4294cbb291cc5a5a4c6c9b1e12845ba9c31fb79e8724cffa60a72653d324db6，补丁摘要 5703d62ebb7bf16540e58e60089df0ee3a4111ca203d70f900704c988e2336b1。模块共 68 份文件，只改 go.mod 和 pkg/cmd/release/delete/outbound.go；原测试、断言、skip/tag、go.sum、LICENSE 及支持包均保持字节。

实际复核者 codex:/root 兼任此前门禁开发、协议准备和执行协调；生成者为独立 DeepSeek 对话，但共享 SDK、Go 和执行设施仍有共同盲区。助手先读原候选/规范并记录疑点，再执行固定参考，随后完成疑点诊断和最终复核；实际 RFC3339 时间、文件及理由见[复核 JSON](../../runs/ghcli-dev-eval-v0.2.0-result/reviews/ghcli.json)。没有冒用人类签署或预填 pass。

## 个案：额外清空响应改变函数边界

SDK 路径、Dial、带 ctx 的 Call 和逐调用 30 秒均已适配。原 dispatchHTTP 及 cancelBody 没有修改，仍从原 HTTP parent 派生 body 生命周期，保留调用方读取和 Close 的责任。候选额外加入：

```go
if err != nil {
    response.Response = nil
}
```

位置为候选 outbound.go:47—49。规范第 3 段要求保持返回结果与错误，宿主映射为 BEHAVIOR-001；这个分支不是接口适配的必要修改。静态门禁只报告整体边界未证明，不能据此称它已经定位或证明了这处违规。

实际助手复核补充了一个纯内存 HTTP 重定向拒绝场景：自定义 RoundTripper 返回 302，CheckRedirect 返回普通 sentinel 错误。分别将原基线和候选的完整 outbound.go 原字节放入独立模块，挂载原发布 SDK，用固定禁网 Docker 调用真实 invokeHTTP。两边都保留同一错误链，结果分别为：

| 诊断副本 | 返回响应 | 错误链 | 保持原响应＋错误的断言 |
| --- | --- | --- | --- |
| 原基线 | 非空 | 保持 | pass |
| 原样候选 | nil | 保持 | fail |

诊断日志见[基线](../../runs/ghcli-dev-eval-v0.2.0-result/redirect-witness/baseline.stdout.jsonl)、[候选](../../runs/ghcli-dev-eval-v0.2.0-result/redirect-witness/candidate.stdout.jsonl)和[源码身份](../../runs/ghcli-dev-eval-v0.2.0-result/redirect-witness/source-identity.json)。该失败是实际执行到断言的返回值差异，非编译或设施失败。当前 deleteRelease/deleteTag 先检查 err，诊断没有证明最终 CLI 文本输出也变化；已确认的是规范保护的函数边界返回值改变。

这是看到候选后针对疑点构造的助手诊断，不并入冻结自动参考、不作为未见样本或独立评测分母。固定参考的 pass 保持，完整复核因该反例为 fail。原报告诚实保留 request_scope 未知及 NeedsAttention，因此未把这个候选自动放行；人工确认仍需真正检查未覆盖变更。

## 计数与成本

N=1 / R=1 / Q=1 / A=0 / H=1 / S=0。有效自动成功 **0/1**，需人工确认 **1/1**；A=0，错误放行率无定义，不报告 0%。单个已见样本不能推断总体正确率、错误放行概率或审核成本。

实际一次 Models GET、17 次生成准入、17 次 Complete、25 次工具调用；已知输入 138,544、输出 2,365，合计 **140,909 Token**。17 笔均 settled，未知/在途占用和预算拒绝均为 0。缓存输入不重复计；金额账单和真实人类活动耗时未知。参考、诊断与复核没有额外模型调用，其耗时不混入生成 Run。

原执行失败分布为空，原质量缺口为 request_scope；完整复核的迁移失败另列。候选被 NeedsAttention 拦在常规审核之外，不把自动参考 pass 改算为自动成功，也不将诊断的预期失败当作 Harness 执行失败。

## 保全与交付

原 Campaign 审计 consistency=pass、execution_state=completed、violations/gaps=0；补丁在新基线上无模糊重放后等于保存候选。原审核、批次及新汇总的 95 个本地导航目标存在，实际复核引用位置均可定位。

生成 14、参考 11、补充诊断 2，共 27 个精确容器 ID 已在固定 context/engine 上核对不存在；原 Campaign 实际 registry 标签下也无遗留容器。资源核对只读，见[资源记录](../../runs/ghcli-dev-eval-v0.2.0-result/resources.json)。审计 pass 只证明记录一致，不能替代上述语义复核。

原批次非缓存文件在评测前固定为 14,516 份，参考/诊断/重放/复核/汇总均放在独立结果目录，不覆写原运行。实现、冻结材料、历史及读取前后保全核对见[验收汇总](../../runs/ghcli-dev-eval-v0.2.0-result/verification.json)。无新生产 Go 变更、无新交付包、无继续生成。

本轮交付的是有明确失败依据的候选与可审查材料。旧四任务 1/4、旧失败启动、原协议和里程碑总体验收缺口保持。下一项先将已确认的响应＋错误反例转为单独的离线回归与支持边界设计；本次结果不修改后再重跑，也不靠追加模型请求代替已得到的证据。

收尾校验：781 份实现/导出材料及二进制、12 份协议成员和 freeze、59,589 份历史文件及旧失败启动原文均保持；6 份变更 Markdown 的 284 个本地目标、35 个锚点及代码围栏检查通过，git diff --check 通过。本轮没有变更生产 Go，未重复运行项目测试或追加真实生成。
