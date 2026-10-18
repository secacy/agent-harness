# T3 离线验收工具

本包运行冻结 T3 数据的离线验收，命令入口为 `./scripts/verify-rpcx-t3.sh`；数据默认位于 `scenarios/rpcx/t3`。它比较实际观察与四项公开预期，不输出运行时质量 verdict。

数据范围与清单校验使用 `t3data`，包含 fixtures/specs/cases/oracle 及 freeze.json。根说明不参与数据身份，意外根文件会被拒绝；工具源码置于本目录，变更后不改变数据版本。

schema_version=2 的报告保存 `data_version`、`inputs_sha256`、逐文件输入摘要、`tool_sha256` 和代码版本。`inputs/_snapshot/` 保留实际数据，`materials/*.txt` 保留本包五份实现源码和共用 t3data/data.go；工具摘要为这些原始源码的路径→SHA-256 映射按 Go JSON 编码后的 SHA-256。实现测试与执行后端源码不在该局部工具摘要中。

默认每条检查在禁网 Docker 中执行，显式 `--trusted-local` 仅用于受控夹具。正式验收要求冻结匹配、原测试与参考结果完整、真实到期检查通过、输入不变和无执行错误。开发/quick 模式永不 accepted。

历史 schema=1 报告和混合冻结场景见 [_archive](../_archive/README.md)，长期选择见 [ADR-005](../../../docs/adr/005-versioned-evaluation-data.md)。
