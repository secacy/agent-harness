# 仓库命令执行

Executor 同步执行宿主给定的命令，分别保留 stdout/stderr、退出码和设施错误。Local 仅面向明确可信的代码；Docker 默认禁止联网、源码挂载只读，并限制 CPU、内存、PIDs 和输出。镜像必须预先存在，不隐式拉取。Limited 在底层退出和回收返回后释放共享槽位。

## Docker 资源归属

ResourcePolicy 固定 docker-intent-v1、显式 context 与 engine ID。Campaign 发布计划后先 CreateResourceRegistry；Runner 创建真实 Run 记录后才通过 WithResourceOwner 绑定执行器，覆盖命令请求中的 Owner。所有基线、Agent go_check 和独立门禁都沿同一后端保存记录，控制目录不进入源码挂载。

```text
resources/
  registry.json
  <command-id>/
    intent.json       # 归属、独占名称、镜像/挂载、参数摘要
    dispatch.json     # 下一步可能发出唯一一次 create
    created.json      # 创建完成见证：成功响应或核验后的 inspect
    removed.json      # 确认缺失后的移除见证
```

每条记录为规范 JSON，发布前同步文件，rename 后同步目录，已发布内容不覆盖。Decoder 拒绝重复键、未知字段、隐式缺字段及原地重排。ReadResources 只加载/校验数据，不推断运行资源静止。

Docker.ReconcileResources(cleanup=false) 实时只读核对；cleanup=true 仅供持原 Campaign 锁且完成静态审计的宿主使用。它验证 engine、完整容器身份、标签、镜像和挂载，先存创建见证，再按完整 ID 删除，最后确认不存在。原名称被重用、容器被改名、镜像/挂载/标签不匹配、ID 冲突或未登记容器均阻塞；不会按公共标签批量删除。其他注册表的资源不操作。

dispatch 存在但没有 created 且查不到时保持未知，不能当作零资源；以后发现并认证迟到创建才可回收。daemon 不可用、删除/记录发布失败也不放行。ResourceGuard 在回收结果未知时关闭本代后续命令准入，避免释放槽位后继续堆积容器；在途命令仍各自收尾，没有自动解除或重试。

当前新 batch 的 Docker 后端自动发布资源协议；单 Run 和旧无协议输入仍保留原运行方式，不因此获得恢复能力。资源清理与恢复组装见 [recovery](../recovery/README.md#docker-资源核对与清理)；协议取舍见 [ADR-010](../../docs/adr/010-docker-resource-ownership.md)。Local 后端没有对应的通用中断资源证明。

受控 CLI 替身使用本机 Python 3，不需要下载模块。故障测试：`go test -race ./internal/execution -run 'TestOwned|TestResourceOwner|TestResourceGuard'`。真实 Docker 强杀/清理/新 Run 验收见[命令说明](../../docs/command.md#docker-资源清理验收)。模型均为受控替身，不需要密钥。
