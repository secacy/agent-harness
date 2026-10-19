# Workouts 参考复核清单

- test_preservation（TEST-001）：逐文件核对 trainings 原测试名称、断言、表数据和 helper 的差异，只允许必要 API/导入适配；核对 common 三项实际测试事件。通过状态不能证明断言完整。
- change_coverage（API/CTX/TIME/BEHAVIOR）：核对 Handle→仓储→回调的 ctx 来源、余额为零的分支、两客户端映射/各自期限与释放。确认没有以 Handle 共用期限替代逐调用期限，没有引入重试、补偿或虚构事务回滚；review helper/WithDeadline 等行为等价改写的覆盖与缺口。原领域行为和两种 decorator 均需审查。
- delivery_evidence（BUILD/SCOPE/PATH）：核对实际 Run 的 trainings/common/SDK/规范身份、default 配置、两个客户端的证据及未证明范围；没有真实交付材料保持未知。

三项默认 inconclusive；实际复核者填写身份、RFC3339 时间、理由与文件/证据定位。助手源码校准须标 development_calibration，不能冒称独立人工复核或真实生成效果。没有活动计时则审核成本未知。
