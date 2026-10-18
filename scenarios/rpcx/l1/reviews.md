# L1 v0.1.0 人工规则依据

这些构造任务的预期由补丁意图和 v0.2.0 条款确定，属于可审查开发参考，不是未见样本成绩。manifest 固定每项检查、主要原因码和条款；独立 oracle 另行产生行为证据，不导入门禁规则。参考通过只表示默认配置下的列明行为通过，不能证明测试完整性、SDK 不可改或所有配置正确。新的请求变体在此版本参考中加入，不回写旧 T3 或运行时探针。

## correct-timeout

两次调用分别从传入 ctx 派生 30 秒期限并 defer cancel，调用链传递请求 context，错误包装及订单更新保留。原测试只改必需 API/context。预期八项通过；参考期限、取消、错误、额外请求以及真实到期全部通过。

## missing-timeout

只完成 T1/T2，没有补每次 RPC 的 30 秒上限和调用后释放，违反 TIME-001、TIME-003。原测试仍可能通过。门禁行为失败，参考期限及资源检查独立失败。

## release-missing-timeout

Reserve 正确，Release 仍直接使用父 ctx，违反 TIME-001、TIME-003。两处位置对应不能代替逐处语义检查；预期拒绝，参考 Release 路径失败。

## wrong-duration

明确使用 3 秒替代 30 秒，违反 TIME-001，并可能覆盖本应保留的更早父期限规则 TIME-002。类型常量提供充分拒绝依据，后续可信行为阻断；参考仍独立运行并失败。

## reset-parent

以 Background 作为派生父 context，丢失值、父取消与短期限，违反 CTX-003、TIME-002。默认 30 秒字段不足以证明正确。预期行为拒绝，参考父传播检查失败。

## shared-expired-context

在 New 创建一次派生 context，Reserve/Release 复用并在每次返回取消它。第一次成功后对象已经失效，第二次调用不能进入后端；违反逐调用边界及 TIME-003，不是纯粹的形式变化。原 Cancel 和重复调用出现真实失败，参考 repeat/资源路径独立提供 TIME-003。

## swallowed-error

Reserve 吞掉后端错误，可能导致错误下单，违反 BEHAVIOR-001、TIME-004。门禁原测试失败已足够拒绝，可信行为阻断；参考独立执行错误和订单状态检查，不依赖门禁的停止位置。

## deleted-assertion

只删除原 HTTP 创建状态断言，没有替代。测试名称和原测试通过不能补偿 TEST-001 的削弱。门禁结构拒绝；生产代码行为应通过参考，包括实际到期。参考通过不否定拒绝。

## deleted-test

删除原 inventory.TestRelease，直接违反 TEST-001 的原测试身份要求。缺事件与结构删除分别保留，结构提供明确拒绝；生产参考通过不代表原测试被保留。

## skipped-test

原 TestReserve 首语句显式 Skip，违反 TEST-001 必须实际通过的要求。门禁执行及结构均拒绝；参考剥离候选测试后生产行为通过。

## opaque-helper

helper 的函数体仍为 WithTimeout(ctx, 30 秒)，逐次调用并释放，具有语义正确的人工依据。有限分析无法证明当前 helper 传播，生产结构也超出最小适配，预期 NeedsAttention。参考快速及真实到期均须通过，避免把未知误当错误。

## rewritten-assertion

把整数不等式两侧交换，人工可判断该处等价；首版原断言比较不证明任意重写。TEST-001 未获自动证明，预期 NeedsAttention，参考生产行为通过。

## unverified-build-tag

增加默认配置不激活的 extra.go。默认行为可通过，但不能声称其他配置已验证，SCOPE-001 缺口必须保留。参考声明默认配置，不能将其通过解释为全配置正确。

## fail-and-unknown

同时使用 3 秒期限并添加未覆盖 tag。已知 TIME-001 违规优先拒绝，SCOPE-001 未知及未运行行为保留。参考默认配置仍应报告错误期限；未知不会抵消已知错误。

## protected-path

只给受保护 pb 文件增加换行，字节仍已变化，违反 PATH-001。行为不变，参考应通过；这项拒绝依据来自保护契约而非行为 oracle。

## missed-callsite

handler 的 Cancel 调用漏掉新增 ctx，产生类型错误，违反 API/CTX 适配要求。生产构建拒绝；独立参考构建也应失败，其行为测试记录 blocked，不能伪造为语义断言失败。

## sdk-change

给候选 SDK 文件增加换行，违反 DEP-001 的 SDK 不可改要求。参考生产副本使用认证 SDK，只证明业务行为，预期通过；SDK 修改依据是补丁字节和规则，而非参考行为失败。

## test-init-exit

新增候选测试 init 主动退出，原测试没有充分实际执行证据，TEST-001 未证明。门禁保留未知；独立参考剥离候选测试入口后应真实执行通过。参考通过不能消除入口干扰。

## unprobed-branch

handler 为 mode=alternate 新增提前返回 418 的路径，默认探针不触发它。request_scope 将变化记为 SCOPE-001 未知，预期 NeedsAttention，不会自动放行。本版本新增独立请求变体，应发现 BEHAVIOR-001 违规；这是真实的参考/运行时覆盖差异，应保留而不是把 oracle 改成通过。
