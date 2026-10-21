# Campaign 写者排他

新建 Campaign 在发布计划前调用 `Create(root)`，返回的 `Lease` 持有固定 `.writer.lock` 上的非阻塞排他锁，直到执行、预算结算和终态发布全部结束。`Open(root)` 只打开已有锁，绝不为旧目录补锁；`ErrBusy` 表示其他协作写者仍持锁。

锁文件保持为空且不删除、不替换。实现拒绝符号链接、硬链接、特殊文件和锁路径身份变化；文件描述符不通过 exec 继承。`Close` 幂等，退出或 SIGKILL 会由内核释放锁，文件本身保留。没有 PID 探活、TTL 或强制抢锁。

首版支持 macOS/Linux 的可信本地文件系统；其他平台返回 `ErrUnsupported`。这是协作排他协议，不能约束恶意改文件、跨机器操作、复制目录后的独立消费或遗留执行资源。取得锁不证明 Go 命令、容器或供应商请求已经停止。

`go test -race ./internal/writer` 会启动自己的辅助进程，验证双进程争用、SIGSTOP、SIGKILL、正常释放和描述符不继承；只向测试所创建的进程发送信号。设计及结果见 [M4.2a 工作记录](../../docs/worklog/2026-10-20-m4-recovery.md#10-m42a写者排他与只读恢复检查)。
