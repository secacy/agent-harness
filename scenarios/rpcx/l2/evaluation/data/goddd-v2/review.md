# Goddd 参考复核清单

此记录由复核者基于实际候选填写。工具生成的 review-template.json 保持 inconclusive；未填写的项目不能算通过。先审查候选与参考证据，再查看原 Run 的交付报告。

- test_preservation（TEST-001）：逐文件核对基线与候选的测试差异，确认原 22 项测试/子例、断言、表数据及 helper 只发生允许的 API/ctx 适配；不能仅检查名称或通过状态。列出检查过的测试文件及具体理由。
- change_coverage（API/CTX/TIME/BEHAVIOR）：核对所有生产改动，说明参考 HTTP→booking→装饰器→RPC 测试覆盖的调用路径、未覆盖分支及等价 helper。确认没有通过新增业务分支、重试或错误转换改变契约；无法判断时保留 inconclusive。
- delivery_evidence（SCOPE-001）：核对实际 Run 的规范/候选/配置身份、命令和日志、审核报告与其门禁声明。NeedsAttention 的等价 helper 不因结构不同而错误；ReadyForReview 若声称已证明但必需证据缺失，记录可定位的交付违规。未取得真实交付材料时保留 inconclusive。

每项填写 status、reason、files 和 evidence（具体文件/行号或报告证据定位）；fail 也必须给出理由及定位。人员身份和时间由实际复核者填写，不把助手的自动校准记录当作独立人员复核。评估函数验证记录的完整性与身份绑定，不证明填写内容真实或完成了人工审查。
