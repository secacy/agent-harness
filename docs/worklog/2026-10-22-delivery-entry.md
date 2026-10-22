# 项目入口与离线演示收敛

- 日期：2026-10-22。
- 状态：已验收；范围为文档入口及已有离线命令核对。
- 代码基线：b61eafb。
- 目标：新读者从 README 能理解系统职责、实际结果、运行入口和剩余边界。

## 范围与取舍

当前已有完整工程链路、四任务真实评测归档及 GHCLI 误拒修正，但根 README 只有文档链接；DELIVERY 的主体仍围绕旧三仓实验，下一步包含已完成事项。此项工作只整理当前入口和验证已有命令，不重做历史材料、协议或交付包，不调用模型。

演示可新建 demo CLI，也可复用已有场景检查和交付包 verify。前者需要增加一套参数、产物和测试，还容易模糊确定性脚本与自主生成的区别；现有入口已经能在源码中展示固定候选门禁、在归档中审计真实批次并精确重算指标。因此采用已有入口，增加阅读顺序与预期结果，不新增执行机制。

源码检查仅证明固定候选的门禁机制；归档复验核对既有批次的记录与指标，不重新执行 Agent、候选或独立参考。归档使用冻结的旧二进制，保留 GHCLI 原 Rejected；当前代码的新分类修正另列。两条路径均不读取 .env 或请求模型。

## 实施与验收计划

1. 更新 README：项目问题、运行架构、核心约束、已观测结果、源码快速检查及演示入口。
2. 新增 DEMO，重写 DELIVERY 的当前状态，修正 PROJECT 中过时的推进描述；历史证据与严格验收缺口保留，文档索引同步。
3. 实际执行已有源码检查和包内离线复验，核对预期判定、原指标、归档完整性、文档链接和 Git 差异。

已有完整门禁与消费端测试刚在前一工作单元通过。本轮只改文档，验证实际引用的命令和数据，不另建镜像实现的测试或重复整套 Go 回归。验证结果完成后补录；一个工作单元合并提交。

## 实际实施

根 README 增加问题背景、运行架构、预算/故障/证据约束、执行与质量状态区别、四任务原结果及代码导航。DEMO 给出归档摘要、解包、审计/重算的已有命令，列出预期字段，并用 Workouts 放行与 GHCLI 误拒展示证据阅读顺序；只有源码时的检查单独说明范围。

DELIVERY 改为以最新四任务评测为主，区分当前源码、固定评测包、旧工程包及 M3 索引；原三仓实验压缩为历史事实和链接。PROJECT、MILESTONES、文档索引及 command 同步当前状态，保留原人员/严格留出缺口，没有把模块通过改成全部阶段验收。核对入口代码后明确：CLI 提供单任务、batch 和 audit，恢复由 Go API 提供。

没有修改生产代码、冻结输入/规范/协议、历史报告、交付包或收据；没有构建新的发布包。

## 验证结果

环境：macOS arm64，Go 1.26.5，Python 3.12.14。两条主入口均完成，退出 0：

| 验证 | 实际结果 | 本地证据 |
| --- | --- | --- |
| `./scripts/verify-rpcx-t1-t2.sh` | 五例全部 expected=true；correct 为 ReadyForReview，其余四例 Rejected；原夹具不变 | `runs/delivery-entry-source-demo.log`；`runs/rpcx-t1-t2-1356198314/report.json` |
| 包内 `package_delivery.py verify` | 完整性 pass，原批次审计 pass/completed；精确重算原 counts/metrics/cost/targets，包未变 | `runs/delivery-entry-offline-01.log`、`runs/delivery-entry-offline-01/verification.json` |
| 包内 `verify --integrity-only` | integrity=pass、execution_checked=false；不算执行复验通过 | `runs/delivery-entry-integrity-only.log` |
| 包外 SHA-256 核对 | 归档摘要与原 `.sha256` 一致，打印 OK | `runs/delivery-entry-archive-sha.log` |
| 文档与演示材料路径 | 264 个本地链接目标、15 处归档材料路径和代码围栏通过；原计数/预算与指南一致 | `runs/delivery-entry-checks.json` |
| Git 差异边界 | 仅八份 Markdown；git diff --check 通过 | 本轮 Git 差异 |

本轮复验直接使用已解包的原发布目录和包内旧工具，输出到新的包外目录；没有重复解包或再做一次隔离环境验收。归档在新目录、禁网并禁止回读原工作区的验证已有[原收据](../delivery/fixed-evaluation-v0.1.0.json)，本轮没有将该历史验证冒称为新增测试。包核对前后不变，原有效自动成功仍为 1/4、错误放行 0/1，120 次准入、1,885,967 已知 Token，未知/在途为 0。

首轮临时材料路径检查误将 Workouts 假定为与 GHCLI 相同的 `evidence/typed-analysis.json` 文件名；指南实际只在 GHCLI 引用该文件。检查器修正为核对指南实际引用的 15 处材料后通过，诊断保留在 `runs/delivery-entry-checks-first.json`，不是运行时或演示命令失败。

## 结论

新读者可从首页了解职责与实际结果，按 DEMO 的既有命令复验，再从补丁和检查走到独立参考/复核。当前实现与历史实验的差异可见，原严格验收缺口保持。

本轮没有模型调用，没有重跑四任务候选或其独立参考，也没有新增效果指标；源码门禁检查实际构建并测试了五个固定补丁。下一项能力工作可再具体设计有边界的 GHCLI 别名/资源释放证明，不为文档整理扩展运行机制。
