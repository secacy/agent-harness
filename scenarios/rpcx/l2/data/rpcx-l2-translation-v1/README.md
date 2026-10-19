# rpcx-l2-translation-v1

开发样本 l2-clean-translation 的迁移前基线，来源为 [evrone/go-clean-template 固定 commit](https://github.com/evrone/go-clean-template/tree/d1f46523e9a8435713aabb70222c6189564d4501)（v1.18.0，MIT）。这是裁剪并植入本地 rpcx 的业务切片，不是原工程整仓或自然 SDK 升级；准备过程见[工作记录](../../../../../docs/worklog/2026-10-19-translation-baseline.md)。

- fixtures/translation 保留翻译 entity/usecase、两层 tracing、完整 repo/usecase contracts、生成 mock、原翻译与所需实体测试；用户/任务服务实现、数据库、消息系统、REST 和 exporter 启动不属于切片。
- fixtures/rpcx-v1、rpcx-v2 与 rpcx-l2-goddd-v1 中共用 SDK 逐字节一致。v1 隐式 30 秒、v2 显式 ctx，无网络传输。
- default 选择 translation_google.go，rpcx_alternate 选择 translation_rpcx_alternate.go；二者互斥，各有实际 RPC 调用点。第二实现和构建标签是植入条件，同源变体只计一个样本。
- 原翻译测试和 mocks_repo_test.go 保留原字节；生成 mock 实际参与测试。保留额外 Task/User 数据类型是为了维持该原 mock 的编译闭包，不表示纳入了对应完整服务。
- 新增适配器和 usecase→tracing→RPC→Store 路径测试验证成功、远端/存储失败、取消预检查、映射及执行顺序；它们是准备时的基线测试，不是迁移 oracle。v1 后端只获得 SDK 自己创建的 ctx，不宣称已经传播请求 ctx。
- provenance.json 逐文件说明来源/改动/排除，provenance/upstream/*.txt 为审查原件；adaptation.patch 可从原件重放业务基线。tests.json 固定每种配置均需执行的测试身份，freeze.json 固定整个发布目录。
- dependencies 分发两种配置实际编译/测试需要的完整模块源码；其余模块图只含缓存元信息和许可，modules.json 中 source_included=false 明确区分。不是任意工具/服务包均可编译的整仓缓存。保留 Go 1.26.0 与实际代码依赖的上游版本，验证不下载依赖。

本版本发布后不原地更改。使用已有固定 Docker 镜像复验两种配置：

```sh
go run ./scenarios/rpcx/l2/cmd/verify-baseline -dataset rpcx-l2-translation-v1
```

显式加 -trusted-local 可复跑可信本地夹具；材料、命令、配置和输入前后摘要保存在新的 runs/rpcx-l2-baseline-* 下。accepted 只表示冻结基线完整可复跑；新迁移规范、宿主 Profile、候选门禁与独立评测另行接入。
