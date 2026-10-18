# rpcx T3 测试结构完整性

`Compare` 检查候选是否保留冻结测试的结构，允许宿主定义的最小 API/context 适配，返回 `preserved`、`violated` 或 `unknown`。它不执行候选，不输出最终质量判定。

```go
report, err := testintegrity.Compare(baselineTests, candidateTests)
```

两项输入都是 `map[string][]byte`：键为模块内相对路径，值为文件内容。调用方必须收集完整的 `_test.go` 文件集，包括未激活构建配置下的文件，确认源码在检查期间不变，并把结果绑定至完整基线/候选摘要。这个接口不扫描目录、不加载任意文件，也不认证候选提供的文件清单。

当前仅支持 `policy.json` 绑定的 T3 三份原测试文件，适配计划为：

- 把可信基线的 rpcx 导入切换为 rpcx/v2。
- inventory 的两处 Reserve/Release 测试调用增加 `context.Background()`。
- service 的三处 Cancel 测试调用增加 `context.Background()`。

该计划仅在原文件清单和每个字节的摘要符合策略时执行，核对每个文件的实际适配次数。名称匹配只作用于已认证的冻结基线，候选不会按相同方法名被归一化。这里的 Background 用于原业务测试适配，请求生产链路的 context 约束仍由其他检查承担。

`AdaptBaseline` 单独返回新文件集，供后续可信生产副本安装。它不读取公开补丁、manifest 或 oracle，不修改输入。Docker 对照已确认其输出可与正确候选生产代码一起构建并通过原十个测试；副本装配和运行时门禁接入还待完成。

比较时忽略格式、普通注释和 token 位置，比较完整原测试及 helper 的 AST；导入、包名、文件位置、构建约束和 Go/line 指令另行检查。支持范围为：

| 变化 | 结果 |
| --- | --- |
| 宿主允许的适配，或只改格式/普通注释 | preserved |
| 在现有文件中新增普通 Test 函数，原结构不变 | preserved，并列出新增测试 |
| 原测试名消失 | violated：original_test_removed |
| 原测试首语句直接调用 t.Skip/Skipf/SkipNow | violated：original_test_skipped |
| 仅删除已识别的纯断言，其余结构不变 | violated：original_assertion_deleted |
| 等价 helper 重写、条件重写、条件跳过 | unknown |
| helper、TestMain/init、全局声明、导入或语义指令变化 | unknown |
| 新增文件、跨文件移动、声明重名、候选语法错误 | unknown |

纯断言包括冻结基线中无 Init/Else、单独调用 testing 错误方法的 if，以及直接错误方法语句。比较器要求其他节点逐字段相同；新增测试不能用断言数量补偿原测试的删除。带 Init 的 guard 可能包含执行行为，删除时保留 unknown。若同时出现 helper、文件范围或其他重写，则不会把断言节点消失直接判为删除，使用 assertion_replacement_unproven 保留缺口。

有明确违规时报告仍为 violated，同时保留其余 unknown。不受支持的基线、越界/非测试路径或输入超限返回 error 和空报告。每文件最多 1 MiB，最多 10000 个文件、合计 16 MiB。

报告 schema_version=1：

- `baseline_tests`、`adapted_tests`、`candidate_tests` 保存完整文件摘要集合及集合摘要。
- `material_sha256` 绑定策略及四份实现文件，`verifier_version` 标识策略版本，`go_version` 记录宿主 AST/格式化工具链。
- `declarations` 记录每个原测试/helper 的预期与实际 AST 摘要及结构状态。
- `findings` 关联 TEST-001、原因与物理位置，`added_tests` 保存新增测试清单。

AST 摘要是当前 Go 工具链下去掉位置后的结构打印摘要；跨工具链不承诺相同。删除位置指向 `adapted`，需要同时保存 AdaptBaseline 输出；其他位置指向 baseline 或 candidate，使用物理行号，防止 line 指令重定向位置。调用方负责落盘和关联材料，本包不另建运行记录接口。

**能力边界：**preserved 只证明声明范围内原测试结构保留，不证明测试真的运行、断言必然有效或业务正确。新增 Test 函数依然可能影响执行顺序和共享状态；生产代码也参与运行。必须继续检查原测试通过/跳过事件，并在只含同一候选生产代码的可信副本运行业务与超时检查。范围未知不能被另一项行为通过覆盖。此实现尚未接入旧 Evaluate。

机制测试及四组 Docker 对照见[命令说明](../../../docs/command.md#m23-测试结构完整性)，方案取舍与实际结果见 [M2 工作记录](../../../docs/worklog/2026-10-18-m2-evidence-gate.md#17-m23测试结构完整性与适配对照)。
