# 诊断反馈定点修复协议 v0.2.0

本协议依据已确认的[设计](../../worklog/2026-10-25-diagnostic-repair-comparison.md)，固定一个已见 Translation 错误候选，按 A→B 串行运行，每组一次。A 对照原文，B 额外收到一次起始诊断；两组均无额外诊断工具。当前尚未运行真实模型，无本协议消费授权。

本版只调整独立验证 3→6 分钟、宿主总时限 22→28 分钟，用户已确认；前版 [v0.1.0](../diagnostic-repair-dev-v0.1.0/README.md) 的失败与材料保留。实际校准结果见[实施记录](../../worklog/2026-10-25-diagnostic-repair-comparison.md#8-时间边界修订与第二轮离线校准)。真实执行须先通过本版离线校准并另获一次消费授权。

## 固定材料与预算

- [protocol.json](protocol.json)：每组 12 次生成、24 次工具、150 万 Token；全程 24/48/300 万。Token 包含未知占用，不代表货币账单。
- [seed.json](seed.json)：历史 Run `rpcx-agent-1712004754` 的完整候选，以路径到 Base64 文件字节保存，含原 LICENSE；目录树 SHA-256 为 `19c4a36e7cdd83c0106fbdbbc3c221affe4dd471c0dd41df531491f48c4e6884`。模型只见解码后的业务工作副本，不见整个协议目录。
- [prompt.txt](prompt.txt)：公共任务文本；完整公开规范、相同工具根及组别反馈消息由驱动追加。
- [freeze.json](freeze.json)：驱动及其包构建/嵌入材料、协议输入的 SHA-256 清单；基线/SDK 另按原 `rpcx-translation-t3-v1` Profile 认证。
- 原始业务来源、许可与切片改动沿用 [Translation 数据说明](../../../scenarios/rpcx/l2/data/rpcx-l2-translation-v1/README.md)。这是已见开发修复样本，不是未见留出。

模型为协议固定的 DeepSeek 文本配置，thinking disabled、非流式、输出上限 4096。付费前只查询一次 Models；预期 context 上界 1,048,576，任何不符停止生成。无重试和额度追加。两组各自最多 3 分钟准备、5 分钟生成、6 分钟验证；宿主总时限 28 分钟，结束后最多尝试 60 秒收尾。每次请求调用前按现有账本预留 C+O；额度耗尽时可能不足 12 次就结束。

## 实现入口与离线校准

入口是 runner 测试二进制中显式选择的 `TestDiagnosticRepairExperiment`，不提供新的产品命令。普通测试默认跳过它；驱动不会搜索或读取 `.env`。所有示例从项目根目录开始，先创建产物父目录。

```sh
mkdir -p runs/diagnostic-repair-v0.2-build
go test -c -o runs/diagnostic-repair-v0.2-build/repair.test ./scenarios/rpcx/runner
```

在 runner 目录运行该二进制，`prepare` 模式核对冻结材料，独占写入协议/材料/二进制身份回执，不访问 Docker、凭证或网络：

```sh
cd scenarios/rpcx/runner
HARNESS_REPAIR_MODE=prepare \
HARNESS_REPAIR_LAUNCH=../../../runs/diagnostic-repair-v0.2-build/launch.json \
../../../runs/diagnostic-repair-v0.2-build/repair.test \
  -test.run='^TestDiagnosticRepairExperiment$' -test.v -test.timeout=30m
```

`offline` 模式运行真实 Docker 基线、文件工具、双构建配置和原独立门禁，模型只重放固定编辑；它不能证明模型自主修复。输出目录必须不存在，父目录已存在：

```sh
HARNESS_REPAIR_MODE=offline \
HARNESS_REPAIR_OUTPUT=../../../runs/diagnostic-repair-v0.2-offline-01 \
../../../runs/diagnostic-repair-v0.2-build/repair.test \
  -test.run='^TestDiagnosticRepairExperiment$' -test.v -test.timeout=30m
```

准备回执绑定实际二进制，不复用本机其它构建的回执；代码或冻结材料变化后需要重新准备。该协议发布后不原地修改，实质变更另起版本。

## 真实执行与失败边界

真实执行需先对本版协议取得一次明确授权。授权覆盖读取密钥、一次 `https://api.deepseek.com/models` GET，以及两组向 `https://api.deepseek.com/chat/completions` 发送各自工作区代码、原基线、规范、SDK 和工具交互。实验驱动只从宿主已经注入的 `DEEPSEEK_API_KEY` 环境变量取值。

启动使用同一二进制、精确测试名、`HARNESS_REPAIR_MODE=real` 和准备回执。真实输出固定为项目根下 `runs/diagnostic-repair-dev-v0.2.0`，必须不存在；回执旁的 `.started` 以独占创建标记一次启动，失败也不重试。真实执行命令在授权后单独记录，本轮不执行。

两份基线与相同种子先全部准备、认证并诊断，确认反馈相同且能定位目标 unknown 后，才开始模型。正常的 A 未修复、协议错误、预算耗尽或未提交仍继续预定 B；共享账本、输入、执行设施或证据发布故障停止后续模型调用，保留部分候选与未启动原因。

## 结果与复核

产物入口为 `experiment.json` 和 `summary.md`。A/B 各有 `attempt.json`、起始/结束快照、`initial-messages.json`、原始对话、诊断快照及独立验证材料；共享账本和逐命令记录单独保存。比较器和允许的恢复答案只在宿主，工具根与 Docker 不挂载协议、其它组材料、诊断目录或 oracle。

固定核对项是：原期限断言恢复、其它测试保留、范围保留、双配置实际检查、正常提交、交付说明准确。目标断言只接受原始条件及失败动作，允许原报错文案或种子报错文案；其它等价重写保留 unknown。原生产门禁结果另列，其 unknown 不自动等于断言修复失败。

驱动将 `delivery_evidence` 保留 unknown，待助手按[复核表](review-template.md)核对实际说明和命令；没有该复核不计完整定点修复成功。复核追加新材料，不改原对话、终态或独立门禁。两组都成功不表明诊断有额外价值；B 优于 A 也只报告本例这次差异，不能称稳定增益。固定两次尝试后结束本工作，不自动扩样本或追加预算。
