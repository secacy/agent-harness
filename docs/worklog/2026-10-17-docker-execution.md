# Docker 执行后端

- 日期：2026-10-17
- 状态：共享执行层、本地回归和真实容器集成验收通过
- 对应里程碑：[M1](../MILESTONES.md)
- 决策：[ADR-001](../adr/001-docker-execution.md)

## 调用与所有权

```text
cmd/harness 创建 Docker 后端并注入 Runner
    ├─ repotools.go_check → execution.Executor.Run
    └─ checks.Evaluate   → execution.Executor.Run
```

`Request` 包含命令、工作目录、只读目录、宿主缓存目录及输出上限；只有本地后端使用宿主缓存。`Result` 分开保存 stdout/stderr、退出码、容器 ID、OOM 和输出超限标记。后端返回普通非零退出时 error 为空；执行异常通过 error 返回。验证器先保存日志和元数据，再处理异常，确保资源不足不会被误认成质量通过。

`repotools.GoCheckConfig` 替代原 `LocalGoConfig`，配置 Backend 和明确的 ReadOnlyDirs，挂载目录必须属于工具已有的只读根。通用工具层不硬编码 rpcx 模块。Runner 配置本次候选和两个 SDK；规范只供文件工具读取，不进入检查容器。

原验证器的本地命令也转到共享 Local 后端，统一环境白名单、输出硬上限和普通进程组回收。macOS/Linux 的进程组实现已移入 execution；其他平台保留明确报错的本地后端。Docker 负责自己的容器生命周期。

## 容器生命周期与限制

1. NewDocker 只检查本地镜像，记录具体 sha256 镜像 ID，拒绝非 Linux 镜像及声明匿名卷的镜像。
2. 每次命令使用唯一名称创建容器，禁止隐式拉取。只读挂载当前业务模块和相邻 SDK，挂载内拒绝符号链接与特殊文件。
3. 使用 `/usr/bin/env -i` 启动固定命令，Go 依赖代理、校验服务、工作区、自动工具链和 CGO 均关闭。
4. 默认 2 CPU、2 GiB 内存、128 进程，内存与 swap 总额相同。根文件系统只读，禁网，UID/GID 为 65534，禁提权并去除 capabilities。
5. `/tmp` 为 1 GiB 可执行 tmpfs，保存 Go 缓存、测试可执行文件、构建输出和临时文件。不同命令没有共享可写状态；宿主日志由 Harness 保存。
6. 捕获 stdout/stderr，各保留至多 4 MiB；超限立即取消等待并强制删除容器。模型可见输出仍有 repotools 的较小展示上限，两者分别用于限制执行资源和控制上下文长度。
7. 正常退出后检查容器 State，记录退出码和 OOM。退出 126/127 视为命令不可用的执行错误。取消、超时或输出超限时不根据部分输出推断成功。
8. 无论哪条路径退出，都用独立的至多 10 秒 context 强制删除命名容器。删除失败会让运行失败；不自动降级到本地执行。

业务源码不会在容器内生成或修改。Agent 仍通过宿主的精确替换工具编辑文件；最终验证探针由宿主注入 evidence 副本，再将副本只读挂载。候选固定、输入摘要、补丁导出与发布顺序保持原 Runner 契约。

## 验收条件与证据

环境为 macOS arm64、Go 1.26.5、本机 OrbStack 的 Linux Docker 29.8.2。使用提前拉取的 `golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd`。镜像下载属于准备阶段，测试命令运行时禁止下载。

| 验收 | 证据 |
| --- | --- |
| 普通退出码及 stdout/stderr 独立保存，凭证不继承 | TestLocalPreservesStreamsExitAndControlledEnvironment、TestDockerIsolationAndOrdinaryFailure |
| 源码与根文件系统只读，未挂载宿主文件不可见、禁网、非 root、禁提权、无 capabilities、cgroup 资源限制及临时状态不跨命令保留 | TestDockerIsolationAndOrdinaryFailure |
| 取消、超时、输出超限、OOM 返回错误且容器不再存在 | TestDockerCancellationOutputLimitOOMAndStartupFailure |
| 缺失镜像不隐式拉取；不允许隐式挂载或源码符号链接 | TestDockerMissingImageDoesNotPull、TestDockerConfigurationHasRestrictedMountsAndResources |
| 回收失败不能返回成功 | TestDockerCleanupFailureCannotReturnSuccess（CLI 替身注入删除故障） |
| 真实工具迁移、容器构建、独立验证、补丁还原和历史保留 | TestRunDockerDeliversVerifiedCandidateAndKeepsHistory |
| 命令默认使用 Docker，接通模拟 API 与最终验收 | TestCommandRunsDockerByDefault |
| OOM/输出超限保留命令证据并清空质量判定 | TestEvaluateExecutionLimitsNeverPublishQualityVerdict（注入执行错误） |

首轮真实容器检查已生效，但测试断言预期的 Docker “对象不存在”错误首字母大小写与本机 CLI 不同；已统一按不区分大小写判断，再次验证对象确实删除。全套测试并行进行冷编译时，已有的本地进程组测试曾超过两秒就绪等待；单独重跑通过，已改用 shell 内建路径处理、明确的提前退出诊断和五秒就绪预算，保留原取消与后代不存活断言。

最终 `go test ./...` 与 Docker 集成都通过，五种公开补丁回归仍全部符合预期，原始夹具未变。常规测试与真实 Docker 集成命令见 [command.md](../command.md)。本次真实集成的 JSON 事件保存在 Git 忽略的 `runs/docker-execution-tests.jsonl`，它是测试证据，不是业务迁移报告。

## 当前限制与下一步

未调用真实 DeepSeek 服务，迁移测试使用预设模型，CLI 使用本地模拟 HTTP 服务；不据此统计自主迁移成功率。尚未实现镜像内缓存预热、长期缓存复用、引擎故障后的遗留扫描或宿主崩溃恢复。普通容器依赖可信引擎与镜像，未提供独立内核或虚拟机安全边界。

下一步是在固定规范、模型参数、镜像与调用预算的条件下运行真实模型迁移实验，保存原始失败结果及产物；随后再依据瓶颈推进批量调度和独立评测。
