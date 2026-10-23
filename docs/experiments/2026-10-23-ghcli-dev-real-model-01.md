# GHCLI 开发评测 01：生成前能力身份不匹配

- 日期：2026-10-23（Asia/Taipei）。
- 状态：授权后唯一启动已结束；Campaign Failed，Target/Run 尚未创建，生成调用为 0，未重试。
- 协议：[ghcli-dev-eval-v0.1.0](ghcli-dev-eval-v0.1.0/README.md)；提交 2e58d7e，生产源码基线 7f6828c。冻结文件保持原字节，其中“未授权、未执行”记录冻结时状态；本报告记录后续实际事件。
- 材料：[启动记录](../../runs/ghcli-dev-eval-v0.1.0-launch/launch.json)、[实际 Campaign](../../runs/ghcli-dev-eval-v0.1.0/campaign/campaign.json)、[差异核对](../../runs/ghcli-dev-eval-v0.1.0-result/capability-diff.json)、[收尾验证](../../runs/ghcli-dev-eval-v0.1.0-result/verification.json)。运行产物不随 Git 提供。

## 授权与执行

用户针对本协议回复“授权按本协议运行一次”。许可覆盖读取 .env 的 DEEPSEEK_API_KEY、一次 Models GET，以及规定范围内的代码/规范/允许依赖/工具交互生成请求；最多 40 次生成、80 次工具、250 万保守 Token，Run 10 分钟，宿主 12 分钟中断并最多 60 秒收尾，不重试或追加额度。

固定启动器于 10:56:37.831079+08:00 启动，于 10:56:39.789607+08:00 结束，宿主墙钟 1.961 秒，退出码 1。源码、导出副本、二进制、Go、Docker 镜像/引擎和本地配置检查通过后，程序读取 Key 并完成一次鉴权 Models GET；没有打印或保存 Key。实际能力原文和计划已保存，schema 2 生成前纳入随即拒绝：

```text
campaign control failure: prepare campaign materials: actual campaign settings differ from the frozen protocol
```

Prepare 位于 Target 记录和账本创建之前，因此没有 Target/Run、基线命令、生成请求、仓库工具调用或候选。失败后没有重新启动、修改冻结材料或手动补造纳入记录。

## 差异与原因

对两份 Models JSON 做结构比较，只有两个新增字段：

| 路径 | 当前新增值 |
| --- | --- |
| data[0].api_capabilities.anthropic_messages.tool_update | addition-only |
| data[1].api_capabilities.anthropic_messages.tool_update | leading-only |

实际选择的 deepseek-flash、Chat Completions 端点、context_window=1,048,576、服务 max_output_tokens=393,216 和本次请求输出限制 4,096 均未变化。实际计划与期望计划只有 profile.source_sha256、profile.verified_at 两项差异；查询时间原本就不参与计划设置摘要，原始响应身份参与比较。

历史响应 SHA-256 为 b86388abe65929c84de3825eee299b8d78bae23e82e300239f3c788569eb5f39，当前为 b4a502059eb2dafad6380e0a57b2debf31219ae859cba9f0e019d48ae473be2f。协议固定了整份模型列表原文身份，因而另一个 API 协议下的附加元数据也触发拒绝。上述比较只说明已观察到的字段差异，不证明服务内部行为或计费实现完全相同。

这次停止符合冻结协议，但暴露了可用性代价：全响应身份把记录来源和运行兼容性绑在一起，对无关的附加元数据也会拒绝。后续应先权衡全原文匹配与“固定所用能力契约、另存实际原文身份”的方案；不能在本次失败后放松旧协议继续执行，也不能只换一个历史响应摘要就假设问题已长期解决。本轮未修改生产实现。

## 原始计数与评测边界

| 项目 | 本次实际结果 |
| --- | --- |
| 计划任务 N | 1；这次失败保留，不从计划记录删除 |
| 实际创建 Target / Run | 0 / 0；没有磁盘 Skipped 或 Failed Run |
| Models GET / 生成请求 / 工具调用 | 1 / 0 / 0 |
| 生成 Token 消耗、未知或在途生成占用 | 均为 0；账本未创建，不伪造零用量账本 |
| 候选与质量判定 | 无；ReadyForReview / NeedsAttention / Rejected 均未产生 |
| 有效自动交付 S / 计划 N | 0/1；表示本次计划未交付，不是一次已运行候选的迁移效果测量 |
| 错误放行 k / A | A=0，无定义 |
| 需人工确认 H / 计划 N | 0/1；任务未运行，不能据此推导审核需求低 |
| 失败原因 | 生成前协议准入：能力原文身份不匹配 |

schema 2 纳入未成功，没有 round-manifest.json 或 round-request.json，不能调用 summarize 伪造一个有效评测轮次。独立参考、实际候选复核、补丁重放和候选审核导航均无对象；没有用旧候选替代本轮。这次记录只能评价启动前检查行为，未得到新的自主迁移证据。货币账单未知。

CLI stdout 打印的 Summary 路径是预设路径；实际没有 summary.md/summary.json。应以原 Campaign Failed 及缺失的正式汇总为准，不能把该提示当成已发布材料。

## 审计与材料保全

冻结二进制对已停止 Campaign 执行只读审计，结果为 consistency=pass、execution_state=terminal_with_failures、violations/gaps=0；两条 observation 记录账本和 Target 尚未发布。该 pass 只确认失败记录自洽，不表示实验完成或迁移正确。审计结果见[原生报告](../../runs/ghcli-dev-eval-v0.1.0-audit/audit.json)。

资源注册表存在，没有命令 intent 或容器创建记录。实际 Docker engine 与冻结值相同，使用执行器真实采用的 agent-harness.registry 标签及注册表原文摘要查询，没有遗留容器，无需清理。初次按 Campaign 名称查询使用了非运行时标签，不计作资源验收；随后已按真实 registry 标签完成只读核对，见[资源记录](../../runs/ghcli-dev-eval-v0.1.0-result/resources.json)。

779 份当前源码和导出副本、两份导出二进制、12 份协议成员及 freeze 均保持身份；原登记的 59,589 份历史材料摘要不变。审计实际引用的文件与缺失项再次核对相同；本轮启动、原始输出和审计另存[文件清单](../../runs/ghcli-dev-eval-v0.1.0-result/original-files.json)。未执行候选、参考或 Go 回归，本轮仅新增实际结果与进度文档；原生产路径未修改。

原四任务评测、保存候选、交付归档及有效自动成功 1/4 保持原样。本轮有限开发观察没有达到生成目标；下一项先解决能力契约与原文身份的设计边界，再决定新版本协议和独立的一次执行，不沿用本次启动许可自动重试。

文档收尾：本轮 6 份 Markdown 的 265 个本地链接目标、35 个锚点和代码围栏校验通过，git diff --check 通过；未重复运行 Go 测试或候选验证来替代本次未发生的生成。
