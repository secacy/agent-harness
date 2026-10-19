# Campaign：有界批次调度

`Run(ctx, Config)` 独占创建一个新 Campaign，并为每个 Target 调用一次宿主提供的 `Execute`。包不导入 rpcx，不操作候选、不判定业务质量；rpcx 使用 [CampaignExecutor](../../scenarios/rpcx/runner/campaign.go) 连接现有 Runner。

这是 Go API，目前没有 `harness batch` 命令。真实供应商的能力上界材料尚未认证，不能填入一个估计值后启用所谓严格预算。可运行的受控模型联调见 [campaign_test.go](../../scenarios/rpcx/runner/campaign_test.go)。

## 组装职责

宿主提供：

- `Plan`：唯一 ID、N 个 Target 槽位、K 个命令槽位、冻结的模型 Profile、批次额度、按顺序排列的 Target 输入与限制。`Timeout` 的 JSON 单位为纳秒。
- `Directory`：父目录已存在、自己尚未存在的输出目录。输入输出不得双向包含，符号链接别名也要检查；输入内容由对应 Runner 认证。
- `Backend`：通常为已准备好镜像的 Docker；明确选择 `execution.Local{}` 仅用于可信代码。
- `NewModel`：为每个 Target 创建独立模型状态，返回与 Plan 完全一致的 `budget.ProfiledModel`；工厂不得发起生成或修改输入。调用前完成全部模型配置检查，失败时不创建 Campaign。
- `Execute`：可信宿主适配器。它在 `Services.RunsDir` 创建唯一 Run，通过 `Services.Budget` 绑定模型，所有命令只使用 `Services.Backend`，发布报告及磁盘 Run 终态后才返回。它必须响应 context，返回前完成回收。

rpcx 的组装关系为：`Config.Execute = runner.CampaignExecutor(trustedLocal)`。它将模型及 Target 账本交给 Runner，在真实基线通过后以实际 Run ID 调用 `budget.NewModel`；Agent 仅通过 Loop 接触包装后的模型。适配器验证报告身份、输入/候选摘要、判定及预算引用；Campaign 再检查磁盘终态、所属目录、账本 Run 绑定和报告摘要。

这是宿主代码的协作接口，不是任意第三方插件沙箱。恶意宿主若另存裸客户端或绕开共享 Executor，本包不能限制它。Profile 的真实性仍由宿主认证，格式验证不等于供应商认证。

## 执行与返回

从准备到交付及回收均占 Target 槽位。一个共享 `execution.Limited` 在底层启动前取命令槽，底层返回（包括清理）后才释放；等槽可取消。不同 Target 的模型状态、工作区和可写缓存各自独立。

普通任务错误继续执行其余任务。余额不足不在启动前猜测：任务仍做基线，到模型请求处由账本等待或拒绝，因此它是已启动的 Failed，而非 Skipped。共享账本或调度记录写入故障才取消全批；父 context 取消也停止接纳。等待已启动任务回收后发布汇总，不 recover 宿主 panic，也不承诺强制终止不响应 context 的宿主代码。

`Run` 的 error 表示配置、全批取消或控制故障；**局部任务错误保存在 Summary**。`Record.Status == Completed` 表示所有任务已终结且汇总发布成功，允许其中有 Failed、Rejected 或 NeedsAttention。调用方若要以执行失败数决定退出码，应检查 `Summary.Counts`，不能仅检查 error。

`Attempt` 返回绝对 Run 目录及报告路径；Campaign 检查后转为相对路径。Completed 必须有合法质量判定、匹配的磁盘终态、报告摘要及账本绑定。失败不得附带质量结论；若尚未创建 Run，可返回空身份和非空错误。结果不匹配记本 Target `invalid_result`，不接受其质量结论，也不改写原 Run。

## 产物与局限

```text
<Directory>/
  plan.json
  campaign.json
  budget/budget.json
  targets/<id>/target.json
  targets/<id>/runs/<run>/
  summary.json
  summary.md
```

计划及控制文件采用临时写入、文件同步、rename、目录同步。发布顺序为 Run 终态 → Target 引用 → summary → Campaign 终态；Target/summary 发布失败不能回写已完成 Run。Run 自身的发布协议仍由 Runner 管理，不能据此声称所有 Run 文件都具备断电恢复保证。

summary 按计划顺序列结果，分开统计执行状态、质量判定与已知/预留/未知用量，并保存最终账本快照；不重算门禁，不输出缺乏独立复核的成功率。完整性失败可能发生在快照已经 rename 后，调用方必须同时看 `campaign.json` 终态与返回 error；留存文件本身不代表成功。

只创建新目录，不重开、覆盖、重试或恢复历史 Campaign。Running Target 在回调返回前不保证已带 Run ID，可从其独占 runs 子目录定位未完成尝试；中断识别、恢复与审计归 M4。N/K=1/2/3 的机制测试使用相同构造输入，不构成异构 L2 效果证明。

离线机制测试：

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" GOMODCACHE="$PWD/runs/module-cache" go test -race -p 1 ./internal/campaign ./internal/execution -run 'TestCampaign|TestLimited'
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" GOMODCACHE="$PWD/runs/module-cache" go test -p 1 ./scenarios/rpcx/runner -run '^TestCampaignRPCX'
```

设置 `HARNESS_CAMPAIGN_ARTIFACTS` 为 `runs/` 下的目录可保留联调 Campaign，便于阅读真实 summary 和逐 Target 报告。测试模型使用固定参考编辑，只用于机制验收，不能记为模型自主迁移效果。

设置 `HARNESS_DOCKER_TEST=1` 后可运行 `TestCampaignDockerKilledExecutionIsolation`：先取得单独运行参照，再 SIGKILL 精确匹配本次挂载源的自有容器，断言容器已清理、故障任务零模型调用，其余正确候选和检查结果与参照相同。它杀的是执行容器，不是 goroutine、独立 worker 或 Harness 进程。
