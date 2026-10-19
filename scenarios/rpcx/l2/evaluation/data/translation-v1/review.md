# Translation 参考复核清单

工具生成的模板全部待复核；自动行为通过不表示下列内容已经由独立人员确认。

- test_preservation（TEST-001、PATH-001）：核对所有测试文件差异及每配置的 26 项原有身份，逐项检查断言、表数据、helper 和 mock 的实际使用。事件通过及 mock 文件字节不变不能证明断言完整。
- change_coverage（API-001、BUILD-001、CTX-001、TIME-001/002、BEHAVIOR-001）：核对 default 与 rpcx_alternate 各自仍有真实出站调用、标签未被篡改、New/两层 tracing/Translate/History 的所有生产修改；说明等价 helper 与独立测试的覆盖和缺口。不把有限输入证明为任意输入正确。
- delivery_evidence（SCOPE-001）：核对实际 Run 的双配置证据、活动实现、原报告与候选/规范/配置绑定。默认配置的成功不能代替 alternate；没有真实交付材料时保持未知。

每项由实际复核者填写身份、时间、理由、文件与证据定位。开发助手的源码检查需标注 development_calibration，不冒称独立人工复核；没有活动计时不能推算审核成本。
