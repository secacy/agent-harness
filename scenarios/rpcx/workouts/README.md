# Workouts 取消切片迁移门禁

本场景使用冻结的 [Workouts v1 基线](../l2/data/rpcx-l2-workouts-v1/README.md)和独立发布的 [v0.5.0 规范](specs/v0.5.0.md)。`trainings` 是可编辑业务模块；`common`、rpcx-v1/v2 是只读本地依赖。宿主 Profile 固定输入摘要、保护路径、测试身份和默认构建配置。

`Evaluate` 接受单个候选及宿主执行器，保存规范、文件/工具摘要、原测试结构、common 与 trainings 的构建/测试、两次 RPC 的类型对应和可信行为探针。回调探针特意将仓储传入的 ctx 与 Handle ctx 区分，并观察每次调用的 30 秒期限、较短父期限、取消和释放。业务探针覆盖条件余额、调用顺序、错误链、成功提交及 trainer 失败后已有的远端余额效果。结论为 `ReadyForReview`、`Rejected` 或 `NeedsAttention`；认证或设施错误没有质量判定。

校准材料位于 `cases/`，包含九份候选补丁及其阶段预期。它们只供开发验收使用，不进入 Agent 可见目录或运行门禁输入。等价 helper 超出有限结构证明时保留 `NeedsAttention`。这套切片不证明 Firestore 事务、远端回滚、Schedule/Move 或整仓兼容。

生产结构指纹允许同一 import 块内换序及空行分组变化，保留路径、别名、重复项与导入块边界；正文和语义指令检查不变。这只消除导入排序差异，不能替代上下文、期限、测试和业务证据。实现及新旧验证器的开发校准见[导入顺序工作记录](../../../docs/worklog/2026-10-20-workouts-import-order.md)。

快速机制测试：`go test ./scenarios/rpcx/workouts ./scenarios/rpcx/runner`。完整候选验收显式设置 `HARNESS_WORKOUTS_ACCEPTANCE=1` 后运行 `go test -timeout=20m ./scenarios/rpcx/workouts -run '^TestWorkoutsAcceptance$'`；`HARNESS_WORKOUTS_CASES` 可指定逗号分隔的候选名，`HARNESS_WORKOUTS_BACKEND=docker` 使用固定禁网 Docker 执行器。Runner 交付验收用同一开关运行 `go test -timeout=15m ./scenarios/rpcx/runner -run '^TestRunWorkoutsDelivery$'`。实际取舍与验收结果见[工作记录](../../../docs/worklog/2026-10-19-workouts-evidence-gate.md)。
