# 历史候选门禁价值回顾

`Run(ctx, repositoryRoot, relativeManifest, newOutputDirectory)` 只读固定历史输入，输出 `report.json`、`summary.md` 和逐 Campaign 审计。不调用模型、Docker 或候选命令，不使用正式 round 的成功率分母。

每行保留原门禁检查与复核理由；构建/测试视图独立核对实际快照、原命令、配置与完整测试事件。缺失或身份冲突保留该行并标 insufficient。JSON 的 sources 列出消费文件摘要，结束时重新检查文件与候选/基线树；它们是身份记录，不是签名或完整语义证明。

成本按 build_test_evidence、additional_validation、unclassified 划分原报告命令。记录墙钟之和不等于完整验证、CPU、简化分支或人员活动耗时。未知命令保留未归类项。

旧 GHCLI v2 自动参考及复核保留原字节和身份；当前 v3 的 Assess 不适用，因此本次参考消费记 inconclusive，reference_gap 解释原因。变更覆盖/交付依据列展示原记录，不把原参考改成 v3 或假称重新完成旧版评估。其余受支持版本复用 evaluation.Assess；原 Campaign 复用 audit.Inspect/runner.InspectRun。

机制测试：

```sh
GOCACHE="$PWD/runs/go-cache" go test -race ./scenarios/rpcx/l2/gatevalue
```

从项目根目录执行完整历史回顾（须有固定清单引用的 runs 材料，输出目录须尚不存在）：

```sh
HARNESS_GATE_VALUE_OUTPUT="$PWD/runs/my-gate-value-study" \
GOCACHE="$PWD/runs/go-cache" \
go test -race ./scenarios/rpcx/l2/gatevalue -run '^TestHistoricalStudy$' -count=1
```

检查消费源副本负例（输出副本使用测试临时目录，原材料仅读取）：

```sh
HARNESS_GATE_VALUE_REPORT="$PWD/runs/my-gate-value-study/report.json" \
GOCACHE="$PWD/runs/go-cache" \
go test -race ./scenarios/rpcx/l2/gatevalue -run '^TestHistoricalMutations$' -count=1
```

设计与范围见[工作记录](../../../../docs/worklog/2026-10-24-gate-value-study.md)，实际观察见[回顾报告](../../../../docs/experiments/2026-10-24-gate-value-review.md)。这是已见历史证据投影，非当前版本的独立消融或泛化评测。
