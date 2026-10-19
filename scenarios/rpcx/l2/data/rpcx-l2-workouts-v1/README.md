# rpcx-l2-workouts-v1

固定 ThreeDotsLabs/wild-workouts-go-ddd-example 提交 `8ecfcdf05b1462c4757bd2dcac9086c78e9f7791` 的取消训练开发切片，MIT 许可。来源与逐文件变更见 provenance.json，原审查件在 provenance/upstream，准备补丁在 provenance/adaptation.patch。选择清单不回写；本版本使用后不原地改写，freeze.json 身份由宿主静态注册认证。

## 保真与植入边界

保留原 CancelTraining.Handle 回调及条件逻辑、原四个取消子例、完整 training 领域和原测试、common 泛型 command/query 装饰器及所需 errors/logs/metrics 文件。trainings/common 仍是两个独立 Go 模块，通过原路径的本地 replace 连接；两个模块均保留 Go 1.25.0，不自动下载工具链。

TrainerService 仅保留取消操作；Schedule/Move、其他 command、HTTP/Firestore/gRPC 服务启动和云端组件测试明确排除。领域中附带的 reschedule 规则与原测试因完整包闭包而保留，其通过不代表对应远程业务已纳入迁移。原 source 英文注释按字节保留，新增代码注释用中文。

UsersGrpc.UpdateTrainingBalance 和 TrainerGrpc.CancelTraining 保留原类型/方法名称与用户、余额、训练时刻含义，实际传输替换为内存 rpcx-v1。新增构造器接收已注册本地后端地址，确认响应为空；不证明原网络/Protobuf 兼容。两个 SDK 与 rpcx-l2-sdk-v1 的字节相同。30 秒默认超时来自 SDK 植入；v1 不接收父 ctx，迁移后需显式恢复上下文和期限，不将其误称为原上游的语义。

## 测试与副作用

tests.json 分列原测试和新增测试。原取消 command 与领域测试不改断言。新增真实 RPC 路径检查四种余额规则、零余额分支、请求字段、RPC 顺序、错误链和默认期限/释放；回调失败不提交本地仓储副本，但教练失败前已成功的远端余额操作不会凭空回滚。内存测试仓储不代替真实 Firestore 事务语义。common 新增装饰器测试验证 ctx/错误不变及成功/失败计数。

本地模块和 SDK 各自构建、运行完整测试；common 还被 trainings 的原取消路径实际使用。dependencies/modules.json 标明实际分发完整源码的模块和仅含图元信息/许可的版本；归档不含候选答案、迁移补丁或 oracle。正式执行禁网、只读依赖、空构建缓存。

```sh
go run ./scenarios/rpcx/l2/cmd/verify-baseline -dataset rpcx-l2-workouts-v1
```

默认使用已准备固定 Docker 镜像；可信本地开发副本可加 -trusted-local。输出报告保存配置、命令、测试事件、输入/工具摘要和容器身份。accepted 只表示此冻结基线复验通过，不表示具备迁移 Profile/门禁、模型自动成功或独立参考评测。后续迁移支持与原始证据见[工作记录](../../../../../docs/worklog/2026-10-19-workouts-baseline.md)。
