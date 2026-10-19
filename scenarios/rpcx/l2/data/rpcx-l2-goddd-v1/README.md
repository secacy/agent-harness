# rpcx-l2-goddd-v1：迁移前离线开发基线

本发布来自 `marcusolsson/goddd@7c414addc7a127df0b4ea38653be6a7c6380a37a`，保留 MIT 许可，是已见开发样本 l2-goddd-booking。它包含人工准备的 v1 基线，不含迁移答案、候选质量预期或独立 oracle。

- `fixtures/goddd`：原根领域、booking、装饰器、inmem/mock 和裁剪后的 HTTP booking；出站路线查询替换为本地 RPC。
- `fixtures/rpcx-v1`、`fixtures/rpcx-v2`：共用 L2 SDK 契约 `rpcx-l2-sdk-v1`。后续样本复用同一对源材料及身份，不另设业务专用 SDK。v1 Call(method, request, response) 创建 30 秒上下文；v2 Dial/Call(ctx, method, request, response) 使用调用者 ctx，不隐式加超时。响应为后端填充的类型化指针。
- `dependencies/modules.tar.gz`：7 个固定版本的完整模块内容和离线所需缓存元数据；516 个文件，展开 4,783,818 字节。每个模块的校验和及许可路径见 modules.json，kr/logfmt 的许可位于 Readme。Go-kit 原模块包含 metrics/debug.test，按原模块保留；命令仅构建所需包和执行夹具测试，不运行该文件。
- `provenance.json`、`provenance/`：逐文件来源、排除理由、植入差异、改动前原件及 Goddd 适配补丁。15 个原测试来自三个原样保留的测试文件，断言未改变；新增测试和 SDK 自测另列。
- `tests.json`：每个模块的必需包、原测试、新增测试/子测试；无测试的 inmem/mock 包显式列出。预期全部执行通过，不接受删除、跳过或缺失事件。
- `freeze.json`：本根目录全部普通文件的精确 SHA-256 清单（不包含清单自身）；提交后数据更新另发版本。验收工具不属于数据身份。

保留的请求链是 HTTP requestRoutes → booking Service → 日志/指标装饰器 → RoutingService → rpcx-v1。请求 ctx 尚未沿链路传递，30 秒来自植入 SDK。路线字段、远端失败返回空路线、未知货物不发 RPC 等规则保留；原 HTTP 客户端、Hystrix、其他业务服务、Prometheus、UI 和部署不在覆盖范围。服务原本不返回远端 error，此处不改成新业务 API。

复验从项目根运行：

```sh
env GOPROXY=off GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOCACHE="$PWD/runs/go-cache" go run ./scenarios/rpcx/l2/cmd/verify-baseline
```

默认使用已有固定 Docker 镜像，每条命令新容器、断网、只读挂载、空构建缓存。显式加 `-trusted-local` 可检查可信本地夹具，构建缓存同样按命令新建；本地模式通过关闭 Go 模块下载验证依赖闭包，网络隔离证明来自 Docker。验收会重新装配工作区和完整模块材料，保存版本/构建/测试日志、工具材料、输入摘要及报告，核对 37 项测试及子测试（Goddd 22、v1 5、v2 10）和各包终态。失败保留已有诊断，输出始终创建在新的 runs/rpcx-l2-baseline-* 下。

accepted 仅表示这份冻结基线在记录环境下通过复验，不代表已有迁移 Profile、运行时质量门禁适配、Agent 迁移效果或整个 M3.4 完成。新规范和候选对照仍需后续准备。
