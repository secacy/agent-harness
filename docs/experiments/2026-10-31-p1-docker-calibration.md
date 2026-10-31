# P1 已知候选 Docker 校准

2026-10-31，Asia/Taipei。类型为 `development_calibration`，正式生成次数 **0**，供应商生成请求 **0**，人工复核未进行。这份报告验证三组策略及独立参考的真实执行链路，不报告模型自主迁移成功率。

## 执行前固定

选择 Goddd 已发布的 `correct`、`missing-timeout`、`mapping-helper` 三份补丁，来源、预期与补丁摘要来自 [case manifest](../../scenarios/rpcx/goddd/cases/manifest.json) 及其 freeze。每个候选分别运行 A/B/C，共九次；C 的预期分别为 ReadyForReview、Rejected、NeedsAttention。A/B 预期正常提交即放行全部三种候选。

独占产物根为 `runs/p1-docker-calibration-2026-10-31/`。`calibration-plan.json` 在第一个候选命令运行前保存全部槽位、预期、补丁/候选/脚本摘要、846 份来源摘要、实际镜像/engine/资源与阶段时限。过程没有根据结果改预期或增加重跑；最终来源复核一致。

模型是进程内已知答案脚本，先通过真实 `replace_text` 工具逐件修改，再调用实际 `go_check` 构建测试，最后提交。它不直接写模型工作区。三个模型轮次的 usage 为合成零值，**不是真实供应商用量、免费模型结论或可用于效果/费用统计的数据**。三组同字节已知候选用于机制校准，不能充当主实验独立生成。

Docker 使用固定 Go 镜像 `sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd`，2 CPU、2 GiB、128 PID。准备、生成、门禁、参考时限分别为 2、4、4、5 分钟；这是本轮校准条件，与正式协议分别冻结。入口为 [显式测试](../../scenarios/rpcx/runner/p1_calibration_test.go)，普通测试默认跳过。

```sh
HARNESS_P1_CALIBRATION_OUTPUT=/absolute/path/to/new-output \
go test ./scenarios/rpcx/runner -run '^TestP1DockerCalibration$' -count=1 -timeout 50m -v
```

## 实际结果

| 已知候选 | A | B | C | C 的独立自动参考 | 未填人工复核后的候选真值 |
| --- | --- | --- | --- | --- | --- |
| correct | Completed / 放行 | Completed / 放行 | ReadyForReview / 放行 | pass | inconclusive |
| missing-timeout | Completed / 放行 | Completed / 放行 | Rejected / 不放行 | fail | fail |
| mapping-helper | Completed / 放行 | Completed / 放行 | NeedsAttention / 不放行 | pass | inconclusive |

九次均正常提交和交付，候选摘要与预制补丁逐项一致，实验回放、控制流和共享账本核对全部通过。A/B 没有独立门禁阶段，验证了错误候选不会被隐藏的提交后构建条件挡住。C 不放行 Rejected 或 NeedsAttention。独立参考分别在三份 C 固定候选上执行；A/B 的同字节已知答案不重复执行参考，本轮不存在正式效果分母。

准备阶段 17.26–18.42 秒，生成（包含真实构建测试）17.76–19.20 秒，收尾 0.078–0.100 秒。C 门禁：correct 112.97 秒、missing-timeout 73.89 秒、mapping-helper 111.47 秒。整个测试命令通过，包耗时 **812.357 秒**；子测试显示的短耗时只是断言时间，不能代替真实阶段耗时。

85 条登记命令均有回收记录；完成后对该 registry 标签的 `docker ps --all` 实时查询返回空集合，退出码 0。没有按预期标签清理其他容器。输入/来源复核及账本没有遗留故障。

## 材料与限制

原始日志：`runs/p1-docker-calibration-2026-10-31.log`。产物包含 `calibration-marker.json`、`calibration-plan.json`、`calibration-results.json`、九个实验收据/trace/候选/补丁、三份独立参考、预算账本和资源记录。`runs/` 不随 Git 提供；复验须用新目录，不能覆盖本轮产物。

该校准只覆盖一个已见仓库和有限三种已知候选，证明本轮实际策略与记录链路能协作，不证明四仓正式时限充分、不证明系统有效性、未见泛化、人工真值或 P4 越权边界。正式实验仍要求四仓逐一人工复核、明确费用授权和源码/二进制冻结；候选正确性与说明准确性继续分列，未填复核不能当作通过。
