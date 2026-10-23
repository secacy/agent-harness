# GHCLI：一次新生成开发观察的准备

- 日期：2026-10-23。
- 状态：本地材料固定与离线预检完成；随后获授权的一次启动已在能力身份核对时停止，零生成，见[实际结果](../experiments/2026-10-23-ghcli-dev-real-model-01.md)。下文保留准备时事实。
- 代码基线：7f6828c。
- 目标：形成具体、可核对的一次开发观察协议和执行材料，授权成为实际 API 请求前的最后一步。
- 协议：[ghcli-dev-eval-v0.1.0](../experiments/ghcli-dev-eval-v0.1.0/README.md)。

## 问题与取舍

新门禁已解释保存候选并通过完整 Harness 交付/审计，但脚本模型只能证明链路，不能说明新的模型轨迹会生成什么。现在的选择是继续扩展规则、重跑四仓，或仅观察发生变化的 GHCLI。第一项没有新反例或审核成本证据支撑；四仓新增外发、费用和随机性，而其余三仓规则没有变化。因此选单个 GHCLI、一次尝试的 development_evaluation，沿用原单任务限制，接受 N=1 只能提供有限观察。

本轮不是验证器效果的因果对照，也不是严格留出；旧候选、生成上下文和校准已被开发者看到，这个暴露事实必须固定。任何新结果不能改写旧四任务 1/4，更不自动成为扩大 helper 支持或追加实验的理由。沿用 schema 2 的通用单 Target 能力、batch 和 summarize，不新增另一套执行/预算/评测入口。

## 准备与验收

1. 固定已发布 GHCLI 输入/规范/Profile、参考发布元数据、当前实现源文件及导出二进制、只读实测的当前 Docker 环境。
2. 固定一个 Target、一次尝试、40 次生成/80 次工具/250 万 Token、Run 10 分钟及宿主停止规则，生成 schema 2 计划/纳入/四材料/freeze。模型能力只复用明确标时的历史材料作期望，不冒称本次已查询。
3. 用导出二进制执行不读密钥/不联网/不执行仓库的 --check；使用 Go 契约核对期望计划、四材料、一个 Target 及参数漂移拒绝。确认实际执行目录未创建，旧材料摘要不变，再取得本次外发与用量授权。

准备不运行候选、参考或新容器，不读取 .env。实际 API 查询和生成都是后续动作，本轮不把 --check 或期望计划绑定算成迁移验收。

## 实际结果

实际复用现有单 Target schema 2 契约；未修改生产 Go、Profile、规范、参考或原协议。新输入清单明确记录开发者已经检查旧候选、编写门禁并做过校准，不沿用旧数据中“父 Agent 仅见元数据”的历史说法。实际复核者固定为 codex:/root，三项复核判据保持，不预签结果。

当前 Git 基线 7f6828c 的 cmd/internal/scenarios/scripts/go.mod 共 779 份源码/场景/脚本材料已按原字节导出，排除 docs/runs/凭证/Git 元数据；当前工作区和导出副本逐件相同。整体材料身份为 f45197969e8c90f011ca3f8d4b632f44224563e6c95a6ae2e6937af1734c10ee。从导出副本在禁用模块网络条件下构建 harness 和 summarize，没有打新交付包。harness SHA-256 为 d5397060673f645405ef1b1b8b59da1d81e6cd5511dab4ad4582f42ba1b375dd，summarize 为 9a54037a169969ba7d7f6c9e746e5d34d7303cf186fe4e47b63d0d060068ba8e。

本机只读检查固定镜像、context/engine；Go 为 1.26.5 darwin/arm64。模型能力原文仍是 2026-10-20 的既有材料，明确标为历史期望；本轮没有带鉴权 GET，也没有查询生成接口。已有 C/O/Q 不能冒充当前服务保证，实际启动若发现身份变化就零生成停止。

新增本协议的 launch.py，复用旧实际启动方法：默认要求明确选择 --check 或 --execute 并提供 freeze 摘要；前者只做本地完整核对，后者在调用方取得本次授权后才创建独占启动记录、解析 .env Key、传递受限环境并调用固定二进制。12 分钟中断、最多 60 秒收尾；没有循环或重试。记录存在即拒绝重放，即使查询/凭证失败也不擅自追加尝试。Python 优化模式会禁用 assert，因此显式拒绝该模式，避免材料核对被跳过。

| 验证 | 实际结果 | 证据（runs/ghcli-dev-eval-v0.1.0-preparation/ 下） |
| --- | --- | --- |
| 源码导出与离线构建 | 779 份材料与原/副本一致，两份二进制成功构建；凭证、缓存、旧运行不入源材料 | source-identity.json、snapshot/、build-harness.log、build-summarize.log |
| 当前输入和只读 Docker 身份 | 应用、SDK、规范摘要符合发布 Profile；只读现存镜像/context/engine，未创建容器 | metadata.json、协议 environment.json |
| 期望计划和 schema 2 纳入 | 1 Target / 4 材料，离线 Bind 成功；额度漂移、第二次尝试和材料篡改均被拒绝 | expected-binding.json、bind.go |
| 固定二进制的 batch --check | 退出 0，1 Target，未查询模型 | check.log |
| 启动器实际 --check | valid=true、model_queried=false、credentials_read=false、batch_created=false，源码/副本/二进制/Go/镜像/引擎均匹配 | launch-preflight.json |
| 启动器局部契约 | 9 个对照匹配：普通/export Key、缺项/空值/重复/非法形式、shell 文本不执行、优化模式拒绝；只读临时假 Key，未读真实 .env | launcher-contract.json |
| 冻结与原材料 | 本版 12 份成员及 freeze 身份一致；旧 59,589 份登记材料保持摘要，未创建本轮执行/启动目录 | verification.json |

临时组装脚本初稿误用了旧 dataset 的集合字段名，并有一处 Python 字典括号错误，未能完成离线组装；修正后才生成最终纳入与 freeze。没有外部请求、候选执行或冻结历史修改；这些准备代码错误不被当作真实迁移失败。

本版 freeze.json SHA-256 为 291b2f7a1c1322e54f2c1d2fa59913b115d4632feb07a49e68d115aa1bd86727。--check、期望计划和构造拒绝对照只验证准备机制，不能算新一次模型/迁移验收。本轮没有运行候选、参考、完整质量门禁或重复 Go 大回归；这些路径已由前一单元实际验收。

待用户看到具体协议并明确授权后，才允许一次 Models GET 和最多 40 次生成，传输边界、250 万 Token/80 次工具、10 分钟 Run 与 12+1 分钟宿主停止、不追加额度/重试均固定。此前真实实验的许可为旧协议一次，不能覆盖本轮。真实执行、结果复核和归档将另记实际实验报告，不回写本版冻结材料或旧四任务结果。

文档收尾：6 份变更 Markdown 的 268 个本地链接目标、35 个锚点与围栏有效；启动器语法检查及 git diff --check 通过。新协议、启动器及当前进度记录作为同一准备工作提交；授权前不运行 execute。检查与身份汇总保存在 runs/ghcli-dev-eval-v0.1.0-preparation/verification.json，准备产物不入 Git。
