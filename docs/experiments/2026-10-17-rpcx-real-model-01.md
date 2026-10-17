# rpcx 真实模型迁移实验 01

- 日期：2026-10-17
- 结果：Agent 在 20 次模型调用预算内未提交，原 Run 为 Failed；保留候选的事后独立诊断为 ReadyForReview。
- 原 Run：`rpcx-agent-2368039229`
- 实验目录：`runs/rpcx-real-nnupis6i/`（Git 忽略）
- 代码提交：`3d62c1964e984648d87fac7f0ebf1407b73aadcb`

## 目标与条件

检验真实模型能否从迁移前基线自主探索、适配代码与测试、使用构建/测试反馈，并在预算内提交候选交由独立门禁验收。本次只运行一次模型任务，不重试，不增加预算或修改提示后覆盖原结果。

| 条件 | 配置 |
| --- | --- |
| 请求模型 | deepseek-flash；服务实际模型快照未记录 |
| API | https://api.deepseek.com/chat/completions |
| 请求方式 | 非流式，thinking disabled，未显式设置 temperature/seed |
| 规范 | v0.1.0；SHA-256 `330e439ab69a9c84dbb88343ef5ccc430eeec357bb3f54e01dcad83ce860e251` |
| 基线 | 原始 demo-orders，10 个业务测试 |
| 输入范围 | 规范、业务仓库副本、只读 rpcx-v1/rpcx-v2；未提供 cases、参考补丁、oracle 或验证器实现 |
| 执行环境 | macOS arm64 上的本机 Docker 29.8.2，Linux 容器 |
| 镜像 | `golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd` |
| 容器配置 | 2 CPU、2048 MiB 内存、128 进程，禁网，只读源码与 SDK，临时缓存随命令销毁 |
| 预算 | 20 次模型调用、80 次工具调用、单次最多 4096 输出 Token、整体 10 分钟 |
| 密钥 | 由实验启动器作为数据读取本地 .env，仅传入运行进程；未进入提示、报告或提交 |

入口仍不自动加载 `.env`。本次实验启动器只解析需要的两个变量，不执行文件中的 shell 指令。调用的实际命令及不含密钥的配置保存在 `experiment.json`。Go 二进制提前构建，下面为等价调用：

```sh
./runs/harness \
  --model deepseek-flash \
  --runs runs/rpcx-real-nnupis6i/tasks \
  --max-model-calls 20 \
  --max-tool-calls 80 \
  --max-tokens 4096 \
  --timeout 10m
```

## 原始结果

| 项目 | 实测 |
| --- | --- |
| 进程退出码 | 1 |
| Run 状态 / 阶段 | Failed / Generating |
| Agent 退出原因 | limit_reached（模型调用预算耗尽） |
| 模型 / 工具调用 | 20 / 47 |
| 工具错误 | 0 |
| 外层任务耗时 | 59.137 秒，含后端预检及收尾，不含提前构建二进制 |
| 原 Run 最终质量判定 | 空 |
| 原 Run 独立验证 | 未执行，report.json 的 validation 为空 |

实际工具分布为：list_files 12 次、read_file 18 次、replace_text 14 次、search 1 次、go_check 2 次。模型可以在一次响应中发出多个工具调用，工具次数不等于模型轮数。

20 次响应均返回 usage；累加输入 148561、输出 5246、总计 153807 Token，缓存命中 139264、未命中 9297。输入累计包含每轮重复发送的历史，不是单次上下文长度；本次没有推算货币费用。

## 调用轨迹与失败原因

| 模型轮次 | 实际行为 |
| --- | --- |
| 1–8 | 列目录、读取规范、SDK、业务源码和测试；workspace 根目录被列举三次 |
| 9–15 | 完成 go.mod、库存封装、订单服务、HTTP handler 和原有测试的迁移 |
| 16 | 搜索 rpcx 引用，容器内 go build 通过 |
| 17 | 容器内 go test 通过，此时尚未增加后续六个测试 |
| 18–20 | 重新读取部分测试，添加 context 传播与取消测试 |
| 下一轮前 | 模型调用预算已耗尽，循环退出，没有收到 stop 提交 |

直接原因是缺少正常提交，不是 API、工具或容器执行失败。模型在第 17 轮已有构建和测试通过的候选，随后继续扩充测试；新增测试符合规范允许范围，但消耗了余下预算，也没有在原 Run 中得到再次执行。

现有提示没有告知本次剩余模型调用数。轨迹支持进一步检验“重复探索和提交时机管理是否影响预算内完成”的假设；单次实验不能证明增加预算或修改提示一定有效。

## 保留补丁与事后诊断

原补丁修改 7 个文件：go.mod、inventory/client.go、orders/service.go、server/handler.go 及三个受影响的测试文件。人工差异检查确认：

- 导入和依赖升级到 rpcx/v2 v2.0.0，构造函数改为 Dial。
- Place 传递已有 context；Cancel 从 HTTP r.Context() 沿 service → inventory → RPC 传递，没有请求链路 Background/TODO 兜底。
- 库存参数、错误包装、锁和订单状态更新顺序保持原有行为，失败 RPC 不提交状态变更。
- 原有测试断言未被删除或削弱；新增 6 个传播/取消测试。
- internal/pb 和原始 fixture 文件保持不变。

为区分代码质量与提交失败，实验后另行调用同一个 Evaluate，使用 Docker 执行，证据写入 `diagnostic/`。该步骤没有调用模型，也没有修改原 Run、候选或原报告。

诊断的 dependencies、protected_paths、build、baseline_tests、test_preservation、context_propagation 六项均通过，16 个业务测试执行通过，诊断判定为 ReadyForReview。它是事后代码质量证据，不能替代原 Run 缺失的提交或算作自动成功。

将导出补丁应用到干净基线后，文件树摘要与保留候选相同：

- 候选与补丁还原摘要：`0cbea6ef8f2e22e2591acdf4df2fd5af375f24ef73a5a7c170fdebc40f05c112`
- 补丁摘要：`b360e747c88d3b136014faf7196678851ab5d68d13f0898957fb1f67b7112447`

检查结束后未发现遗留的 Harness 执行容器；实验 JSON、日志和补丁未检出本次 API Key。

## 证据位置

均相对于 `runs/rpcx-real-nnupis6i/`：

- `experiment.json`、`console.log`：调用条件、原始退出与耗时。
- `tasks/rpcx-agent-2368039229/run.json`、`tasks/rpcx-agent-2368039229/artifacts/report.json`：原 Run 的失败状态，无最终质量判定。
- `tasks/rpcx-agent-2368039229/artifacts/agent.json`、`tasks/rpcx-agent-2368039229/artifacts/candidate.patch`：完整轨迹、逐次用量及实际补丁。
- `diagnostic/report.json`、`diagnostic/evidence/`：事后独立诊断及命令日志。
- `analysis.json`、`patch-replay/`：统计、人工差异检查结论与补丁还原。

## 结论与后续

本次真实模型生成了通过事后门禁的补丁，但未在规定调用预算内提交，按既定规则不计为自动迁移成功，M1 仍未完成端到端验收。

下一项改进应聚焦预算可见性和及时提交：让模型知道剩余调用数，在预算接近耗尽时优先验证和提交已有改动，避免继续扩大改动范围。改进后用新 Run 复测，保留本次失败结果。提高预算可以作为单独的对照条件，不与提示或工具改动混在同一轮比较，也不从单次结果推算总体成功率。
