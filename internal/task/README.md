# 固定的仓库任务输入

Target 是场景无关的输入描述：任务 ID、宿主 Profile ID、构建配置，以及业务仓库、只读依赖和规范的路径/摘要。本包不读取文件、不执行命令、不选择迁移规则，也不持有模型、预算账本或输出目录。

目录快照沿用当前验证器的算法：相对文件路径→文件 SHA-256 的 Go encoding/json 紧凑对象，再对该 JSON 求 SHA-256。规范是文件字节摘要。Validate 只检查 ID、绝对路径及摘要格式；实际路径边界、内容认证及 Profile 适用性由场景 Runner 检查。

rpcx 当前支持两个宿主 Profile，接入见 [Runner](../../scenarios/rpcx/runner/README.md)。同一份输入迁移到不同物理目录不改变内容身份；同一 Target 的不同执行尝试由新的 Run ID 表示。Campaign 列表中的 ID 唯一性、调度和重试仍由后续阶段实现。
