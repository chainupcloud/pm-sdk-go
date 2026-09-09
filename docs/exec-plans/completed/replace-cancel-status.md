---
issue: 36
milestone: M23
status: completed
phase: implementation-and-review-complete
---

# 保留 ReplaceOrders 撤单原始状态与原因

关联 chainupcloud/pm-cup2026-liquidity#2003。真实 SDK 将 canceled 与 not_found 合并为 Err=nil，
使调用方日志无法区分实际撤单与未找到订单；失败原因也未透传。

实现：CancelResult 增加 Status/ErrorMsg，保留现有 not_found 幂等 Err=nil 兼容语义；
其他失败保留原始原因并 Err 非空。测试通过 httptest 驱动真实 Facade，包括 cancel fail-stop 与缺失结果。
不改鉴权、签名、定价或传输重试。库 PR 到 main，供 liquidity dev pin 后验证。

## 验证

- [x] 真实 HTTP 红测与最小绿化。
- [x] 全仓测试、vet、定向 race。
- [x] 独立 Spec 审查 0 项；Standards 唯一文档归档问题已修正。
- [x] PR #37；业务服务使用已固定 commit，服务 dev 验证由关联 #2003 收口。

- 已跑真实 HTTP 红测：原实现丢失 Status/ErrorMsg；空响应与错位 orderID 冒充撤单成功。
- 修复后 `GOWORK=off go test ./... -count=1` 通过；replace 故障结果保持可解析。
- 本次新增字段保持 not_found 的 Err=nil 兼容；调用方必须结合 Status 与 live-set 判断终态。
