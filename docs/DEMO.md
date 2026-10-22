# 离线演示：从批次结果走到判定依据

本指南复验一次已有的真实模型批次，展示任务状态、预算、候选、质量依据和独立评测如何关联。归档复验不调用模型，不执行候选或重跑参考探针，也不需要 Docker。末尾另列源码固定候选检查的执行范围。

归档固定在 GHCLI 分类修正之前，原判定为 Rejected。当前源码的[修正回归](worklog/2026-10-22-ghcli-proof-classification.md)是另一份证据，不在本归档中；演示结束仍应重算得到原有效自动成功 1/4。

## 准备材料

需要另行取得这两个文件，Git 克隆不包含运行产物：

- `harness-fixed-evaluation-v0.1.0.tar.gz`，约 721 MB。
- `harness-fixed-evaluation-v0.1.0.tar.gz.sha256`。

版本与 SHA-256 见[已提交交付收据](delivery/fixed-evaluation-v0.1.0.json)。完整审计/重算使用包内 darwin/arm64 二进制，适用于 macOS Apple Silicon；需要 Python 3.11+ 和约 1.1 GB 解包空间，另预留复验输出空间。其他平台可用下文的完整性检查，不能据此声称执行复验通过。

## 1. 核对并解包

在归档所在目录执行，`demo-unpacked` 须为新目录：

```sh
shasum -a 256 -c harness-fixed-evaluation-v0.1.0.tar.gz.sha256
mkdir demo-unpacked
tar -xzf harness-fixed-evaluation-v0.1.0.tar.gz -C demo-unpacked
cd demo-unpacked/harness-fixed-evaluation-v0.1.0
```

摘要检查预期打印 `harness-fixed-evaluation-v0.1.0.tar.gz: OK`。其身份为 `ce5876abc4ffd2b4a52bbf5f139482cbddb19795b41598510f3788c7d94041b3`。

## 2. 审计并重算原轮次

在上一步进入的包根目录执行：

```sh
PYTHONDONTWRITEBYTECODE=1 python3 source/scripts/package_delivery.py verify \
  --root . --output ../verification-01
```

输出目录必须在包外且不存在；再次演示时使用新的输出目录名。命令先核对全部成员、权限和摘要，然后调用包内 `harness audit` 和 `summarize`，比较原始计数、指标、成本及逐任务结果，最后确认包未变。不需要本机 Go 或原开发工作区。

在 `../verification-01/verification.json` 中应看到：

| 字段 | 预期 |
| --- | --- |
| `integrity` | `pass` |
| `audits[0].consistency` | `pass` |
| `audits[0].execution_state` | `completed` |
| `rounds[0].counts.targets` / `runs` | 4 / 4 |
| `rounds[0].counts.released` / `success` | 1 / 1 |
| `rounds[0].counts.needs_attention` | 2 |
| `rounds[0].counts.wrong_releases` / `unknown_releases` | 0 / 0 |
| `bundle_unchanged` | `true` |

重算的 `../verification-01/round-fixed-final/round.json` 保留成本：120 次准入、1,885,967 已知 Token、0 未知与在途占用。错误放行分母为 1，有效自动成功分母为 4，不能只看一个百分比。

其他平台仅核对完整性：

```sh
PYTHONDONTWRITEBYTECODE=1 python3 source/scripts/package_delivery.py verify \
  --root . --integrity-only
```

该模式预期 `integrity=pass`、`execution_checked=false`，不运行二进制、不产生审计/重算结果。

## 3. 阅读一个可进入审核的结果

以下材料路径均相对包内 `source/runs/l2-fixed-eval-v0.1.0/`：

1. 打开 `campaign/summary.md`：4 个 Target 均 Completed，Workouts 为 ReadyForReview。执行完成与质量放行是不同字段。
2. 进入 `campaign/targets/workouts/runs/rpcx-agent-1147620506/artifacts/`，先读 `candidate.patch`，再读 `review.md` 的规范条款、检查结论和证据链接；在 `report.json` 的 `validation.checks` 查看机器可读结果。
3. 阅读 `references/workouts/reference.json`，结合 `reviews/workouts.json` 的实际助手复核；`references/results.json` 另列原路径与摘要身份。只有原门禁放行、独立参考及复核均支持，才计入本轮有效自动成功。
4. 打开 `campaign/budget/budget.json`，查看逐请求的预留与结算记录；`round-final/round.json` 保存从账本核对的批次成本，不再累加一遍逐任务费用。

审核文档中的源码链接可在解包材料内导航；原命令及计划中的旧绝对路径保留历史出处，复验工具使用新映射，不要求旧路径存在。

## 4. 阅读一个暴露门禁缺陷的结果

打开 `campaign/targets/ghcli/runs/rpcx-agent-3621433355/artifacts/review.md` 及其 `report.json`：原门禁因为 `migration_completeness=fail` 给出 Rejected。

继续看该 Run 的 `evidence/typed-analysis.json`、`reviews/ghcli.json` 和 `reviews/ghcli-static-proof-review.json`。类型分析器没有识别局部父上下文变量，却把证明不足写成明确丢失父上下文；独立自动参考通过，助手因此将原交付依据记 fail。审计 pass 说明记录自洽，不能证明这一语义判定正确。

回到当前源码的[修正记录](worklog/2026-10-22-ghcli-proof-classification.md)：保存候选在新门禁下为 NeedsAttention，直接 Background 负例仍 Rejected。原实验和归档保留旧结果，展示“发现缺陷 → 新实现回归 → 保留原评测”的完整链路。

## 只有源码时

在仓库根目录运行：

```sh
./scripts/verify-rpcx-t1-t2.sh
```

需要 Go 与 Git，本轮使用 Go 1.26.5。脚本禁用模块网络，通过本地 SDK 运行五个固定补丁；预期 `correct` 为 ReadyForReview，其余四个为 Rejected，均 `expected=true`。终端打印新报告路径，报告包含各检查及证据。该路径验证 T1/T2 门禁机制，不展示真实模型自主迁移或四仓批量效果。

真实生成入口与配置见[命令参考](command.md#受预算约束的批次入口)。日常演示复用上述离线材料即可；新付费实验应另行固定配置与预算。
