# GHCLI 响应返回值的开发回归

将 [v0.2.0 真实候选](../../../../docs/experiments/2026-10-23-ghcli-dev-real-model-02.md)已确认的响应丢失转成可重复测试。此包属于已见样本的开发回归，不进入运行时门禁或冻结的独立参考，不产生新的迁移效果指标。

从项目根目录执行：

```sh
go test ./scenarios/rpcx/ghcli/regression -count=1
```

默认使用临时目录，不需要 runs、Docker、模型密钥或网络。Go 需已在本机安装；子模块只使用标准库及本地 SDK，禁止依赖下载。测试固定、已审查的源码，不接受任意候选，不提供执行不可信代码的隔离保证。

| 输入 | 四项返回值契约的预期结果 |
| --- | --- |
| 原 v1 基线 | 全通过 |
| 原样 v2 模型候选 | 拒绝重定向时丢失非空响应，失败 1 项 |
| 仅移除清空响应分支的 v2 对照 | 全通过 |
| 在上一对照中故意丢弃错误 | 传输错误、拒绝重定向的错误链丢失，失败 2 项 |

四项契约分别检查成功响应、普通传输错误、CheckRedirect 拒绝产生的响应与错误，以及 ErrUseLastResponse 保留响应。另核对相应 Body 的可读性、关闭责任和 Close 错误。外层测试只有在所有正反例结果匹配时才通过；编译错误、panic、超时、跳过或事件缺失不能冒充预期失败。

testdata/response-v1 保存原模型边界的原始字节及来源摘要。测试核对基线、SDK、规范，并从边界和 go.mod 变更重建完整候选摘要；不依赖旧 runs。两个修改对照仅用于证明契约的区分能力，不是新生成成果或完整正确性证明。

如需保留各模块、原始 go test -json 日志和 results.json，指定一个父目录已存在、输出目录尚不存在的位置：

```sh
mkdir -p runs/ghcli-response-regression-local
HARNESS_GHCLI_RESPONSE_OUTPUT="$PWD/runs/ghcli-response-regression-local/cases" \
  go test ./scenarios/rpcx/ghcli/regression -count=1 -v
```

再次执行应另选目录；默认测试不依赖这份产物。设计、验证和适用范围见[工作记录](../../../../docs/worklog/2026-10-23-ghcli-response-regression.md)。
