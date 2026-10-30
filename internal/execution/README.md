# 执行与资源归属

`Executor.Run` 同步等待命令退出和回收；`Limited` 到底层返回后才释放命令槽。Local 仅用于可信代码。Docker 使用预备镜像，默认断网、只读挂载并限制 CPU、内存、PIDs 和输出；有界 tmpfs 不等于宿主磁盘总配额。

## Docker 资源归属

Campaign 先登记绑定 context/engine 的资源协议；Runner 分配真实 Run 后再用 `WithResourceOwner` 绑定后端，覆盖请求中的 Owner。业务代码不能自行指定资源归属。

资源记录按以下顺序发布，已发布内容不覆盖：

```text
registry → intent → dispatch → created → removed
```

- intent 固定命令及挂载身份；dispatch 表示可能已发出 create；created 记录已核实的容器；removed 记录确认不存在。
- `ReadResources` 只验证磁盘记录。`ReconcileResources(cleanup=false)` 才实时核对；清理模式要求调用方持原 Campaign 写锁并完成静态审计。
- 清理只接受已登记且 engine、容器、镜像、挂载和 Run 身份完全匹配的资源，再按完整容器 ID 删除。
- 有 dispatch 却无 created、且实时查不到容器时仍为未知，不能推断没有迟到创建。未知归属、daemon 不可用或回收不确定均阻塞后续恢复。
- `ResourceGuard` 在回收结果未知后停止本代新命令准入；在途命令继续收尾，不自动解除。

具体格式与操作约束见 [resources.go](resources.go)、[docker_resources.go](docker_resources.go)；持锁清理流程见 [recovery](../recovery/README.md#docker-资源核对与清理)，设计依据见 [ADR-010](../../docs/adr/010-docker-resource-ownership.md)。
